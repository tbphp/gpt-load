package control

import (
	"context"
	"encoding/json"
	"fmt"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/storage/models"
)

type GroupModel struct {
	ID    string `json:"id"`
	Alias string `json:"alias"`
}

type GroupResponse struct {
	ID          uint                `json:"id"`
	Name        string              `json:"name"`
	UpstreamURL string              `json:"upstream_url"`
	Protocols   []protocol.Protocol `json:"protocols"`
	Models      []GroupModel        `json:"models"`
	Enabled     bool                `json:"enabled"`
	KeyCount    int64               `json:"key_count"`
}

func (s *Service) ListGroups(ctx context.Context) ([]GroupResponse, error) {
	var groups []models.Group
	if err := s.db.WithContext(ctx).Order("id ASC").Find(&groups).Error; err != nil {
		return nil, app_errors.ParseDBError(err)
	}

	type keyCountRow struct {
		GroupID uint
		Count   int64
	}
	var countRows []keyCountRow
	if err := s.db.WithContext(ctx).Model(&models.UpstreamKey{}).
		Select("group_id, COUNT(*) AS count").Group("group_id").Find(&countRows).Error; err != nil {
		return nil, app_errors.ParseDBError(err)
	}
	counts := make(map[uint]int64, len(countRows))
	for _, row := range countRows {
		counts[row.GroupID] = row.Count
	}

	result := make([]GroupResponse, 0, len(groups))
	for _, group := range groups {
		var protocols []protocol.Protocol
		if err := json.Unmarshal(group.Protocols, &protocols); err != nil {
			return nil, fmt.Errorf("decode group %d protocols: %w", group.ID, err)
		}
		var groupModels []GroupModel
		if err := json.Unmarshal(group.Models, &groupModels); err != nil {
			return nil, fmt.Errorf("decode group %d models: %w", group.ID, err)
		}
		result = append(result, GroupResponse{
			ID: group.ID, Name: group.Name, UpstreamURL: group.UpstreamURL,
			Protocols: protocols, Models: groupModels, Enabled: group.Enabled,
			KeyCount: counts[group.ID],
		})
	}
	return result, nil
}
