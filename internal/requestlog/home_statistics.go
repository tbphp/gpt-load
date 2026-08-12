package requestlog

import (
	"context"
	"fmt"
	"sort"

	"gorm.io/gorm"

	"gpt-load/internal/platform/epochms"
	"gpt-load/internal/storage/dbtx"
	"gpt-load/internal/storage/models"
)

const (
	homeStatisticsRankingLimit       = 5
	maxJSONSafeInteger         int64 = 9_007_199_254_740_991
)

type HomeStatisticsRange string

const (
	HomeStatistics24H HomeStatisticsRange = "24h"
	HomeStatistics30D HomeStatisticsRange = "30d"
)

type HomeStatisticsQuery struct {
	Range        HomeStatisticsRange
	ObservedAtMS int64
	AccessKeyID  *uint
}

type HomeStatisticsRef struct {
	ID      uint
	Name    *string
	Deleted bool
}

type HomeModelRanking struct {
	Model string
	UsageAggregate
}

type HomeGroupRanking struct {
	Group HomeStatisticsRef
	UsageAggregate
}

type HomeAccessKeyRanking struct {
	AccessKey HomeStatisticsRef
	UsageAggregate
}

type HomeStatisticsReport struct {
	Range         HomeStatisticsRange
	ObservedAtMS  int64
	FromMS        int64
	ToMS          int64
	Granularity   UsageGranularity
	Summary       UsageAggregate
	Series        []UsageSeriesPoint
	TopModels     []HomeModelRanking
	TopGroups     []HomeGroupRanking
	TopAccessKeys []HomeAccessKeyRanking
}

func (service *Service) QueryHomeStatistics(
	ctx context.Context,
	input HomeStatisticsQuery,
) (HomeStatisticsReport, error) {
	if service == nil || service.db == nil {
		return HomeStatisticsReport{}, fmt.Errorf("query home statistics: database is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	width, count, granularity, err := homeStatisticsWindow(input.Range)
	if err != nil {
		return HomeStatisticsReport{}, err
	}
	if input.AccessKeyID != nil && *input.AccessKeyID == 0 {
		return HomeStatisticsReport{}, fmt.Errorf(
			"query home statistics: invalid access key scope",
		)
	}
	fromMS, toMS, err := epochms.WindowEndingAt(input.ObservedAtMS, width, count)
	if err != nil {
		return HomeStatisticsReport{}, fmt.Errorf("query home statistics: invalid observed time: %w", err)
	}
	usageInput := UsageQuery{
		FromMS:        fromMS,
		ToMS:          toMS,
		Granularity:   granularity,
		BucketWidthMS: width,
		AccessKeyID:   input.AccessKeyID,
	}

	report := HomeStatisticsReport{
		Range:        input.Range,
		ObservedAtMS: input.ObservedAtMS,
		FromMS:       fromMS,
		ToMS:         toMS,
		Granularity:  granularity,
	}
	err = dbtx.Run(ctx, service.db, dbtx.Options{
		Mode:           dbtx.ReadSnapshot,
		CleanupTimeout: usageRollbackTimeout,
		Operation:      "home statistics read transaction",
	}, func(connection *gorm.DB) error {
		scope := usageStatScope(connection, usageInput)
		if err := validateUsageStatIntegrity(scope); err != nil {
			return err
		}
		summary, err := queryUsageSummary(usageStatScope(connection, usageInput))
		if err != nil {
			return err
		}
		if err := validateHomeStatisticsAggregate(summary); err != nil {
			return fmt.Errorf("validate home statistics summary: %w", err)
		}
		sparseSeries, err := queryUsageSeries(
			usageStatScope(connection, usageInput),
			width,
		)
		if err != nil {
			return err
		}
		for _, point := range sparseSeries {
			if err := validateHomeStatisticsAggregate(point.UsageAggregate); err != nil {
				return fmt.Errorf("validate home statistics series: %w", err)
			}
		}
		modelRows, err := queryHomeModelRankings(
			usageStatScope(connection, usageInput),
		)
		if err != nil {
			return err
		}
		var groupRows []homeGroupRankingRow
		var accessRows []homeAccessKeyRankingRow
		groupRefs := map[uint]HomeStatisticsRef{}
		accessRefs := map[uint]HomeStatisticsRef{}
		if input.AccessKeyID == nil {
			groupRows, err = queryHomeGroupRankings(
				usageStatScope(connection, usageInput),
			)
			if err != nil {
				return err
			}
			accessRows, err = queryHomeAccessKeyRankings(
				usageStatScope(connection, usageInput),
			)
			if err != nil {
				return err
			}
			groupRefs, err = loadHomeGroupRefs(connection, groupRows)
			if err != nil {
				return err
			}
			accessRefs, err = loadHomeAccessKeyRefs(connection, accessRows)
			if err != nil {
				return err
			}
		}

		report.Summary = summary
		report.Series = denseHomeStatisticsSeries(
			fromMS,
			toMS,
			width,
			sparseSeries,
		)
		report.TopModels = mapHomeModelRankings(modelRows)
		report.TopGroups = mapHomeGroupRankings(groupRows, groupRefs)
		report.TopAccessKeys = mapHomeAccessKeyRankings(accessRows, accessRefs)

		return nil
	})
	if err != nil {
		return HomeStatisticsReport{}, fmt.Errorf("query home statistics: %w", err)
	}
	return report, nil
}

func homeStatisticsWindow(
	value HomeStatisticsRange,
) (width int64, count int, granularity UsageGranularity, err error) {
	switch value {
	case HomeStatistics24H:
		return epochms.MillisecondsPerHour, 24, UsageGranularityHour, nil
	case HomeStatistics30D:
		return epochms.MillisecondsPerDay, 30, UsageGranularityDay, nil
	default:
		return 0, 0, "", fmt.Errorf(
			"query home statistics: unsupported range %q",
			value,
		)
	}
}

func denseHomeStatisticsSeries(
	fromMS int64,
	toMS int64,
	width int64,
	sparse []UsageSeriesPoint,
) []UsageSeriesPoint {
	byStart := make(map[int64]UsageAggregate, len(sparse))
	for _, point := range sparse {
		byStart[point.BucketStartMS] = point.UsageAggregate
	}
	result := make([]UsageSeriesPoint, 0, int((toMS-fromMS)/width))
	for bucketStartMS := fromMS; bucketStartMS < toMS; bucketStartMS += width {
		result = append(result, UsageSeriesPoint{
			BucketStartMS:  bucketStartMS,
			BucketEndMS:    bucketStartMS + width,
			UsageAggregate: byStart[bucketStartMS],
		})
	}
	return result
}

type homeModelRankingRow struct {
	Model string
	UsageAggregate
}

type homeGroupRankingRow struct {
	GroupID uint
	UsageAggregate
}

type homeAccessKeyRankingRow struct {
	AccessKeyID uint
	UsageAggregate
}

func queryHomeModelRankings(scope *gorm.DB) ([]homeModelRankingRow, error) {
	var rows []homeModelRankingRow
	if err := scope.
		Select("model, " + usageAggregateSelect).
		Group("model").
		Order("SUM(estimated_cost_nano_usd) DESC").
		Order("SUM(request_count) DESC").
		Order("model ASC").
		Limit(homeStatisticsRankingLimit).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("query home model rankings: %w", err)
	}
	for _, row := range rows {
		if err := validateHomeStatisticsAggregate(row.UsageAggregate); err != nil {
			return nil, fmt.Errorf("validate home model ranking: %w", err)
		}
	}
	return rows, nil
}

func queryHomeGroupRankings(scope *gorm.DB) ([]homeGroupRankingRow, error) {
	var rows []homeGroupRankingRow
	if err := scope.
		Select("group_id, " + usageAggregateSelect).
		Group("group_id").
		Order("SUM(estimated_cost_nano_usd) DESC").
		Order("SUM(request_count) DESC").
		Order("group_id ASC").
		Limit(homeStatisticsRankingLimit).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("query home group rankings: %w", err)
	}
	for _, row := range rows {
		if err := validateHomeStatisticsAggregate(row.UsageAggregate); err != nil {
			return nil, fmt.Errorf("validate home group ranking: %w", err)
		}
	}
	return rows, nil
}

func queryHomeAccessKeyRankings(scope *gorm.DB) ([]homeAccessKeyRankingRow, error) {
	var rows []homeAccessKeyRankingRow
	if err := scope.
		Select("access_key_id, " + usageAggregateSelect).
		Group("access_key_id").
		Order("SUM(estimated_cost_nano_usd) DESC").
		Order("SUM(request_count) DESC").
		Order("access_key_id ASC").
		Limit(homeStatisticsRankingLimit).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("query home access key rankings: %w", err)
	}
	for _, row := range rows {
		if err := validateHomeStatisticsAggregate(row.UsageAggregate); err != nil {
			return nil, fmt.Errorf("validate home access key ranking: %w", err)
		}
	}
	return rows, nil
}

func validateHomeStatisticsAggregate(value UsageAggregate) error {
	if err := validateUsageAggregate(value); err != nil {
		return err
	}
	for _, field := range []struct {
		name  string
		value int64
	}{
		{name: "request count", value: value.RequestCount},
		{name: "success count", value: value.SuccessCount},
		{name: "failure count", value: value.FailureCount},
		{name: "uncached input tokens", value: value.UncachedInputTokens},
		{name: "cache read tokens", value: value.CacheReadTokens},
		{name: "cache write 5m tokens", value: value.CacheWrite5MTokens},
		{name: "cache write 1h tokens", value: value.CacheWrite1HTokens},
		{name: "cache write unknown tokens", value: value.CacheWriteUnknownTokens},
		{name: "output tokens", value: value.OutputTokens},
		{name: "usage missing count", value: value.UsageMissingCount},
		{name: "partial count", value: value.PartialCount},
		{name: "unpriced request count", value: value.UnpricedRequestCount},
		{name: "pricing partial count", value: value.PricingPartialCount},
	} {
		if field.value > maxJSONSafeInteger {
			return fmt.Errorf("%s exceeds JSON safe integer", field.name)
		}
	}
	return nil
}

func loadHomeGroupRefs(
	tx *gorm.DB,
	groupRows []homeGroupRankingRow,
) (map[uint]HomeStatisticsRef, error) {
	ids := make(map[uint]struct{}, len(groupRows))
	for _, row := range groupRows {
		ids[row.GroupID] = struct{}{}
	}
	return loadHomeRefs[models.Group](tx, ids, "groups")
}

func loadHomeAccessKeyRefs(
	tx *gorm.DB,
	rows []homeAccessKeyRankingRow,
) (map[uint]HomeStatisticsRef, error) {
	ids := make(map[uint]struct{}, len(rows))
	for _, row := range rows {
		ids[row.AccessKeyID] = struct{}{}
	}
	return loadHomeRefs[models.AccessKey](tx, ids, "access keys")
}

func loadHomeRefs[T any](
	tx *gorm.DB,
	idSet map[uint]struct{},
	label string,
) (map[uint]HomeStatisticsRef, error) {
	ids := make([]uint, 0, len(idSet))
	for id := range idSet {
		if id != 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })

	refs := make(map[uint]HomeStatisticsRef, len(idSet))
	for id := range idSet {
		refs[id] = HomeStatisticsRef{ID: id, Deleted: true}
	}
	if len(ids) == 0 {
		return refs, nil
	}
	var rows []struct {
		ID   uint
		Name string
	}
	if err := tx.Session(&gorm.Session{NewDB: true}).Model(new(T)).
		Select("id", "name").
		Where("id IN ?", ids).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("query home statistics %s: %w", label, err)
	}
	for _, row := range rows {
		name := row.Name
		refs[row.ID] = HomeStatisticsRef{
			ID:      row.ID,
			Name:    &name,
			Deleted: false,
		}
	}
	return refs, nil
}

func mapHomeModelRankings(
	rows []homeModelRankingRow,
) []HomeModelRanking {
	result := make([]HomeModelRanking, 0, len(rows))
	for _, row := range rows {
		result = append(result, HomeModelRanking{
			Model:          row.Model,
			UsageAggregate: row.UsageAggregate,
		})
	}
	return result
}

func mapHomeGroupRankings(
	rows []homeGroupRankingRow,
	refs map[uint]HomeStatisticsRef,
) []HomeGroupRanking {
	result := make([]HomeGroupRanking, 0, len(rows))
	for _, row := range rows {
		result = append(result, HomeGroupRanking{
			Group:          refs[row.GroupID],
			UsageAggregate: row.UsageAggregate,
		})
	}
	return result
}

func mapHomeAccessKeyRankings(
	rows []homeAccessKeyRankingRow,
	refs map[uint]HomeStatisticsRef,
) []HomeAccessKeyRanking {
	result := make([]HomeAccessKeyRanking, 0, len(rows))
	for _, row := range rows {
		result = append(result, HomeAccessKeyRanking{
			AccessKey:      refs[row.AccessKeyID],
			UsageAggregate: row.UsageAggregate,
		})
	}
	return result
}
