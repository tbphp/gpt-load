package control

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
)

type AccessKeyCollectionScope string

const (
	AccessKeyCollectionScopeUnlimited  AccessKeyCollectionScope = "unlimited"
	AccessKeyCollectionScopeRestricted AccessKeyCollectionScope = "restricted"
)

type AccessKeyCollectionItem struct {
	AccessKeyMetadata
	Scope AccessKeyCollectionScope `json:"scope"`
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

func (s *Service) ListAccessKeyCollection(
	ctx context.Context,
	query AccessKeyCollectionQuery,
) (AccessKeyCollectionResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return AccessKeyCollectionResponse{}, err
	}
	if s == nil || s.db == nil {
		return AccessKeyCollectionResponse{}, fmt.Errorf(
			"list access key collection: dependencies unavailable: %w",
			app_errors.ErrInternalServer,
		)
	}

	var rows []accessKeyMetadataRow
	if err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		return tx.Model(&models.AccessKey{}).
			Select(
				"id", "name", "key_suffix", "status", "filters", "rpm_limit",
				"created_at_ms", "updated_at_ms",
			).
			Scan(&rows).Error
	}); err != nil {
		if parentErr := ctx.Err(); parentErr != nil {
			return AccessKeyCollectionResponse{}, parentErr
		}
		return AccessKeyCollectionResponse{}, app_errors.ParseDBError(err)
	}

	records := make([]accessKeyCollectionRecord, 0, len(rows))
	for _, row := range rows {
		metadata, err := mapAccessKeyMetadataRow(row)
		if err != nil {
			return AccessKeyCollectionResponse{}, err
		}
		records = append(records, accessKeyCollectionRecord{
			AccessKeyCollectionItem: AccessKeyCollectionItem{
				AccessKeyMetadata: metadata,
				Scope:             accessKeyCollectionScope(metadata.Filters),
			},
		})
	}
	return queryAccessKeyCollectionRecords(records, normalizeAccessKeyCollectionQuery(query)), nil
}

func accessKeyCollectionScope(filters AccessKeyFilters) AccessKeyCollectionScope {
	if len(filters.Groups) == 0 && len(filters.Protocols) == 0 && len(filters.Models) == 0 {
		return AccessKeyCollectionScopeUnlimited
	}
	return AccessKeyCollectionScopeRestricted
}
