package requestlog

import (
	"context"
	"fmt"
	"math"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
	"gpt-load/internal/usage"
)

const (
	defaultUsageBreakdownLimit = 100
	maxUsageBreakdownLimit     = 100
	maxUsageSeriesHours        = 30 * 24
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

	input.From = input.From.UTC()
	input.To = input.To.UTC()
	limit := input.Limit
	if limit <= 0 || limit > maxUsageBreakdownLimit {
		limit = defaultUsageBreakdownLimit
	}

	var report UsageReport
	err := service.db.WithContext(ctx).Connection(func(connection *gorm.DB) error {
		if err := connection.Exec("BEGIN DEFERRED").Error; err != nil {
			return fmt.Errorf("begin usage read transaction: %w", err)
		}
		committed := false
		defer func() {
			if !committed {
				_ = connection.Exec("ROLLBACK").Error
			}
		}()

		summary, err := queryUsageSummary(usageStatScope(connection, input))
		if err != nil {
			return err
		}
		series, err := queryUsageSeries(usageStatScope(connection, input), input.Granularity)
		if err != nil {
			return err
		}
		breakdown, truncated, err := queryUsageBreakdown(usageStatScope(connection, input), limit)
		if err != nil {
			return err
		}
		report = UsageReport{
			Summary:            summary,
			Series:             series,
			Breakdown:          breakdown,
			BreakdownTruncated: truncated,
		}
		if err := connection.Exec("COMMIT").Error; err != nil {
			return fmt.Errorf("commit usage read transaction: %w", err)
		}
		committed = true
		return nil
	})
	if err != nil {
		return UsageReport{}, fmt.Errorf("query usage: %w", err)
	}
	return report, nil
}

func validateUsageQuery(input UsageQuery) error {
	if input.From.IsZero() || input.To.IsZero() || !input.From.Before(input.To) {
		return fmt.Errorf("query usage: invalid time range")
	}
	if input.To.Sub(input.From) > maxUsageSeriesHours*time.Hour {
		return fmt.Errorf("query usage: time range exceeds %d hours", maxUsageSeriesHours)
	}
	switch input.Granularity {
	case UsageGranularityHour, UsageGranularityDay:
		return nil
	default:
		return fmt.Errorf("query usage: unsupported granularity %q", input.Granularity)
	}
}

func usageStatScope(db *gorm.DB, input UsageQuery) *gorm.DB {
	scope := db.Session(&gorm.Session{NewDB: true}).Model(&models.UsageStat{}).
		Where("hour_bucket >= ? AND hour_bucket < ?", input.From, input.To)
	if input.GroupID != nil {
		scope = scope.Where("group_id = ?", *input.GroupID)
	}
	if input.Model != "" {
		scope = scope.Where("model = ?", input.Model)
	}
	return scope
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
	if err := scope.Select("hour_bucket, " + usageAggregateSelect).
		Group("hour_bucket").
		Order("hour_bucket ASC").
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
			start := point.HourBucket.UTC()
			series = append(series, UsageSeriesPoint{
				BucketStart:    start,
				BucketEnd:      start.Add(time.Hour),
				UsageAggregate: point.UsageAggregate,
			})
		}
		return series, nil
	}
	return mergeUsageHoursToDays(source)
}

func queryUsageBreakdown(scope *gorm.DB, limit int) ([]UsageBreakdown, bool, error) {
	var rows []usageBreakdownRow
	if err := scope.Select("group_id, model, " + usageAggregateSelect).
		Group("group_id, model").
		Order("SUM(request_count) DESC").
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
	series := make([]UsageSeriesPoint, 0, len(source))
	for _, point := range source {
		dayStart := point.HourBucket.UTC().Truncate(24 * time.Hour)
		if len(series) == 0 || !series[len(series)-1].BucketStart.Equal(dayStart) {
			series = append(series, UsageSeriesPoint{
				BucketStart:    dayStart,
				BucketEnd:      dayStart.AddDate(0, 0, 1),
				UsageAggregate: point.UsageAggregate,
			})
			continue
		}
		merged, err := addUsageAggregates(series[len(series)-1].UsageAggregate, point.UsageAggregate)
		if err != nil {
			return nil, fmt.Errorf("merge usage day %s: %w", dayStart.Format(time.RFC3339), err)
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
		{"output tokens", left.OutputTokens, right.OutputTokens, &result.OutputTokens},
		{"usage missing count", left.UsageMissingCount, right.UsageMissingCount, &result.UsageMissingCount},
		{"partial count", left.PartialCount, right.PartialCount, &result.PartialCount},
		{"unpriced request count", left.UnpricedRequestCount, right.UnpricedRequestCount, &result.UnpricedRequestCount},
	}
	for _, field := range fields {
		value, ok := usage.CheckedAdd(field.left, field.right)
		if !ok {
			return UsageAggregate{}, fmt.Errorf("%s overflow or negative", field.name)
		}
		*field.target = value
	}
	result.Cost = left.Cost + right.Cost
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
		{"output tokens", aggregate.OutputTokens},
		{"usage missing count", aggregate.UsageMissingCount},
		{"partial count", aggregate.PartialCount},
		{"unpriced request count", aggregate.UnpricedRequestCount},
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
		aggregate.OutputTokens,
	} {
		var added bool
		totalTokens, added = usage.CheckedAdd(totalTokens, value)
		if !added {
			return fmt.Errorf("total tokens overflow")
		}
	}
	if aggregate.Cost < 0 || math.IsNaN(aggregate.Cost) || math.IsInf(aggregate.Cost, 0) {
		return fmt.Errorf("invalid cost")
	}
	return nil
}

const usageAggregateSelect = "" +
	"COALESCE(SUM(request_count), 0) AS request_count, " +
	"COALESCE(SUM(success_count), 0) AS success_count, " +
	"COALESCE(SUM(failure_count), 0) AS failure_count, " +
	"COALESCE(SUM(input_tokens), 0) AS uncached_input_tokens, " +
	"COALESCE(SUM(cache_read_tokens), 0) AS cache_read_tokens, " +
	"COALESCE(SUM(cache_write_5m_tokens), 0) AS cache_write5_m_tokens, " +
	"COALESCE(SUM(cache_write_1h_tokens), 0) AS cache_write1_h_tokens, " +
	"COALESCE(SUM(output_tokens), 0) AS output_tokens, " +
	"COALESCE(SUM(cost), 0) AS cost, " +
	"COALESCE(SUM(usage_missing_count), 0) AS usage_missing_count, " +
	"COALESCE(SUM(partial_count), 0) AS partial_count, " +
	"COALESCE(SUM(unpriced_request_count), 0) AS unpriced_request_count"

type usageHourPoint struct {
	HourBucket time.Time
	UsageAggregate
}

type usageBreakdownRow struct {
	GroupID uint
	Model   string
	UsageAggregate
}
