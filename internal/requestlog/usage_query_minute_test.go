package requestlog

import (
	"context"
	"math"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/channel"
	"gpt-load/internal/storage/models"
)

func TestQueryUsageMinuteReadsRollingLogsAndClampsBuckets(t *testing.T) {
	db := openRequestLogQueryDB(t)
	start := time.Date(2026, time.September, 7, 13, 2, 17, 123_000_000, time.UTC)
	end := start.Add(time.Hour)
	rows := []models.RequestLog{
		aggregationRow(aggregationRequestID(100), start.Add(-time.Millisecond), 7, "model"),
		aggregationRow(aggregationRequestID(101), start, 7, "model"),
		aggregationRow(aggregationRequestID(102), start.Truncate(time.Hour).Add(time.Hour), 7, "model"),
		aggregationRow(aggregationRequestID(103), end.Add(-time.Millisecond), 7, "model"),
		aggregationRow(aggregationRequestID(104), end, 7, "model"),
		aggregationRow(aggregationRequestID(105), start.Add(time.Minute), 7, "model"),
	}
	rows[5].AttemptCount = 0
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	createUsageStats(t, db, usageStat(start.Truncate(time.Hour), 7, "model", 999))
	report, err := newRequestLogTestService(db).QueryUsage(context.Background(), minuteUsageQuery(start))
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	if report.Summary.RequestCount != 3 || report.Summary.EstimatedCostNanoUSD != 750_000_000 {
		t.Fatalf("summary = %#v, want three in-range attempted requests", report.Summary)
	}
	if len(report.Series) != 2 || report.Series[0].BucketStartMS != start.UnixMilli() ||
		report.Series[0].BucketEndMS != start.Truncate(time.Hour).Add(5*time.Minute).UnixMilli() ||
		report.Series[0].RequestCount != 1 ||
		report.Series[1].BucketStartMS != end.Truncate(time.Hour).UnixMilli() ||
		report.Series[1].BucketEndMS != end.UnixMilli() || report.Series[1].RequestCount != 2 {
		t.Fatalf("series = %#v, want sparse five-minute buckets clipped to rolling range", report.Series)
	}
	assertMinuteUsageReportTotals(t, report)
	hourQuery := UsageQuery{
		FromMS:      start.Truncate(time.Hour).UnixMilli(),
		ToMS:        start.Truncate(time.Hour).Add(24 * time.Hour).UnixMilli(),
		Granularity: UsageGranularityHour,
	}
	hour, err := newRequestLogTestService(db).QueryUsage(context.Background(), hourQuery)
	if err != nil || hour.Summary.RequestCount != 999 {
		t.Fatalf("hourly report = %#v/%v, want unchanged hourly aggregate source", hour, err)
	}
}

func TestQueryUsageMinuteCanIncludeThirteenPartialAndFullBuckets(t *testing.T) {
	db := openRequestLogQueryDB(t)
	start := time.Date(2026, time.September, 7, 13, 2, 0, 0, time.UTC)
	rows := []models.RequestLog{aggregationRow(aggregationRequestID(600), start, 7, "model")}
	for index := 1; index <= 12; index++ {
		rows = append(rows, aggregationRow(aggregationRequestID(600+index), start.Truncate(time.Hour).Add(time.Duration(index)*5*time.Minute), 7, "model"))
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	query := minuteUsageQuery(start)
	query.BucketWidthMS = UsageFiveMinuteBucketMS
	report, err := newRequestLogTestService(db).QueryUsage(context.Background(), query)
	if err != nil || len(report.Series) != 13 {
		t.Fatalf("QueryUsage() series/error = %#v/%v, want thirteen natural buckets", report.Series, err)
	}
	for index, point := range report.Series {
		if point.BucketStartMS < query.FromMS || point.BucketEndMS > query.ToMS ||
			point.BucketStartMS >= point.BucketEndMS || point.RequestCount != 1 {
			t.Fatalf("invalid bucket %d: %#v", index, point)
		}
		if index > 0 && report.Series[index-1].BucketEndMS != point.BucketStartMS {
			t.Fatalf("noncontiguous buckets %d and %d", index-1, index)
		}
	}
	assertMinuteUsageReportTotals(t, report)
}

func TestQueryUsageMinuteClipsBucketNearTimestampLimit(t *testing.T) {
	db := openRequestLogQueryDB(t)
	const maxSafeMilliseconds int64 = 1<<53 - 1
	start := time.UnixMilli(maxSafeMilliseconds - time.Hour.Milliseconds())
	row := aggregationRow(aggregationRequestID(700), time.UnixMilli(maxSafeMilliseconds-1), 7, "model")
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	report, err := newRequestLogTestService(db).QueryUsage(context.Background(), minuteUsageQuery(start))
	if err != nil || len(report.Series) != 1 || report.Series[0].BucketEndMS != maxSafeMilliseconds ||
		report.Series[0].BucketStartMS < start.UnixMilli() {
		t.Fatalf("QueryUsage() series/error = %#v/%v, want clipped bucket without overflow", report.Series, err)
	}
}

func TestQueryUsageMinuteMatchesFrozenHourlyAccounting(t *testing.T) {
	db := openRequestLogQueryDB(t)
	start := time.Date(2026, time.September, 7, 13, 0, 0, 0, time.UTC)
	rows := make([]models.RequestLog, 7)
	for index := range rows {
		rows[index] = aggregationRow(aggregationRequestID(200+index), start.Add(time.Duration(index)*5*time.Minute), 7, "model")
		rows[index].CacheReadTokens = 3
		rows[index].CacheWrite5MTokens = 4
		rows[index].CacheWrite1HTokens = 5
		rows[index].CacheWriteUnknownTokens = 6
	}
	rows[1].Status = "error"
	rows[1].UsageState, rows[1].CostState, rows[1].PricingCompleteness = "missing", "unpriced", "unavailable"
	rows[1].EstimatedCostNanoUSD = 0
	rows[2].Status = "incomplete"
	rows[2].UsageState, rows[2].PricingCompleteness = "partial", "partial"
	rows[3].Status = "canceled"
	rows[3].UsageState, rows[3].CostState, rows[3].PricingCompleteness = "partial", "unpriced", "unavailable"
	rows[3].EstimatedCostNanoUSD = 0
	rows[4].CostState, rows[4].PricingCompleteness = "unpriced", "unavailable"
	rows[4].EstimatedCostNanoUSD = 0
	rows[5].UsageState, rows[5].CostState, rows[5].PricingCompleteness = "not_applicable", "not_applicable", "not_applicable"
	rows[5].EstimatedCostNanoUSD = 0
	rows[6].AttemptCount = 0
	if err := (&gormBatchWriter{db: db}).WriteBatch(context.Background(), rows); err != nil {
		t.Fatal(err)
	}
	service := newRequestLogTestService(db)
	minute, err := service.QueryUsage(context.Background(), minuteUsageQuery(start))
	if err != nil {
		t.Fatalf("minute QueryUsage() error = %v", err)
	}
	hourQuery := minuteUsageQuery(start)
	hourQuery.Granularity = UsageGranularityHour
	hourQuery.ToMS = start.Add(2 * time.Hour).UnixMilli()
	hour, err := service.QueryUsage(context.Background(), hourQuery)
	if err != nil {
		t.Fatalf("hour QueryUsage() error = %v", err)
	}
	if minute.Summary != hour.Summary || !reflect.DeepEqual(minute.Distributions, hour.Distributions) {
		t.Fatalf("frozen accounting differs: minute=%#v hour=%#v", minute, hour)
	}
	if minute.Summary.RequestCount != 6 || minute.Summary.SuccessCount != 3 || minute.Summary.FailureCount != 3 ||
		minute.Summary.UsageMissingCount != 1 || minute.Summary.PartialCount != 2 ||
		minute.Summary.UnpricedRequestCount != 2 || minute.Summary.PricingPartialCount != 1 ||
		minute.Summary.EstimatedCostNanoUSD != 500_000_000 || minute.Summary.UncachedInputTokens != 4 {
		t.Fatalf("summary = %#v", minute.Summary)
	}
	assertMinuteUsageReportTotals(t, minute)
}

func TestQueryUsageMinuteFiltersFinalAttributionAndAccessKey(t *testing.T) {
	db := openRequestLogQueryDB(t)
	start := time.Date(2026, time.September, 7, 13, 0, 0, 0, time.UTC)
	rows := make([]models.RequestLog, 6)
	for index := range rows {
		rows[index] = aggregationRow(aggregationRequestID(300+index), start.Add(time.Minute), 7, "final-model")
		rows[index].ChannelID, rows[index].CredentialID, rows[index].AccessKeyID = "openai", 11, 41
		rows[index].AttemptCount = 2
	}
	rows[1].GroupID = 8
	rows[2].ChannelID = "anthropic"
	rows[3].CredentialID = 12
	rows[4].AccessKeyID = 42
	rows[5].UpstreamModel = "other-model"
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	// 先前尝试既不能匹配最终归属筛选，也不能让同一请求被重复计数。
	for index := range rows {
		attempts := []models.RequestLogAttempt{
			{RequestID: rows[index].ID, Sequence: 1, CompletedAtMS: rows[index].CompletedAtMS, GroupID: 99, CredentialID: 99, ChannelID: "gemini", UpstreamModel: "earlier-model", FailureCategory: "rate_limited", Action: "retry"},
			{RequestID: rows[index].ID, Sequence: 2, CompletedAtMS: rows[index].CompletedAtMS, GroupID: rows[index].GroupID, CredentialID: rows[index].CredentialID, ChannelID: rows[index].ChannelID, UpstreamModel: rows[index].UpstreamModel, FailureCategory: "ok", Action: "terminate"},
		}
		if err := db.Create(&attempts).Error; err != nil {
			t.Fatal(err)
		}
	}
	groupID, credentialID, accessKeyID := uint(7), uint(11), uint(41)
	query := minuteUsageQuery(start)
	query.GroupID, query.ChannelID, query.CredentialID = &groupID, channel.OpenAI, &credentialID
	query.AccessKeyID, query.UpstreamModel = &accessKeyID, "final-model"
	query.SelfScoped = true
	report, err := newRequestLogTestService(db).QueryUsage(context.Background(), query)
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	if report.Summary.RequestCount != 1 || len(report.Distributions.Group) != 0 || len(report.Distributions.AccessKey) != 0 {
		t.Fatalf("scoped report = %#v", report)
	}
	assertMinuteUsageReportTotals(t, report)
	query.GroupID, query.ChannelID, query.CredentialID = nil, "", nil
	query.UpstreamModel = "earlier-model"
	report, err = newRequestLogTestService(db).QueryUsage(context.Background(), query)
	if err != nil || report.Summary.RequestCount != 0 {
		t.Fatalf("earlier attempt filter = %#v/%v, want empty report", report, err)
	}
}

func TestQueryUsageMinuteUsesOneReadSnapshot(t *testing.T) {
	db, dsn := openRequestLogFileDB(t)
	start := time.Date(2026, time.September, 7, 13, 2, 0, 0, time.UTC)
	row := aggregationRow(aggregationRequestID(400), start, 7, "before")
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	writerDB, closeWriter := openUsageQueryWriterDB(t, dsn)
	defer closeWriter()
	inserted := false
	const callbackName = "test:minute_usage_snapshot_insert"
	if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if inserted || tx.DryRun {
			return
		}
		inserted = true
		after := aggregationRow(aggregationRequestID(401), start.Add(time.Minute), 8, "after")
		if err := writerDB.Create(&after).Error; err != nil {
			t.Errorf("insert concurrent request log: %v", err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Callback().Query().Remove(callbackName); err != nil {
			t.Error(err)
		}
	})
	report, err := newRequestLogTestService(db).QueryUsage(context.Background(), minuteUsageQuery(start))
	if err != nil || !inserted || report.Summary.RequestCount != 1 {
		t.Fatalf("snapshot report/inserted/error = %#v/%t/%v", report, inserted, err)
	}
	assertMinuteUsageReportTotals(t, report)
}

func TestQueryUsageDerivesWidthFromTimeRange(t *testing.T) {
	start := time.Date(2026, time.September, 7, 13, 2, 0, 0, time.UTC)
	for _, test := range []struct {
		name     string
		duration time.Duration
		width    time.Duration
		want     time.Duration
	}{
		{"short range", 59 * time.Minute, 5 * time.Minute, 5 * time.Minute},
		{"long range", 61 * time.Minute, 5 * time.Minute, time.Hour},
		{"caller bucket is ignored", time.Hour, time.Minute, 5 * time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := minuteUsageQuery(start)
			query.ToMS, query.BucketWidthMS = start.Add(test.duration).UnixMilli(), test.width.Milliseconds()
			if width, err := validateUsageQuery(query); err != nil || width != test.want.Milliseconds() {
				t.Fatalf("validateUsageQuery() width/error = %d/%v, want %s", width, err, test.want)
			}
		})
	}
}

func TestQueryUsageMinuteRejectsTokenAndCostOverflow(t *testing.T) {
	for _, field := range []string{"uncached_input_tokens", "estimated_cost_nano_usd"} {
		t.Run(field, func(t *testing.T) {
			db := openRequestLogQueryDB(t)
			start := time.Date(2026, time.September, 7, 13, 2, 0, 0, time.UTC)
			rows := []models.RequestLog{
				aggregationRow(aggregationRequestID(500), start, 7, "model"),
				aggregationRow(aggregationRequestID(501), start.Add(time.Minute), 8, "model"),
			}
			if err := db.Create(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&models.RequestLog{}).Where("id = ?", rows[0].ID).Update(field, int64(math.MaxInt64)).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := newRequestLogTestService(db).QueryUsage(context.Background(), minuteUsageQuery(start)); err == nil {
				t.Fatal("QueryUsage() error = nil, want numeric overflow rejection")
			}
		})
	}
}

func minuteUsageQuery(start time.Time) UsageQuery {
	return UsageQuery{FromMS: start.UnixMilli(), ToMS: start.Add(time.Hour).UnixMilli(), Granularity: UsageGranularityMinute}
}

func assertMinuteUsageReportTotals(t *testing.T, report UsageReport) {
	t.Helper()
	var total UsageAggregate
	for _, point := range report.Series {
		var err error
		total, err = addUsageAggregates(total, point.UsageAggregate)
		if err != nil {
			t.Fatal(err)
		}
	}
	if total != report.Summary {
		t.Fatalf("series sum = %#v, want summary %#v", total, report.Summary)
	}
	tokens, err := usageAggregateTotalTokens(report.Summary)
	if err != nil {
		t.Fatal(err)
	}
	want := UsageDistributionAggregate{RequestCount: total.RequestCount, TotalTokens: tokens, EstimatedCostNanoUSD: total.EstimatedCostNanoUSD}
	for _, distributions := range []map[UsageDistributionMetric]UsageDistribution{report.Distributions.Group, report.Distributions.Model, report.Distributions.AccessKey} {
		for _, distribution := range distributions {
			var got UsageDistributionAggregate
			for _, item := range distribution.Items {
				got, err = addUsageDistributionAggregates(got, item.UsageDistributionAggregate)
				if err != nil {
					t.Fatal(err)
				}
			}
			if distribution.Other != nil {
				got, err = addUsageDistributionAggregates(got, *distribution.Other)
				if err != nil {
					t.Fatal(err)
				}
			}
			if got != want {
				t.Fatalf("%s/%s total = %#v, want %#v", distribution.Dimension, distribution.Metric, got, want)
			}
		}
	}
}
