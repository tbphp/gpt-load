package control

import (
	"context"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
)

type GroupModelsUpdateRequest struct {
	Models optionalGroupModels `json:"models"`
}

type GroupModelResponse struct {
	ID            string        `json:"id"`
	Alias         string        `json:"alias"`
	AliasEnabled  bool          `json:"alias_enabled"`
	ClientModel   string        `json:"client_model"`
	PricingStatus PricingStatus `json:"pricing_status"`
}

type GroupModelsResponse struct {
	Items   []GroupModelResponse `json:"items"`
	Total   int                  `json:"total"`
	Pending int                  `json:"pending"`
}

type ModelNameConflict struct {
	ClientModel string `json:"client_model"`
	Indexes     []int  `json:"indexes"`
}

type ModelNameConflictData struct {
	Conflicts []ModelNameConflict `json:"conflicts"`
}

func (s *Service) GetGroupModels(ctx context.Context, groupID uint) (GroupModelsResponse, error) {
	if groupID == 0 {
		return GroupModelsResponse{}, app_errors.ErrBadRequest
	}

	s.writeMu.RLock()
	defer s.writeMu.RUnlock()

	group, err := loadGroupRow(s.db.WithContext(ctx), groupID)
	if err != nil {
		return GroupModelsResponse{}, err
	}
	groupModels := make([]GroupModel, 0)
	if err := decodeGroupDiscoveryJSON(group.Models, &groupModels); err != nil {
		return GroupModelsResponse{}, fmt.Errorf("decode group %d models: %w", group.ID, err)
	}
	scopeKey, err := PriceScopeKeyForGroup(group)
	if err != nil {
		return GroupModelsResponse{}, fmt.Errorf("validate group %d price scope: %w", group.ID, app_errors.ErrInternalServer)
	}
	rows, err := loadPriceRowsByScope(ctx, s.db, scopeKey)
	if err != nil {
		return GroupModelsResponse{}, err
	}

	return mapGroupModelsResponse(groupModels, rows)
}

func mapGroupModelsResponse(
	groupModels []GroupModel,
	rows map[string]*models.ModelPrice,
) (GroupModelsResponse, error) {
	result := GroupModelsResponse{Items: make([]GroupModelResponse, 0, len(groupModels))}
	for _, model := range groupModels {
		item := GroupModelResponse{
			ID:            model.ID,
			Alias:         model.Alias,
			AliasEnabled:  model.Alias != "",
			ClientModel:   model.ID,
			PricingStatus: PricingStatusPending,
		}
		if item.AliasEnabled {
			item.ClientModel = model.Alias
		}
		item.PricingStatus = resolvePricingStatus(rows[model.ID])
		if item.PricingStatus == PricingStatusPending {
			result.Pending++
		}
		result.Items = append(result.Items, item)
	}
	result.Total = len(result.Items)
	return result, nil
}

func (s *Service) UpdateGroupModels(
	ctx context.Context,
	groupID uint,
	request GroupModelsUpdateRequest,
) (GroupModelsResponse, error) {
	if groupID == 0 {
		return GroupModelsResponse{}, app_errors.ErrBadRequest
	}
	if !request.Models.Set {
		return GroupModelsResponse{}, app_errors.ErrValidation
	}
	normalized, err := normalizeGroupModels(request.Models.Values)
	if err != nil {
		return GroupModelsResponse{}, err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return GroupModelsResponse{}, fmt.Errorf("encode group models: %w", err)
	}

	modelIDsChanged := false
	_, err = s.writeGroupConfig(ctx, func(tx *gorm.DB) error {
		group, err := loadGroupRow(tx, groupID)
		if err != nil {
			return err
		}
		if err := validateGroupRowCandidate(ctx, tx, group); err != nil {
			return fmt.Errorf("validate existing group %d: %w", groupID, app_errors.ErrInternalServer)
		}
		var previous []GroupModel
		if err := decodeGroupDiscoveryJSON(group.Models, &previous); err != nil {
			return fmt.Errorf("decode group %d models: %w", groupID, app_errors.ErrInternalServer)
		}
		modelIDsChanged = group.ProviderID != nil && !sameGroupModelIDs(previous, normalized)

		group.Models = models.JSON(encoded)
		if err := validateGroupRowCandidate(ctx, tx, group); err != nil {
			return app_errors.ErrValidation
		}
		if err := tx.Model(&models.Group{}).
			Where("id = ?", groupID).
			Update("models", group.Models).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		return nil
	}, nil)
	if err != nil {
		return GroupModelsResponse{}, withControlOperationContext(err, groupID, 0)
	}
	if modelIDsChanged && s.catalogSync != nil {
		s.catalogSync.RequestGroupSync()
	}
	result, err := s.GetGroupModels(ctx, groupID)
	if err != nil {
		return GroupModelsResponse{}, fmt.Errorf(
			"load group %d models after update: %w",
			groupID,
			app_errors.ErrInternalServer,
		)
	}
	return result, nil
}

func sameGroupModelIDs(left, right []GroupModel) bool {
	if len(left) != len(right) {
		return false
	}
	ids := make(map[string]struct{}, len(left))
	for _, model := range left {
		ids[model.ID] = struct{}{}
	}
	if len(ids) != len(right) {
		return false
	}
	for _, model := range right {
		if _, exists := ids[model.ID]; !exists {
			return false
		}
	}
	return true
}
