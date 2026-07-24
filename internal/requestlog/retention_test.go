package requestlog

import (
	"bytes"
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
)

func TestRetentionSettingDefaultsAndStrictValidation(t *testing.T) {
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)

	t.Run("missing uses seven days without persisting a default", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		service := NewService(db, redact.New())
		createRetentionRow(t, db, 1, now.Add(-8*24*time.Hour))
		createRetentionRow(t, db, 2, now.Add(-7*24*time.Hour))
		createRetentionRow(t, db, 3, now.Add(-6*24*time.Hour))

		service.Sweep(context.Background(), now)

		assertRetentionRows(t, db, 2, 3)
		var settingCount int64
		if err := db.Model(&models.SystemSetting{}).
			Where("key = ?", RetentionSettingKey).
			Count(&settingCount).Error; err != nil {
			t.Fatalf("count retention settings: %v", err)
		}
		if settingCount != 0 {
			t.Fatalf("retention setting rows = %d, want 0", settingCount)
		}
		if stats := service.Stats(); stats.RetentionInvalidSettingTotal != 0 ||
			stats.RetentionDeleteFailureTotal != 0 || !stats.LastRetentionFailureAt.IsZero() {
			t.Fatalf("Stats() = %#v, want no retention failures", stats)
		}
	})

	for _, days := range []int64{1, 7, 365} {
		t.Run(fmt.Sprintf("accepts_%d", days), func(t *testing.T) {
			db := openRequestLogQueryDB(t)
			service := NewService(db, redact.New())
			storeRetentionSetting(t, db, fmt.Sprintf("%d", days))
			cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
			createRetentionRow(t, db, 1, cutoff.Add(-time.Nanosecond))
			createRetentionRow(t, db, 2, cutoff)
			createRetentionRow(t, db, 3, cutoff.Add(time.Nanosecond))

			service.Sweep(context.Background(), now)

			assertRetentionRows(t, db, 2, 3)
			if stats := service.Stats(); stats.RetentionInvalidSettingTotal != 0 ||
				stats.RetentionDeleteFailureTotal != 0 {
				t.Fatalf("Stats() = %#v, want no retention failures", stats)
			}
		})
	}

	for _, value := range []string{
		"0", "-1", "366", "1.5", "1e2", `"7"`, "true", "null",
		" 7", "7 ", "07", "+7",
	} {
		t.Run("rejects_"+strings.NewReplacer(`"`, "quote", " ", "space", "+", "plus", "-", "minus", ".", "dot").Replace(value), func(t *testing.T) {
			db := openRequestLogQueryDB(t)
			service := NewService(db, redact.New())
			service.now = func() time.Time { return now }
			discardRetentionWarnings(service)
			storeRetentionSetting(t, db, value)
			createRetentionRow(t, db, 1, now.Add(-400*24*time.Hour))

			service.Sweep(context.Background(), now)

			assertRetentionRows(t, db, 1)
			stats := service.Stats()
			if stats.RetentionInvalidSettingTotal != 1 ||
				stats.RetentionDeleteFailureTotal != 0 ||
				!stats.LastRetentionFailureAt.Equal(now) {
				t.Fatalf("Stats() = %#v, want one invalid-setting failure at %v", stats, now)
			}
		})
	}
}

func TestServiceSweepDeletesStrictlyOlderRowsInBatches(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := NewService(db, redact.New())
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
	if stats := service.Stats(); stats.RetentionInvalidSettingTotal != 0 ||
		stats.RetentionDeleteFailureTotal != 0 || !stats.LastRetentionFailureAt.IsZero() {
		t.Fatalf("Stats() = %#v, want no retention failures", stats)
	}
}

func TestServiceSweepSkipsInvalidSettingAndTracksFailure(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := NewService(db, redact.New())
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	logger := logrus.New()
	var output bytes.Buffer
	logger.SetOutput(&output)
	service.logger = logger

	const unsafeSetting = `"retention-secret-value"`
	storeRetentionSetting(t, db, unsafeSetting)
	createRetentionRow(t, db, 1, now.Add(-400*24*time.Hour))

	service.Sweep(context.Background(), now)
	service.Sweep(context.Background(), now.Add(30*time.Second))

	assertRetentionRows(t, db, 1)
	stats := service.Stats()
	if stats.RetentionInvalidSettingTotal != 2 ||
		stats.RetentionDeleteFailureTotal != 0 ||
		!stats.LastRetentionFailureAt.Equal(now.Add(30*time.Second)) {
		t.Fatalf("Stats() = %#v, want two invalid-setting failures", stats)
	}
	logged := output.String()
	if strings.Contains(logged, unsafeSetting) || strings.Contains(logged, "retention-secret-value") {
		t.Fatalf("warning leaked setting content: %q", logged)
	}
	if got := strings.Count(logged, "level=warning"); got != 1 {
		t.Fatalf("warning count = %d, want throttled single warning; output=%q", got, logged)
	}
}

func TestServiceSweepStopsOnContextAndDeleteFailure(t *testing.T) {
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)

	t.Run("empty result is successful completion", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		service := NewService(db, redact.New())

		service.Sweep(context.Background(), now)

		if stats := service.Stats(); stats.RetentionDeleteFailureTotal != 0 ||
			!stats.LastRetentionFailureAt.IsZero() {
			t.Fatalf("Stats() = %#v, empty sweep must not be a failure", stats)
		}
	})

	t.Run("already canceled context is not a failure", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		service := NewService(db, redact.New())
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
		service := NewService(db, redact.New())
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

	t.Run("setting select failure is tracked", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		service := NewService(db, redact.New())
		service.now = func() time.Time { return now }
		discardRetentionWarnings(service)
		const callbackName = "test:retention_setting_query_failure"
		if err := db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Table == "system_settings" {
				tx.AddError(errors.New("forced setting query failure"))
			}
		}); err != nil {
			t.Fatalf("register query callback: %v", err)
		}

		service.Sweep(context.Background(), now)

		stats := service.Stats()
		if stats.RetentionDeleteFailureTotal != 1 ||
			stats.RetentionInvalidSettingTotal != 0 ||
			!stats.LastRetentionFailureAt.Equal(now) {
			t.Fatalf("Stats() = %#v, want one select failure", stats)
		}
	})

	t.Run("expired ID selection failure is tracked without deleting", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		service := NewService(db, redact.New())
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
			stats.RetentionInvalidSettingTotal != 0 ||
			!stats.LastRetentionFailureAt.Equal(now) {
			t.Fatalf("Stats() = %#v, want one expired ID select failure", stats)
		}
	})

	t.Run("delete failure is tracked and stops the sweep", func(t *testing.T) {
		db := openRequestLogQueryDB(t)
		service := NewService(db, redact.New())
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
			stats.RetentionInvalidSettingTotal != 0 ||
			!stats.LastRetentionFailureAt.Equal(now) {
			t.Fatalf("Stats() = %#v, want one delete failure", stats)
		}
	})
}

func storeRetentionSetting(t *testing.T, db *gorm.DB, value string) {
	t.Helper()
	if err := db.Create(&models.SystemSetting{
		Key:   RetentionSettingKey,
		Value: value,
	}).Error; err != nil {
		t.Fatalf("create retention setting %q: %v", value, err)
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
