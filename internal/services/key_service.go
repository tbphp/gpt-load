package services

import (
	"encoding/json"
	"fmt"
	"gpt-load/internal/keypool"
	"gpt-load/internal/models"
	"io"
	"regexp"
	"strings"

	"gorm.io/gorm"
)

const (
	maxRequestKeys = 5000
	chunkSize      = 1000
)

// AddKeysResult holds the result of adding multiple keys.
type AddKeysResult struct {
	AddedCount   int   `json:"added_count"`
	IgnoredCount int   `json:"ignored_count"`
	TotalInGroup int64 `json:"total_in_group"`
}

// DeleteKeysResult holds the result of deleting multiple keys.
type DeleteKeysResult struct {
	DeletedCount int   `json:"deleted_count"`
	IgnoredCount int   `json:"ignored_count"`
	TotalInGroup int64 `json:"total_in_group"`
}

// RestoreKeysResult holds the result of restoring multiple keys.
type RestoreKeysResult struct {
	RestoredCount int   `json:"restored_count"`
	IgnoredCount  int   `json:"ignored_count"`
	TotalInGroup  int64 `json:"total_in_group"`
}

// KeyService provides services related to API keys.
type KeyService struct {
	DB           *gorm.DB
	KeyProvider  *keypool.KeyProvider
	KeyValidator *keypool.KeyValidator
}

// NewKeyService creates a new KeyService.
func NewKeyService(db *gorm.DB, keyProvider *keypool.KeyProvider, keyValidator *keypool.KeyValidator) *KeyService {
	return &KeyService{
		DB:           db,
		KeyProvider:  keyProvider,
		KeyValidator: keyValidator,
	}
}

// AddMultipleKeys handles the business logic of creating new keys from a text block.
// deprecated: use KeyImportService for large imports
func (s *KeyService) AddMultipleKeys(groupID uint, keysText string) (*AddKeysResult, error) {
	keys := s.ParseKeysFromText(keysText)
	if len(keys) > maxRequestKeys {
		return nil, fmt.Errorf("batch size exceeds the limit of %d keys, got %d", maxRequestKeys, len(keys))
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no valid keys found in the input text")
	}

	addedCount, ignoredCount, err := s.processAndCreateKeys(groupID, keys, nil)
	if err != nil {
		return nil, err
	}

	var totalInGroup int64
	if err := s.DB.Model(&models.APIKey{}).Where("group_id = ?", groupID).Count(&totalInGroup).Error; err != nil {
		return nil, err
	}

	return &AddKeysResult{
		AddedCount:   addedCount,
		IgnoredCount: ignoredCount,
		TotalInGroup: totalInGroup,
	}, nil
}

// processAndCreateKeys is the lowest-level reusable function for adding keys.
func (s *KeyService) processAndCreateKeys(
	groupID uint,
	keys []string,
	progressCallback func(processed int),
) (addedCount int, ignoredCount int, err error) {
	// 1. Get existing keys in the group for deduplication
	var existingKeys []models.APIKey
	if err := s.DB.Where("group_id = ?", groupID).Select("key_value").Find(&existingKeys).Error; err != nil {
		return 0, 0, err
	}
	existingKeyMap := make(map[string]bool)
	for _, k := range existingKeys {
		existingKeyMap[k.KeyValue] = true
	}

	// 2. Prepare new keys for creation
	var newKeysToCreate []models.APIKey
	uniqueNewKeys := make(map[string]bool)

	for _, keyVal := range keys {
		trimmedKey := strings.TrimSpace(keyVal)
		if trimmedKey == "" {
			continue
		}
		if existingKeyMap[trimmedKey] || uniqueNewKeys[trimmedKey] {
			continue
		}
		if s.isValidKeyFormat(trimmedKey) {
			uniqueNewKeys[trimmedKey] = true
			newKeysToCreate = append(newKeysToCreate, models.APIKey{
				GroupID:  groupID,
				KeyValue: trimmedKey,
				Status:   models.KeyStatusActive,
			})
		}
	}

	if len(newKeysToCreate) == 0 {
		return 0, len(keys), nil
	}

	// 3. Use KeyProvider to add keys in chunks
	for i := 0; i < len(newKeysToCreate); i += chunkSize {
		end := i + chunkSize
		if end > len(newKeysToCreate) {
			end = len(newKeysToCreate)
		}
		chunk := newKeysToCreate[i:end]
		if err := s.KeyProvider.AddKeys(groupID, chunk); err != nil {
			return addedCount, len(keys) - addedCount, err
		}
		addedCount += len(chunk)

		if progressCallback != nil {
			progressCallback(i + len(chunk))
		}
	}

	return addedCount, len(keys) - addedCount, nil
}

// ParseKeysFromText parses a string of keys from various formats into a string slice.
// This function is exported to be shared with the handler layer.
func (s *KeyService) ParseKeysFromText(text string) []string {
	var keys []string

	// First, try to parse as a JSON array of strings
	if json.Unmarshal([]byte(text), &keys) == nil && len(keys) > 0 {
		return s.filterValidKeys(keys)
	}

	// 通用解析：通过分隔符分割文本，不使用复杂的正则表达式
	delimiters := regexp.MustCompile(`[\s,;|\n\r\t]+`)
	splitKeys := delimiters.Split(strings.TrimSpace(text), -1)

	for _, key := range splitKeys {
		key = strings.TrimSpace(key)
		if key != "" {
			keys = append(keys, key)
		}
	}

	return s.filterValidKeys(keys)
}

// filterValidKeys validates and filters potential API keys
func (s *KeyService) filterValidKeys(keys []string) []string {
	var validKeys []string
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if s.isValidKeyFormat(key) {
			validKeys = append(validKeys, key)
		}
	}
	return validKeys
}

// isValidKeyFormat performs basic validation on key format
func (s *KeyService) isValidKeyFormat(key string) bool {
	if len(key) < 4 || len(key) > 1000 {
		return false
	}

	if key == "" ||
		strings.TrimSpace(key) == "" {
		return false
	}

	validChars := regexp.MustCompile(`^[a-zA-Z0-9_\-./+=:]+$`)
	return validChars.MatchString(key)
}

// RestoreMultipleKeys handles the business logic of restoring keys from a text block.
func (s *KeyService) RestoreMultipleKeys(groupID uint, keysText string) (*RestoreKeysResult, error) {
	keysToRestore := s.ParseKeysFromText(keysText)
	if len(keysToRestore) > maxRequestKeys {
		return nil, fmt.Errorf("batch size exceeds the limit of %d keys, got %d", maxRequestKeys, len(keysToRestore))
	}
	if len(keysToRestore) == 0 {
		return nil, fmt.Errorf("no valid keys found in the input text")
	}

	var totalRestoredCount int64
	for i := 0; i < len(keysToRestore); i += chunkSize {
		end := i + chunkSize
		if end > len(keysToRestore) {
			end = len(keysToRestore)
		}
		chunk := keysToRestore[i:end]
		restoredCount, err := s.KeyProvider.RestoreMultipleKeys(groupID, chunk)
		if err != nil {
			return nil, err
		}
		totalRestoredCount += restoredCount
	}

	ignoredCount := len(keysToRestore) - int(totalRestoredCount)

	var totalInGroup int64
	if err := s.DB.Model(&models.APIKey{}).Where("group_id = ?", groupID).Count(&totalInGroup).Error; err != nil {
		return nil, err
	}

	return &RestoreKeysResult{
		RestoredCount: int(totalRestoredCount),
		IgnoredCount:  ignoredCount,
		TotalInGroup:  totalInGroup,
	}, nil
}

// RestoreAllInvalidKeys sets the status of all 'inactive' keys in a group to 'active'.
func (s *KeyService) RestoreAllInvalidKeys(groupID uint) (int64, error) {
	return s.KeyProvider.RestoreKeys(groupID)
}

// ClearAllInvalidKeys deletes all 'inactive' keys from a group.
func (s *KeyService) ClearAllInvalidKeys(groupID uint) (int64, error) {
	return s.KeyProvider.RemoveInvalidKeys(groupID)
}

// ClearAllKeys deletes all keys from a group.
func (s *KeyService) ClearAllKeys(groupID uint) (int64, error) {
	return s.KeyProvider.RemoveAllKeys(groupID)
}

// DeleteMultipleKeys handles the business logic of deleting keys from a text block.
func (s *KeyService) DeleteMultipleKeys(groupID uint, keysText string) (*DeleteKeysResult, error) {
	keysToDelete := s.ParseKeysFromText(keysText)
	if len(keysToDelete) > maxRequestKeys {
		return nil, fmt.Errorf("batch size exceeds the limit of %d keys, got %d", maxRequestKeys, len(keysToDelete))
	}
	if len(keysToDelete) == 0 {
		return nil, fmt.Errorf("no valid keys found in the input text")
	}

	var totalDeletedCount int64
	for i := 0; i < len(keysToDelete); i += chunkSize {
		end := i + chunkSize
		if end > len(keysToDelete) {
			end = len(keysToDelete)
		}
		chunk := keysToDelete[i:end]
		deletedCount, err := s.KeyProvider.RemoveKeys(groupID, chunk)
		if err != nil {
			return nil, err
		}
		totalDeletedCount += deletedCount
	}

	ignoredCount := len(keysToDelete) - int(totalDeletedCount)

	var totalInGroup int64
	if err := s.DB.Model(&models.APIKey{}).Where("group_id = ?", groupID).Count(&totalInGroup).Error; err != nil {
		return nil, err
	}

	return &DeleteKeysResult{
		DeletedCount: int(totalDeletedCount),
		IgnoredCount: ignoredCount,
		TotalInGroup: totalInGroup,
	}, nil
}

// ListKeysInGroupQuery builds a query to list all keys within a specific group, filtered by status.
func (s *KeyService) ListKeysInGroupQuery(groupID uint, statusFilter string, searchKeyword string) *gorm.DB {
	query := s.DB.Model(&models.APIKey{}).Where("group_id = ?", groupID)

	if statusFilter != "" {
		query = query.Where("status = ?", statusFilter)
	}

	if searchKeyword != "" {
		query = query.Where("key_value LIKE ?", "%"+searchKeyword+"%")
	}

	query = query.Order("last_used_at desc, updated_at desc")

	return query
}

// TestMultipleKeys handles a one-off validation test for multiple keys.
func (s *KeyService) TestMultipleKeys(group *models.Group, keysText string) ([]keypool.KeyTestResult, error) {
	keysToTest := s.ParseKeysFromText(keysText)
	if len(keysToTest) > maxRequestKeys {
		return nil, fmt.Errorf("batch size exceeds the limit of %d keys, got %d", maxRequestKeys, len(keysToTest))
	}
	if len(keysToTest) == 0 {
		return nil, fmt.Errorf("no valid keys found in the input text")
	}

	var allResults []keypool.KeyTestResult
	for i := 0; i < len(keysToTest); i += chunkSize {
		end := i + chunkSize
		if end > len(keysToTest) {
			end = len(keysToTest)
		}
		chunk := keysToTest[i:end]
		results, err := s.KeyValidator.TestMultipleKeys(group, chunk)
		if err != nil {
			return nil, err
		}
		allResults = append(allResults, results...)
	}

	return allResults, nil
}

// StreamKeysToWriter fetches keys from the database in batches and writes them to the provided writer.
func (s *KeyService) StreamKeysToWriter(groupID uint, statusFilter string, writer io.Writer) error {
	query := s.DB.Model(&models.APIKey{}).Where("group_id = ?", groupID).Select("id, key_value")

	switch statusFilter {
	case models.KeyStatusActive, models.KeyStatusInvalid:
		query = query.Where("status = ?", statusFilter)
	case "all":
	default:
		return fmt.Errorf("invalid status filter: %s", statusFilter)
	}

	var keys []models.APIKey
	err := query.FindInBatches(&keys, chunkSize, func(tx *gorm.DB, batch int) error {
		for _, key := range keys {
			if _, err := writer.Write([]byte(key.KeyValue + "\n")); err != nil {
				return err
			}
		}
		return nil
	}).Error

	return err
}
