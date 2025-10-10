package services

import (
	"fmt"
	"gpt-load/internal/models"

	"github.com/sirupsen/logrus"
)

const (
	deleteChunkSize = 1000
)

// KeyDeleteResult holds the result of a delete task.
type KeyDeleteResult struct {
	DeletedCount int `json:"deleted_count"`
	IgnoredCount int `json:"ignored_count"`
}

// KeyDeleteService handles the asynchronous deletion of a large number of keys.
type KeyDeleteService struct {
	TaskService *TaskService
	KeyService  *KeyService
}

// NewKeyDeleteService creates a new KeyDeleteService.
func NewKeyDeleteService(taskService *TaskService, keyService *KeyService) *KeyDeleteService {
	return &KeyDeleteService{
		TaskService: taskService,
		KeyService:  keyService,
	}
}

// StartDeleteTask initiates a new asynchronous key deletion task.
func (s *KeyDeleteService) StartDeleteTask(group *models.Group, keysText string) (*TaskStatus, error) {
	keys := s.KeyService.ParseKeysFromText(keysText)
	if len(keys) == 0 {
		return nil, fmt.Errorf("no valid keys found in the input text")
	}

	initialStatus, err := s.TaskService.StartTask(TaskTypeKeyDelete, group.Name, len(keys))
	if err != nil {
		return nil, err
	}

	go s.runDelete(group, keys)

	return initialStatus, nil
}

func (s *KeyDeleteService) runDelete(group *models.Group, keys []string) {
	progressCallback := func(processed int) {
		if err := s.TaskService.UpdateProgress(processed); err != nil {
			logrus.Warnf("Failed to update task progress for group %d: %v", group.ID, err)
		}
	}

	deletedCount, ignoredCount, err := s.processAndDeleteKeys(group.ID, keys, progressCallback)
	if err != nil {
		if endErr := s.TaskService.EndTask(nil, err); endErr != nil {
			logrus.Errorf("Failed to end task with error for group %d: %v (original error: %v)", group.ID, endErr, err)
		}
		return
	}

	result := KeyDeleteResult{
		DeletedCount: deletedCount,
		IgnoredCount: ignoredCount,
	}

	if endErr := s.TaskService.EndTask(result, nil); endErr != nil {
		logrus.Errorf("Failed to end task with success result for group %d: %v", group.ID, endErr)
	}
}

// processAndDeleteKeys is the core function for deleting keys with progress tracking.
func (s *KeyDeleteService) processAndDeleteKeys(
	groupID uint,
	keys []string,
	progressCallback func(processed int),
) (deletedCount int, ignoredCount int, err error) {
	var totalDeletedCount int64

	for i := 0; i < len(keys); i += deleteChunkSize {
		end := i + deleteChunkSize
		if end > len(keys) {
			end = len(keys)
		}
		chunk := keys[i:end]

		deletedChunkCount, err := s.KeyService.KeyProvider.RemoveKeys(groupID, chunk)
		if err != nil {
			return int(totalDeletedCount), len(keys) - int(totalDeletedCount), err
		}

		totalDeletedCount += deletedChunkCount

		if progressCallback != nil {
			progressCallback(i + len(chunk))
		}
	}

	return int(totalDeletedCount), len(keys) - int(totalDeletedCount), nil
}
