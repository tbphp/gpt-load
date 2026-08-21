package control

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

type GroupCollectionStatus string

const (
	GroupCollectionStatusAvailable   GroupCollectionStatus = "available"
	GroupCollectionStatusUnavailable GroupCollectionStatus = "unavailable"
	GroupCollectionStatusDisabled    GroupCollectionStatus = "disabled"
)

type GroupUnavailableReason string

const (
	GroupUnavailableReasonNoAvailableCredentials GroupUnavailableReason = "no_available_credentials"
	GroupUnavailableReasonNoModels               GroupUnavailableReason = "no_models"
)

type GroupCollectionCredentialCounts struct {
	Total       int64 `json:"total"`
	Available   int64 `json:"available"`
	Cooldown    int64 `json:"cooldown"`
	Blacklisted int64 `json:"blacklisted"`
	Disabled    int64 `json:"disabled"`
}

type GroupCollectionItem struct {
	ID               uint                            `json:"id"`
	Name             string                          `json:"name"`
	ChannelID        channel.ID                      `json:"channel_id"`
	ConnectionType   models.ConnectionType           `json:"connection_type"`
	Params           json.RawMessage                 `json:"params"`
	Status           GroupCollectionStatus           `json:"status"`
	ModelCount       int64                           `json:"model_count"`
	CredentialCounts GroupCollectionCredentialCounts `json:"credential_counts"`
}

type groupCollectionRecord struct {
	GroupCollectionItem
	CreatedAtMS       int64
	UnavailableReason *GroupUnavailableReason
}

type groupCollectionRows struct {
	groups      []models.Group
	credentials []models.Credential
}

func cloneGroupRows(rows []models.Group) []models.Group {
	cloned := make([]models.Group, len(rows))
	for index := range rows {
		cloned[index] = rows[index]
		cloned[index].Params = append(models.JSON(nil), rows[index].Params...)
		cloned[index].Models = append(models.JSON(nil), rows[index].Models...)
		cloned[index].Overrides = append(models.JSON(nil), rows[index].Overrides...)
		if rows[index].WeightManual != nil {
			value := *rows[index].WeightManual
			cloned[index].WeightManual = &value
		}
		if rows[index].ValidationModel != nil {
			value := *rows[index].ValidationModel
			cloned[index].ValidationModel = &value
		}
		cloned[index].Credentials = nil
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
	records, err := mapGroupCollectionRecords(snapshot, runtimeKeys, rows, observedAt, s.channelRegistry)
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
		rows.groups = cloneGroupRows(groups)
		var credentials []models.Credential
		if err := tx.Model(&models.Credential{}).
			Select("id", "group_id", "fingerprint", "identity_fingerprint", "secret_version", "status", "weight_manual").
			Order("group_id ASC, id ASC").Find(&credentials).Error; err != nil {
			return err
		}
		rows.credentials = cloneCredentialRows(credentials)
		return nil
	})
	return rows, err
}

func mapGroupCollectionRecords(
	snapshot *state.ConfigSnapshot,
	runtimeKeys []state.CredentialRuntimeView,
	rows groupCollectionRows,
	observedAt time.Time,
	registries ...*channel.Registry,
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

	runtimeByID := make(map[uint]state.CredentialRuntimeView, len(runtimeKeys))
	for _, key := range runtimeKeys {
		if key.ID == 0 {
			return nil, groupCollectionDataError("runtime key has zero id")
		}
		if _, duplicate := runtimeByID[key.ID]; duplicate {
			return nil, groupCollectionDataError("duplicate runtime key %d", key.ID)
		}
		runtimeByID[key.ID] = key
	}
	var registry *channel.Registry
	for _, candidate := range registries {
		if candidate != nil {
			registry = candidate
			break
		}
	}
	credentialsByGroup := make(map[uint][]models.Credential)
	persistedCredentialByID := make(map[uint]models.Credential, len(rows.credentials))
	for _, credential := range rows.credentials {
		if credential.ID == 0 {
			return nil, groupCollectionDataError("persisted credential has zero id")
		}
		if _, duplicate := persistedCredentialByID[credential.ID]; duplicate {
			return nil, groupCollectionDataError("duplicate persisted credential %d", credential.ID)
		}
		_, exists := persistedGroups[credential.GroupID]
		if !exists {
			return nil, groupCollectionDataError(
				"persisted credential %d references missing group %d",
				credential.ID,
				credential.GroupID,
			)
		}
		persistedCredentialByID[credential.ID] = credential
		credentialsByGroup[credential.GroupID] = append(credentialsByGroup[credential.GroupID], credential)
	}
	if len(persistedCredentialByID) != len(runtimeByID) {
		return nil, groupCollectionDataError("persisted and runtime credential sets differ")
	}
	for credentialID, persistedCredential := range persistedCredentialByID {
		runtimeCredential, exists := runtimeByID[credentialID]
		if !exists {
			return nil, groupCollectionDataError(
				"persisted credential %d is missing from runtime registry",
				credentialID,
			)
		}
		status, err := groupCollectionRuntimeCredentialStatus(persistedCredential.Status)
		if err != nil {
			return nil, err
		}
		if runtimeCredential.ID != persistedCredential.ID ||
			runtimeCredential.GroupID != persistedCredential.GroupID ||
			runtimeCredential.Status != status ||
			runtimeCredential.Version != groupCollectionCredentialVersion(persistedCredential.SecretVersion) ||
			runtimeCredential.IdentityGeneration != groupCollectionCredentialIdentity(
				persistedCredential.IdentityFingerprint,
				persistedGroups[persistedCredential.GroupID],
			) ||
			!equalGroupCollectionWeight(runtimeCredential.WeightManual, persistedCredential.WeightManual) {
			return nil, groupCollectionDataError(
				"persisted credential %d differs from runtime registry",
				credentialID,
			)
		}
	}
	records := make([]groupCollectionRecord, 0, len(rows.groups))
	for _, group := range rows.groups {
		channelID := channel.ID(group.ChannelID)
		if registry == nil {
			return nil, groupCollectionDataError("channel registry is nil")
		}
		validated, err := registry.ValidateParams(channelID, json.RawMessage(group.Params))
		if err != nil {
			return nil, groupCollectionDataError("validate group %d params: %v", group.ID, err)
		}
		params := validated.CanonicalJSON()
		if _, err := registry.Resolve(channelID, params); err != nil {
			return nil, groupCollectionDataError("resolve group %d channel: %v", group.ID, err)
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
				ID: group.ID, Name: group.Name, ChannelID: channelID,
				ConnectionType: normalizeGroupConnectionType(group.ConnectionType),
				Params:         append(json.RawMessage(nil), params...),
				ModelCount:     int64(len(groupModels)),
			},
			CreatedAtMS: group.CreatedAtMS,
		}
		for _, persistedCredential := range credentialsByGroup[group.ID] {
			bucket := classifyHealthKey(catalog, runtimeByID[persistedCredential.ID], observedAt)
			addGroupCollectionCredentialCount(&record.CredentialCounts, bucket)
		}
		record.Status, record.UnavailableReason = groupCollectionStatusAndReason(
			catalog,
			record.CredentialCounts,
			record.ModelCount,
		)
		records = append(records, record)
	}
	return records, nil
}

func cloneCredentialRows(rows []models.Credential) []models.Credential {
	cloned := make([]models.Credential, len(rows))
	for index := range rows {
		cloned[index] = rows[index]
		cloned[index].Group = nil
		cloned[index].Data = ""
		cloned[index].WeightManual = cloneInt(rows[index].WeightManual)
	}
	return cloned
}

func groupCollectionRuntimeCredentialStatus(status models.CredentialStatus) (state.CredentialStatus, error) {
	switch status {
	case models.CredentialStatusActive:
		return state.CredentialStatusActive, nil
	case models.CredentialStatusDisabled:
		return state.CredentialStatusDisabled, nil
	default:
		return "", groupCollectionDataError("invalid persisted credential status %q", status)
	}
}

func groupCollectionCredentialVersion(secretVersion uint64) uint64 {
	if secretVersion < 1 {
		return 1
	}
	return secretVersion
}

func groupCollectionCredentialIdentity(fingerprint string, group models.Group) uint64 {
	return stateloader.CredentialIdentityGeneration(
		fingerprint,
		group.ChannelID,
		string(group.ConnectionType),
		json.RawMessage(group.Params),
	)
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

func addGroupCollectionCredentialCount(counts *GroupCollectionCredentialCounts, bucket healthBucket) {
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
	counts GroupCollectionCredentialCounts,
	modelCount int64,
) GroupCollectionStatus {
	status, _ := groupCollectionStatusAndReason(
		group,
		counts,
		modelCount,
	)
	return status
}

func groupCollectionStatusAndReason(
	group state.GroupCatalogView,
	counts GroupCollectionCredentialCounts,
	modelCount int64,
) (GroupCollectionStatus, *GroupUnavailableReason) {
	if !group.Enabled || (group.WeightManual != nil && *group.WeightManual == 0) {
		return GroupCollectionStatusDisabled, nil
	}
	if counts.Available == 0 {
		reason := GroupUnavailableReasonNoAvailableCredentials
		return GroupCollectionStatusUnavailable, &reason
	}
	if modelCount > 0 {
		return GroupCollectionStatusAvailable, nil
	}
	reason := GroupUnavailableReasonNoModels
	return GroupCollectionStatusUnavailable, &reason
}
