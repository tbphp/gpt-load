package requestlog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

func TestRetentionSweepUsesSnapshotRetentionPolicy(t *testing.T) {
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		days   int
		cutoff time.Time
	}{
		{name: "1", days: 1, cutoff: time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)},
		{name: "7", days: 7, cutoff: time.Date(2026, time.July, 17, 12, 0, 0, 0, time.UTC)},
		{name: "365", days: 365, cutoff: time.Date(2025, time.July, 24, 12, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openRequestLogQueryDB(t)
			service := NewService(
				db,
				redact.New(),
				staticRetentionPolicy{days: tt.days},
			)
			createRetentionRow(t, db, 1, tt.cutoff.Add(-time.Nanosecond))
			createRetentionRow(t, db, 2, tt.cutoff)

			var settingQueries atomic.Int64
			const callbackName = "test:no_runtime_setting_query"
			if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
				if tx.Statement.Table == "system_settings" {
					settingQueries.Add(1)
				}
			}); err != nil {
				t.Fatal(err)
			}

			service.Sweep(context.Background(), now)
			assertRetentionRows(t, db, 2)
			if settingQueries.Load() != 0 {
				t.Fatalf("system_settings queries = %d", settingQueries.Load())
			}
		})
	}
}

func TestRetentionSweepDeletesStrictlyOlderRowsInBatches(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-7 * 24 * time.Hour)

	expired := make([]models.RequestLog, 0, retentionBatchSize+1)
	for index := 1; index <= retentionBatchSize+1; index++ {
		expired = append(expired, retentionRow(index, cutoff.Add(-time.Duration(index)*time.Nanosecond)))
	}
	if err := db.CreateInBatches(expired, 200).Error; err != nil {
		t.Fatalf("create expired RequestLogs: %v", err)
	}
	createRetentionRow(t, db, retentionBatchSize+2, cutoff)
	createRetentionRow(t, db, retentionBatchSize+3, cutoff.Add(time.Nanosecond))

	var deleteBatches atomic.Int64
	const callbackName = "test:retention_delete_batches"
	if err := db.Callback().Delete().After("gorm:delete").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "request_logs" && tx.Error == nil {
			deleteBatches.Add(1)
		}
	}); err != nil {
		t.Fatalf("register delete callback: %v", err)
	}

	service.Sweep(context.Background(), now)

	assertRetentionRows(t, db, retentionBatchSize+2, retentionBatchSize+3)
	if got := deleteBatches.Load(); got != 2 {
		t.Fatalf("delete batches = %d, want 2", got)
	}
	if stats := service.Stats(); stats.RetentionDeleteFailureTotal != 0 ||
		!stats.LastRetentionFailureAt.IsZero() {
		t.Fatalf("Stats() = %#v, want no retention failures", stats)
	}
}

func TestRetentionSweepUsesFixedThirtyFiveDayIntegerUsageBoundary(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := NewService(
		db,
		redact.New(),
		staticRetentionPolicy{days: 1},
	)
	now := time.Date(2026, time.July, 24, 12, 34, 56, 789_000_000, time.UTC)
	const usageCutoffMS int64 = 1_781_870_400_000
	for index, bucketStartMS := range []int64{
		usageCutoffMS - 3_600_000,
		usageCutoffMS,
		usageCutoffMS + 3_600_000,
	} {
		if err := db.Create(&models.UsageStat{
			BucketStartMS: bucketStartMS,
			AccessKeyID:   uint(index),
			GroupID:       17,
			Model:         "retention-model",
			RequestCount:  1,
			SuccessCount:  1,
		}).Error; err != nil {
			t.Fatalf("create UsageStat at %d: %v", bucketStartMS, err)
		}
	}

	service.Sweep(context.Background(), now)

	var remaining []models.UsageStat
	if err := db.Order("bucket_start_ms ASC").Find(&remaining).Error; err != nil {
		t.Fatalf("query remaining UsageStats: %v", err)
	}
	if len(remaining) != 2 ||
		remaining[0].BucketStartMS != usageCutoffMS ||
		remaining[1].BucketStartMS != usageCutoffMS+3_600_000 {
		t.Fatalf("remaining UsageStats = %+v, want cutoff and newer integer buckets", remaining)
	}
}

func TestRetentionSweepCleansUsageStatsWhenRequestLogDeleteFails(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	discardRetentionWarnings(service)
	createRetentionRow(t, db, 1, now.Add(-8*24*time.Hour))
	if err := db.Create(&models.UsageStat{
		BucketStartMS: now.Add(-36 * 24 * time.Hour).UnixMilli(),
		AccessKeyID:   1,
		GroupID:       1,
		Model:         "expired",
		RequestCount:  1,
		SuccessCount:  1,
	}).Error; err != nil {
		t.Fatalf("create expired UsageStat: %v", err)
	}
	if err := db.Exec(`CREATE TRIGGER fail_request_log_retention_delete
		BEFORE DELETE ON request_logs
		BEGIN
			SELECT RAISE(FAIL, 'forced request log retention delete failure');
		END`).Error; err != nil {
		t.Fatalf("create request log delete trigger: %v", err)
	}

	service.Sweep(context.Background(), now)

	assertRetentionRows(t, db, 1)
	var usageCount int64
	if err := db.Model(&models.UsageStat{}).Count(&usageCount).Error; err != nil {
		t.Fatalf("count remaining UsageStats: %v", err)
	}
	stats := service.Stats()
	if usageCount != 0 || stats.RetentionDeleteFailureTotal != 1 ||
		!stats.LastRetentionFailureAt.Equal(now) {
		t.Fatalf(
			"remaining usage/Stats = %d/%#v, want 0 and one request-log failure",
			usageCount,
			stats,
		)
	}
}

func TestRetentionSweepStopsOnContextAndDeleteFailure(t *testing.T) {
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)

	t.Run("empty result is successful completion", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		service := newRequestLogTestService(db)

		service.Sweep(context.Background(), now)

		if stats := service.Stats(); stats.RetentionDeleteFailureTotal != 0 ||
			!stats.LastRetentionFailureAt.IsZero() {
			t.Fatalf("Stats() = %#v, empty sweep must not be a failure", stats)
		}
	})

	t.Run("already canceled context is not a failure", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		service := newRequestLogTestService(db)
		createRetentionRow(t, db, 1, now.Add(-8*24*time.Hour))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		service.Sweep(ctx, now)

		assertRetentionRows(t, db, 1)
		if stats := service.Stats(); stats.RetentionDeleteFailureTotal != 0 ||
			!stats.LastRetentionFailureAt.IsZero() {
			t.Fatalf("Stats() = %#v, canceled sweep must not be a failure", stats)
		}
	})

	t.Run("cancellation between batches stops without failure", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		service := newRequestLogTestService(db)
		cutoff := now.Add(-7 * 24 * time.Hour)
		rows := make([]models.RequestLog, 0, retentionBatchSize+1)
		for index := 1; index <= retentionBatchSize+1; index++ {
			rows = append(rows, retentionRow(index, cutoff.Add(-time.Duration(index)*time.Nanosecond)))
		}
		if err := db.CreateInBatches(rows, 200).Error; err != nil {
			t.Fatalf("create expired RequestLogs: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var deletes atomic.Int64
		const callbackName = "test:retention_cancel_after_delete"
		if err := db.Callback().Delete().After("gorm:delete").Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Table == "request_logs" && tx.Error == nil && deletes.Add(1) == 1 {
				cancel()
			}
		}); err != nil {
			t.Fatalf("register delete callback: %v", err)
		}

		service.Sweep(ctx, now)

		var remaining int64
		if err := db.Model(&models.RequestLog{}).Count(&remaining).Error; err != nil {
			t.Fatalf("count remaining RequestLogs: %v", err)
		}
		if remaining != 1 {
			t.Fatalf("remaining RequestLogs = %d, want 1 after one batch", remaining)
		}
		if stats := service.Stats(); stats.RetentionDeleteFailureTotal != 0 ||
			!stats.LastRetentionFailureAt.IsZero() {
			t.Fatalf("Stats() = %#v, cancellation must not be a failure", stats)
		}
	})

	t.Run("expired ID selection failure is tracked without deleting", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		service := newRequestLogTestService(db)
		service.now = func() time.Time { return now }
		discardRetentionWarnings(service)
		createRetentionRow(t, db, 1, now.Add(-8*24*time.Hour))
		const callbackName = "test:retention_request_log_query_failure"
		if err := db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Table == "request_logs" {
				tx.AddError(errors.New("forced expired ID query failure"))
			}
		}); err != nil {
			t.Fatalf("register query callback: %v", err)
		}

		service.Sweep(context.Background(), now)

		var remaining int64
		if err := db.Raw("SELECT COUNT(*) FROM request_logs").Scan(&remaining).Error; err != nil {
			t.Fatalf("count remaining RequestLogs: %v", err)
		}
		if remaining != 1 {
			t.Fatalf("remaining RequestLogs = %d, want 1 after select failure", remaining)
		}
		stats := service.Stats()
		if stats.RetentionDeleteFailureTotal != 1 ||
			!stats.LastRetentionFailureAt.Equal(now) {
			t.Fatalf("Stats() = %#v, want one expired ID select failure", stats)
		}
	})

	t.Run("delete failure is tracked and stops the sweep", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		service := newRequestLogTestService(db)
		service.now = func() time.Time { return now }
		discardRetentionWarnings(service)
		createRetentionRow(t, db, 1, now.Add(-8*24*time.Hour))
		const callbackName = "test:retention_delete_failure"
		if err := db.Callback().Delete().Before("gorm:delete").Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Table == "request_logs" {
				tx.AddError(errors.New("forced retention delete failure"))
			}
		}); err != nil {
			t.Fatalf("register delete callback: %v", err)
		}

		service.Sweep(context.Background(), now)

		assertRetentionRows(t, db, 1)
		stats := service.Stats()
		if stats.RetentionDeleteFailureTotal != 1 ||
			!stats.LastRetentionFailureAt.Equal(now) {
			t.Fatalf("Stats() = %#v, want one delete failure", stats)
		}
	})

	t.Run("usage stat selection failure is tracked", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		service := newRequestLogTestService(db)
		service.now = func() time.Time { return now }
		discardRetentionWarnings(service)
		const callbackName = "test:retention_usage_stat_query_failure"
		if err := db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Table == "usage_stats" {
				tx.AddError(errors.New("forced usage stat retention query failure"))
			}
		}); err != nil {
			t.Fatalf("register query callback: %v", err)
		}

		service.Sweep(context.Background(), now)

		stats := service.Stats()
		if stats.RetentionDeleteFailureTotal != 1 ||
			!stats.LastRetentionFailureAt.Equal(now) {
			t.Fatalf("Stats() = %#v, want one usage stat selection failure", stats)
		}
	})

	t.Run("usage stat delete failure is tracked", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		service := newRequestLogTestService(db)
		service.now = func() time.Time { return now }
		discardRetentionWarnings(service)
		if err := db.Create(&models.UsageStat{
			BucketStartMS: now.Add(-36 * 24 * time.Hour).UnixMilli(),
			AccessKeyID:   1,
			GroupID:       1,
			Model:         "expired",
			RequestCount:  1,
			SuccessCount:  1,
		}).Error; err != nil {
			t.Fatalf("create expired UsageStat: %v", err)
		}
		const callbackName = "test:retention_usage_stat_delete_failure"
		if err := db.Callback().Delete().Before("gorm:delete").Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Table == "usage_stats" {
				tx.AddError(errors.New("forced usage stat retention delete failure"))
			}
		}); err != nil {
			t.Fatalf("register delete callback: %v", err)
		}

		service.Sweep(context.Background(), now)

		var remaining int64
		if err := db.Model(&models.UsageStat{}).Count(&remaining).Error; err != nil {
			t.Fatalf("count remaining UsageStats: %v", err)
		}
		stats := service.Stats()
		if remaining != 1 || stats.RetentionDeleteFailureTotal != 1 ||
			!stats.LastRetentionFailureAt.Equal(now) {
			t.Fatalf("remaining/Stats = %d/%#v, want 1 and one delete failure", remaining, stats)
		}
	})
}

func TestRetentionReplayBoundaryPreservesThenReaggregatesUsageStat(t *testing.T) {
	db := openRequestLogQueryDB(t)
	writer := &gormBatchWriter{db: db}
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	row := aggregationRow(aggregationRequestID(70), now.Add(-8*24*time.Hour), 15, "retention-aggregate")
	row.Status = string(telemetry.RequestStatusSuccess)
	row.UsageState = string(usage.StateComplete)

	if err := writer.WriteBatch(context.Background(), []models.RequestLog{row}); err != nil {
		t.Fatalf("initial WriteBatch() error = %v", err)
	}
	assertRetentionReplayState(t, db, 1, 1)

	if err := writer.WriteBatch(context.Background(), []models.RequestLog{row}); err != nil {
		t.Fatalf("retained replay WriteBatch() error = %v", err)
	}
	assertRetentionReplayState(t, db, 1, 1)

	newRequestLogTestService(db).Sweep(context.Background(), now)
	assertRetentionReplayState(t, db, 0, 1)

	if err := writer.WriteBatch(context.Background(), []models.RequestLog{row}); err != nil {
		t.Fatalf("post-retention replay WriteBatch() error = %v", err)
	}
	assertRetentionReplayState(t, db, 1, 2)
}

func assertRetentionReplayState(
	t *testing.T,
	db *gorm.DB,
	wantRequestLogs int64,
	wantAggregatedRequests int64,
) {
	t.Helper()
	var requestLogs int64
	if err := db.Model(&models.RequestLog{}).Count(&requestLogs).Error; err != nil {
		t.Fatalf("count RequestLogs: %v", err)
	}
	var stats []models.UsageStat
	if err := db.Find(&stats).Error; err != nil {
		t.Fatalf("query UsageStats: %v", err)
	}
	if requestLogs != wantRequestLogs || len(stats) != 1 ||
		stats[0].RequestCount != wantAggregatedRequests {
		t.Fatalf("retention replay state = logs:%d stats:%+v, want logs:%d requests:%d",
			requestLogs, stats, wantRequestLogs, wantAggregatedRequests)
	}
}

func createRetentionRow(t *testing.T, db *gorm.DB, index int, completedAt time.Time) {
	t.Helper()
	row := retentionRow(index, completedAt)
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create retention RequestLog %d: %v", index, err)
	}
}

func retentionRow(index int, completedAt time.Time) models.RequestLog {
	return requestLogQueryRow(
		fmt.Sprintf("00000000-0000-4000-8000-%012d", index),
		completedAt,
		41,
		"retention-model",
		nil,
	)
}

func assertRetentionRows(t *testing.T, db *gorm.DB, indexes ...int) {
	t.Helper()
	var rows []models.RequestLog
	if err := db.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("list remaining RequestLogs: %v", err)
	}
	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, row.ID)
	}
	want := make([]string, 0, len(indexes))
	for _, index := range indexes {
		want = append(want, fmt.Sprintf("00000000-0000-4000-8000-%012d", index))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("remaining RequestLogs = %v, want %v", got, want)
	}
}

func discardRetentionWarnings(service *Service) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	service.logger = logger
}
