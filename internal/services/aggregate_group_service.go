package services

import (
	"context"

	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/models"

	"gorm.io/gorm"
)

// SubGroupInput defines the input payload for aggregate group member configuration.
type SubGroupInput struct {
	GroupID uint
	Weight  int
}

// AggregateValidationResult captures the normalized aggregate group parameters.
type AggregateValidationResult struct {
	ValidationEndpoint string
	SubGroups          []models.GroupSubGroup
}

// AggregateGroupService encapsulates aggregate group specific behaviours.
type AggregateGroupService struct {
	db *gorm.DB
}

// NewAggregateGroupService constructs an AggregateGroupService instance.
func NewAggregateGroupService(db *gorm.DB) *AggregateGroupService {
	return &AggregateGroupService{db: db}
}

// ValidateSubGroups ensures the provided sub-groups are valid for aggregate usage.
func (s *AggregateGroupService) ValidateSubGroups(ctx context.Context, channelType string, inputs []SubGroupInput) (*AggregateValidationResult, error) {
	if len(inputs) == 0 {
		return nil, NewI18nError(app_errors.ErrValidation, "validation.sub_groups_required", nil)
	}

	subGroupIDs := make([]uint, 0, len(inputs))
	for _, input := range inputs {
		if input.GroupID == 0 {
			return nil, NewI18nError(app_errors.ErrValidation, "validation.invalid_sub_group_id", nil)
		}
		if input.Weight < 0 {
			return nil, NewI18nError(app_errors.ErrValidation, "validation.sub_group_weight_negative", nil)
		}
		if input.Weight > 1000 {
			return nil, NewI18nError(app_errors.ErrValidation, "validation.sub_group_weight_max_exceeded", nil)
		}
		subGroupIDs = append(subGroupIDs, input.GroupID)
	}

	var subGroupModels []models.Group
	if err := s.db.WithContext(ctx).Where("id IN ?", subGroupIDs).Find(&subGroupModels).Error; err != nil {
		return nil, app_errors.ParseDBError(err)
	}

	if len(subGroupModels) != len(subGroupIDs) {
		return nil, NewI18nError(app_errors.ErrValidation, "validation.sub_group_not_found", nil)
	}

	subGroupMap := make(map[uint]models.Group, len(subGroupModels))
	var validationEndpoint string
	for idx, sg := range subGroupModels {
		if sg.GroupType == "aggregate" {
			return nil, NewI18nError(app_errors.ErrValidation, "validation.sub_group_cannot_be_aggregate", nil)
		}
		if sg.ChannelType != channelType {
			return nil, NewI18nError(app_errors.ErrValidation, "validation.sub_group_channel_mismatch", nil)
		}
		if idx == 0 {
			validationEndpoint = sg.ValidationEndpoint
		} else if validationEndpoint != sg.ValidationEndpoint {
			return nil, NewI18nError(app_errors.ErrValidation, "validation.sub_group_validation_endpoint_mismatch", nil)
		}
		subGroupMap[sg.ID] = sg
	}

	resultSubGroups := make([]models.GroupSubGroup, 0, len(inputs))
	for _, input := range inputs {
		if _, ok := subGroupMap[input.GroupID]; !ok {
			return nil, NewI18nError(app_errors.ErrValidation, "validation.sub_group_not_found", nil)
		}
		resultSubGroups = append(resultSubGroups, models.GroupSubGroup{
			SubGroupID: input.GroupID,
			Weight:     input.Weight,
		})
	}

	return &AggregateValidationResult{
		ValidationEndpoint: validationEndpoint,
		SubGroups:          resultSubGroups,
	}, nil
}

// LoadSubGroupDetails attaches group details to a set of aggregate relationships.
func (s *AggregateGroupService) LoadSubGroupDetails(ctx context.Context, aggregates []models.GroupSubGroup) error {
	if len(aggregates) == 0 {
		return nil
	}

	idSet := make(map[uint]struct{}, len(aggregates))
	ids := make([]uint, 0, len(aggregates))
	for _, rel := range aggregates {
		if _, exists := idSet[rel.SubGroupID]; exists {
			continue
		}
		idSet[rel.SubGroupID] = struct{}{}
		ids = append(ids, rel.SubGroupID)
	}

	if len(ids) == 0 {
		return nil
	}

	var groups []models.Group
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&groups).Error; err != nil {
		return app_errors.ParseDBError(err)
	}

	groupMap := make(map[uint]models.Group, len(groups))
	for _, g := range groups {
		groupMap[g.ID] = g
	}

	for idx := range aggregates {
		if grp, ok := groupMap[aggregates[idx].SubGroupID]; ok {
			aggregates[idx].SubGroup = grp
		}
	}

	return nil
}

// EnsureSubGroupAssociations persists aggregate relationships for a group ID.
func (s *AggregateGroupService) EnsureSubGroupAssociations(ctx context.Context, tx *gorm.DB, groupID uint, subGroups []models.GroupSubGroup) error {
	if err := tx.WithContext(ctx).Where("group_id = ?", groupID).Delete(&models.GroupSubGroup{}).Error; err != nil {
		return app_errors.ParseDBError(err)
	}

	if len(subGroups) == 0 {
		return nil
	}

	for idx := range subGroups {
		subGroups[idx].GroupID = groupID
	}

	if err := tx.WithContext(ctx).Create(&subGroups).Error; err != nil {
		return app_errors.ParseDBError(err)
	}

	return nil
}

// BuildAggregateResponse converts aggregate relationships to response friendly structures.
func (s *AggregateGroupService) BuildAggregateResponse(relations []models.GroupSubGroup) map[uint][]models.GroupSubGroup {
	if len(relations) == 0 {
		return map[uint][]models.GroupSubGroup{}
	}

	groupMap := make(map[uint][]models.GroupSubGroup)
	for _, rel := range relations {
		groupMap[rel.GroupID] = append(groupMap[rel.GroupID], rel)
	}

	return groupMap
}
