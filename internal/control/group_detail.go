package control

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

type GroupEffectiveConfigResponse struct {
	ConnectTimeout     int64               `json:"connect_timeout"`
	FirstByteTimeout   int64               `json:"first_byte_timeout"`
	RequestTimeout     int64               `json:"request_timeout"`
	StreamIdleTimeout  int64               `json:"stream_idle_timeout"`
	HeaderRules        HeaderRulesResponse `json:"header_rules"`
	InjectUsageOptions bool                `json:"inject_usage_options"`
}

type GroupDetailResponse struct {
	ID              uint                         `json:"id"`
	Name            string                       `json:"name"`
	UpstreamURL     string                       `json:"upstream_url"`
	Protocols       []protocol.Protocol          `json:"protocols"`
	Models          []GroupModel                 `json:"models"`
	Enabled         bool                         `json:"enabled"`
	ValidationModel *string                      `json:"validation_model"`
	WeightManual    *int                         `json:"weight_manual"`
	Config          config.Settings              `json:"config"`
	EffectiveConfig GroupEffectiveConfigResponse `json:"effective_config"`
	KeyCount        int64                        `json:"key_count"`
}

// GroupSummaryResponse contains the group fields required by the detail page header.
// It deliberately excludes models and configuration that are loaded through focused
// resources.
type GroupSummaryResponse struct {
	ID            uint                  `json:"id"`
	Name          string                `json:"name"`
	ServiceStatus GroupCollectionStatus `json:"service_status"`
	UpstreamURL   string                `json:"upstream_url"`
	Protocols     []protocol.Protocol   `json:"protocols"`
	KeyCount      int64                 `json:"key_count"`
	ModelCount    int                   `json:"model_count"`
}

func (s *Service) GetGroupSummary(ctx context.Context, groupID uint) (GroupSummaryResponse, error) {
	if groupID == 0 {
		return GroupSummaryResponse{}, app_errors.ErrBadRequest
	}
	_, records, err := s.captureGroupCollectionRecords(ctx)
	if err != nil {
		return GroupSummaryResponse{}, err
	}
	for _, record := range records {
		if record.ID != groupID {
			continue
		}
		return GroupSummaryResponse{
			ID:            record.ID,
			Name:          record.Name,
			ServiceStatus: record.Status,
			UpstreamURL:   record.UpstreamURL,
			Protocols:     append([]protocol.Protocol(nil), record.Protocols...),
			KeyCount:      record.KeyCounts.Total,
			ModelCount:    int(record.ModelCount),
		}, nil
	}
	return GroupSummaryResponse{}, app_errors.ErrResourceNotFound
}

func (s *Service) GetGroup(ctx context.Context, groupID uint) (GroupDetailResponse, error) {
	if groupID == 0 {
		return GroupDetailResponse{}, app_errors.ErrBadRequest
	}

	s.writeMu.RLock()
	defer s.writeMu.RUnlock()

	result, _, err := loadGroupDetail(s.db.WithContext(ctx), groupID)
	if err != nil {
		return GroupDetailResponse{}, err
	}
	snapshot := s.manager.Current()
	if snapshot == nil {
		return GroupDetailResponse{}, fmt.Errorf("runtime snapshot unavailable: %w", app_errors.ErrInternalServer)
	}
	result.EffectiveConfig, err = effectiveGroupConfig(snapshot.Settings, result.Config)
	if err != nil {
		return GroupDetailResponse{}, fmt.Errorf(
			"resolve group %d effective config: %w",
			groupID,
			app_errors.ErrInternalServer,
		)
	}
	return result, nil
}

func effectiveGroupConfig(
	system state.RuntimeSettings,
	overrides config.Settings,
) (GroupEffectiveConfigResponse, error) {
	resolved, err := state.ResolveGroupRuntimeSettings(system, overrides)
	if err != nil {
		return GroupEffectiveConfigResponse{}, err
	}
	set := make(map[string]string, len(resolved.HeaderRules.Set))
	for name, value := range resolved.HeaderRules.Set {
		set[name] = value
	}
	return GroupEffectiveConfigResponse{
		ConnectTimeout:    durationSeconds(resolved.Timeouts.Connect),
		FirstByteTimeout:  durationSeconds(resolved.Timeouts.FirstByte),
		RequestTimeout:    durationSeconds(resolved.Timeouts.Request),
		StreamIdleTimeout: durationSeconds(resolved.Timeouts.StreamIdle),
		HeaderRules: HeaderRulesResponse{
			Set:    set,
			Remove: append([]string{}, resolved.HeaderRules.Remove...),
		},
		InjectUsageOptions: resolved.InjectUsageOptions,
	}, nil
}

func loadGroupDetail(db *gorm.DB, groupID uint) (GroupDetailResponse, models.Group, error) {
	var group models.Group
	if err := db.Where("id = ?", groupID).Take(&group).Error; err != nil {
		return GroupDetailResponse{}, models.Group{}, app_errors.ParseDBError(err)
	}
	var keyCount int64
	if err := db.Model(&models.UpstreamKey{}).
		Where("group_id = ?", groupID).
		Count(&keyCount).Error; err != nil {
		return GroupDetailResponse{}, models.Group{}, app_errors.ParseDBError(err)
	}
	result, err := mapPersistedGroupDetail(group, keyCount)
	if err != nil {
		return GroupDetailResponse{}, models.Group{}, err
	}
	return result, group, nil
}

func mapPersistedGroupDetail(group models.Group, keyCount int64) (GroupDetailResponse, error) {
	protocols := make([]protocol.Protocol, 0)
	if err := decodeGroupDiscoveryJSON(group.Protocols, &protocols); err != nil {
		return GroupDetailResponse{}, fmt.Errorf("decode group %d protocols: %w", group.ID, err)
	}
	groupModels := make([]GroupModel, 0)
	if err := decodeGroupDiscoveryJSON(group.Models, &groupModels); err != nil {
		return GroupDetailResponse{}, fmt.Errorf("decode group %d models: %w", group.ID, err)
	}
	settings := make(config.Settings)
	if len(group.Config) > 0 {
		if err := decodeGroupDiscoveryJSON(group.Config, &settings); err != nil {
			return GroupDetailResponse{}, fmt.Errorf("decode group %d config: %w", group.ID, err)
		}
	}
	if settings == nil {
		settings = make(config.Settings)
	}
	var validationModel *string
	if group.ValidationModel != nil {
		value := *group.ValidationModel
		validationModel = &value
	}
	return GroupDetailResponse{
		ID: group.ID, Name: group.Name, UpstreamURL: group.UpstreamURL,
		Protocols: protocols, Models: groupModels, Enabled: group.Enabled,
		ValidationModel: validationModel,
		WeightManual:    cloneInt(group.WeightManual),
		Config:          settings, KeyCount: keyCount,
	}, nil
}
