package services

import (
	"fmt"
	"gpt-load/internal/config"
	"gpt-load/internal/keypool"
	"gpt-load/internal/models"
	"gpt-load/internal/types"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// ManualValidationResult holds the result of a manual validation task.
type ManualValidationResult struct {
	TotalKeys   int `json:"total_keys"`
	ValidKeys   int `json:"valid_keys"`
	InvalidKeys int `json:"invalid_keys"`
}

// KeyManualValidationService handles user-initiated key validation for a group.
type KeyManualValidationService struct {
	DB              *gorm.DB
	Validator       *keypool.KeyValidator
	TaskService     *TaskService
	SettingsManager *config.SystemSettingsManager
	ConfigManager   types.ConfigManager
}

// NewKeyManualValidationService creates a new KeyManualValidationService.
func NewKeyManualValidationService(db *gorm.DB, validator *keypool.KeyValidator, taskService *TaskService, settingsManager *config.SystemSettingsManager, configManager types.ConfigManager) *KeyManualValidationService {
	return &KeyManualValidationService{
		DB:              db,
		Validator:       validator,
		TaskService:     taskService,
		SettingsManager: settingsManager,
		ConfigManager:   configManager,
	}
}

// StartValidationTask starts a new manual validation task for a given group.
func (s *KeyManualValidationService) StartValidationTask(group *models.Group, status string) (*TaskStatus, error) {
	var keys []models.APIKey
	query := s.DB.Where("group_id = ?", group.ID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if err := query.Find(&keys).Error; err != nil {
		return nil, fmt.Errorf("failed to get keys for group %s with status '%s': %w", group.Name, status, err)
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("no keys to validate in group %s", group.Name)
	}

	timeout := 30 * time.Minute

	taskStatus, err := s.TaskService.StartTask(TaskTypeKeyValidation, group.Name, len(keys), timeout)
	if err != nil {
		return nil, err
	}

	// Run the validation in a separate goroutine
	go s.runValidation(group, keys, status)

	return taskStatus, nil
}

func (s *KeyManualValidationService) runValidation(group *models.Group, keys []models.APIKey, status string) {
	logFields := logrus.Fields{
		"group":  group.Name,
		"status": status,
	}
	if status == "" {
		logFields["status"] = "all"
	}
	logrus.WithFields(logFields).Info("Starting manual validation")

	jobs := make(chan models.APIKey, len(keys))
	results := make(chan bool, len(keys))

	concurrency := group.EffectiveConfig.KeyValidationConcurrency

	var wg sync.WaitGroup
	for range concurrency {
		wg.Add(1)
		go s.validationWorker(&wg, group, jobs, results)
	}

	for _, key := range keys {
		jobs <- key
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	validCount := 0
	processedCount := 0
	lastUpdateTime := time.Now()

	for isValid := range results {
		processedCount++
		if isValid {
			validCount++
		}

		// Throttle progress updates to once per second
		if time.Since(lastUpdateTime) > time.Second {
			if err := s.TaskService.UpdateProgress(processedCount); err != nil {
				logrus.Warnf("Failed to update task progress: %v", err)
			}
			lastUpdateTime = time.Now()
		}
	}

	// Ensure the final progress is always updated
	if err := s.TaskService.UpdateProgress(processedCount); err != nil {
		logrus.Warnf("Failed to update final task progress: %v", err)
	}

	result := ManualValidationResult{
		TotalKeys:   len(keys),
		ValidKeys:   validCount,
		InvalidKeys: len(keys) - validCount,
	}

	// End the task and store the final result
	if err := s.TaskService.EndTask(result, nil); err != nil {
		logrus.Errorf("Failed to end task for group %s: %v", group.Name, err)
	}
	logrus.Infof("Manual validation finished for group %s: %+v", group.Name, result)
}

// validationResult 包含验证结果信息
func (s *KeyManualValidationService) validationWorker(wg *sync.WaitGroup, group *models.Group, jobs <-chan models.APIKey, results chan<- bool) {
	defer wg.Done()
	for key := range jobs {
		isValid, _ := s.Validator.ValidateSingleKey(&key, group)
		results <- isValid
	}
}
