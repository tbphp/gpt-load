package requestlog

import (
	"fmt"

	"gorm.io/gorm"

	"gpt-load/internal/platform/epochms"
)

// ResolveUsageTimeBucket 根据精确时间跨度选择趋势桶，不依赖前端快捷范围。
func ResolveUsageTimeBucket(fromMS, toMS int64) (UsageGranularity, int64, error) {
	const maxSafeMilliseconds int64 = 1<<53 - 1
	if fromMS < 0 || toMS <= fromMS || toMS > maxSafeMilliseconds {
		return "", 0, fmt.Errorf("query usage: invalid or unsafe time range")
	}
	spanMS := toMS - fromMS
	hour, day := epochms.MillisecondsPerHour, epochms.MillisecondsPerDay
	switch {
	case spanMS <= hour:
		return UsageGranularityMinute, UsageFiveMinuteBucketMS, nil
	case spanMS <= day:
		return UsageGranularityHour, hour, nil
	case spanMS <= 3*day:
		return UsageGranularityHour, 3 * hour, nil
	case spanMS <= 7*day:
		return UsageGranularityHour, 6 * hour, nil
	case spanMS <= 15*day:
		return UsageGranularityHour, 12 * hour, nil
	default:
		// 向上取整时不先加除数，超长区间仍保持安全整数运算。
		return UsageGranularityDay, ((spanMS-1)/(30*day) + 1) * day, nil
	}
}

// usageWindowScope 先按时间拆分来源，再合并行集供总览、趋势和分布使用。
// 数据缺失不会改变分段，也不会把首尾小时的整桶统计带入精确区间。
func usageWindowScope(db *gorm.DB, input UsageQuery) *gorm.DB {
	hour := epochms.MillisecondsPerHour
	if input.ToMS-input.FromMS <= hour {
		return usageRequestLogScope(db, input)
	}
	fullFromMS := input.FromMS
	if remainder := fullFromMS % hour; remainder != 0 {
		fullFromMS += hour - remainder
	}
	fullToMS := input.ToMS - input.ToMS%hour
	full := input
	full.FromMS, full.ToMS = fullFromMS, fullToMS
	// 数值运算固定参数类型，避免 PostgreSQL 把 SELECT 中的独立参数推断为 text。
	parts := []any{usageStatScope(db, full).Select(usageWindowColumns+", ? + 0 AS bucket_alignment_ms", hour)}
	unionSQL := "?"
	for _, boundary := range [][2]int64{{input.FromMS, fullFromMS}, {fullToMS, input.ToMS}} {
		if boundary[0] >= boundary[1] {
			continue
		}
		part := input
		part.FromMS, part.ToMS = boundary[0], boundary[1]
		parts = append(parts, usageRequestLogScope(db, part).Select(usageWindowColumns+", bucket_alignment_ms"))
		unionSQL += " UNION ALL ?"
	}
	return db.Session(&gorm.Session{NewDB: true}).Table("("+unionSQL+") AS usage_rows", parts...)
}

func queryUsageWindowSeries(scope *gorm.DB, input UsageQuery, widthMS int64) ([]UsageSeriesPoint, error) {
	var rows []struct {
		BucketStartMS int64 `gorm:"column:series_bucket_ms"`
		UsageAggregate
	}
	// 直接按最终自然桶聚合，年度等长区间不会把每个小时传回进程再合并。
	if err := scope.Select("bucket_start_ms - bucket_start_ms % ? AS series_bucket_ms, "+usageAggregateSelect, widthMS).
		Group("series_bucket_ms").Order("series_bucket_ms ASC").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("query usage window series: %w", err)
	}
	series := make([]UsageSeriesPoint, 0, len(rows))
	for _, row := range rows {
		if err := validateUsageAggregate(row.UsageAggregate); err != nil {
			return nil, fmt.Errorf("validate usage window series: %w", err)
		}
		endMS := input.ToMS
		if input.ToMS-row.BucketStartMS > widthMS {
			endMS = row.BucketStartMS + widthMS
		}
		series = append(series, UsageSeriesPoint{
			BucketStartMS: max(row.BucketStartMS, input.FromMS), BucketEndMS: endMS,
			UsageAggregate: row.UsageAggregate,
		})
	}
	return series, nil
}

const usageWindowColumns = `bucket_start_ms, group_id, access_key_id, model,
	request_count, success_count, failure_count,
	uncached_input_tokens, cache_read_tokens, cache_write_5m_tokens, cache_write_1h_tokens,
	cache_write_unknown_tokens, output_tokens, estimated_cost_nano_usd,
	usage_missing_count, partial_count, unpriced_request_count, pricing_partial_count`
