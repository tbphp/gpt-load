package control

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
)

type AccessKeyReferenceSummary struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

type GroupInUseData struct {
	AccessKeys []AccessKeyReferenceSummary `json:"access_keys"`
}

func explicitGroupReferences(
	tx *gorm.DB,
	groupID uint,
) ([]AccessKeyReferenceSummary, error) {
	type accessKeyFilterRow struct {
		ID      uint
		Name    string
		Filters []byte
	}
	var rows []accessKeyFilterRow
	if err := tx.Table("access_keys").
		Select("id", "name", "filters").
		Order("id ASC").
		Scan(&rows).Error; err != nil {
		return nil, app_errors.ParseDBError(err)
	}
	result := make([]AccessKeyReferenceSummary, 0)
	for _, row := range rows {
		filters, err := decodeStoredAccessKeyFilters(row.Filters)
		if err != nil {
			return nil, fmt.Errorf(
				"decode access key %d filters for group delete: %w",
				row.ID,
				app_errors.ErrInternalServer,
			)
		}
		for _, referencedID := range filters.Groups {
			if referencedID == groupID {
				result = append(result, AccessKeyReferenceSummary{
					ID:   row.ID,
					Name: row.Name,
				})
				break
			}
		}
	}
	return result, nil
}

func (s *Service) DeleteGroup(ctx context.Context, groupID uint) error {
	if groupID == 0 {
		return app_errors.ErrBadRequest
	}
	var deletedKeyIDs []uint
	_, err := s.writeConfig(ctx, func(tx *gorm.DB) error {
		var group models.Group
		if err := tx.Select("id").Where("id = ?", groupID).Take(&group).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		references, err := explicitGroupReferences(tx, groupID)
		if err != nil {
			return err
		}
		if len(references) > 0 {
			return app_errors.NewAPIErrorWithData(
				app_errors.ErrGroupInUse,
				GroupInUseData{AccessKeys: references},
			)
		}
		if err := tx.Model(&models.UpstreamKey{}).
			Where("group_id = ?", groupID).
			Order("id ASC").
			Pluck("id", &deletedKeyIDs).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		if err := tx.Delete(&group).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		return nil
	}, func() error {
		s.registry.RemoveGroup(groupID)
		for _, keyID := range deletedKeyIDs {
			if _, exists := s.registry.EncryptedValue(keyID); exists {
				return fmt.Errorf("deleted Registry key %d remains", keyID)
			}
		}
		return nil
	})
	return withControlOperationContext(err, groupID, 0)
}
