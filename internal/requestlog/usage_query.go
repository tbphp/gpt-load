package requestlog

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/platform/dbtx"
	"gpt-load/internal/platform/epochms"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/usage"
)

const (
	defaultUsageBreakdownLimit = 100
	maxUsageBreakdownLimit     = 100
	maxUsageSeriesHours        = 30 * 24
	usageRollbackTimeout       = time.Second
)

func (service *Service) QueryUsage(ctx context.Context, input UsageQuery) (UsageReport, error) {
	if service == nil || service.db == nil {
		return UsageReport{}, fmt.Errorf("query usage: database is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateUsageQuery(input); err != nil {
		return UsageReport{}, err
	}

	limit := input.Limit
	if limit <= 0 || limit > maxUsageBreakdownLimit {
		limit = defaultUsageBreakdownLimit
	}

	var report UsageReport
	err := dbtx.Run(ctx, service.db, dbtx.Options{
		Mode:           dbtx.ReadSnapshot,
		CleanupTimeout: usageRollbackTimeout,
		Operation:      "usage read transaction",
	}, func(connection *gorm.DB) error {
		if err := validateUsageStatIntegrity(usageStatScope(connection, input)); err != nil {
			return err
		}
		summary, err := queryUsageSummary(usageStatScope(connection, input))
		if err != nil {
			return err
		}
		series, err := queryUsageSeries(usageStatScope(connection, input), input.Granularity)
		if err != nil {
			return err
		}
		breakdownCount, err := queryUsageBreakdownCount(usageStatScope(connection, input))
		if err != nil {
			return err
		}
		breakdown, truncated, err := queryUsageBreakdown(
			usageStatScope(connection, input),
			limit,
			input.BreakdownOrder,
		)
		if err != nil {
			return err
		}
		report = UsageReport{
			Summary:            summary,
			Series:             series,
			Breakdown:          breakdown,
			BreakdownTruncated: truncated,
			BreakdownOrder:     input.BreakdownOrder,
			BreakdownCount:     breakdownCount,
		}
		return nil
	})
	if err != nil {
		return UsageReport{}, fmt.Errorf("query usage: %w", err)
	}
	return report, nil
}

func validateUsageQuery(input UsageQuery) error {
	if input.FromMS < 0 || input.ToMS <= input.FromMS {
		return fmt.Errorf("query usage: invalid time range")
	}
	if input.ToMS-input.FromMS >
		int64(maxUsageSeriesHours)*epochms.MillisecondsPerHour {
		return fmt.Errorf("query usage: time range exceeds %d hours", maxUsageSeriesHours)
	}
	switch input.Granularity {
	case UsageGranularityHour, UsageGranularityDay:
	default:
		return fmt.Errorf("query usage: unsupported granularity %q", input.Granularity)
	}
	switch input.BreakdownOrder {
	case UsageBreakdownOrderRequests, UsageBreakdownOrderCost:
		return nil
	default:
		return fmt.Errorf(
			"query usage: unsupported breakdown order %q",
			input.BreakdownOrder,
		)
	}
}

func usageStatScope(db *gorm.DB, input UsageQuery) *gorm.DB {
	scope := db.Session(&gorm.Session{NewDB: true}).Model(&models.UsageStat{}).
		Where("bucket_start_ms >= ? AND bucket_start_ms < ?", input.FromMS, input.ToMS).
		// Older versions aggregated zero-attempt requests under the unbound
		// (group_id=0, model='') key. Keep those derived rows invisible until
		// retention removes them so home and monitor share the new contract.
		Where("NOT (group_id = ? AND model = ?)", 0, "")
	if input.GroupID != nil {
		scope = scope.Where("group_id = ?", *input.GroupID)
	}
	if input.UpstreamModel != "" {
		scope = scope.Where("model = ?", input.UpstreamModel)
	}
	return scope
}

func validateUsageStatIntegrity(scope *gorm.DB) error {
	var integrity usageStatIntegrity
	if err := scope.Select(`
		COALESCE(MAX(CASE
			WHEN bucket_start_ms < 0
				OR bucket_start_ms % 3600000 != 0
			THEN 1 ELSE 0 END), 0) AS invalid_bucket,
		COALESCE(MAX(CASE
			WHEN request_count < 0 OR success_count < 0 OR failure_count < 0
					OR usage_missing_count < 0 OR partial_count < 0 OR unpriced_request_count < 0
					OR pricing_partial_count < 0
				OR CASE
					WHEN request_count < success_count THEN 1
					WHEN request_count - success_count != failure_count THEN 1
					ELSE 0
				END = 1
			THEN 1 ELSE 0 END), 0) AS invalid_count,
		COALESCE(MAX(CASE
			WHEN uncached_input_tokens < 0 OR output_tokens < 0 OR cache_read_tokens < 0
					OR cache_write_5m_tokens < 0 OR cache_write_1h_tokens < 0
					OR cache_write_unknown_tokens < 0
			THEN 1 ELSE 0 END), 0) AS invalid_token,
		COALESCE(MAX(CASE
			WHEN estimated_cost_nano_usd < 0
			THEN 1 ELSE 0 END), 0) AS invalid_cost
	`).Find(&integrity).Error; err != nil {
		return fmt.Errorf("check usage stat integrity: %w", err)
	}
	if integrity.InvalidBucket != 0 || integrity.InvalidCount != 0 ||
		integrity.InvalidToken != 0 || integrity.InvalidCost != 0 {
		return fmt.Errorf("check usage stat integrity: corrupt row")
	}
	return nil
}

func queryUsageSummary(scope *gorm.DB) (UsageAggregate, error) {
	var summary UsageAggregate
	if err := scope.Select(usageAggregateSelect).Find(&summary).Error; err != nil {
		return UsageAggregate{}, fmt.Errorf("query usage summary: %w", err)
	}
	if err := validateUsageAggregate(summary); err != nil {
		return UsageAggregate{}, fmt.Errorf("validate usage summary: %w", err)
	}
	return summary, nil
}

func queryUsageSeries(scope *gorm.DB, granularity UsageGranularity) ([]UsageSeriesPoint, error) {
	var source []usageHourPoint
	if err := scope.Select("bucket_start_ms, " + usageAggregateSelect).
		Group("bucket_start_ms").
		Order("bucket_start_ms ASC").
		Find(&source).Error; err != nil {
		return nil, fmt.Errorf("query usage source series: %w", err)
	}

	for _, point := range source {
		if err := validateUsageAggregate(point.UsageAggregate); err != nil {
			return nil, fmt.Errorf("validate usage source series: %w", err)
		}
	}
	if granularity == UsageGranularityHour {
		series := make([]UsageSeriesPoint, 0, len(source))
		for _, point := range source {
			series = append(series, UsageSeriesPoint{
				BucketStartMS:  point.BucketStartMS,
				BucketEndMS:    point.BucketStartMS + epochms.MillisecondsPerHour,
				UsageAggregate: point.UsageAggregate,
			})
		}
		return series, nil
	}
	return mergeUsageHoursToDays(source)
}

func queryUsageBreakdownCount(scope *gorm.DB) (int64, error) {
	grouped := scope.Select("group_id, model").Group("group_id, model")
	var count int64
	if err := scope.Session(&gorm.Session{NewDB: true}).
		Table("(?) AS usage_breakdown_items", grouped).
		Count(&count).Error; err != nil {
		return 0, fmt.Errorf("query usage breakdown group count: %w", err)
	}
	if count < 0 {
		return 0, fmt.Errorf("query usage breakdown group count: negative count")
	}
	return count, nil
}

func queryUsageBreakdown(
	scope *gorm.DB,
	limit int,
	order UsageBreakdownOrder,
) ([]UsageBreakdown, bool, error) {
	var rows []usageBreakdownRow
	query := scope.Select("group_id, model, " + usageAggregateSelect).
		Group("group_id, model")
	switch order {
	case UsageBreakdownOrderRequests:
		query = query.
			Order("SUM(request_count) DESC").
			Order("SUM(estimated_cost_nano_usd) DESC")
	case UsageBreakdownOrderCost:
		query = query.
			Order("SUM(estimated_cost_nano_usd) DESC").
			Order("SUM(request_count) DESC")
	default:
		return nil, false, fmt.Errorf(
			"query usage breakdown: unsupported order %q",
			order,
		)
	}
	if err := query.
		Order("group_id ASC").
		Order("model ASC").
		Limit(limit + 1).
		Find(&rows).Error; err != nil {
		return nil, false, fmt.Errorf("query usage breakdown: %w", err)
	}

	truncated := len(rows) > limit
	if truncated {
		rows = rows[:limit]
	}
	breakdown := make([]UsageBreakdown, 0, len(rows))
	for _, row := range rows {
		if err := validateUsageAggregate(row.UsageAggregate); err != nil {
			return nil, false, fmt.Errorf("validate usage breakdown: %w", err)
		}
		breakdown = append(breakdown, UsageBreakdown{
			GroupID:        row.GroupID,
			Model:          row.Model,
			UsageAggregate: row.UsageAggregate,
		})
	}
	return breakdown, truncated, nil
}

func mergeUsageHoursToDays(source []usageHourPoint) ([]UsageSeriesPoint, error) {
	// queryUsageSeries orders source by bucket_start_ms ASC; adjacent-only
	// daily folding depends on it.
	series := make([]UsageSeriesPoint, 0, len(source))
	for _, point := range source {
		dayStartMS, err := epochms.AlignDown(
			point.BucketStartMS,
			epochms.MillisecondsPerDay,
		)
		if err != nil {
			return nil, fmt.Errorf("align usage day: %w", err)
		}
		if len(series) == 0 || series[len(series)-1].BucketStartMS != dayStartMS {
			series = append(series, UsageSeriesPoint{
				BucketStartMS:  dayStartMS,
				BucketEndMS:    dayStartMS + epochms.MillisecondsPerDay,
				UsageAggregate: point.UsageAggregate,
			})
			continue
		}
		merged, err := addUsageAggregates(series[len(series)-1].UsageAggregate, point.UsageAggregate)
		if err != nil {
			return nil, fmt.Errorf("merge usage day %d: %w", dayStartMS, err)
		}
		series[len(series)-1].UsageAggregate = merged
	}
	return series, nil
}

func addUsageAggregates(left, right UsageAggregate) (UsageAggregate, error) {
	result := UsageAggregate{}
	fields := []struct {
		name        string
		left, right int64
		target      *int64
	}{
		{"request count", left.RequestCount, right.RequestCount, &result.RequestCount},
		{"success count", left.SuccessCount, right.SuccessCount, &result.SuccessCount},
		{"failure count", left.FailureCount, right.FailureCount, &result.FailureCount},
		{"uncached input tokens", left.UncachedInputTokens, right.UncachedInputTokens, &result.UncachedInputTokens},
		{"cache read tokens", left.CacheReadTokens, right.CacheReadTokens, &result.CacheReadTokens},
		{"cache write 5m tokens", left.CacheWrite5MTokens, right.CacheWrite5MTokens, &result.CacheWrite5MTokens},
		{"cache write 1h tokens", left.CacheWrite1HTokens, right.CacheWrite1HTokens, &result.CacheWrite1HTokens},
		{"cache write unknown tokens", left.CacheWriteUnknownTokens, right.CacheWriteUnknownTokens, &result.CacheWriteUnknownTokens},
		{"output tokens", left.OutputTokens, right.OutputTokens, &result.OutputTokens},
		{"usage missing count", left.UsageMissingCount, right.UsageMissingCount, &result.UsageMissingCount},
		{"partial count", left.PartialCount, right.PartialCount, &result.PartialCount},
		{"unpriced request count", left.UnpricedRequestCount, right.UnpricedRequestCount, &result.UnpricedRequestCount},
		{"pricing partial count", left.PricingPartialCount, right.PricingPartialCount, &result.PricingPartialCount},
	}
	for _, field := range fields {
		value, ok := usage.CheckedAdd(field.left, field.right)
		if !ok {
			return UsageAggregate{}, fmt.Errorf("%s overflow or negative", field.name)
		}
		*field.target = value
	}
	cost, ok := pricing.CheckedAddNanoUSD(
		pricing.NanoUSD(left.EstimatedCostNanoUSD),
		pricing.NanoUSD(right.EstimatedCostNanoUSD),
	)
	if !ok {
		return UsageAggregate{}, fmt.Errorf("estimated cost nano USD overflow or negative")
	}
	result.EstimatedCostNanoUSD = int64(cost)
	if err := validateUsageAggregate(result); err != nil {
		return UsageAggregate{}, err
	}
	return result, nil
}

func validateUsageAggregate(aggregate UsageAggregate) error {
	fields := []struct {
		name  string
		value int64
	}{
		{"request count", aggregate.RequestCount},
		{"success count", aggregate.SuccessCount},
		{"failure count", aggregate.FailureCount},
		{"uncached input tokens", aggregate.UncachedInputTokens},
		{"cache read tokens", aggregate.CacheReadTokens},
		{"cache write 5m tokens", aggregate.CacheWrite5MTokens},
		{"cache write 1h tokens", aggregate.CacheWrite1HTokens},
		{"cache write unknown tokens", aggregate.CacheWriteUnknownTokens},
		{"output tokens", aggregate.OutputTokens},
		{"usage missing count", aggregate.UsageMissingCount},
		{"partial count", aggregate.PartialCount},
		{"unpriced request count", aggregate.UnpricedRequestCount},
		{"pricing partial count", aggregate.PricingPartialCount},
	}
	for _, field := range fields {
		if field.value < 0 {
			return fmt.Errorf("negative %s", field.name)
		}
	}
	requestCount, ok := usage.CheckedAdd(aggregate.SuccessCount, aggregate.FailureCount)
	if !ok || requestCount != aggregate.RequestCount {
		return fmt.Errorf("request count does not equal success plus failure")
	}
	totalTokens := int64(0)
	for _, value := range []int64{
		aggregate.UncachedInputTokens,
		aggregate.CacheReadTokens,
		aggregate.CacheWrite5MTokens,
		aggregate.CacheWrite1HTokens,
		aggregate.CacheWriteUnknownTokens,
		aggregate.OutputTokens,
	} {
		var added bool
		totalTokens, added = usage.CheckedAdd(totalTokens, value)
		if !added {
			return fmt.Errorf("total tokens overflow")
		}
	}
	if aggregate.EstimatedCostNanoUSD < 0 {
		return fmt.Errorf("invalid cost")
	}
	return nil
}

// Aliases follow GORM's snake-case mapping for embedded UsageAggregate fields (for example, 5M -> 5_m).
const usageAggregateSelect = "" +
	"COALESCE(SUM(request_count), 0) AS request_count, " +
	"COALESCE(SUM(success_count), 0) AS success_count, " +
	"COALESCE(SUM(failure_count), 0) AS failure_count, " +
	"COALESCE(SUM(uncached_input_tokens), 0) AS uncached_input_tokens, " +
	"COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens, " +
	"COALESCE(SUM(cache_write_5m_tokens), 0) AS cache_write5_m_tokens, " +
	"COALESCE(SUM(cache_write_1h_tokens), 0) AS cache_write1_h_tokens, " +
	"COALESCE(SUM(cache_write_unknown_tokens), 0) AS cache_write_unknown_tokens, " +
	"COALESCE(SUM(output_tokens), 0) AS output_tokens, " +
	"COALESCE(SUM(estimated_cost_nano_usd), 0) AS estimated_cost_nano_usd, " +
	"COALESCE(SUM(usage_missing_count), 0) AS usage_missing_count, " +
	"COALESCE(SUM(partial_count), 0) AS partial_count, " +
	"COALESCE(SUM(unpriced_request_count), 0) AS unpriced_request_count, " +
	"COALESCE(SUM(pricing_partial_count), 0) AS pricing_partial_count"

type usageHourPoint struct {
	BucketStartMS int64
	UsageAggregate
}

type usageStatIntegrity struct {
	InvalidBucket int64
	InvalidCount  int64
	InvalidToken  int64
	InvalidCost   int64
}

type usageBreakdownRow struct {
	GroupID uint
	Model   string
	UsageAggregate
}
