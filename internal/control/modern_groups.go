package control

import (
	"context"
	"encoding/json"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/storage/models"
)

// ModernGroupItem 只提供工作区需要的展示事实，不改变分组的配置或健康状态。
type ModernGroupItem struct {
	ID                     uint                            `json:"id"`
	Name                   string                          `json:"name"`
	ChannelID              channel.ID                      `json:"channel_id"`
	ChannelName            string                          `json:"channel_name"`
	ChannelMark            string                          `json:"channel_mark"`
	ChannelIcon            string                          `json:"channel_icon"`
	ConnectionType         models.ConnectionType           `json:"connection_type"`
	Endpoint               string                          `json:"endpoint"`
	Enabled                bool                            `json:"enabled"`
	Availability           string                          `json:"availability"`
	Weight                 int                             `json:"weight"`
	PriceMultiplier        string                          `json:"price_multiplier"`
	ModelCount             int64                           `json:"model_count"`
	ModelNames             []string                        `json:"model_names"`
	Credentials            GroupCollectionCredentialCounts `json:"credentials"`
	LastActiveHourMS       *int64                          `json:"last_active_hour_ms"`
	LastActiveHourRequests int64                           `json:"last_active_hour_requests"`
}

type ModernGroupsResponse struct {
	AutoModels   []string          `json:"auto_models"`
	ObservedAtMS int64             `json:"observed_at_ms"`
	Items        []ModernGroupItem `json:"items"`
}

func (s *Service) ListModernGroups(ctx context.Context) (ModernGroupsResponse, error) {
	observedAt, records, err := s.captureGroupCollectionRecords(ctx, true)
	if err != nil {
		return ModernGroupsResponse{}, err
	}
	channels, err := s.ListChannels(ctx, "")
	if err != nil {
		return ModernGroupsResponse{}, err
	}
	byID := make(map[channel.ID]ChannelListItem, len(channels.Items))
	for _, item := range channels.Items {
		byID[item.ID] = item
	}
	result := ModernGroupsResponse{AutoModels: s.autoModelNames(), ObservedAtMS: observedAt, Items: make([]ModernGroupItem, 0, len(records))}
	for _, record := range records {
		var params struct {
			BaseURL string `json:"base_url"`
		}
		if err := json.Unmarshal(record.Params, &params); err != nil {
			return ModernGroupsResponse{}, err
		}
		definition := byID[record.ChannelID]
		endpoint := params.BaseURL
		if endpoint == "" {
			endpoint = definition.DefaultBaseURL
		}
		result.Items = append(result.Items, ModernGroupItem{
			ID: record.ID, Name: record.Name, ChannelID: record.ChannelID,
			ChannelName: definition.Name, ChannelMark: definition.Mark, ChannelIcon: definition.Icon,
			ConnectionType: record.ConnectionType, Endpoint: endpoint,
			Enabled: record.Enabled, Availability: modernGroupAvailability(record),
			Weight: record.Weight, PriceMultiplier: record.PriceMultiplier,
			ModelCount:             record.ModelCount,
			ModelNames:             record.ModelNames,
			Credentials:            record.CredentialCounts,
			LastActiveHourMS:       record.LastActiveAtMS,
			LastActiveHourRequests: record.LastActiveHourRequestCount,
		})
	}
	return result, nil
}

func modernGroupAvailability(record groupCollectionRecord) string {
	switch {
	case record.Status == GroupCollectionStatusDisabled:
		return "paused"
	case record.CredentialCounts.Total == 0:
		return "no_credentials"
	case record.ModelCount == 0:
		return "no_models"
	case record.CredentialCounts.Available == 0:
		return "unavailable"
	case record.CredentialCounts.Available < record.CredentialCounts.Total || record.CredentialCounts.ModelCooldown > 0:
		return "limited"
	default:
		return "ready"
	}
}

func (s *Server) handleListModernGroups(c *gin.Context) {
	if c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery {
		writeServiceError(c, "list_modern_groups", app_errors.ErrBadRequest)
		return
	}
	result, err := s.service.ListModernGroups(c.Request.Context())
	if err != nil {
		writeServiceError(c, "list_modern_groups", err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.SuccessI18n(c, "common.success", result)
}
