package control

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/platform/epochms"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/storage/models"
)

type AccessKeyCollectionItem struct {
	AccessKeyMetadata
	Usage           *usageDistributionAggregateResponse `json:"usage,omitempty"`
	LastRequestAtMS *int64                              `json:"last_request_at_ms"`
	Expired         bool                                `json:"expired"`
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

type AccessKeyUsageWindow struct {
	ObservedAtMS int64  `json:"observed_at_ms"`
	Range        string `json:"range"`
	FromMS       int64  `json:"from_ms"`
	ToMS         int64  `json:"to_ms"`
}

type AccessKeyCollectionResponse struct {
	UsageWindow *AccessKeyUsageWindow         `json:"usage_window,omitempty"`
	Summary     AccessKeyCollectionSummary    `json:"summary"`
	Items       []AccessKeyCollectionItem     `json:"items"`
	Pagination  AccessKeyCollectionPagination `json:"pagination"`
}

type accessKeyCollectionRecord struct {
	AccessKeyCollectionItem
	usageCost int64
}

type accessKeyCollectionRow struct {
	PriceMultiplierMicros *int64
	ID                    uint
	Name                  string
	KeySuffix             string
	Status                string
	Filters               models.JSON
	RPMLimit              int64
	ExpiresAtMS           *int64
	CreatedAtMS           int64
	UpdatedAtMS           int64
	LastRequestAtMS       *int64
}

func (s *Service) ListAccessKeyCollection(
	ctx context.Context,
	query AccessKeyCollectionQuery,
) (AccessKeyCollectionResponse, error) {
	query = normalizeAccessKeyCollectionQuery(query)
	observedAt := time.Now()
	if s.now != nil {
		observedAt = s.now()
	}
	observedAtMS, err := safeEpochMilliseconds(observedAt)
	if err != nil {
		return AccessKeyCollectionResponse{}, err
	}
	// 密钥列表保留固定七天口径，通过明确区间与用量页联动。
	fromMS, toMS, err := epochms.WindowEndingAt(observedAtMS, 6*epochms.MillisecondsPerHour, 28)
	if err != nil {
		return AccessKeyCollectionResponse{}, app_errors.ErrInternalServer
	}
	usageQuery := requestlog.UsageQuery{
		FromMS: fromMS, ToMS: toMS,
		Granularity: requestlog.UsageGranularityHour, BucketWidthMS: 6 * epochms.MillisecondsPerHour,
	}
	records, err := s.captureAccessKeyCollectionRecords(ctx, usageQuery, observedAt)
	if err != nil {
		return AccessKeyCollectionResponse{}, err
	}
	result := queryAccessKeyCollectionRecords(records, query)
	result.UsageWindow = &AccessKeyUsageWindow{ObservedAtMS: observedAtMS, Range: "7d", FromMS: usageQuery.FromMS, ToMS: usageQuery.ToMS}
	return result, nil
}

func (s *Service) captureAccessKeyCollectionRecords(
	ctx context.Context,
	usageQuery requestlog.UsageQuery, observedAt time.Time,
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
	var usageByKey map[uint]requestlog.UsageAggregate
	if err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		if err := tx.Model(&models.AccessKey{}).
			Select(
				"access_keys.id", "access_keys.name", "access_keys.key_suffix",
				"access_keys.status", "access_keys.filters", "access_keys.rpm_limit",
				"access_keys.expires_at_ms", "access_keys.price_multiplier_micros",
				"access_keys.created_at_ms", "access_keys.updated_at_ms",
				"(SELECT MAX(request_logs.completed_at_ms) FROM request_logs WHERE request_logs.access_key_id = access_keys.id) AS last_request_at_ms",
			).
			Order("access_keys.id ASC").
			Scan(&rows).Error; err != nil {
			return err
		}
		var usageErr error
		usageByKey, usageErr = requestlog.ReadAccessKeyUsage(tx, usageQuery)
		if usageErr != nil {
			return usageErr
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
	observedAtMS, err := safeEpochMilliseconds(observedAt)
	if err != nil {
		return nil, app_errors.ErrInternalServer
	}
	for _, row := range rows {
		metadata, err := mapAccessKeyMetadataRow(accessKeyMetadataRow{
			PriceMultiplierMicros: row.PriceMultiplierMicros,
			ID:                    row.ID,
			Name:                  row.Name,
			KeySuffix:             row.KeySuffix,
			Status:                row.Status,
			Filters:               row.Filters,
			RPMLimit:              row.RPMLimit,
			ExpiresAtMS:           row.ExpiresAtMS,
			CreatedAtMS:           row.CreatedAtMS,
			UpdatedAtMS:           row.UpdatedAtMS,
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
		aggregate, err := mapUsageAggregate(usageByKey[row.ID])
		if err != nil {
			return nil, err
		}
		records = append(records, accessKeyCollectionRecord{
			usageCost: usageByKey[row.ID].EstimatedCostNanoUSD,
			AccessKeyCollectionItem: AccessKeyCollectionItem{
				Usage:             &usageDistributionAggregateResponse{RequestCount: aggregate.RequestCount, TotalTokens: aggregate.TotalTokens, EstimatedCostNanoUSD: aggregate.EstimatedCostNanoUSD},
				AccessKeyMetadata: metadata,
				LastRequestAtMS:   row.LastRequestAtMS,
				Expired: metadata.ExpiresAtMS != nil &&
					observedAtMS >= *metadata.ExpiresAtMS,
			},
		})
	}
	return records, nil
}
