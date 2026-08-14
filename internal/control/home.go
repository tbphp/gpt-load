package control

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gpt-load/internal/platform/epochms"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/platform/version"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

type HomeInventory struct {
	GroupCount               int64 `json:"group_count"`
	CredentialCount          int64 `json:"credential_count"`
	AvailableCredentialCount int64 `json:"available_credential_count"`
	ModelCount               int64 `json:"model_count"`
}

type HomeAccessKey struct {
	ID        uint                `json:"id"`
	Name      string              `json:"name"`
	MaskedKey string              `json:"masked_key"`
	Protocols []protocol.Protocol `json:"protocols"`
}

type HomeBase struct {
	Inventory        HomeInventory            `json:"inventory"`
	AccessKeys       []HomeAccessKey          `json:"access_keys"`
	CurrentAccessKey *AccessKeyCollectionItem `json:"current_access_key"`
}

type homeResponse struct {
	ServerNowMS int64  `json:"server_now_ms"`
	StartedAtMS int64  `json:"started_at_ms"`
	Version     string `json:"version"`
	HomeBase
}

type homeCredentialRow struct {
	ID                  uint
	GroupID             uint
	ChannelID           string
	ConnectionType      models.ConnectionType
	Params              models.JSON
	Fingerprint         string
	IdentityFingerprint string
	SecretVersion       uint64
	Status              models.CredentialStatus
}

type homeAccessKeyRow struct {
	ID              uint
	Name            string
	KeySuffix       string
	Status          string
	Filters         models.JSON
	RPMLimit        int64
	CreatedAtMS     int64
	UpdatedAtMS     int64
	LastRequestAtMS *int64
}

type homeReadRows struct {
	groupCount  int64
	credentials []homeCredentialRow
	accessKeys  []homeAccessKeyRow
}

func (s *Service) ReadHomeBase(
	ctx context.Context,
	nowMS int64,
) (HomeBase, error) {
	return s.readHomeBase(ctx, nowMS, nil)
}

func (s *Service) ReadAccessKeyHomeBase(
	ctx context.Context,
	nowMS int64,
	accessKeyID uint,
) (HomeBase, error) {
	if accessKeyID == 0 {
		return HomeBase{}, app_errors.ErrUnauthorized
	}
	return s.readHomeBase(ctx, nowMS, &accessKeyID)
}

func (s *Service) readHomeBase(
	ctx context.Context,
	nowMS int64,
	accessKeyID *uint,
) (HomeBase, error) {
	if s == nil || s.db == nil || s.manager == nil || s.registrySnapshot == nil {
		return HomeBase{}, fmt.Errorf(
			"read home base: dependencies unavailable: %w",
			app_errors.ErrInternalServer,
		)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateSafeMilliseconds(nowMS); err != nil {
		return HomeBase{}, fmt.Errorf(
			"read home base: invalid observation time: %w",
			app_errors.ErrInternalServer,
		)
	}
	now, err := epochms.ToTime(nowMS)
	if err != nil {
		return HomeBase{}, fmt.Errorf(
			"read home base: invalid observation time: %w",
			app_errors.ErrInternalServer,
		)
	}

	s.writeMu.RLock()
	defer s.writeMu.RUnlock()

	snapshot := s.manager.Current()
	if snapshot == nil {
		return HomeBase{}, fmt.Errorf(
			"read home base: runtime snapshot unavailable: %w",
			app_errors.ErrInternalServer,
		)
	}
	runtimeKeys := s.registrySnapshot()
	rows, err := s.readHomeRows(ctx)
	if err != nil {
		return HomeBase{}, app_errors.ParseDBError(err)
	}

	var allowedGroups map[uint]struct{}
	if accessKeyID != nil {
		accessKey, exists := snapshot.AccessKeysByID[*accessKeyID]
		if !exists || accessKey.Status != state.AccessKeyStatusActive {
			return HomeBase{}, app_errors.ErrUnauthorized
		}
		allowedGroups = accessibleHomeGroups(snapshot, accessKey)
	}

	inventory := HomeInventory{}
	if accessKeyID == nil {
		inventory.GroupCount = rows.groupCount
		inventory.CredentialCount = int64(len(rows.credentials))
		inventory.ModelCount = countHomeModels(snapshot)
	} else {
		inventory.GroupCount = int64(len(allowedGroups))
		inventory.CredentialCount = countHomeCredentials(rows.credentials, allowedGroups)
		inventory.ModelCount = countScopedHomeModels(snapshot, allowedGroups, snapshot.AccessKeysByID[*accessKeyID])
	}
	inventory.AvailableCredentialCount, err = countAvailableHomeCredentialsInGroups(
		snapshot,
		rows.credentials,
		runtimeKeys,
		now,
		allowedGroups,
	)
	if err != nil {
		return HomeBase{}, err
	}
	if err := validateHomeInventory(inventory); err != nil {
		return HomeBase{}, err
	}
	accessKeyRows := rows.accessKeys
	if accessKeyID != nil {
		accessKeyRows = filterHomeAccessKeyRows(rows.accessKeys, *accessKeyID)
		if len(accessKeyRows) != 1 {
			return HomeBase{}, app_errors.ErrUnauthorized
		}
	}
	accessKeys, err := mapHomeAccessKeys(accessKeyRows)
	if err != nil {
		return HomeBase{}, err
	}
	result := HomeBase{
		Inventory:  inventory,
		AccessKeys: accessKeys,
	}
	if accessKeyID != nil {
		current, err := mapHomeCurrentAccessKey(accessKeyRows[0])
		if err != nil {
			return HomeBase{}, err
		}
		result.CurrentAccessKey = &current
	}
	return result, nil
}

func (s *Server) handleHome(c *gin.Context) {
	if c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery {
		writeServiceError(c, "home", app_errors.ErrBadRequest)
		return
	}
	serverNowMS, err := safeEpochMilliseconds(s.now())
	if err != nil {
		writeServiceError(c, "home", err)
		return
	}
	startedAtMS, err := safeEpochMilliseconds(s.startedAt)
	if err != nil {
		writeServiceError(c, "home", err)
		return
	}
	var base HomeBase
	if accessKeyID, scoped := currentAccessKeyID(c); scoped {
		base, err = s.service.ReadAccessKeyHomeBase(
			c.Request.Context(),
			serverNowMS,
			accessKeyID,
		)
	} else {
		base, err = s.service.ReadHomeBase(c.Request.Context(), serverNowMS)
	}
	if err != nil {
		writeServiceError(c, "home", err)
		return
	}
	response.SuccessI18n(c, "common.success", homeResponse{
		ServerNowMS: serverNowMS,
		StartedAtMS: startedAtMS,
		Version:     version.Version,
		HomeBase:    base,
	})
}

func (s *Service) readHomeRows(
	ctx context.Context,
) (homeReadRows, error) {
	var result homeReadRows
	err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		if err := tx.Model(&models.Group{}).Count(&result.groupCount).Error; err != nil {
			return fmt.Errorf("count home groups: %w", err)
		}
		if err := tx.Model(&models.Credential{}).
			Select(
				"credentials.id", "credentials.group_id", "groups.channel_id", "groups.connection_type", "groups.params",
				"credentials.fingerprint", "credentials.identity_fingerprint", "credentials.secret_version", "credentials.status",
			).
			Joins("JOIN groups ON groups.id = credentials.group_id").
			Order("credentials.id ASC").
			Find(&result.credentials).Error; err != nil {
			return fmt.Errorf("query home credentials: %w", err)
		}
		if err := tx.Model(&models.AccessKey{}).
			Select(
				"id", "name", "key_suffix", "status", "filters", "rpm_limit",
				"created_at_ms", "updated_at_ms",
				"(SELECT MAX(request_logs.completed_at_ms) FROM request_logs WHERE request_logs.access_key_id = access_keys.id) AS last_request_at_ms",
			).
			Where("status = ?", state.AccessKeyStatusActive).
			Order("id ASC").
			Find(&result.accessKeys).Error; err != nil {
			return fmt.Errorf("query home access keys: %w", err)
		}
		return nil
	})
	if err != nil {
		return homeReadRows{}, err
	}
	return result, nil
}

func countHomeModels(snapshot *state.ConfigSnapshot) int64 {
	names := make(map[string]struct{})
	for _, group := range snapshot.Groups {
		for _, model := range group.Models {
			name := strings.TrimSpace(model.Alias)
			if name == "" {
				name = strings.TrimSpace(model.ID)
			}
			if name != "" {
				names[name] = struct{}{}
			}
		}
	}
	return int64(len(names))
}

func accessibleHomeGroups(
	snapshot *state.ConfigSnapshot,
	accessKey state.AccessKeyView,
) map[uint]struct{} {
	result := make(map[uint]struct{})
	for groupID, group := range snapshot.Groups {
		if len(accessKey.Filters.Groups) > 0 {
			if _, allowed := accessKey.Filters.Groups[groupID]; !allowed {
				continue
			}
		}
		protocolAllowed := len(accessKey.Filters.Protocols) == 0
		for _, value := range group.ClientProtocols {
			if _, allowed := accessKey.Filters.Protocols[value]; allowed {
				protocolAllowed = true
				break
			}
		}
		if !protocolAllowed {
			continue
		}
		if len(accessKey.Filters.Models) > 0 {
			modelAllowed := false
			for _, model := range group.Models {
				if _, allowed := accessKey.Filters.Models[homeExternalModelName(model)]; allowed {
					modelAllowed = true
					break
				}
			}
			if !modelAllowed {
				continue
			}
		}
		result[groupID] = struct{}{}
	}
	return result
}

func countScopedHomeModels(
	snapshot *state.ConfigSnapshot,
	allowedGroups map[uint]struct{},
	accessKey state.AccessKeyView,
) int64 {
	names := make(map[string]struct{})
	for groupID := range allowedGroups {
		group, exists := snapshot.Groups[groupID]
		if !exists {
			continue
		}
		for _, model := range group.Models {
			name := homeExternalModelName(model)
			if name == "" {
				continue
			}
			if len(accessKey.Filters.Models) > 0 {
				if _, allowed := accessKey.Filters.Models[name]; !allowed {
					continue
				}
			}
			names[name] = struct{}{}
		}
	}
	return int64(len(names))
}

func homeExternalModelName(model state.ModelConfig) string {
	if alias := strings.TrimSpace(model.Alias); alias != "" {
		return alias
	}
	return strings.TrimSpace(model.ID)
}

func countHomeCredentials(
	rows []homeCredentialRow,
	allowedGroups map[uint]struct{},
) int64 {
	var count int64
	for _, row := range rows {
		if _, allowed := allowedGroups[row.GroupID]; allowed {
			count++
		}
	}
	return count
}

func countAvailableHomeCredentials(
	snapshot *state.ConfigSnapshot,
	persisted []homeCredentialRow,
	runtime []state.CredentialRuntimeView,
	now time.Time,
) (int64, error) {
	return countAvailableHomeCredentialsInGroups(snapshot, persisted, runtime, now, nil)
}

func countAvailableHomeCredentialsInGroups(
	snapshot *state.ConfigSnapshot,
	persisted []homeCredentialRow,
	runtime []state.CredentialRuntimeView,
	now time.Time,
	allowedGroups map[uint]struct{},
) (int64, error) {
	runtimeByID := make(map[uint]state.CredentialRuntimeView, len(runtime))
	for _, credential := range runtime {
		if credential.ID == 0 {
			return 0, fmt.Errorf(
				"count available home credentials: runtime credential has zero id: %w",
				app_errors.ErrInternalServer,
			)
		}
		if _, duplicate := runtimeByID[credential.ID]; duplicate {
			return 0, fmt.Errorf(
				"count available home credentials: duplicate runtime credential %d: %w",
				credential.ID,
				app_errors.ErrInternalServer,
			)
		}
		runtimeByID[credential.ID] = credential
	}

	seen := make(map[uint]struct{}, len(persisted))
	var available int64
	for _, row := range persisted {
		if row.ID == 0 {
			return 0, fmt.Errorf(
				"count available home credentials: persisted credential has zero id: %w",
				app_errors.ErrInternalServer,
			)
		}
		if _, duplicate := seen[row.ID]; duplicate {
			return 0, fmt.Errorf(
				"count available home credentials: duplicate persisted credential %d: %w",
				row.ID,
				app_errors.ErrInternalServer,
			)
		}
		seen[row.ID] = struct{}{}
		group, groupExists := snapshot.GroupCatalog[row.GroupID]
		credential, credentialExists := runtimeByID[row.ID]
		if !groupExists || !credentialExists || credential.GroupID != row.GroupID {
			return 0, fmt.Errorf(
				"count available home credential %d: runtime configuration mismatch: %w",
				row.ID,
				app_errors.ErrInternalServer,
			)
		}

		var status state.CredentialStatus
		switch row.Status {
		case models.CredentialStatusActive:
			status = state.CredentialStatusActive
		case models.CredentialStatusDisabled:
			status = state.CredentialStatusDisabled
		default:
			return 0, fmt.Errorf(
				"count available home credential %d: invalid persisted status: %w",
				row.ID,
				app_errors.ErrInternalServer,
			)
		}
		if credential.Status != status ||
			credential.Version != groupCollectionCredentialVersion(row.SecretVersion) ||
			credential.IdentityGeneration != groupCollectionCredentialIdentity(
				row.IdentityFingerprint,
				models.Group{
					ID: row.GroupID, ChannelID: row.ChannelID,
					ConnectionType: row.ConnectionType, Params: row.Params,
				},
			) {
			return 0, fmt.Errorf(
				"count available home credential %d: runtime identity mismatch: %w",
				row.ID,
				app_errors.ErrInternalServer,
			)
		}
		_, groupAllowed := allowedGroups[row.GroupID]
		if (allowedGroups == nil || groupAllowed) && group.Enabled &&
			status == state.CredentialStatusActive &&
			credential.RuntimeState(now) == state.CredentialRuntimeAvailable {
			available++
		}
	}
	if len(runtimeByID) != len(seen) {
		return 0, fmt.Errorf(
			"count available home credentials: runtime credential set mismatch: %w",
			app_errors.ErrInternalServer,
		)
	}
	return available, nil
}

func mapHomeAccessKeys(rows []homeAccessKeyRow) ([]HomeAccessKey, error) {
	result := make([]HomeAccessKey, 0, len(rows))
	for _, row := range rows {
		if uint64(row.ID) > uint64(maxSafeInteger) {
			return nil, fmt.Errorf(
				"map home access key %d: unsafe id: %w",
				row.ID,
				app_errors.ErrInternalServer,
			)
		}
		if !validAccessKeySuffix(row.KeySuffix) {
			return nil, fmt.Errorf(
				"map home access key %d: invalid persisted suffix: %w",
				row.ID,
				app_errors.ErrInternalServer,
			)
		}
		filters, err := decodeStoredAccessKeyFilters(row.Filters)
		if err != nil {
			return nil, fmt.Errorf(
				"map home access key %d filters: %w",
				row.ID,
				app_errors.ErrInternalServer,
			)
		}
		protocols := filters.Protocols
		if len(protocols) == 0 {
			protocols = protocol.DataPlaneProtocols()
		} else {
			protocols = append([]protocol.Protocol(nil), protocols...)
		}
		result = append(result, HomeAccessKey{
			ID: row.ID, Name: row.Name,
			MaskedKey: maskedAccessKey(row.KeySuffix),
			Protocols: protocols,
		})
	}
	return result, nil
}

func filterHomeAccessKeyRows(rows []homeAccessKeyRow, accessKeyID uint) []homeAccessKeyRow {
	result := make([]homeAccessKeyRow, 0, 1)
	for _, row := range rows {
		if row.ID == accessKeyID {
			result = append(result, row)
		}
	}
	return result
}

func mapHomeCurrentAccessKey(row homeAccessKeyRow) (AccessKeyCollectionItem, error) {
	metadata, err := mapAccessKeyMetadataRow(accessKeyMetadataRow{
		ID: row.ID, Name: row.Name, KeySuffix: row.KeySuffix,
		Status: row.Status, Filters: row.Filters, RPMLimit: row.RPMLimit,
		CreatedAtMS: row.CreatedAtMS, UpdatedAtMS: row.UpdatedAtMS,
	})
	if err != nil {
		return AccessKeyCollectionItem{}, err
	}
	if row.LastRequestAtMS != nil {
		if err := validateSafeMilliseconds(*row.LastRequestAtMS); err != nil {
			return AccessKeyCollectionItem{}, fmt.Errorf(
				"map home current access key last request: %w",
				err,
			)
		}
	}
	return AccessKeyCollectionItem{
		AccessKeyMetadata: metadata,
		LastRequestAtMS:   row.LastRequestAtMS,
	}, nil
}

func validateHomeInventory(value HomeInventory) error {
	for _, field := range []struct {
		name  string
		value int64
	}{
		{name: "group count", value: value.GroupCount},
		{name: "credential count", value: value.CredentialCount},
		{
			name:  "available credential count",
			value: value.AvailableCredentialCount,
		},
		{name: "model count", value: value.ModelCount},
	} {
		if field.value < 0 || field.value > maxSafeInteger {
			return fmt.Errorf(
				"map home inventory %s: unsafe integer: %w",
				field.name,
				app_errors.ErrInternalServer,
			)
		}
	}
	return nil
}
