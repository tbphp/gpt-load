package control

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"gorm.io/gorm"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

type AccessKeyFilters struct {
	Groups    []uint              `json:"groups"`
	Protocols []protocol.Protocol `json:"protocols"`
	Models    []string            `json:"models"`
}

type AccessKeyCreateRequest struct {
	Name    string            `json:"name"`
	Filters *AccessKeyFilters `json:"filters"`
}

type AccessKeyUpdateRequest struct {
	Name    *string                `json:"name"`
	Status  *state.AccessKeyStatus `json:"status"`
	Filters *AccessKeyFilters      `json:"filters"`
}

type AccessKeyResponse struct {
	ID      uint                  `json:"id"`
	Name    string                `json:"name"`
	Key     string                `json:"key"`
	Status  state.AccessKeyStatus `json:"status"`
	Filters AccessKeyFilters      `json:"filters"`
}

const accessKeyPrefix = "sk-gl-"

func (s *Service) newAccessKeyRow(
	name string,
	filters AccessKeyFilters,
) (models.AccessKey, string, error) {
	encodedFilters, err := json.Marshal(filters)
	if err != nil {
		return models.AccessKey{}, "", fmt.Errorf("encode access key filters: %w", err)
	}
	randomBytes := make([]byte, 16)
	if _, err := io.ReadFull(s.random, randomBytes); err != nil {
		return models.AccessKey{}, "", fmt.Errorf("generate access key: %w", err)
	}
	plaintext := accessKeyPrefix + hex.EncodeToString(randomBytes)
	ciphertext, err := s.encryption.Encrypt(plaintext)
	if err != nil {
		return models.AccessKey{}, "", fmt.Errorf("encrypt access key: %w", err)
	}
	return models.AccessKey{
		Name:     name,
		KeyValue: ciphertext,
		KeyHash:  s.encryption.Hash(plaintext),
		Status:   string(state.AccessKeyStatusActive),
		Filters:  models.JSON(encodedFilters),
	}, plaintext, nil
}

func (s *Service) CreateAccessKey(
	ctx context.Context,
	request AccessKeyCreateRequest,
) (AccessKeyResponse, error) {
	name, err := normalizeAccessKeyName(request.Name)
	if err != nil {
		return AccessKeyResponse{}, err
	}
	filters, err := normalizeAccessKeyFilters(request.Filters)
	if err != nil {
		return AccessKeyResponse{}, err
	}

	var result AccessKeyResponse
	_, err = s.writeConfig(ctx, func(tx *gorm.DB) error {
		if err := validateFilterGroupReferences(tx, filters.Groups); err != nil {
			return err
		}
		row, plaintext, err := s.newAccessKeyRow(name, filters)
		if err != nil {
			return err
		}
		if err := tx.Create(&row).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		result = AccessKeyResponse{
			ID: row.ID, Name: row.Name, Key: plaintext,
			Status: state.AccessKeyStatusActive, Filters: filters,
		}
		return nil
	}, nil)
	if err != nil {
		return AccessKeyResponse{}, err
	}
	return result, nil
}

func (s *Service) ListAccessKeys(ctx context.Context) ([]AccessKeyResponse, error) {
	var rows []models.AccessKey
	if err := s.db.WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, app_errors.ParseDBError(err)
	}

	result := make([]AccessKeyResponse, 0, len(rows))
	for _, row := range rows {
		status := state.AccessKeyStatus(row.Status)
		if status != state.AccessKeyStatusActive && status != state.AccessKeyStatusDisabled {
			return nil, fmt.Errorf("access key %d has invalid status", row.ID)
		}
		filters, err := decodeStoredAccessKeyFilters(row.Filters)
		if err != nil {
			return nil, fmt.Errorf("decode access key %d filters: %w", row.ID, err)
		}
		plaintext, err := s.encryption.Decrypt(row.KeyValue)
		if err != nil {
			return nil, fmt.Errorf("decrypt access key %d: %w", row.ID, err)
		}
		result = append(result, AccessKeyResponse{
			ID: row.ID, Name: row.Name, Key: plaintext, Status: status, Filters: filters,
		})
	}
	return result, nil
}

func (s *Service) UpdateAccessKey(
	ctx context.Context,
	id uint,
	request AccessKeyUpdateRequest,
) (AccessKeyResponse, error) {
	if id == 0 || (request.Name == nil && request.Status == nil && request.Filters == nil) {
		return AccessKeyResponse{}, app_errors.ErrBadRequest
	}

	var name *string
	if request.Name != nil {
		normalized, err := normalizeAccessKeyName(*request.Name)
		if err != nil {
			return AccessKeyResponse{}, err
		}
		name = &normalized
	}
	if request.Status != nil &&
		*request.Status != state.AccessKeyStatusActive &&
		*request.Status != state.AccessKeyStatusDisabled {
		return AccessKeyResponse{}, app_errors.ErrValidation
	}
	var filters *AccessKeyFilters
	var encodedFilters []byte
	if request.Filters != nil {
		normalized, err := normalizeAccessKeyFilters(request.Filters)
		if err != nil {
			return AccessKeyResponse{}, err
		}
		encoded, err := json.Marshal(normalized)
		if err != nil {
			return AccessKeyResponse{}, fmt.Errorf("encode access key filters: %w", err)
		}
		filters = &normalized
		encodedFilters = encoded
	}

	var result AccessKeyResponse
	_, err := s.writeConfig(ctx, func(tx *gorm.DB) error {
		var row models.AccessKey
		if err := tx.First(&row, id).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		plaintext, err := s.encryption.Decrypt(row.KeyValue)
		if err != nil {
			return fmt.Errorf("decrypt access key %d: %w", row.ID, err)
		}
		currentFilters, err := decodeStoredAccessKeyFilters(row.Filters)
		if err != nil {
			return fmt.Errorf("decode access key %d filters: %w", row.ID, err)
		}
		status := state.AccessKeyStatus(row.Status)
		if status != state.AccessKeyStatusActive && status != state.AccessKeyStatusDisabled {
			return fmt.Errorf("access key %d has invalid status", row.ID)
		}

		updates := make(map[string]any, 3)
		if name != nil {
			row.Name = *name
			updates["name"] = row.Name
		}
		if request.Status != nil {
			status = *request.Status
			updates["status"] = string(status)
		}
		if filters != nil {
			if err := validateFilterGroupReferences(tx, filters.Groups); err != nil {
				return err
			}
			currentFilters = *filters
			updates["filters"] = models.JSON(encodedFilters)
		}
		if err := tx.Model(&row).Updates(updates).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		result = AccessKeyResponse{
			ID: row.ID, Name: row.Name, Key: plaintext, Status: status, Filters: currentFilters,
		}
		return nil
	}, nil)
	if err != nil {
		return AccessKeyResponse{}, err
	}
	return result, nil
}

func (s *Service) DeleteAccessKey(ctx context.Context, id uint) error {
	if id == 0 {
		return app_errors.ErrBadRequest
	}
	_, err := s.writeConfig(ctx, func(tx *gorm.DB) error {
		var row models.AccessKey
		if err := tx.Select("id").First(&row, id).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		if err := tx.Delete(&row).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		return nil
	}, nil)
	return err
}

func normalizeAccessKeyName(raw string) (string, error) {
	normalized, err := normalizeGroupName(&raw)
	if err != nil {
		return "", err
	}
	return *normalized, nil
}

func normalizeAccessKeyFilters(input *AccessKeyFilters) (AccessKeyFilters, error) {
	result := AccessKeyFilters{
		Groups: make([]uint, 0), Protocols: make([]protocol.Protocol, 0), Models: make([]string, 0),
	}
	if input == nil {
		return result, nil
	}

	seenGroups := make(map[uint]struct{}, len(input.Groups))
	for _, groupID := range input.Groups {
		if groupID == 0 {
			return AccessKeyFilters{}, app_errors.ErrValidation
		}
		if _, duplicate := seenGroups[groupID]; duplicate {
			continue
		}
		seenGroups[groupID] = struct{}{}
		result.Groups = append(result.Groups, groupID)
	}
	seenProtocols := make(map[protocol.Protocol]struct{}, len(input.Protocols))
	for _, value := range input.Protocols {
		if !value.Valid() {
			return AccessKeyFilters{}, app_errors.ErrValidation
		}
		if _, duplicate := seenProtocols[value]; duplicate {
			continue
		}
		seenProtocols[value] = struct{}{}
		result.Protocols = append(result.Protocols, value)
	}
	seenModels := make(map[string]struct{}, len(input.Models))
	for _, value := range input.Models {
		normalized := strings.TrimSpace(value)
		if normalized == "" {
			return AccessKeyFilters{}, app_errors.ErrValidation
		}
		if _, duplicate := seenModels[normalized]; duplicate {
			continue
		}
		seenModels[normalized] = struct{}{}
		result.Models = append(result.Models, normalized)
	}
	return result, nil
}

func validateFilterGroupReferences(tx *gorm.DB, groupIDs []uint) error {
	if len(groupIDs) == 0 {
		return nil
	}
	var count int64
	if err := tx.Model(&models.Group{}).Where("id IN ?", groupIDs).Count(&count).Error; err != nil {
		return app_errors.ParseDBError(err)
	}
	if count != int64(len(groupIDs)) {
		return app_errors.ErrValidation
	}
	return nil
}

func decodeStoredAccessKeyFilters(raw models.JSON) (AccessKeyFilters, error) {
	if len(raw) == 0 {
		return normalizeAccessKeyFilters(nil)
	}
	var decoded AccessKeyFilters
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return AccessKeyFilters{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return AccessKeyFilters{}, fmt.Errorf("multiple JSON values")
		}
		return AccessKeyFilters{}, err
	}
	return normalizeAccessKeyFilters(&decoded)
}
