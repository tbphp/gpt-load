package services

import (
	"context"

	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/models"
	"gpt-load/internal/utils"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// SubGroupInput defines the input payload for aggregate group member configuration.
type SubGroupInput struct {
	GroupID uint `json:"group_id"`
	Weight  int  `json:"weight"`
}

// AggregateValidationResult captures the normalized aggregate group parameters.
type AggregateValidationResult struct {
	ValidationEndpoint string
	SubGroups          []models.GroupSubGroup
}

// AggregateGroupService encapsulates aggregate group specific behaviours.
type AggregateGroupService struct {
	db           *gorm.DB
	groupManager *GroupManager
}

// NewAggregateGroupService constructs an AggregateGroupService instance.
func NewAggregateGroupService(db *gorm.DB, groupManager *GroupManager) *AggregateGroupService {
	return &AggregateGroupService{
		db:           db,
		groupManager: groupManager,
	}
}

// ValidateSubGroups validates sub-groups with an optional existing validation endpoint for consistency check.
func (s *AggregateGroupService) ValidateSubGroups(ctx context.Context, channelType string, inputs []SubGroupInput, existingEndpoint string) (*AggregateValidationResult, error) {
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

	// If there's an existing endpoint, use it as the expected endpoint
	if existingEndpoint != "" {
		validationEndpoint = existingEndpoint
	}

	for _, sg := range subGroupModels {
		if sg.GroupType == "aggregate" {
			return nil, NewI18nError(app_errors.ErrValidation, "validation.sub_group_cannot_be_aggregate", nil)
		}
		if sg.ChannelType != channelType {
			return nil, NewI18nError(app_errors.ErrValidation, "validation.sub_group_channel_mismatch", nil)
		}

		// If no existing endpoint, use the first sub-group's effective endpoint
		if validationEndpoint == "" {
			validationEndpoint = utils.GetValidationEndpoint(&sg)
		} else if validationEndpoint != utils.GetValidationEndpoint(&sg) {
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

// GetSubGroups returns sub groups for an aggregate group
func (s *AggregateGroupService) GetSubGroups(ctx context.Context, groupID uint) ([]models.SubGroupInfo, error) {
	var group models.Group
	if err := s.db.WithContext(ctx).First(&group, groupID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, NewI18nError(app_errors.ErrResourceNotFound, "group.not_found", nil)
		}
		return nil, err
	}

	if group.GroupType != "aggregate" {
		return nil, NewI18nError(app_errors.ErrBadRequest, "group.not_aggregate", nil)
	}

	var groupSubGroups []models.GroupSubGroup
	if err := s.db.WithContext(ctx).Where("group_id = ?", groupID).Find(&groupSubGroups).Error; err != nil {
		return nil, err
	}

	if len(groupSubGroups) == 0 {
		return []models.SubGroupInfo{}, nil
	}

	subGroupIDs := make([]uint, 0, len(groupSubGroups))
	weightMap := make(map[uint]int, len(groupSubGroups))

	for _, gsg := range groupSubGroups {
		subGroupIDs = append(subGroupIDs, gsg.SubGroupID)
		weightMap[gsg.SubGroupID] = gsg.Weight
	}

	var subGroupModels []models.Group
	if err := s.db.WithContext(ctx).Where("id IN ?", subGroupIDs).Find(&subGroupModels).Error; err != nil {
		return nil, err
	}

	subGroups := make([]models.SubGroupInfo, 0, len(subGroupModels))
	for _, subGroup := range subGroupModels {
		subGroups = append(subGroups, models.SubGroupInfo{
			GroupID:     subGroup.ID,
			Name:        subGroup.Name,
			DisplayName: subGroup.DisplayName,
			Weight:      weightMap[subGroup.ID],
		})
	}

	return subGroups, nil
}

// AddSubGroups adds new sub groups to an aggregate group
func (s *AggregateGroupService) AddSubGroups(ctx context.Context, groupID uint, inputs []SubGroupInput) error {
	var group models.Group
	if err := s.db.WithContext(ctx).First(&group, groupID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return NewI18nError(app_errors.ErrResourceNotFound, "group.not_found", nil)
		}
		return err
	}

	if group.GroupType != "aggregate" {
		return NewI18nError(app_errors.ErrBadRequest, "group.not_aggregate", nil)
	}

	// Check if there are existing sub groups and get their validation endpoint
	var existingEndpoint string
	var existingSubGroup models.GroupSubGroup
	if err := s.db.WithContext(ctx).Where("group_id = ?", groupID).First(&existingSubGroup).Error; err == nil {
		// If we have existing sub groups, get one of their validation endpoints
		var existingGroup models.Group
		if err := s.db.WithContext(ctx).First(&existingGroup, existingSubGroup.SubGroupID).Error; err == nil {
			existingEndpoint = utils.GetValidationEndpoint(&existingGroup)
		}
	}

	// Validate sub groups with existing endpoint for consistency
	result, err := s.ValidateSubGroups(ctx, group.ChannelType, inputs, existingEndpoint)
	if err != nil {
		return err
	}

	// Manually query existing sub groups
	var existingSubGroups []models.GroupSubGroup
	if err := s.db.WithContext(ctx).Where("group_id = ?", groupID).Find(&existingSubGroups).Error; err != nil {
		return err
	}

	// Check for duplicates with existing sub groups
	existingSubGroupIDs := make(map[uint]bool)
	for _, sg := range existingSubGroups {
		existingSubGroupIDs[sg.SubGroupID] = true
	}

	for _, newSg := range result.SubGroups {
		if existingSubGroupIDs[newSg.SubGroupID] {
			return NewI18nError(app_errors.ErrBadRequest, "group.sub_group_already_exists",
				map[string]any{"sub_group_id": newSg.SubGroupID})
		}
	}

	// Add new sub groups
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, newSg := range result.SubGroups {
			newSg.GroupID = groupID
			if err := tx.Create(&newSg).Error; err != nil {
				return app_errors.ParseDBError(err)
			}
		}

		// 触发缓存更新
		if err := s.groupManager.Invalidate(); err != nil {
			logrus.WithContext(ctx).WithError(err).Error("failed to invalidate group cache after adding sub groups")
		}

		return nil
	})
}

// UpdateSubGroupWeight updates the weight of a specific sub group
func (s *AggregateGroupService) UpdateSubGroupWeight(ctx context.Context, groupID, subGroupID uint, weight int) error {
	var group models.Group
	if err := s.db.WithContext(ctx).First(&group, groupID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return NewI18nError(app_errors.ErrResourceNotFound, "group.not_found", nil)
		}
		return err
	}

	if group.GroupType != "aggregate" {
		return NewI18nError(app_errors.ErrBadRequest, "group.not_aggregate", nil)
	}

	if weight < 0 {
		return NewI18nError(app_errors.ErrValidation, "validation.sub_group_weight_negative", nil)
	}

	if weight > 1000 {
		return NewI18nError(app_errors.ErrValidation, "validation.sub_group_weight_max_exceeded", nil)
	}

	// 检查子分组关联是否存在
	var existingRecord models.GroupSubGroup
	if err := s.db.WithContext(ctx).Where("group_id = ? AND sub_group_id = ?", groupID, subGroupID).First(&existingRecord).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return NewI18nError(app_errors.ErrResourceNotFound, "group.sub_group_not_found", nil)
		}
		return err
	}

	result := s.db.WithContext(ctx).
		Model(&models.GroupSubGroup{}).
		Where("group_id = ? AND sub_group_id = ?", groupID, subGroupID).
		Update("weight", weight)

	if result.Error != nil {
		return result.Error
	}

	if result.RowsAffected == 0 {
		return NewI18nError(app_errors.ErrResourceNotFound, "group.sub_group_not_found", nil)
	}

	// 触发缓存更新
	if err := s.groupManager.Invalidate(); err != nil {
		logrus.WithContext(ctx).WithError(err).Error("failed to invalidate group cache after updating sub group weight")
	}

	return nil
}

// DeleteSubGroup removes a sub group from an aggregate group
func (s *AggregateGroupService) DeleteSubGroup(ctx context.Context, groupID, subGroupID uint) error {
	var group models.Group
	if err := s.db.WithContext(ctx).First(&group, groupID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return NewI18nError(app_errors.ErrResourceNotFound, "group.not_found", nil)
		}
		return err
	}

	if group.GroupType != "aggregate" {
		return NewI18nError(app_errors.ErrBadRequest, "group.not_aggregate", nil)
	}

	result := s.db.WithContext(ctx).
		Where("group_id = ? AND sub_group_id = ?", groupID, subGroupID).
		Delete(&models.GroupSubGroup{})

	if result.Error != nil {
		return result.Error
	}

	if result.RowsAffected == 0 {
		return NewI18nError(app_errors.ErrResourceNotFound, "group.sub_group_not_found", nil)
	}

	// 触发缓存更新
	if err := s.groupManager.Invalidate(); err != nil {
		logrus.WithContext(ctx).WithError(err).Error("failed to invalidate group cache after deleting sub group")
	}

	return nil
}
