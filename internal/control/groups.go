package control

import (
	"context"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"

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

type groupKeyCountRow struct {
	GroupID uint
	Count   int64
}

type listGroupsSnapshotRows struct {
	groups    []models.Group
	keyCounts []groupKeyCountRow
}

func (s *Service) readListGroupsSnapshot(
	ctx context.Context,
) (listGroupsSnapshotRows, error) {
	var rows listGroupsSnapshotRows
	err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		var groups []models.Group
		if err := tx.Order("id ASC").Find(&groups).Error; err != nil {
			return err
		}
		var keyCounts []groupKeyCountRow
		if err := tx.Model(&models.UpstreamKey{}).
			Select("group_id, COUNT(*) AS count").
			Group("group_id").
			Find(&keyCounts).Error; err != nil {
			return err
		}
		rows.groups = cloneGroupRows(groups)
		rows.keyCounts = append([]groupKeyCountRow(nil), keyCounts...)
		return nil
	})
	if err != nil {
		return listGroupsSnapshotRows{}, err
	}
	return rows, nil
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
		if rows[index].ValidationModel != nil {
			value := *rows[index].ValidationModel
			cloned[index].ValidationModel = &value
		}
		cloned[index].UpstreamKeys = nil
	}
	return cloned
}

func mapListGroupsSnapshot(
	rows listGroupsSnapshotRows,
) ([]GroupResponse, error) {
	counts := make(map[uint]int64, len(rows.keyCounts))
	for _, row := range rows.keyCounts {
		counts[row.GroupID] = row.Count
	}

	result := make([]GroupResponse, 0, len(rows.groups))
	for _, group := range rows.groups {
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

func (s *Service) ListGroups(ctx context.Context) ([]GroupResponse, error) {
	rows, err := s.readListGroupsSnapshot(ctx)
	if parentErr := ctx.Err(); parentErr != nil {
		return nil, parentErr
	}
	if err != nil {
		return nil, app_errors.ParseDBError(err)
	}
	result, err := mapListGroupsSnapshot(rows)
	if parentErr := ctx.Err(); parentErr != nil {
		return nil, parentErr
	}
	return result, err
}
