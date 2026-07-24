package control

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/storage/models"
)

type GroupDetailResponse struct {
	ID              uint                `json:"id"`
	Name            string              `json:"name"`
	UpstreamURL     string              `json:"upstream_url"`
	Protocols       []protocol.Protocol `json:"protocols"`
	Models          []GroupModel        `json:"models"`
	Enabled         bool                `json:"enabled"`
	ValidationModel *string             `json:"validation_model"`
	WeightManual    *int                `json:"weight_manual"`
	Config          config.Settings     `json:"config"`
	KeyCount        int64               `json:"key_count"`
}

func (s *Service) GetGroup(ctx context.Context, groupID uint) (GroupDetailResponse, error) {
	if groupID == 0 {
		return GroupDetailResponse{}, app_errors.ErrBadRequest
	}
	result, _, err := loadGroupDetail(s.db.WithContext(ctx), groupID)
	return result, err
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
