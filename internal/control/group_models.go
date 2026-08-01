package control

import (
	"context"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

type GroupModelsUpdateRequest struct {
	Models optionalGroupModels `json:"models"`
}

type GroupModelResponse struct {
	ID            string `json:"id"`
	Alias         string `json:"alias"`
	AliasEnabled  bool   `json:"alias_enabled"`
	ClientModel   string `json:"client_model"`
	PricingStatus string `json:"pricing_status"`
}

type GroupModelsResponse struct {
	Items    []GroupModelResponse `json:"items"`
	Total    int                  `json:"total"`
	Unpriced int                  `json:"unpriced"`
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

	detail, _, err := loadGroupDetail(s.db.WithContext(ctx), groupID)
	if err != nil {
		return GroupModelsResponse{}, err
	}
	table := s.priceRuntime.Load()
	if table == nil {
		return GroupModelsResponse{}, fmt.Errorf("pricing runtime unavailable: %w", app_errors.ErrInternalServer)
	}

	return mapGroupModelsResponse(detail.Models, table), nil
}

func mapGroupModelsResponse(models []GroupModel, table *pricing.Table) GroupModelsResponse {
	result := GroupModelsResponse{Items: make([]GroupModelResponse, 0, len(models))}
	for _, model := range models {
		item := GroupModelResponse{
			ID:            model.ID,
			Alias:         model.Alias,
			AliasEnabled:  model.Alias != "",
			ClientModel:   model.ID,
			PricingStatus: "unpriced",
		}
		if item.AliasEnabled {
			item.ClientModel = model.Alias
		}
		if _, priced := table.Match(model.ID); priced {
			item.PricingStatus = "priced"
		} else {
			result.Unpriced++
		}
		result.Items = append(result.Items, item)
	}
	result.Total = len(result.Items)
	return result
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

	_, err = s.writeConfig(ctx, func(tx *gorm.DB) error {
		_, group, err := loadGroupDetail(tx, groupID)
		if err != nil {
			return err
		}
		if err := validateGroupRowCandidate(ctx, tx, group); err != nil {
			return fmt.Errorf("validate existing group %d: %w", groupID, app_errors.ErrInternalServer)
		}

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
