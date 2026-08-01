package control

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
)

type AccessKeyCollectionItem struct {
	AccessKeyMetadata
	LastRequestAtMS *int64 `json:"last_request_at_ms"`
}

type AccessKeyCollectionSummary struct {
	Total    int64 `json:"total"`
	Active   int64 `json:"active"`
	Disabled int64 `json:"disabled"`
}

type AccessKeyCollectionPagination struct {
	Page       int64 `json:"page"`
	PageSize   int64 `json:"page_size"`
	TotalItems int64 `json:"total_items"`
	TotalPages int64 `json:"total_pages"`
}

type AccessKeyCollectionResponse struct {
	Summary    AccessKeyCollectionSummary    `json:"summary"`
	Items      []AccessKeyCollectionItem     `json:"items"`
	Pagination AccessKeyCollectionPagination `json:"pagination"`
}

type accessKeyCollectionRecord struct {
	AccessKeyCollectionItem
}

type accessKeyCollectionRow struct {
	ID              uint
	Name            string
	KeySuffix       string
	Status          string
	Filters         models.JSON
	RPMLimit        int64
	CreatedAtMS     int64
	UpdatedAtMS     int64
	LastRequestAtMS *int64
}

func (s *Service) ListAccessKeyCollection(
	ctx context.Context,
	query AccessKeyCollectionQuery,
) (AccessKeyCollectionResponse, error) {
	records, err := s.captureAccessKeyCollectionRecords(ctx)
	if err != nil {
		return AccessKeyCollectionResponse{}, err
	}
	return queryAccessKeyCollectionRecords(records, normalizeAccessKeyCollectionQuery(query)), nil
}

func (s *Service) captureAccessKeyCollectionRecords(
	ctx context.Context,
) ([]accessKeyCollectionRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.db == nil {
		return nil, fmt.Errorf(
			"list access key collection: dependencies unavailable: %w",
			app_errors.ErrInternalServer,
		)
	}

	var rows []accessKeyCollectionRow
	if err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		return tx.Model(&models.AccessKey{}).
			Select(
				"access_keys.id", "access_keys.name", "access_keys.key_suffix",
				"access_keys.status", "access_keys.filters", "access_keys.rpm_limit",
				"access_keys.created_at_ms", "access_keys.updated_at_ms",
				"(SELECT MAX(request_logs.completed_at_ms) FROM request_logs WHERE request_logs.access_key_id = access_keys.id) AS last_request_at_ms",
			).
			Order("access_keys.id ASC").
			Scan(&rows).Error
	}); err != nil {
		if parentErr := ctx.Err(); parentErr != nil {
			return nil, parentErr
		}
		return nil, app_errors.ParseDBError(err)
	}

	records := make([]accessKeyCollectionRecord, 0, len(rows))
	for _, row := range rows {
		metadata, err := mapAccessKeyMetadataRow(accessKeyMetadataRow{
			ID:          row.ID,
			Name:        row.Name,
			KeySuffix:   row.KeySuffix,
			Status:      row.Status,
			Filters:     row.Filters,
			RPMLimit:    row.RPMLimit,
			CreatedAtMS: row.CreatedAtMS,
			UpdatedAtMS: row.UpdatedAtMS,
		})
		if err != nil {
			return nil, err
		}
		records = append(records, accessKeyCollectionRecord{
			AccessKeyCollectionItem: AccessKeyCollectionItem{
				AccessKeyMetadata: metadata,
				LastRequestAtMS:   row.LastRequestAtMS,
			},
		})
	}
	return records, nil
}
