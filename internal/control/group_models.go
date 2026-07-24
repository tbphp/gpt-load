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

func (s *Service) UpdateGroupModels(
	ctx context.Context,
	groupID uint,
	request GroupModelsUpdateRequest,
) (GroupDetailResponse, error) {
	if groupID == 0 {
		return GroupDetailResponse{}, app_errors.ErrBadRequest
	}
	if !request.Models.Set {
		return GroupDetailResponse{}, app_errors.ErrValidation
	}
	normalized, err := normalizeGroupModels(request.Models.Values)
	if err != nil {
		return GroupDetailResponse{}, err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return GroupDetailResponse{}, fmt.Errorf("encode group models: %w", err)
	}

	var result GroupDetailResponse
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
		result, _, err = loadGroupDetail(tx, groupID)
		return err
	}, nil)
	if err != nil {
		return GroupDetailResponse{}, withControlOperationContext(err, groupID, 0)
	}
	return result, nil
}
