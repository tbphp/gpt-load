package requestlog

import (
	"context"
	"math"
	"testing"
	"time"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gpt-load/internal/storage/models"
)

func TestQueryUsageHourAggregatesFiltersAndLeavesSparseBucketsAbsent(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)

	rows := make([]models.UsageStat, 0, 26)
	for hour := 0; hour < 24; hour++ {
		if hour == 7 {
			continue
		}
		rows = append(rows, usageStat(start.Add(time.Duration(hour)*time.Hour), 7, "target", int64(hour+1)))
	}
	rows = append(rows,
		usageStat(start.Add(3*time.Hour), 8, "target", 500),
		usageStat(start.Add(4*time.Hour), 7, "other", 600),
		usageStat(start.Add(7*time.Hour), 8, "target", 700),
	)
	rows[0].PartialCount = 1
	createUsageStats(t, db, rows...)

	groupID := uint(7)
	report, err := service.QueryUsage(context.Background(), UsageQuery{
		FromMS:         start.UnixMilli(),
		ToMS:           start.Add(24 * time.Hour).UnixMilli(),
		Granularity:    UsageGranularityHour,
		GroupID:        &groupID,
		UpstreamModel:  "target",
		Limit:          100,
		BreakdownOrder: UsageBreakdownOrderRequests,
	})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	if report.Summary.RequestCount != 292 || report.Summary.SuccessCount != 292 ||
		report.Summary.FailureCount != 0 || report.Summary.UncachedInputTokens != 2920 ||
		report.Summary.CacheReadTokens != 292 || report.Summary.CacheWrite5MTokens != 20 ||
		report.Summary.CacheWrite1HTokens != 49 || report.Summary.OutputTokens != 584 ||
		report.Summary.EstimatedCostNanoUSD != 29_200_000_000 ||
		report.Summary.UsageMissingCount != 12 ||
		report.Summary.PartialCount != 1 || report.Summary.UnpricedRequestCount != 12 {
		t.Fatalf("summary = %#v", report.Summary)
	}
	if len(report.Series) != 23 {
		t.Fatalf("series length = %d, want 23 actual hour buckets", len(report.Series))
	}
	for _, point := range report.Series {
		if point.BucketStartMS == start.Add(7*time.Hour).UnixMilli() {
			t.Fatalf("series synthesized missing hour: %#v", point)
		}
		if point.BucketEndMS-point.BucketStartMS != 3_600_000 {
			t.Fatalf(
				"hour point duration = %dms, want 3600000",
				point.BucketEndMS-point.BucketStartMS,
			)
		}
	}
	if len(report.Breakdown) != 1 || report.Breakdown[0].GroupID != 7 ||
		report.Breakdown[0].Model != "target" || report.Breakdown[0].RequestCount != 292 ||
		report.BreakdownTruncated {
		t.Fatalf("filtered breakdown = %#v truncated=%t", report.Breakdown, report.BreakdownTruncated)
	}
}

func TestQueryUsageExcludesLegacyZeroAttemptAggregates(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.August, 8, 14, 0, 0, 0, time.UTC)
	attributed := usageStat(start, 7, "model-a", 3)
	attributed.AccessKeyID = 1
	legacyZeroAttempt := models.UsageStat{
		BucketStartMS: start.UnixMilli(),
		AccessKeyID:   1,
		GroupID:       0,
		Model:         "",
		RequestCount:  2,
		FailureCount:  2,
	}
	createUsageStats(t, db, attributed, legacyZeroAttempt)

	report, err := service.QueryUsage(context.Background(), UsageQuery{
		FromMS:         start.UnixMilli(),
		ToMS:           start.Add(time.Hour).UnixMilli(),
		Granularity:    UsageGranularityHour,
		Limit:          100,
		BreakdownOrder: UsageBreakdownOrderRequests,
	})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	if report.Summary.RequestCount != 3 || report.Summary.SuccessCount != 3 ||
		report.Summary.FailureCount != 0 {
		t.Fatalf("summary = %#v, want only attributed requests", report.Summary)
	}
	if len(report.Series) != 1 || report.Series[0].RequestCount != 3 ||
		report.Series[0].FailureCount != 0 {
		t.Fatalf("series = %#v, want only attributed requests", report.Series)
	}
	if report.BreakdownCount != 1 || len(report.Breakdown) != 1 ||
		report.Breakdown[0].GroupID != 7 || report.Breakdown[0].Model != "model-a" {
		t.Fatalf("breakdown = %#v count=%d", report.Breakdown, report.BreakdownCount)
	}
}

func TestQueryUsageScopesAccessKeyAndCollapsesBreakdownByModel(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.August, 8, 15, 0, 0, 0, time.UTC)

	firstGroup := usageStat(start, 7, "shared-model", 2)
	firstGroup.AccessKeyID = 41
	secondGroup := usageStat(start, 8, "shared-model", 3)
	secondGroup.AccessKeyID = 41
	otherModel := usageStat(start, 8, "other-model", 1)
	otherModel.AccessKeyID = 41
	otherAccessKey := usageStat(start, 7, "shared-model", 100)
	otherAccessKey.AccessKeyID = 42
	createUsageStats(t, db, firstGroup, secondGroup, otherModel, otherAccessKey)

	accessKeyID := uint(41)
	report, err := service.QueryUsage(context.Background(), UsageQuery{
		FromMS:         start.UnixMilli(),
		ToMS:           start.Add(time.Hour).UnixMilli(),
		Granularity:    UsageGranularityHour,
		AccessKeyID:    &accessKeyID,
		Limit:          100,
		BreakdownOrder: UsageBreakdownOrderRequests,
	})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	if report.Summary.RequestCount != 6 || report.BreakdownCount != 2 ||
		len(report.Breakdown) != 2 {
		t.Fatalf("scoped report = %#v", report)
	}
	if report.Breakdown[0].GroupID != 0 ||
		report.Breakdown[0].Model != "shared-model" ||
		report.Breakdown[0].RequestCount != 5 {
		t.Fatalf("collapsed breakdown[0] = %#v", report.Breakdown[0])
	}
	if report.Breakdown[1].GroupID != 0 ||
		report.Breakdown[1].Model != "other-model" ||
		report.Breakdown[1].RequestCount != 1 {
		t.Fatalf("collapsed breakdown[1] = %#v", report.Breakdown[1])
	}
}

func TestQueryUsageRejectsZeroAccessKeyScope(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.August, 8, 16, 0, 0, 0, time.UTC)
	zero := uint(0)

	_, err := service.QueryUsage(context.Background(), UsageQuery{
		FromMS:         start.UnixMilli(),
		ToMS:           start.Add(time.Hour).UnixMilli(),
		Granularity:    UsageGranularityHour,
		AccessKeyID:    &zero,
		Limit:          100,
		BreakdownOrder: UsageBreakdownOrderRequests,
	})
	if err == nil {
		t.Fatal("QueryUsage() error = nil, want zero AccessKey scope rejection")
	}
}

func TestQueryUsageMergesHourlyRowsIntoThirtyUTCDays(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)
	rows := make([]models.UsageStat, 0, 60)
	for day := 0; day < 30; day++ {
		bucket := start.AddDate(0, 0, day)
		rows = append(rows,
			usageStat(bucket.Add(2*time.Hour), 9, "merge", 1),
			usageStat(bucket.Add(18*time.Hour), 9, "merge", 2),
		)
	}
	createUsageStats(t, db, rows...)

	report, err := service.QueryUsage(context.Background(), UsageQuery{
		FromMS:         start.UnixMilli(),
		ToMS:           start.AddDate(0, 0, 30).UnixMilli(),
		Granularity:    UsageGranularityDay,
		Limit:          100,
		BreakdownOrder: UsageBreakdownOrderRequests,
	})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	if len(report.Series) != 30 || report.Summary.RequestCount != 90 {
		t.Fatalf("day series/summary = %d/%#v, want 30/90", len(report.Series), report.Summary)
	}
	for day, point := range report.Series {
		wantStart := start.AddDate(0, 0, day)
		if point.BucketStartMS != wantStart.UnixMilli() ||
			point.BucketEndMS != wantStart.AddDate(0, 0, 1).UnixMilli() ||
			point.RequestCount != 3 || point.SuccessCount != 3 || point.FailureCount != 0 ||
			point.UncachedInputTokens != 30 || point.CacheReadTokens != 3 || point.OutputTokens != 6 ||
			point.EstimatedCostNanoUSD != 300_000_000 ||
			point.UsageMissingCount != 1 || point.UnpricedRequestCount != 1 {
			t.Fatalf("day %d point = %#v", day, point)
		}
	}
}

func TestQueryUsageMergesHourlyRowsIntoAdaptiveBuckets(t *testing.T) {
	start := time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)
	for _, width := range []time.Duration{3 * time.Hour, 6 * time.Hour, 12 * time.Hour} {
		t.Run(width.String(), func(t *testing.T) {
			db := openRequestLogQueryDB(t)
			service := newRequestLogTestService(db)
			createUsageStats(
				t,
				db,
				usageStat(start, 9, "merge", 1),
				usageStat(start.Add(width-time.Hour), 9, "merge", 2),
				usageStat(start.Add(width), 9, "merge", 3),
				usageStat(start.Add(2*width-time.Hour), 9, "merge", 4),
			)

			report, err := service.QueryUsage(context.Background(), UsageQuery{
				FromMS:         start.UnixMilli(),
				ToMS:           start.Add(2 * width).UnixMilli(),
				Granularity:    UsageGranularityHour,
				BucketWidthMS:  width.Milliseconds(),
				Limit:          100,
				BreakdownOrder: UsageBreakdownOrderRequests,
			})
			if err != nil {
				t.Fatalf("QueryUsage() error = %v", err)
			}
			if len(report.Series) != 2 || report.Summary.RequestCount != 10 {
				t.Fatalf("adaptive series/summary = %#v/%#v, want two buckets/10 requests", report.Series, report.Summary)
			}
			for index, wantRequests := range []int64{3, 7} {
				point := report.Series[index]
				wantStart := start.Add(time.Duration(index) * width)
				if point.BucketStartMS != wantStart.UnixMilli() ||
					point.BucketEndMS != wantStart.Add(width).UnixMilli() ||
					point.RequestCount != wantRequests || point.SuccessCount != wantRequests ||
					point.UncachedInputTokens != wantRequests*10 ||
					point.CacheReadTokens != wantRequests ||
					point.OutputTokens != wantRequests*2 ||
					point.EstimatedCostNanoUSD != wantRequests*100_000_000 {
					t.Fatalf("adaptive point %d = %#v", index, point)
				}
			}
		})
	}
}

func TestQueryUsageRejectsInvalidBucketWidths(t *testing.T) {
	start := time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		from        time.Time
		to          time.Time
		granularity UsageGranularity
		width       time.Duration
	}{
		{name: "not whole hours", from: start, to: start.Add(5 * time.Hour), granularity: UsageGranularityHour, width: 150 * time.Minute},
		{name: "hour bucket is not aligned", from: start.Add(time.Hour), to: start.Add(7 * time.Hour), granularity: UsageGranularityHour, width: 3 * time.Hour},
		{name: "hour range is not divisible", from: start, to: start.Add(5 * time.Hour), granularity: UsageGranularityHour, width: 3 * time.Hour},
		{name: "daily granularity requires a day", from: start, to: start.Add(24 * time.Hour), granularity: UsageGranularityDay, width: 12 * time.Hour},
		{name: "hour granularity cannot use a day", from: start, to: start.Add(24 * time.Hour), granularity: UsageGranularityHour, width: 24 * time.Hour},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openRequestLogQueryDB(t)
			service := newRequestLogTestService(db)
			_, err := service.QueryUsage(context.Background(), UsageQuery{
				FromMS:         test.from.UnixMilli(),
				ToMS:           test.to.UnixMilli(),
				Granularity:    test.granularity,
				BucketWidthMS:  test.width.Milliseconds(),
				Limit:          100,
				BreakdownOrder: UsageBreakdownOrderRequests,
			})
			if err == nil {
				t.Fatal("QueryUsage() error = nil, want invalid bucket width rejection")
			}
		})
	}
}

func TestQueryUsageLimitsBreakdownToStableTopHundred(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.July, 2, 0, 0, 0, 0, time.UTC)
	rows := make([]models.UsageStat, 0, 101)
	for groupID := uint(1); groupID <= 101; groupID++ {
		rows = append(rows, usageStat(start, groupID, "same-count", 1))
	}
	createUsageStats(t, db, rows...)

	report, err := service.QueryUsage(context.Background(), UsageQuery{
		FromMS:         start.UnixMilli(),
		ToMS:           start.Add(time.Hour).UnixMilli(),
		Granularity:    UsageGranularityHour,
		Limit:          100,
		BreakdownOrder: UsageBreakdownOrderRequests,
	})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	if len(report.Breakdown) != 100 || !report.BreakdownTruncated {
		t.Fatalf("breakdown length/truncated = %d/%t, want 100/true", len(report.Breakdown), report.BreakdownTruncated)
	}
	if report.Summary.RequestCount != 101 {
		t.Fatalf("summary request count = %d, want all 101 rows", report.Summary.RequestCount)
	}
	for index, row := range report.Breakdown {
		if row.GroupID != uint(index+1) || row.Model != "same-count" || row.RequestCount != 1 {
			t.Fatalf("breakdown[%d] = %#v, want stable group order", index, row)
		}
	}
}

func TestQueryUsageOrdersBreakdownByRequestsAndCost(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.July, 2, 1, 0, 0, 0, time.UTC)
	requestHeavy := usageStat(start, 1, "request-heavy", 20)
	requestHeavy.EstimatedCostNanoUSD = 1_000_000_000
	expensiveLowRequests := usageStat(start, 2, "expensive-low", 5)
	expensiveLowRequests.EstimatedCostNanoUSD = 10_000_000_000
	expensiveHighRequests := usageStat(start, 3, "expensive-high", 7)
	expensiveHighRequests.EstimatedCostNanoUSD = 10_000_000_000
	createUsageStats(t, db, requestHeavy, expensiveLowRequests, expensiveHighRequests)

	tests := []struct {
		name  string
		order UsageBreakdownOrder
		want  []uint
	}{
		{
			name:  "requests use cost then identity tie breakers",
			order: UsageBreakdownOrderRequests,
			want:  []uint{1, 3, 2},
		},
		{
			name:  "cost uses requests then identity tie breakers",
			order: UsageBreakdownOrderCost,
			want:  []uint{3, 2, 1},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, err := service.QueryUsage(context.Background(), UsageQuery{
				FromMS:         start.UnixMilli(),
				ToMS:           start.Add(time.Hour).UnixMilli(),
				Granularity:    UsageGranularityHour,
				Limit:          100,
				BreakdownOrder: test.order,
			})
			if err != nil {
				t.Fatalf("QueryUsage() error = %v", err)
			}
			if report.BreakdownOrder != test.order || report.BreakdownCount != 3 {
				t.Fatalf(
					"breakdown metadata = %q/%d, want %q/3",
					report.BreakdownOrder,
					report.BreakdownCount,
					test.order,
				)
			}
			if len(report.Breakdown) != len(test.want) {
				t.Fatalf("breakdown = %#v, want %d rows", report.Breakdown, len(test.want))
			}
			for index, wantGroupID := range test.want {
				if report.Breakdown[index].GroupID != wantGroupID {
					t.Fatalf(
						"breakdown[%d].GroupID = %d, want %d; report=%#v",
						index,
						report.Breakdown[index].GroupID,
						wantGroupID,
						report.Breakdown,
					)
				}
			}
		})
	}
}

func TestQueryUsageCountsDistinctGroupModelBreakdownItemsWithinFilters(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.July, 2, 2, 0, 0, 0, time.UTC)
	createUsageStats(
		t,
		db,
		usageStat(start, 1, "model-a", 1),
		usageStat(start, 1, "model-b", 1),
		usageStat(start, 2, "model-a", 1),
		usageStat(start, 3, "model-b", 1),
	)
	groupID := uint(1)
	tests := []struct {
		name    string
		groupID *uint
		model   string
		want    int64
	}{
		{name: "all group model items", want: 4},
		{name: "one group across models", groupID: &groupID, want: 2},
		{name: "model a", model: "model-a", want: 2},
		{name: "model b", model: "model-b", want: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, err := service.QueryUsage(context.Background(), UsageQuery{
				FromMS:         start.UnixMilli(),
				ToMS:           start.Add(time.Hour).UnixMilli(),
				Granularity:    UsageGranularityHour,
				GroupID:        test.groupID,
				UpstreamModel:  test.model,
				Limit:          100,
				BreakdownOrder: UsageBreakdownOrderRequests,
			})
			if err != nil {
				t.Fatalf("QueryUsage() error = %v", err)
			}
			if report.BreakdownCount != test.want {
				t.Fatalf(
					"BreakdownCount = %d, want %d; report=%#v",
					report.BreakdownCount,
					test.want,
					report,
				)
			}
		})
	}
}

func TestQueryUsageRejectsInvalidBreakdownOrder(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.July, 2, 3, 0, 0, 0, time.UTC)
	_, err := service.QueryUsage(context.Background(), UsageQuery{
		FromMS:         start.UnixMilli(),
		ToMS:           start.Add(time.Hour).UnixMilli(),
		Granularity:    UsageGranularityHour,
		Limit:          100,
		BreakdownOrder: UsageBreakdownOrder("unknown"),
	})
	if err == nil {
		t.Fatal("QueryUsage() error = nil, want invalid breakdown order rejection")
	}
}

func TestQueryUsageUsesOneReadSnapshot(t *testing.T) {
	db, dsn := openRequestLogFileDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.July, 3, 0, 0, 0, 0, time.UTC)
	createUsageStats(t, db, usageStat(start, 1, "before", 1))

	writerDB, closeWriter := openUsageQueryWriterDB(t, dsn)
	defer closeWriter()
	inserted := false
	const callbackName = "test:usage_query_snapshot_insert"
	if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if inserted || tx.Statement.Table != "usage_stats" {
			return
		}
		inserted = true
		if err := writerDB.Create(&models.UsageStat{
			BucketStartMS: start.UnixMilli(),
			GroupID:       2,
			Model:         "after-summary",
			RequestCount:  1,
			SuccessCount:  1,
		}).Error; err != nil {
			t.Errorf("insert concurrent UsageStat: %v", err)
		}
	}); err != nil {
		t.Fatalf("register query callback: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Callback().Query().Remove(callbackName); err != nil {
			t.Errorf("remove query callback: %v", err)
		}
	})

	report, err := service.QueryUsage(context.Background(), UsageQuery{
		FromMS:         start.UnixMilli(),
		ToMS:           start.Add(time.Hour).UnixMilli(),
		Granularity:    UsageGranularityHour,
		Limit:          100,
		BreakdownOrder: UsageBreakdownOrderRequests,
	})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	if !inserted || report.Summary.RequestCount != 1 || len(report.Series) != 1 ||
		report.BreakdownCount != 1 ||
		len(report.Breakdown) != 1 || report.Breakdown[0].Model != "before" {
		t.Fatalf("report did not retain one snapshot: inserted=%t report=%#v", inserted, report)
	}
}

func TestQueryUsageCancelsAfterBeginWithoutPoisoningDatabaseConnection(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.July, 4, 0, 0, 0, 0, time.UTC)
	createUsageStats(t, db, usageStat(start, 1, "cancelled", 1))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelled := false
	const callbackName = "test:usage_query_cancel_after_begin"
	if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if cancelled || tx.Statement.Table != "usage_stats" {
			return
		}
		cancelled = true
		cancel()
	}); err != nil {
		t.Fatalf("register query callback: %v", err)
	}
	defer func() {
		if err := db.Callback().Query().Remove(callbackName); err != nil {
			t.Errorf("remove query callback: %v", err)
		}
	}()

	_, err := service.QueryUsage(ctx, UsageQuery{
		FromMS:         start.UnixMilli(),
		ToMS:           start.Add(time.Hour).UnixMilli(),
		Granularity:    UsageGranularityHour,
		Limit:          100,
		BreakdownOrder: UsageBreakdownOrderRequests,
	})
	if err == nil || !cancelled {
		t.Fatalf("QueryUsage() error/cancelled = %v/%t, want cancelled query", err, cancelled)
	}

	var count int64
	if err := db.Model(&models.UsageStat{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("query after cancellation = %d/%v, want 1/nil", count, err)
	}
	if err := db.Create(&models.UsageStat{
		BucketStartMS: start.UnixMilli(),
		GroupID:       2,
		Model:         "after-cancellation",
		RequestCount:  1,
		SuccessCount:  1,
	}).Error; err != nil {
		t.Fatalf("write after cancellation: %v", err)
	}
}

func TestQueryUsageRejectsCorruptRowsOutsideTopBreakdown(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	start := time.Date(2026, time.July, 5, 0, 0, 0, 0, time.UTC)
	rows := make([]models.UsageStat, 0, 102)
	for groupID := uint(1); groupID <= 100; groupID++ {
		rows = append(rows, usageStat(start, groupID, "top", 3))
	}
	compensating := usageStat(start, 101, "compensating", 2)
	compensating.SuccessCount = 2
	compensating.FailureCount = 0
	compensating.UncachedInputTokens = 1
	compensating.EstimatedCostNanoUSD = 1_000_000_000
	corrupt := usageStat(start, 102, "corrupt", 1)
	corrupt.SuccessCount = 1
	corrupt.FailureCount = 0
	corrupt.UncachedInputTokens = -1
	rows = append(rows, compensating, corrupt)
	createCorruptUsageStats(t, db, rows...)

	input := UsageQuery{
		FromMS:         start.UnixMilli(),
		ToMS:           start.Add(time.Hour).UnixMilli(),
		Granularity:    UsageGranularityHour,
		Limit:          100,
		BreakdownOrder: UsageBreakdownOrderRequests,
	}
	if summary, err := queryUsageSummary(usageStatScope(db, input)); err != nil || summary.RequestCount != 303 {
		t.Fatalf("pre-integrity summary = %#v/%v, want valid 303-request aggregate", summary, err)
	}
	if series, err := queryUsageSeries(usageStatScope(db, input), time.Hour.Milliseconds()); err != nil || len(series) != 1 {
		t.Fatalf("pre-integrity series = %#v/%v, want one valid hour aggregate", series, err)
	}
	if breakdown, truncated, err := queryUsageBreakdown(
		usageStatScope(db, input),
		input.Limit,
		input.BreakdownOrder,
		false,
	); err != nil ||
		len(breakdown) != 100 || !truncated || breakdown[99].GroupID != 100 {
		t.Fatalf("pre-integrity breakdown = %#v/%t/%v, want valid top 100 without group 102", breakdown, truncated, err)
	}

	_, err := service.QueryUsage(context.Background(), input)
	if err == nil {
		t.Fatal("QueryUsage() error = nil, want corrupt 102nd breakdown row rejection")
	}
}

func TestQueryUsageRejectsCorruptAggregates(t *testing.T) {
	start := time.Date(2026, time.July, 4, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		rows []models.UsageStat
	}{
		{
			name: "negative count",
			rows: []models.UsageStat{func() models.UsageStat {
				row := usageStat(start, 1, "negative", 1)
				row.FailureCount = -1
				return row
			}()},
		},
		{
			name: "negative input tokens",
			rows: []models.UsageStat{func() models.UsageStat {
				row := usageStat(start, 1, "negative-input", 1)
				row.UncachedInputTokens = -1
				return row
			}()},
		},
		{
			name: "negative cached tokens",
			rows: []models.UsageStat{func() models.UsageStat {
				row := usageStat(start, 1, "negative-cache", 1)
				row.CacheWrite5MTokens = -1
				return row
			}()},
		},
		{
			name: "request count mismatch",
			rows: []models.UsageStat{func() models.UsageStat {
				row := usageStat(start, 1, "mismatch", 3)
				row.SuccessCount = 1
				row.FailureCount = 1
				return row
			}()},
		},
		{
			name: "checked total token overflow",
			rows: []models.UsageStat{func() models.UsageStat {
				row := usageStat(start, 1, "token-overflow", 1)
				row.UncachedInputTokens = math.MaxInt64
				row.OutputTokens = 1
				return row
			}()},
		},
		{
			name: "checked count overflow",
			rows: []models.UsageStat{
				{
					BucketStartMS: start.UnixMilli(),
					GroupID:       1,
					Model:         "overflow-a",
					RequestCount:  math.MaxInt64,
					SuccessCount:  math.MaxInt64,
				},
				{
					BucketStartMS: start.UnixMilli(),
					GroupID:       2,
					Model:         "overflow-b",
					RequestCount:  1,
					SuccessCount:  1,
				},
			},
		},
		{
			name: "checked cost overflow",
			rows: []models.UsageStat{func() models.UsageStat {
				row := usageStat(start, 1, "huge-cost-a", 1)
				row.EstimatedCostNanoUSD = math.MaxInt64
				return row
			}(), func() models.UsageStat {
				row := usageStat(start, 2, "huge-cost-b", 1)
				row.EstimatedCostNanoUSD = math.MaxInt64
				return row
			}()},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openRequestLogQueryDB(t)
			createCorruptUsageStats(t, db, test.rows...)
			_, err := newRequestLogTestService(db).QueryUsage(context.Background(), UsageQuery{
				FromMS:         start.UnixMilli(),
				ToMS:           start.Add(time.Hour).UnixMilli(),
				Granularity:    UsageGranularityHour,
				Limit:          100,
				BreakdownOrder: UsageBreakdownOrderRequests,
			})
			if err == nil {
				t.Fatal("QueryUsage() error = nil, want corrupt aggregate rejection")
			}
		})
	}
}

func usageStat(hour time.Time, groupID uint, model string, requestCount int64) models.UsageStat {
	return models.UsageStat{
		BucketStartMS:        hour.UTC().UnixMilli(),
		GroupID:              groupID,
		Model:                model,
		RequestCount:         requestCount,
		SuccessCount:         requestCount,
		UncachedInputTokens:  requestCount * 10,
		OutputTokens:         requestCount * 2,
		CacheReadTokens:      requestCount,
		CacheWrite5MTokens:   requestCount / 10,
		CacheWrite1HTokens:   requestCount / 5,
		EstimatedCostNanoUSD: requestCount * 100_000_000,
		UsageMissingCount:    requestCount % 2,
		PartialCount:         0,
		UnpricedRequestCount: requestCount % 2,
	}
}

func createUsageStats(t *testing.T, db *gorm.DB, rows ...models.UsageStat) {
	t.Helper()
	for _, row := range rows {
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("create UsageStat %#v: %v", row, err)
		}
	}
}

func createCorruptUsageStats(t *testing.T, db *gorm.DB, rows ...models.UsageStat) {
	t.Helper()
	if err := db.Exec(`PRAGMA ignore_check_constraints = ON`).Error; err != nil {
		t.Fatalf("disable SQLite CHECK constraints: %v", err)
	}
	for _, row := range rows {
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("create corrupt UsageStat %#v: %v", row, err)
		}
	}
	if err := db.Exec(`PRAGMA ignore_check_constraints = OFF`).Error; err != nil {
		t.Fatalf("restore SQLite CHECK constraints: %v", err)
	}
}

func openUsageQueryWriterDB(t *testing.T, dsn string) (*gorm.DB, func()) {
	t.Helper()
	db, err := gorm.Open(
		gormsqlite.Open(dsn+"?_pragma=busy_timeout(5000)"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatalf("open concurrent UsageStat writer: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get concurrent UsageStat writer DB: %v", err)
	}
	return db, func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close concurrent UsageStat writer: %v", err)
		}
	}
}
