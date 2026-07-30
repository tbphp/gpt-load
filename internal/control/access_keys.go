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

type OptionalRPMLimit struct {
	Set   bool
	Value int64
}

func (value *OptionalRPMLimit) UnmarshalJSON(data []byte) error {
	if value == nil || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return app_errors.ErrValidation
	}
	var decoded int64
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	value.Set = true
	value.Value = decoded
	return nil
}

type AccessKeyCreateRequest struct {
	Name     string            `json:"name"`
	Filters  *AccessKeyFilters `json:"filters"`
	RPMLimit OptionalRPMLimit  `json:"rpm_limit"`
}

type AccessKeyUpdateRequest struct {
	Name     *string                `json:"name"`
	Status   *state.AccessKeyStatus `json:"status"`
	Filters  *AccessKeyFilters      `json:"filters"`
	RPMLimit OptionalRPMLimit       `json:"rpm_limit"`
}

type AccessKeyMetadata struct {
	ID          uint                  `json:"id"`
	Name        string                `json:"name"`
	MaskedKey   string                `json:"masked_key"`
	Status      state.AccessKeyStatus `json:"status"`
	Filters     AccessKeyFilters      `json:"filters"`
	RPMLimit    int64                 `json:"rpm_limit"`
	CreatedAtMS int64                 `json:"created_at_ms"`
	UpdatedAtMS int64                 `json:"updated_at_ms"`
}

type AccessKeyCreateResult struct {
	AccessKeyMetadata
	Key      string `json:"key,omitempty"`
	Replayed bool   `json:"replayed"`
}

type AccessKeyOption struct {
	ID     uint                  `json:"id"`
	Name   string                `json:"name"`
	Status state.AccessKeyStatus `json:"status"`
}

type AccessKeyRevealResult struct {
	ID           uint   `json:"id"`
	Key          string `json:"key"`
	RevealedAtMS int64  `json:"revealed_at_ms"`
}

type accessKeyMetadataRow struct {
	ID          uint
	Name        string
	KeySuffix   string
	Status      string
	Filters     models.JSON
	RPMLimit    int64
	CreatedAtMS int64
	UpdatedAtMS int64
}

const accessKeyPrefix = "sk-gl-"

func (s *Service) newAccessKeyRow(
	name string,
	filters AccessKeyFilters,
	rpmLimit int64,
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
		Name:      name,
		KeyValue:  ciphertext,
		KeyHash:   s.encryption.Hash(plaintext),
		KeySuffix: plaintext[len(plaintext)-4:],
		Status:    string(state.AccessKeyStatusActive),
		Filters:   models.JSON(encodedFilters),
		RPMLimit:  rpmLimit,
	}, plaintext, nil
}

func (s *Service) CreateAccessKey(
	ctx context.Context,
	request AccessKeyCreateRequest,
) (AccessKeyCreateResult, error) {
	name, err := normalizeAccessKeyName(request.Name)
	if err != nil {
		return AccessKeyCreateResult{}, err
	}
	filters, err := normalizeAccessKeyFilters(request.Filters)
	if err != nil {
		return AccessKeyCreateResult{}, err
	}
	rpmLimit, err := normalizeRPMLimit(request.RPMLimit, 0)
	if err != nil {
		return AccessKeyCreateResult{}, err
	}

	var result AccessKeyCreateResult
	_, err = s.writeConfig(ctx, func(tx *gorm.DB) error {
		if err := validateFilterGroupReferences(tx, filters.Groups); err != nil {
			return err
		}
		row, plaintext, err := s.newAccessKeyRow(name, filters, rpmLimit)
		if err != nil {
			return err
		}
		if err := tx.Create(&row).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		metadata, err := mapAccessKeyMetadataRow(accessKeyMetadataRow{
			ID: row.ID, Name: row.Name, KeySuffix: row.KeySuffix,
			Status: row.Status, Filters: row.Filters, RPMLimit: row.RPMLimit,
			CreatedAtMS: row.CreatedAtMS, UpdatedAtMS: row.UpdatedAtMS,
		})
		if err != nil {
			return err
		}
		result = AccessKeyCreateResult{
			AccessKeyMetadata: metadata,
			Key:               plaintext,
		}
		return nil
	}, nil)
	if err != nil {
		return AccessKeyCreateResult{}, err
	}
	return result, nil
}

func (s *Service) ListAccessKeys(ctx context.Context) ([]AccessKeyMetadata, error) {
	var rows []accessKeyMetadataRow
	if err := s.db.WithContext(ctx).
		Model(&models.AccessKey{}).
		Select(
			"id", "name", "key_suffix", "status", "filters", "rpm_limit",
			"created_at_ms", "updated_at_ms",
		).
		Order("id ASC").
		Scan(&rows).Error; err != nil {
		return nil, app_errors.ParseDBError(err)
	}

	result := make([]AccessKeyMetadata, 0, len(rows))
	for _, row := range rows {
		metadata, err := mapAccessKeyMetadataRow(row)
		if err != nil {
			return nil, err
		}
		result = append(result, metadata)
	}
	return result, nil
}

func (s *Service) UpdateAccessKey(
	ctx context.Context,
	id uint,
	request AccessKeyUpdateRequest,
) (AccessKeyMetadata, error) {
	if id == 0 || (request.Name == nil && request.Status == nil && request.Filters == nil && !request.RPMLimit.Set) {
		return AccessKeyMetadata{}, app_errors.ErrBadRequest
	}
	if _, err := normalizeRPMLimit(request.RPMLimit, 0); err != nil {
		return AccessKeyMetadata{}, err
	}

	var name *string
	if request.Name != nil {
		normalized, err := normalizeAccessKeyName(*request.Name)
		if err != nil {
			return AccessKeyMetadata{}, err
		}
		name = &normalized
	}
	if request.Status != nil &&
		*request.Status != state.AccessKeyStatusActive &&
		*request.Status != state.AccessKeyStatusDisabled {
		return AccessKeyMetadata{}, app_errors.ErrValidation
	}
	var filters *AccessKeyFilters
	var encodedFilters []byte
	if request.Filters != nil {
		normalized, err := normalizeAccessKeyFilters(request.Filters)
		if err != nil {
			return AccessKeyMetadata{}, err
		}
		encoded, err := json.Marshal(normalized)
		if err != nil {
			return AccessKeyMetadata{}, fmt.Errorf("encode access key filters: %w", err)
		}
		filters = &normalized
		encodedFilters = encoded
	}

	var result AccessKeyMetadata
	_, err := s.writeConfig(ctx, func(tx *gorm.DB) error {
		var row accessKeyMetadataRow
		if err := tx.Model(&models.AccessKey{}).
			Select(
				"id", "name", "key_suffix", "status", "filters", "rpm_limit",
				"created_at_ms", "updated_at_ms",
			).
			Where("id = ?", id).
			Take(&row).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		if !validAccessKeySuffix(row.KeySuffix) {
			return fmt.Errorf(
				"access key %d has invalid persisted suffix: %w",
				row.ID,
				app_errors.ErrInternalServer,
			)
		}
		currentFilters, err := decodeStoredAccessKeyFilters(row.Filters)
		if err != nil {
			return fmt.Errorf("decode access key %d filters: %w", row.ID, err)
		}
		if filters != nil {
			if err := validateAccessKeyGroupUpdate(
				tx,
				currentFilters.Groups,
				filters.Groups,
			); err != nil {
				return err
			}
		}
		status := state.AccessKeyStatus(row.Status)
		if status != state.AccessKeyStatusActive && status != state.AccessKeyStatusDisabled {
			return fmt.Errorf("access key %d has invalid status", row.ID)
		}

		updates := make(map[string]any, 4)
		if name != nil {
			row.Name = *name
			updates["name"] = row.Name
		}
		if request.Status != nil {
			status = *request.Status
			updates["status"] = string(status)
		}
		if filters != nil {
			currentFilters = *filters
			updates["filters"] = models.JSON(encodedFilters)
		}
		if request.RPMLimit.Set {
			row.RPMLimit = request.RPMLimit.Value
			updates["rpm_limit"] = row.RPMLimit
		}
		if err := tx.Model(&models.AccessKey{}).
			Where("id = ?", row.ID).
			Updates(updates).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		if err := tx.Model(&models.AccessKey{}).
			Select(
				"id", "name", "key_suffix", "status", "filters", "rpm_limit",
				"created_at_ms", "updated_at_ms",
			).
			Where("id = ?", row.ID).
			Take(&row).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		result, err = mapAccessKeyMetadataRow(row)
		return err
	}, nil)
	if err != nil {
		return AccessKeyMetadata{}, err
	}
	return result, nil
}

func (s *Service) ListAccessKeyOptions(ctx context.Context) ([]AccessKeyOption, error) {
	var rows []AccessKeyOption
	if err := s.db.WithContext(ctx).
		Model(&models.AccessKey{}).
		Select("id", "name", "status").
		Order("id ASC").
		Scan(&rows).Error; err != nil {
		return nil, app_errors.ParseDBError(err)
	}
	for _, row := range rows {
		if row.Status != state.AccessKeyStatusActive &&
			row.Status != state.AccessKeyStatusDisabled {
			return nil, fmt.Errorf(
				"access key %d has invalid status: %w",
				row.ID,
				app_errors.ErrInternalServer,
			)
		}
	}
	return rows, nil
}

func (s *Service) RevealAccessKey(
	ctx context.Context,
	id uint,
) (AccessKeyRevealResult, error) {
	if id == 0 {
		return AccessKeyRevealResult{}, app_errors.ErrBadRequest
	}
	var row struct {
		ID       uint
		KeyValue string
	}
	if err := s.db.WithContext(ctx).
		Model(&models.AccessKey{}).
		Select("id", "key_value").
		Where("id = ?", id).
		Take(&row).Error; err != nil {
		return AccessKeyRevealResult{}, app_errors.ParseDBError(err)
	}
	plaintext, err := s.encryption.Decrypt(row.KeyValue)
	if err != nil || !validAccessKeyPlaintext(plaintext) {
		return AccessKeyRevealResult{}, fmt.Errorf(
			"reveal access key %d: %w",
			row.ID,
			app_errors.ErrInternalServer,
		)
	}
	revealedAtMS, err := safeEpochMilliseconds(s.now())
	if err != nil {
		return AccessKeyRevealResult{}, app_errors.ErrInternalServer
	}
	return AccessKeyRevealResult{
		ID:           row.ID,
		Key:          plaintext,
		RevealedAtMS: revealedAtMS,
	}, nil
}

func mapAccessKeyMetadataRow(row accessKeyMetadataRow) (AccessKeyMetadata, error) {
	if err := validateSafeMilliseconds(row.CreatedAtMS); err != nil {
		return AccessKeyMetadata{}, fmt.Errorf(
			"access key %d has invalid created_at_ms: %w",
			row.ID,
			app_errors.ErrInternalServer,
		)
	}
	if err := validateSafeMilliseconds(row.UpdatedAtMS); err != nil {
		return AccessKeyMetadata{}, fmt.Errorf(
			"access key %d has invalid updated_at_ms: %w",
			row.ID,
			app_errors.ErrInternalServer,
		)
	}
	status := state.AccessKeyStatus(row.Status)
	if status != state.AccessKeyStatusActive && status != state.AccessKeyStatusDisabled {
		return AccessKeyMetadata{}, fmt.Errorf(
			"access key %d has invalid status: %w",
			row.ID,
			app_errors.ErrInternalServer,
		)
	}
	if !validAccessKeySuffix(row.KeySuffix) {
		return AccessKeyMetadata{}, fmt.Errorf(
			"access key %d has invalid persisted suffix: %w",
			row.ID,
			app_errors.ErrInternalServer,
		)
	}
	filters, err := decodeStoredAccessKeyFilters(row.Filters)
	if err != nil {
		return AccessKeyMetadata{}, fmt.Errorf(
			"decode access key %d filters: %w",
			row.ID,
			app_errors.ErrInternalServer,
		)
	}
	return AccessKeyMetadata{
		ID: row.ID, Name: row.Name,
		MaskedKey: accessKeyPrefix + "••••••••" + row.KeySuffix,
		Status:    status, Filters: filters, RPMLimit: row.RPMLimit,
		CreatedAtMS: row.CreatedAtMS, UpdatedAtMS: row.UpdatedAtMS,
	}, nil
}

func validAccessKeySuffix(value string) bool {
	if len(value) != 4 {
		return false
	}
	for _, character := range []byte(value) {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}

func validAccessKeyPlaintext(value string) bool {
	if len(value) != len(accessKeyPrefix)+32 ||
		!strings.HasPrefix(value, accessKeyPrefix) {
		return false
	}
	for _, character := range []byte(strings.TrimPrefix(value, accessKeyPrefix)) {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
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

func normalizeRPMLimit(value OptionalRPMLimit, defaultValue int64) (int64, error) {
	if !value.Set {
		return defaultValue, nil
	}
	if value.Value < 0 {
		return 0, app_errors.ErrValidation
	}
	return value.Value, nil
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
		if !value.DataPlaneEnabled() {
			return AccessKeyFilters{}, app_errors.ErrValidation
		}
		seenProtocols[value] = struct{}{}
	}
	for _, value := range protocol.DataPlaneProtocols() {
		if _, exists := seenProtocols[value]; exists {
			result.Protocols = append(result.Protocols, value)
		}
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

func validateAccessKeyGroupUpdate(
	tx *gorm.DB,
	current []uint,
	requested []uint,
) error {
	if len(requested) == 0 {
		return nil
	}
	currentSet := make(map[uint]struct{}, len(current))
	for _, groupID := range current {
		currentSet[groupID] = struct{}{}
	}
	var existing []uint
	if err := tx.Model(&models.Group{}).
		Where("id IN ?", requested).
		Pluck("id", &existing).Error; err != nil {
		return app_errors.ParseDBError(err)
	}
	existingSet := make(map[uint]struct{}, len(existing))
	for _, groupID := range existing {
		existingSet[groupID] = struct{}{}
	}
	for _, groupID := range requested {
		if _, exists := existingSet[groupID]; exists {
			continue
		}
		if _, historical := currentSet[groupID]; !historical {
			return app_errors.ErrValidation
		}
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
