package control

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

type GroupKeyImportRequest struct {
	Keys string `json:"keys"`
}

type GroupKeyImportResult struct {
	GroupID        uint `json:"group_id"`
	KeysAdded      int  `json:"keys_added"`
	KeysDuplicated int  `json:"keys_duplicated"`
}

func (s *Service) ImportGroupKeys(
	ctx context.Context,
	groupID uint,
	request GroupKeyImportRequest,
) (GroupKeyImportResult, error) {
	if groupID == 0 {
		return GroupKeyImportResult{}, app_errors.ErrValidation
	}
	keys, err := s.normalizeUpstreamKeys(request.Keys)
	if err != nil {
		return GroupKeyImportResult{}, err
	}

	result := GroupKeyImportResult{GroupID: groupID}
	var entries []state.KeyEntry
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	err = s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		var group struct{ ID uint }
		if err := tx.Model(&models.Group{}).
			Select("id").Where("id = ?", groupID).Take(&group).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		entries, result.KeysAdded, result.KeysDuplicated, err =
			s.persistUpstreamKeys(tx, groupID, keys)
		if err != nil {
			return err
		}
		return state.ValidateKeyEntries(entries)
	})
	if err != nil {
		return GroupKeyImportResult{}, err
	}
	if err := s.applyMissingRegistryKeys(groupID, entries); err != nil {
		return GroupKeyImportResult{}, fmt.Errorf("apply committed Registry update: %w", app_errors.ErrInternalServer)
	}
	return result, nil
}
