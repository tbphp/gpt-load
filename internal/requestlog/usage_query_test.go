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
		From:        start,
		To:          start.Add(24 * time.Hour),
		Granularity: UsageGranularityHour,
		GroupID:     &groupID,
		Model:       "target",
		Limit:       100,
	})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	if report.Summary.RequestCount != 292 || report.Summary.SuccessCount != 292 ||
		report.Summary.FailureCount != 0 || report.Summary.UncachedInputTokens != 2920 ||
		report.Summary.CacheReadTokens != 292 || report.Summary.CacheWrite5MTokens != 20 ||
		report.Summary.CacheWrite1HTokens != 49 || report.Summary.OutputTokens != 584 ||
		!sameUsageCost(report.Summary.Cost, 29.2) || report.Summary.UsageMissingCount != 12 ||
		report.Summary.PartialCount != 1 || report.Summary.UnpricedRequestCount != 12 {
		t.Fatalf("summary = %#v", report.Summary)
	}
	if len(report.Series) != 23 {
		t.Fatalf("series length = %d, want 23 actual hour buckets", len(report.Series))
	}
	for _, point := range report.Series {
		if point.BucketStart.Equal(start.Add(7 * time.Hour)) {
			t.Fatalf("series synthesized missing hour: %#v", point)
		}
		if point.BucketEnd.Sub(point.BucketStart) != time.Hour {
			t.Fatalf("hour point duration = %s, want 1h", point.BucketEnd.Sub(point.BucketStart))
		}
	}
	if len(report.Breakdown) != 1 || report.Breakdown[0].GroupID != 7 ||
		report.Breakdown[0].Model != "target" || report.Breakdown[0].RequestCount != 292 ||
		report.BreakdownTruncated {
		t.Fatalf("filtered breakdown = %#v truncated=%t", report.Breakdown, report.BreakdownTruncated)
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
		From:        start,
		To:          start.AddDate(0, 0, 30),
		Granularity: UsageGranularityDay,
		Limit:       100,
	})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	if len(report.Series) != 30 || report.Summary.RequestCount != 90 {
		t.Fatalf("day series/summary = %d/%#v, want 30/90", len(report.Series), report.Summary)
	}
	for day, point := range report.Series {
		wantStart := start.AddDate(0, 0, day)
		if !point.BucketStart.Equal(wantStart) || !point.BucketEnd.Equal(wantStart.AddDate(0, 0, 1)) ||
			point.RequestCount != 3 || point.SuccessCount != 3 || point.FailureCount != 0 ||
			point.UncachedInputTokens != 30 || point.CacheReadTokens != 3 || point.OutputTokens != 6 ||
			!sameUsageCost(point.Cost, 0.3) || point.UsageMissingCount != 1 || point.UnpricedRequestCount != 1 {
			t.Fatalf("day %d point = %#v", day, point)
		}
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
		From:        start,
		To:          start.Add(time.Hour),
		Granularity: UsageGranularityHour,
		Limit:       100,
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
			HourBucket:   start,
			GroupID:      2,
			Model:        "after-summary",
			RequestCount: 1,
			SuccessCount: 1,
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
		From:        start,
		To:          start.Add(time.Hour),
		Granularity: UsageGranularityHour,
		Limit:       100,
	})
	if err != nil {
		t.Fatalf("QueryUsage() error = %v", err)
	}
	if !inserted || report.Summary.RequestCount != 1 || len(report.Series) != 1 ||
		len(report.Breakdown) != 1 || report.Breakdown[0].Model != "before" {
		t.Fatalf("report did not retain one snapshot: inserted=%t report=%#v", inserted, report)
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
				row.InputTokens = -1
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
				row.InputTokens = math.MaxInt64
				row.OutputTokens = 1
				return row
			}()},
		},
		{
			name: "checked count overflow",
			rows: []models.UsageStat{
				usageStat(start, 1, "overflow-a", math.MaxInt64),
				usageStat(start, 2, "overflow-b", 1),
			},
		},
		{
			name: "negative cost",
			rows: []models.UsageStat{func() models.UsageStat {
				row := usageStat(start, 1, "negative-cost", 1)
				row.Cost = -0.1
				return row
			}()},
		},
		{
			name: "non-finite summed cost",
			rows: []models.UsageStat{func() models.UsageStat {
				row := usageStat(start, 1, "huge-cost-a", 1)
				row.Cost = math.MaxFloat64
				return row
			}(), func() models.UsageStat {
				row := usageStat(start, 2, "huge-cost-b", 1)
				row.Cost = math.MaxFloat64
				return row
			}()},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openRequestLogQueryDB(t)
			createUsageStats(t, db, test.rows...)
			_, err := newRequestLogTestService(db).QueryUsage(context.Background(), UsageQuery{
				From:        start,
				To:          start.Add(time.Hour),
				Granularity: UsageGranularityHour,
				Limit:       100,
			})
			if err == nil {
				t.Fatal("QueryUsage() error = nil, want corrupt aggregate rejection")
			}
		})
	}
}

func usageStat(hour time.Time, groupID uint, model string, requestCount int64) models.UsageStat {
	return models.UsageStat{
		HourBucket:           hour,
		GroupID:              groupID,
		Model:                model,
		RequestCount:         requestCount,
		SuccessCount:         requestCount,
		InputTokens:          requestCount * 10,
		OutputTokens:         requestCount * 2,
		CacheReadTokens:      requestCount,
		CacheWrite5MTokens:   requestCount / 10,
		CacheWrite1HTokens:   requestCount / 5,
		Cost:                 float64(requestCount) / 10,
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

func sameUsageCost(got, want float64) bool {
	return math.Abs(got-want) < 1e-12
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
