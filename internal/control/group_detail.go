package control

import (
	"context"

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

// GroupSummaryResponse contains the group fields required by the detail page header.
// It deliberately excludes models and configuration that are loaded through focused
// resources.
type GroupSummaryResponse struct {
	ID            uint                  `json:"id"`
	Name          string                `json:"name"`
	ProviderID    *string               `json:"provider_id"`
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
			ProviderID:    cloneString(record.ProviderID),
			ServiceStatus: record.Status,
			UpstreamURL:   record.UpstreamURL,
			Protocols:     append([]protocol.Protocol(nil), record.Protocols...),
			KeyCount:      record.KeyCounts.Total,
			ModelCount:    int(record.ModelCount),
		}, nil
	}
	return GroupSummaryResponse{}, app_errors.ErrResourceNotFound
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

func loadGroupRow(db *gorm.DB, groupID uint) (models.Group, error) {
	var group models.Group
	if err := db.Where("id = ?", groupID).Take(&group).Error; err != nil {
		return models.Group{}, app_errors.ParseDBError(err)
	}
	return group, nil
}
