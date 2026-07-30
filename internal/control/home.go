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
	GroupCount                int64 `json:"group_count"`
	UpstreamKeyCount          int64 `json:"upstream_key_count"`
	AvailableUpstreamKeyCount int64 `json:"available_upstream_key_count"`
	ModelCount                int64 `json:"model_count"`
}

type HomeAccessKey struct {
	ID        uint                `json:"id"`
	Name      string              `json:"name"`
	MaskedKey string              `json:"masked_key"`
	Protocols []protocol.Protocol `json:"protocols"`
}

type HomeBase struct {
	Inventory  HomeInventory   `json:"inventory"`
	AccessKeys []HomeAccessKey `json:"access_keys"`
}

type homeResponse struct {
	ServerNowMS int64  `json:"server_now_ms"`
	StartedAtMS int64  `json:"started_at_ms"`
	Version     string `json:"version"`
	HomeBase
}

type homeUpstreamKeyRow struct {
	ID      uint
	GroupID uint
	Status  models.UpstreamKeyStatus
}

type homeAccessKeyRow struct {
	ID        uint
	Name      string
	KeySuffix string
	Filters   models.JSON
}

type homeReadRows struct {
	groupCount   int64
	upstreamKeys []homeUpstreamKeyRow
	accessKeys   []homeAccessKeyRow
}

func (s *Service) ReadHomeBase(
	ctx context.Context,
	nowMS int64,
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

	inventory := HomeInventory{
		GroupCount:       rows.groupCount,
		UpstreamKeyCount: int64(len(rows.upstreamKeys)),
		ModelCount:       countHomeModels(snapshot),
	}
	inventory.AvailableUpstreamKeyCount, err = countAvailableHomeKeys(
		snapshot,
		rows.upstreamKeys,
		runtimeKeys,
		now,
	)
	if err != nil {
		return HomeBase{}, err
	}
	if err := validateHomeInventory(inventory); err != nil {
		return HomeBase{}, err
	}
	accessKeys, err := mapHomeAccessKeys(rows.accessKeys)
	if err != nil {
		return HomeBase{}, err
	}
	return HomeBase{
		Inventory:  inventory,
		AccessKeys: accessKeys,
	}, nil
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
	base, err := s.service.ReadHomeBase(c.Request.Context(), serverNowMS)
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
		if err := tx.Model(&models.UpstreamKey{}).
			Select("id", "group_id", "status").
			Order("id ASC").
			Find(&result.upstreamKeys).Error; err != nil {
			return fmt.Errorf("query home upstream keys: %w", err)
		}
		if err := tx.Model(&models.AccessKey{}).
			Select("id", "name", "key_suffix", "filters").
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

func countAvailableHomeKeys(
	snapshot *state.ConfigSnapshot,
	persisted []homeUpstreamKeyRow,
	runtime []state.KeyRuntimeView,
	now time.Time,
) (int64, error) {
	runtimeByID := make(map[uint]state.KeyRuntimeView, len(runtime))
	for _, key := range runtime {
		if key.ID == 0 {
			return 0, fmt.Errorf(
				"count available home keys: runtime key has zero id: %w",
				app_errors.ErrInternalServer,
			)
		}
		if _, duplicate := runtimeByID[key.ID]; duplicate {
			return 0, fmt.Errorf(
				"count available home keys: duplicate runtime key %d: %w",
				key.ID,
				app_errors.ErrInternalServer,
			)
		}
		runtimeByID[key.ID] = key
	}

	seen := make(map[uint]struct{}, len(persisted))
	var available int64
	for _, row := range persisted {
		if row.ID == 0 {
			return 0, fmt.Errorf(
				"count available home keys: persisted key has zero id: %w",
				app_errors.ErrInternalServer,
			)
		}
		if _, duplicate := seen[row.ID]; duplicate {
			return 0, fmt.Errorf(
				"count available home keys: duplicate persisted key %d: %w",
				row.ID,
				app_errors.ErrInternalServer,
			)
		}
		seen[row.ID] = struct{}{}
		group, groupExists := snapshot.GroupCatalog[row.GroupID]
		key, keyExists := runtimeByID[row.ID]
		if !groupExists || !keyExists || key.GroupID != row.GroupID {
			return 0, fmt.Errorf(
				"count available home key %d: runtime configuration mismatch: %w",
				row.ID,
				app_errors.ErrInternalServer,
			)
		}

		var status state.KeyStatus
		switch row.Status {
		case models.UpstreamKeyStatusActive:
			status = state.KeyStatusActive
		case models.UpstreamKeyStatusDisabled:
			status = state.KeyStatusDisabled
		default:
			return 0, fmt.Errorf(
				"count available home key %d: invalid persisted status: %w",
				row.ID,
				app_errors.ErrInternalServer,
			)
		}
		if key.Status != status {
			return 0, fmt.Errorf(
				"count available home key %d: runtime status mismatch: %w",
				row.ID,
				app_errors.ErrInternalServer,
			)
		}
		if group.Enabled &&
			status == state.KeyStatusActive &&
			key.RuntimeState(now) == state.KeyRuntimeAvailable {
			available++
		}
	}
	if len(runtimeByID) != len(seen) {
		return 0, fmt.Errorf(
			"count available home keys: runtime key set mismatch: %w",
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
			MaskedKey: accessKeyPrefix + "••••••••" + row.KeySuffix,
			Protocols: protocols,
		})
	}
	return result, nil
}

func validateHomeInventory(value HomeInventory) error {
	for _, field := range []struct {
		name  string
		value int64
	}{
		{name: "group count", value: value.GroupCount},
		{name: "upstream key count", value: value.UpstreamKeyCount},
		{
			name:  "available upstream key count",
			value: value.AvailableUpstreamKeyCount,
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
