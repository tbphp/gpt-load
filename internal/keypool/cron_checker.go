package keypool

import (
	"context"
	"gpt-load/internal/config"
	"gpt-load/internal/encryption"
	"gpt-load/internal/models"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// NewCronChecker is responsible for periodically validating invalid keys.
type CronChecker struct {
	DB              *gorm.DB
	SettingsManager *config.SystemSettingsManager
	Validator       *KeyValidator
	KeyProvider     *KeyProvider
	EncryptionSvc   encryption.Service
	stopChan        chan struct{}
	wg              sync.WaitGroup
}

// NewCronChecker creates a new CronChecker.
func NewCronChecker(
	db *gorm.DB,
	settingsManager *config.SystemSettingsManager,
	validator *KeyValidator,
	keyProvider *KeyProvider,
	encryptionSvc encryption.Service,
) *CronChecker {
	return &CronChecker{
		DB:              db,
		SettingsManager: settingsManager,
		Validator:       validator,
		KeyProvider:     keyProvider,
		EncryptionSvc:   encryptionSvc,
		stopChan:        make(chan struct{}),
	}
}

// Start begins the cron job execution.
func (s *CronChecker) Start() {
	logrus.Debug("Starting CronChecker...")
	s.wg.Add(1)
	go s.runLoop()
}

// Stop stops the cron job, respecting the context for shutdown timeout.
func (s *CronChecker) Stop(ctx context.Context) {
	close(s.stopChan)

	// Wait for the goroutine to finish, or for the shutdown to time out.
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logrus.Info("CronChecker stopped gracefully.")
	case <-ctx.Done():
		logrus.Warn("CronChecker stop timed out.")
	}
}

func (s *CronChecker) runLoop() {
	defer s.wg.Done()

	s.recoverRateLimitedKeys()
	s.submitValidationJobs()

	recoveryTicker := time.NewTicker(10 * time.Second)
	validationTicker := time.NewTicker(5 * time.Minute)
	defer recoveryTicker.Stop()
	defer validationTicker.Stop()

	for {
		select {
		case <-recoveryTicker.C:
			s.recoverRateLimitedKeys()
		case <-validationTicker.C:
			logrus.Debug("CronChecker: Running as Master, submitting validation jobs.")
			s.submitValidationJobs()
		case <-s.stopChan:
			return
		}
	}
}

// submitValidationJobs finds groups whose keys need validation and validates them concurrently.
func (s *CronChecker) submitValidationJobs() {
	var groups []models.Group
	if err := s.DB.Where("group_type != ? OR group_type IS NULL", "aggregate").Find(&groups).Error; err != nil {
		logrus.Errorf("CronChecker: Failed to get groups: %v", err)
		return
	}

	validationStartTime := time.Now()
	var wg sync.WaitGroup

	for i := range groups {
		group := &groups[i]
		group.EffectiveConfig = s.SettingsManager.GetEffectiveConfig(group.Config)
		interval := time.Duration(group.EffectiveConfig.KeyValidationIntervalMinutes) * time.Minute

		if group.LastValidatedAt == nil || validationStartTime.Sub(*group.LastValidatedAt) > interval {
			wg.Add(1)
			g := group
			go func() {
				defer wg.Done()
				s.validateGroupKeys(g)
			}()
		}
	}

	wg.Wait()
}

func (s *CronChecker) recoverRateLimitedKeys() {
	now := time.Now()
	var rateLimitedKeys []models.APIKey

	if err := s.DB.Where("status = ? AND cooldown_until <= ?", models.KeyStatusRateLimited, now).Find(&rateLimitedKeys).Error; err != nil {
		logrus.Errorf("CronChecker: Failed to get rate-limited keys for recovery: %v", err)
		return
	}

	for i := range rateLimitedKeys {
		key := &rateLimitedKeys[i]
		updates := map[string]any{
			"status":         models.KeyStatusActive,
			"cooldown_until": nil,
		}

		if err := s.DB.Model(&models.APIKey{}).Where("id = ? AND status = ?", key.ID, models.KeyStatusRateLimited).Updates(updates).Error; err != nil {
			logrus.WithError(err).WithField("key_id", key.ID).Error("CronChecker: Failed to recover rate-limited key in DB")
			continue
		}

		key.Status = models.KeyStatusActive
		key.CooldownUntil = nil

		if err := s.KeyProvider.addKeyToStore(key); err != nil {
			// Store 恢复失败，回滚 DB 状态避免 key 永久掉出池
			rollback := map[string]any{
				"status":         models.KeyStatusRateLimited,
				"cooldown_until": now,
			}
			if rollbackErr := s.DB.Model(&models.APIKey{}).Where("id = ?", key.ID).Updates(rollback).Error; rollbackErr != nil {
				logrus.WithError(rollbackErr).WithField("key_id", key.ID).Error("CronChecker: Failed to roll back rate-limited key after store recovery failure")
			}
			logrus.WithError(err).WithField("key_id", key.ID).Error("CronChecker: Failed to recover rate-limited key in store")
			continue
		}
	}
}

// validateGroupKeys validates all invalid keys for a single group concurrently.
func (s *CronChecker) validateGroupKeys(group *models.Group) {
	groupProcessStart := time.Now()

	var invalidKeys []models.APIKey
	err := s.DB.Where("group_id = ? AND status = ?", group.ID, models.KeyStatusInvalid).Find(&invalidKeys).Error
	if err != nil {
		logrus.Errorf("CronChecker: Failed to get invalid keys for group %s: %v", group.Name, err)
		return
	}

	if len(invalidKeys) == 0 {
		if err := s.DB.Model(group).Update("last_validated_at", time.Now()).Error; err != nil {
			logrus.Errorf("CronChecker: Failed to update last_validated_at for group %s: %v", group.Name, err)
		}
		logrus.Infof("CronChecker: Group '%s' has no invalid keys to check.", group.Name)
		return
	}

	var becameValidCount int32
	var keyWg sync.WaitGroup
	jobs := make(chan *models.APIKey, len(invalidKeys))

	concurrency := group.EffectiveConfig.KeyValidationConcurrency
	for range concurrency {
		keyWg.Add(1)
		go func() {
			defer keyWg.Done()
			for {
				select {
				case key, ok := <-jobs:
					if !ok {
						return
					}

					// Decrypt the key before validation
					decryptedKey, err := s.EncryptionSvc.Decrypt(key.KeyValue)
					if err != nil {
						logrus.WithError(err).WithField("key_id", key.ID).Error("CronChecker: Failed to decrypt key for validation, skipping")
						continue
					}

					// Create a copy with decrypted value for validation
					keyForValidation := *key
					keyForValidation.KeyValue = decryptedKey

					isValid, _ := s.Validator.ValidateSingleKey(&keyForValidation, group)
					if isValid {
						atomic.AddInt32(&becameValidCount, 1)
					}
				case <-s.stopChan:
					return
				}
			}
		}()
	}

DistributeLoop:
	for i := range invalidKeys {
		select {
		case jobs <- &invalidKeys[i]:
		case <-s.stopChan:
			break DistributeLoop
		}
	}
	close(jobs)

	keyWg.Wait()

	if err := s.DB.Model(group).Update("last_validated_at", time.Now()).Error; err != nil {
		logrus.Errorf("CronChecker: Failed to update last_validated_at for group %s: %v", group.Name, err)
	}

	duration := time.Since(groupProcessStart)
	logrus.Infof(
		"CronChecker: Group '%s' validation finished. Total checked: %d, became valid: %d. Duration: %s.",
		group.Name,
		len(invalidKeys),
		becameValidCount,
		duration.String(),
	)
}
