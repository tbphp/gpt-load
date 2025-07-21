package services

import (
	"fmt"
	"gpt-load/internal/models"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	importChunkSize = 1000
	importTimeout   = 24 * time.Hour
)

// KeyImportResult holds the result of an import task.
type KeyImportResult struct {
	AddedCount   int `json:"added_count"`
	IgnoredCount int `json:"ignored_count"`
}

// KeyImportService handles the asynchronous import of a large number of keys.
type KeyImportService struct {
	TaskService *TaskService
	KeyService  *KeyService
}

// NewKeyImportService creates a new KeyImportService.
func NewKeyImportService(taskService *TaskService, keyService *KeyService) *KeyImportService {
	return &KeyImportService{
		TaskService: taskService,
		KeyService:  keyService,
	}
}

// StartImportTask initiates a new asynchronous key import task.
func (s *KeyImportService) StartImportTask(group *models.Group, keysText string) (*TaskStatus, error) {
	keys := s.KeyService.ParseKeysFromText(keysText)
	if len(keys) == 0 {
		return nil, fmt.Errorf("no valid keys found in the input text")
	}

	initialStatus, err := s.TaskService.StartTask(TaskTypeKeyImport, group.Name, len(keys), importTimeout)
	if err != nil {
		return nil, err
	}

	go s.runImport(group, keys)

	return initialStatus, nil
}

func (s *KeyImportService) runImport(group *models.Group, keys []string) {
	progressCallback := func(processed int) {
		if err := s.TaskService.UpdateProgress(processed); err != nil {
			logrus.Warnf("Failed to update task progress for group %d: %v", group.ID, err)
		}
	}

	addedCount, ignoredCount, err := s.KeyService.processAndCreateKeys(group.ID, keys, progressCallback)
	if err != nil {
		if endErr := s.TaskService.EndTask(nil, err); endErr != nil {
			logrus.Errorf("Failed to end task with error for group %d: %v (original error: %v)", group.ID, endErr, err)
		}
		return
	}

	result := KeyImportResult{
		AddedCount:   addedCount,
		IgnoredCount: ignoredCount,
	}

	if endErr := s.TaskService.EndTask(result, nil); endErr != nil {
		logrus.Errorf("Failed to end task with success result for group %d: %v", group.ID, endErr)
	}
}
