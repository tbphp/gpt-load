package control

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

type GroupCollectionStatus string

const (
	GroupCollectionStatusAvailable   GroupCollectionStatus = "available"
	GroupCollectionStatusUnavailable GroupCollectionStatus = "unavailable"
	GroupCollectionStatusDisabled    GroupCollectionStatus = "disabled"
)

type GroupCollectionKeyCounts struct {
	Total       int64 `json:"total"`
	Available   int64 `json:"available"`
	Cooldown    int64 `json:"cooldown"`
	Blacklisted int64 `json:"blacklisted"`
	Disabled    int64 `json:"disabled"`
}

type GroupCollectionItem struct {
	ID          uint                     `json:"id"`
	Name        string                   `json:"name"`
	ProviderID  *string                  `json:"provider_id"`
	Status      GroupCollectionStatus    `json:"status"`
	UpstreamURL string                   `json:"upstream_url"`
	Protocols   []protocol.Protocol      `json:"protocols"`
	ModelCount  int64                    `json:"model_count"`
	KeyCounts   GroupCollectionKeyCounts `json:"key_counts"`
}

type groupCollectionRecord struct {
	GroupCollectionItem
	CreatedAtMS int64
}

type groupCollectionRows struct {
	groups []models.Group
	keys   []models.UpstreamKey
}

func cloneGroupRows(rows []models.Group) []models.Group {
	cloned := make([]models.Group, len(rows))
	for index := range rows {
		cloned[index] = rows[index]
		cloned[index].Protocols = append(models.JSON(nil), rows[index].Protocols...)
		cloned[index].Models = append(models.JSON(nil), rows[index].Models...)
		cloned[index].Config = append(models.JSON(nil), rows[index].Config...)
		if rows[index].WeightManual != nil {
			value := *rows[index].WeightManual
			cloned[index].WeightManual = &value
		}
		if rows[index].ProviderID != nil {
			value := *rows[index].ProviderID
			cloned[index].ProviderID = &value
		}
		if rows[index].ValidationModel != nil {
			value := *rows[index].ValidationModel
			cloned[index].ValidationModel = &value
		}
		cloned[index].UpstreamKeys = nil
	}
	return cloned
}

func (s *Service) captureGroupCollectionRecords(
	ctx context.Context,
) (int64, []groupCollectionRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	if s == nil || s.db == nil || s.manager == nil ||
		s.registrySnapshot == nil || s.now == nil {
		return 0, nil, fmt.Errorf(
			"capture group collection: dependencies unavailable: %w",
			app_errors.ErrInternalServer,
		)
	}

	s.writeMu.RLock()
	if err := ctx.Err(); err != nil {
		s.writeMu.RUnlock()
		return 0, nil, err
	}
	snapshot := s.manager.Current()
	runtimeKeys := s.registrySnapshot()
	observedAt := s.now().UTC()
	rows, err := s.readGroupCollectionRows(ctx)
	s.writeMu.RUnlock()
	if parentErr := ctx.Err(); parentErr != nil {
		return 0, nil, parentErr
	}
	if err != nil {
		return 0, nil, app_errors.ParseDBError(err)
	}
	records, err := mapGroupCollectionRecords(snapshot, runtimeKeys, rows, observedAt)
	if parentErr := ctx.Err(); parentErr != nil {
		return 0, nil, parentErr
	}
	if err != nil {
		return 0, nil, err
	}
	return observedAt.UnixMilli(), records, nil
}

func (s *Service) readGroupCollectionRows(
	ctx context.Context,
) (groupCollectionRows, error) {
	var rows groupCollectionRows
	err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		var groups []models.Group
		if err := tx.Order("id ASC").Find(&groups).Error; err != nil {
			return err
		}
		var keys []models.UpstreamKey
		if err := tx.Model(&models.UpstreamKey{}).
			Select("id", "group_id", "status", "weight_manual").
			Order("group_id ASC, id ASC").Find(&keys).Error; err != nil {
			return err
		}
		rows.groups = cloneGroupRows(groups)
		rows.keys = cloneUpstreamKeyRows(keys)
		return nil
	})
	return rows, err
}

func mapGroupCollectionRecords(
	snapshot *state.ConfigSnapshot,
	runtimeKeys []state.KeyRuntimeView,
	rows groupCollectionRows,
	observedAt time.Time,
) ([]groupCollectionRecord, error) {
	if snapshot == nil {
		return nil, groupCollectionDataError("runtime snapshot is nil")
	}

	persistedGroups := make(map[uint]models.Group, len(rows.groups))
	for _, group := range rows.groups {
		if group.ID == 0 {
			return nil, groupCollectionDataError("persisted group has zero id")
		}
		if _, duplicate := persistedGroups[group.ID]; duplicate {
			return nil, groupCollectionDataError("duplicate persisted group %d", group.ID)
		}
		persistedGroups[group.ID] = group
	}
	for groupID, catalog := range snapshot.GroupCatalog {
		if groupID == 0 || catalog.ID == 0 {
			return nil, groupCollectionDataError("runtime catalog group has zero id")
		}
		if catalog.ID != groupID {
			return nil, groupCollectionDataError(
				"runtime catalog group key %d has id %d",
				groupID,
				catalog.ID,
			)
		}
	}
	if len(persistedGroups) != len(snapshot.GroupCatalog) {
		return nil, groupCollectionDataError("persisted and runtime group sets differ")
	}
	for groupID, group := range persistedGroups {
		catalog, exists := snapshot.GroupCatalog[groupID]
		if !exists {
			return nil, groupCollectionDataError(
				"persisted group %d is missing from runtime catalog",
				groupID,
			)
		}
		if catalog.ID != group.ID ||
			catalog.Name != group.Name ||
			catalog.Enabled != group.Enabled ||
			!equalGroupCollectionWeight(catalog.WeightManual, group.WeightManual) {
			return nil, groupCollectionDataError(
				"persisted group %d differs from runtime catalog",
				groupID,
			)
		}
	}

	runtimeByID := make(map[uint]state.KeyRuntimeView, len(runtimeKeys))
	for _, key := range runtimeKeys {
		if key.ID == 0 {
			return nil, groupCollectionDataError("runtime key has zero id")
		}
		if _, duplicate := runtimeByID[key.ID]; duplicate {
			return nil, groupCollectionDataError("duplicate runtime key %d", key.ID)
		}
		runtimeByID[key.ID] = key
	}
	persistedByID := make(map[uint]models.UpstreamKey, len(rows.keys))
	keysByGroup := make(map[uint][]models.UpstreamKey)
	for _, key := range rows.keys {
		if key.ID == 0 {
			return nil, groupCollectionDataError("persisted key has zero id")
		}
		if _, duplicate := persistedByID[key.ID]; duplicate {
			return nil, groupCollectionDataError("duplicate persisted key %d", key.ID)
		}
		if _, exists := persistedGroups[key.GroupID]; !exists {
			return nil, groupCollectionDataError(
				"persisted key %d references missing group %d",
				key.ID,
				key.GroupID,
			)
		}
		persistedByID[key.ID] = key
		keysByGroup[key.GroupID] = append(keysByGroup[key.GroupID], key)
	}
	if len(persistedByID) != len(runtimeByID) {
		return nil, groupCollectionDataError("persisted and runtime key sets differ")
	}
	for keyID, persistedKey := range persistedByID {
		runtimeKey, exists := runtimeByID[keyID]
		if !exists {
			return nil, groupCollectionDataError(
				"persisted key %d is missing from runtime registry",
				keyID,
			)
		}
		status, err := groupCollectionRuntimeKeyStatus(persistedKey.Status)
		if err != nil {
			return nil, err
		}
		if runtimeKey.ID != persistedKey.ID ||
			runtimeKey.GroupID != persistedKey.GroupID ||
			runtimeKey.Status != status ||
			!equalGroupCollectionWeight(runtimeKey.WeightManual, persistedKey.WeightManual) {
			return nil, groupCollectionDataError(
				"persisted key %d differs from runtime registry",
				keyID,
			)
		}
	}

	records := make([]groupCollectionRecord, 0, len(rows.groups))
	for _, group := range rows.groups {
		var protocols []protocol.Protocol
		if err := json.Unmarshal(group.Protocols, &protocols); err != nil {
			return nil, groupCollectionDataError(
				"decode group %d protocols: %v",
				group.ID,
				err,
			)
		}
		if err := validateGroupCollectionProtocols(protocols); err != nil {
			return nil, groupCollectionDataError(
				"validate group %d protocols: %v",
				group.ID,
				err,
			)
		}
		var groupModels []GroupModel
		if err := json.Unmarshal(group.Models, &groupModels); err != nil {
			return nil, groupCollectionDataError(
				"decode group %d models: %v",
				group.ID,
				err,
			)
		}
		if err := validateGroupCollectionModels(groupModels); err != nil {
			return nil, groupCollectionDataError(
				"validate group %d models: %v",
				group.ID,
				err,
			)
		}

		catalog := snapshot.GroupCatalog[group.ID]
		record := groupCollectionRecord{
			GroupCollectionItem: GroupCollectionItem{
				ID: group.ID, Name: group.Name, UpstreamURL: group.UpstreamURL,
				ProviderID: cloneString(group.ProviderID),
				Protocols:  append([]protocol.Protocol(nil), protocols...),
				ModelCount: int64(len(groupModels)),
			},
			CreatedAtMS: group.CreatedAtMS,
		}
		for _, persistedKey := range keysByGroup[group.ID] {
			bucket := classifyHealthKey(catalog, runtimeByID[persistedKey.ID], observedAt)
			addGroupCollectionKeyCount(&record.KeyCounts, bucket)
		}
		record.Status = groupCollectionStatus(
			catalog,
			record.KeyCounts,
			record.Protocols,
			record.ModelCount,
		)
		records = append(records, record)
	}
	return records, nil
}

func groupCollectionDataError(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), app_errors.ErrInternalServer)
}

func equalGroupCollectionWeight(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func groupCollectionRuntimeKeyStatus(
	status models.UpstreamKeyStatus,
) (state.KeyStatus, error) {
	switch status {
	case models.UpstreamKeyStatusActive:
		return state.KeyStatusActive, nil
	case models.UpstreamKeyStatusDisabled:
		return state.KeyStatusDisabled, nil
	default:
		return "", groupCollectionDataError("invalid persisted key status %q", status)
	}
}

func validateGroupCollectionProtocols(values []protocol.Protocol) error {
	if len(values) == 0 {
		return fmt.Errorf("protocols are required")
	}
	seen := make(map[protocol.Protocol]struct{}, len(values))
	for _, value := range values {
		if !value.Valid() {
			return fmt.Errorf("invalid protocol %q", value)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("duplicate protocol %q", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateGroupCollectionModels(values []GroupModel) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		id := strings.TrimSpace(value.ID)
		if id == "" {
			return fmt.Errorf("model id is required")
		}
		external := strings.TrimSpace(value.Alias)
		if external == "" {
			external = id
		}
		if _, duplicate := seen[external]; duplicate {
			return fmt.Errorf("duplicate external model %q", external)
		}
		seen[external] = struct{}{}
	}
	return nil
}

func addGroupCollectionKeyCount(counts *GroupCollectionKeyCounts, bucket healthBucket) {
	counts.Total++
	switch bucket {
	case healthBucketAvailable:
		counts.Available++
	case healthBucketCooldown:
		counts.Cooldown++
	case healthBucketBlacklisted:
		counts.Blacklisted++
	case healthBucketDisabled:
		counts.Disabled++
	}
}

func groupCollectionStatus(
	group state.GroupCatalogView,
	counts GroupCollectionKeyCounts,
	protocols []protocol.Protocol,
	modelCount int64,
) GroupCollectionStatus {
	if !group.Enabled || (group.WeightManual != nil && *group.WeightManual == 0) {
		return GroupCollectionStatusDisabled
	}
	if counts.Available == 0 {
		return GroupCollectionStatusUnavailable
	}
	if modelCount > 0 {
		return GroupCollectionStatusAvailable
	}
	for _, value := range protocols {
		if value.SupportsModelOptionalRequests() {
			return GroupCollectionStatusAvailable
		}
	}
	return GroupCollectionStatusUnavailable
}
