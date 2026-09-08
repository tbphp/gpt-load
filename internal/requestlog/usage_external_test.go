package requestlog

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
)

// 与现有数据库合同共用入口，在真实驱动上验证混合查询和多天分桶 SQL。
func TestExternalDatabaseUsageExactWindow(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_TEST_DSN"))
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	db, err := storage.OpenWithSource(dsn, config.DatabaseSourceExternal)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close usage database: %v", err)
		}
	})
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}

	unique := uint64(time.Now().UnixNano()) & 0xffffffffff
	credentialID := uint(unique%1_000_000_000) + 1_000_000
	groups := make([]models.Group, 6)
	keys := make([]models.AccessKey, 6)
	for i := range groups {
		name := fmt.Sprintf("usage-window-%x-%d", unique, i)
		groups[i] = models.Group{Name: name, ChannelID: "openai", Params: models.JSON(`{}`), Models: models.JSON(`[]`), Enabled: true}
		keys[i] = models.AccessKey{Name: name, KeyValue: "encrypted-test-key", KeyHash: name, KeySuffix: "abcd", Status: "active", Filters: models.JSON(`{}`)}
	}
	var requestIDs []string
	t.Cleanup(func() {
		for _, operation := range []struct {
			model any
			query string
			value any
		}{
			{&models.UsageAggregationJournal{}, "request_id IN ?", requestIDs},
			{&models.RequestLog{}, "id IN ?", requestIDs},
			{&models.UsageStat{}, "credential_id = ?", credentialID},
		} {
			if err := db.Where(operation.query, operation.value).Delete(operation.model).Error; err != nil {
				t.Errorf("cleanup usage fixtures: %v", err)
			}
		}
		for i := range groups {
			if keys[i].ID != 0 {
				if err := db.Delete(&keys[i]).Error; err != nil {
					t.Errorf("cleanup usage access key: %v", err)
				}
			}
			if groups[i].ID != 0 {
				if err := db.Delete(&groups[i]).Error; err != nil {
					t.Errorf("cleanup usage group: %v", err)
				}
			}
		}
	})
	for i := range groups {
		for _, row := range []any{&groups[i], &keys[i]} {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}

	base := time.Date(2026, time.September, 8, 9, 0, 0, 0, time.UTC)
	from, to := base.Add(15*time.Minute), base.Add(6*time.Hour+40*time.Minute)
	var rows []models.RequestLog
	appendRow := func(completedAt time.Time, dimension int) {
		id := fmt.Sprintf("00000000-0000-4000-8000-%010x%02x", unique, len(rows))
		row := aggregationRow(id, completedAt, groups[dimension].ID, fmt.Sprintf("model-%d", dimension))
		row.AccessKeyID, row.CredentialID, row.ChannelID = keys[dimension].ID, credentialID, "openai"
		rows, requestIDs = append(rows, row), append(requestIDs, id)
	}
	for dimension := range groups {
		for range dimension + 1 {
			for _, offset := range []time.Duration{30 * time.Minute, 3*time.Hour + 30*time.Minute, 6*time.Hour + 30*time.Minute} {
				appendRow(base.Add(offset), dimension)
			}
		}
	}
	for _, completedAt := range []time.Time{from.Add(-time.Millisecond), from, base.Add(time.Hour), base.Add(6 * time.Hour), to.Add(-time.Millisecond), to} {
		appendRow(completedAt, 0)
	}
	if err := (&gormBatchWriter{db: db}).WriteBatch(t.Context(), rows); err != nil {
		t.Fatal(err)
	}
	service := newRequestLogTestService(db)
	query := UsageQuery{FromMS: from.UnixMilli(), ToMS: to.UnixMilli(), CredentialID: &credentialID}
	assertCount := func(query UsageQuery, want int64) UsageReport {
		t.Helper()
		report, err := service.QueryUsage(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		if report.Summary.RequestCount != want || report.Summary.UncachedInputTokens != want ||
			report.Summary.OutputTokens != 2*want || report.Summary.EstimatedCostNanoUSD != 250_000_000*want {
			t.Fatalf("usage summary = %+v, want %d frozen requests", report.Summary, want)
		}
		assertMinuteUsageReportTotals(t, report)
		return report
	}
	report := assertCount(query, 67)
	for _, point := range report.Series {
		bucket := point.BucketStartMS - point.BucketStartMS%time.Hour.Milliseconds()
		if point.BucketStartMS != max(bucket, query.FromMS) || point.BucketEndMS != min(bucket+time.Hour.Milliseconds(), query.ToMS) {
			t.Fatalf("mixed series bucket = %+v, want exact clipped hour", point)
		}
	}
	for _, distributions := range []map[UsageDistributionMetric]UsageDistribution{report.Distributions.Group, report.Distributions.Model, report.Distributions.AccessKey} {
		for _, distribution := range distributions {
			if len(distribution.Items) != 5 || distribution.Items[0].RequestCount != 18 ||
				distribution.Other == nil || distribution.Other.RequestCount != 6 {
				t.Fatalf("global Top5 = %+v, want largest 18 and other 6", distribution)
			}
		}
	}
	short := query
	short.ToMS = from.Add(time.Hour).UnixMilli()
	assertCount(short, 23)

	// 中间日志已清理时仍只取小时聚合；首尾日志清理后直接返回剩余数据。
	fullFrom, fullTo := base.Add(time.Hour).UnixMilli(), to.Truncate(time.Hour).UnixMilli()
	deleteLogs := func(scope *gorm.DB) {
		t.Helper()
		if err := scope.Where("credential_id = ?", credentialID).Delete(&models.RequestLog{}).Error; err != nil {
			t.Fatal(err)
		}
	}
	deleteLogs(db.Where("completed_at_ms >= ? AND completed_at_ms < ?", fullFrom, fullTo))
	assertCount(query, 67)
	deleteLogs(db)
	assertCount(query, 22)
	assertCount(short, 0)

	t.Run("year with thirteen day buckets", func(t *testing.T) {
		longFrom := base.AddDate(-2, 0, 0).Add(17 * time.Minute)
		longTo := longFrom.Add(365 * 24 * time.Hour)
		width := (13 * 24 * time.Hour).Milliseconds()
		var stats []models.UsageStat
		wantBuckets := make(map[int64]int64)
		for day := range 365 {
			at := longFrom.Truncate(time.Hour).Add(time.Hour + time.Duration(day)*24*time.Hour)
			row := usageStat(at, groups[0].ID, "retained", 2)
			row.AccessKeyID, row.CredentialID, row.ChannelID = keys[0].ID, credentialID, "openai"
			stats = append(stats, row)
			wantBuckets[at.UnixMilli()-at.UnixMilli()%width] += 2
		}
		for _, at := range []time.Time{longFrom.Truncate(time.Hour), longTo.Truncate(time.Hour)} {
			row := usageStat(at, groups[0].ID, "boundary-without-logs", 100)
			row.AccessKeyID, row.CredentialID, row.ChannelID = keys[0].ID, credentialID, "openai"
			stats = append(stats, row)
		}
		if err := db.CreateInBatches(stats, 100).Error; err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		report, err := service.QueryUsage(t.Context(), UsageQuery{FromMS: longFrom.UnixMilli(), ToMS: longTo.UnixMilli(), CredentialID: &credentialID})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("year query over %d synthetic hourly rows: %s", len(stats), time.Since(started))
		if report.Summary.RequestCount != 730 || len(report.Series) != len(wantBuckets) {
			t.Fatalf("year report = %+v, want 730 requests in %d buckets", report, len(wantBuckets))
		}
		for _, point := range report.Series {
			bucket := point.BucketStartMS - point.BucketStartMS%width
			if point.RequestCount != wantBuckets[bucket] || point.BucketStartMS != max(bucket, longFrom.UnixMilli()) ||
				point.BucketEndMS != min(bucket+width, longTo.UnixMilli()) {
				t.Fatalf("year bucket = %+v, want exact clipped thirteen-day bucket", point)
			}
		}
		assertMinuteUsageReportTotals(t, report)
	})
}
