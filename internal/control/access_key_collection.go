package control

import (
	"context"
	"fmt"
	"time"

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
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()

	var rows []accessKeyCollectionRow
	var costLimitRows []models.AccessKeyCostLimitRule
	if err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		if err := tx.Model(&models.AccessKey{}).
			Select(
				"access_keys.id", "access_keys.name", "access_keys.key_suffix",
				"access_keys.status", "access_keys.filters", "access_keys.rpm_limit",
				"access_keys.created_at_ms", "access_keys.updated_at_ms",
				"(SELECT MAX(request_logs.completed_at_ms) FROM request_logs WHERE request_logs.access_key_id = access_keys.id) AS last_request_at_ms",
			).
			Order("access_keys.id ASC").
			Scan(&rows).Error; err != nil {
			return err
		}
		return tx.Order("access_key_id ASC, CASE WHEN kind = 'total' THEN 0 ELSE 1 END ASC, period_seconds ASC, id ASC").
			Find(&costLimitRows).Error
	}); err != nil {
		if parentErr := ctx.Err(); parentErr != nil {
			return nil, parentErr
		}
		return nil, app_errors.ParseDBError(err)
	}

	rulesByAccessKey := make(map[uint][]models.AccessKeyCostLimitRule)
	for _, row := range costLimitRows {
		rulesByAccessKey[row.AccessKeyID] = append(rulesByAccessKey[row.AccessKeyID], row)
	}
	records := make([]accessKeyCollectionRecord, 0, len(rows))
	observedAt := time.Now()
	if s.now != nil {
		observedAt = s.now()
	}
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
		metadata.CostLimitRules = mapAccessKeyCostLimitRules(rulesByAccessKey[row.ID])
		if s.accessQuota != nil {
			status := mapAccessKeyCostLimitStatus(s.accessQuota.Snapshot(row.ID, observedAt))
			if len(status.Rules) > 0 {
				metadata.CostLimitStatus = &status
			}
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
