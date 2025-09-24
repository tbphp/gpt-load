package keypool

import (
	"context"
	"fmt"
	"gpt-load/internal/channel"
	"gpt-load/internal/config"
	"gpt-load/internal/encryption"
	"gpt-load/internal/models"
	"time"

	"github.com/sirupsen/logrus"
	"go.uber.org/dig"
	"gorm.io/gorm"
)

// KeyTestResult holds the validation result for a single key.
type KeyTestResult struct {
	KeyValue   string `json:"key_value"`
	IsValid    bool   `json:"is_valid"`
	Error      string `json:"error,omitempty"`
	StatusCode int    `json:"status_code,omitempty"`
}

// KeyValidator provides methods to validate API keys.
type KeyValidator struct {
	DB              *gorm.DB
	channelFactory  *channel.Factory
	SettingsManager *config.SystemSettingsManager
	keypoolProvider *KeyProvider
	encryptionSvc   encryption.Service
}

type KeyValidatorParams struct {
	dig.In
	DB              *gorm.DB
	ChannelFactory  *channel.Factory
	SettingsManager *config.SystemSettingsManager
	KeypoolProvider *KeyProvider
	EncryptionSvc   encryption.Service
}

// NewKeyValidator creates a new KeyValidator.
func NewKeyValidator(params KeyValidatorParams) *KeyValidator {
	return &KeyValidator{
		DB:              params.DB,
		channelFactory:  params.ChannelFactory,
		SettingsManager: params.SettingsManager,
		keypoolProvider: params.KeypoolProvider,
		encryptionSvc:   params.EncryptionSvc,
	}
}

// ValidateSingleKey performs a validation check on a single API key.
func (s *KeyValidator) ValidateSingleKey(key *models.APIKey, group *models.Group) (bool, int, error) {
	if group.EffectiveConfig.AppUrl == "" {
		group.EffectiveConfig = s.SettingsManager.GetEffectiveConfig(group.Config)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(group.EffectiveConfig.KeyValidationTimeoutSeconds)*time.Second)
	defer cancel()

	ch, err := s.channelFactory.GetChannel(group)
	if err != nil {
		return false, 0, fmt.Errorf("failed to get channel for group %s: %w", group.Name, err)
	}

	isValid, statusCode, validationErr := ch.ValidateKey(ctx, key, group)

	var errorMsg string
	if !isValid && validationErr != nil {
		errorMsg = validationErr.Error()
	}

	// 更新状态码到数据库
	if statusCode != 0 {
		// keep in-memory model consistent to avoid accidental overwrite
		key.StatusCode = statusCode
		if err := s.DB.Model(key).Update("status_code", statusCode).Error; err != nil {
			logrus.WithError(err).WithField("key_id", key.ID).Error("Failed to update status_code")
		}
	}

	s.keypoolProvider.UpdateStatus(key, group, isValid, errorMsg)

	if !isValid {
		logrus.WithFields(logrus.Fields{
			"error":    validationErr,
			"key_id":   key.ID,
			"group_id": group.ID,
			"status_code": statusCode,
		}).Debug("Key validation failed")
		return false, statusCode, validationErr
	}

	logrus.WithFields(logrus.Fields{
		"key_id":   key.ID,
		"is_valid": isValid,
		"status_code": statusCode,
	}).Debug("Key validation successful")

	return true, statusCode, nil
}

// TestMultipleKeys performs a synchronous validation for a list of key values within a specific group.
func (s *KeyValidator) TestMultipleKeys(group *models.Group, keyValues []string) ([]KeyTestResult, error) {
	results := make([]KeyTestResult, len(keyValues))

	// Generate hashes for all key values
	var keyHashes []string
	for _, keyValue := range keyValues {
		keyHash := s.encryptionSvc.Hash(keyValue)
		if keyHash == "" {
			continue
		}
		keyHashes = append(keyHashes, keyHash)
	}

	// Find which of the provided keys actually exist in the database for this group
	var existingKeys []models.APIKey
	if len(keyHashes) > 0 {
		if err := s.DB.Where("group_id = ? AND key_hash IN ?", group.ID, keyHashes).Find(&existingKeys).Error; err != nil {
			return nil, fmt.Errorf("failed to query keys from DB: %w", err)
		}
	}

	// Create a map of key_hash to APIKey for quick lookup
	existingKeyMap := make(map[string]models.APIKey)
	for _, k := range existingKeys {
		existingKeyMap[k.KeyHash] = k
	}

	for i, kv := range keyValues {
		keyHash := s.encryptionSvc.Hash(kv)
		apiKey, exists := existingKeyMap[keyHash]
		if !exists {
			results[i] = KeyTestResult{
				KeyValue: kv,
				IsValid:  false,
				Error:    "Key does not exist in this group or has been removed.",
			}
			continue
		}

		apiKey.KeyValue = kv

		isValid, statusCode, validationErr := s.ValidateSingleKey(&apiKey, group)

		results[i] = KeyTestResult{
			KeyValue:   kv,
			IsValid:    isValid,
			StatusCode: statusCode,
			Error:      "",
		}
		if validationErr != nil {
			results[i].Error = validationErr.Error()
		}
	}

	return results, nil
}
