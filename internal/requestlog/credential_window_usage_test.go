package requestlog

import (
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

func TestQueryCredentialWindowUsageUsesExactRequestLogsForShortWindow(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	credentialID := uint(31)
	inside := requestLogWindowRow("00000000-0000-4000-8000-000000000731", credentialID, now.Add(-2*time.Hour), 11)
	inside.CacheReadTokens = 7
	inside.OutputTokens = 5
	inside.EstimatedCostNanoUSD = 19
	partial := requestLogWindowRow("00000000-0000-4000-8000-000000000732", credentialID, now.Add(-time.Hour), 13)
	partial.UsageState = string(usage.StatePartial)
	partial.CostState = string(pricing.CostStateUnpriced)
	partial.PricingCompleteness = string(pricing.CompletenessUnavailable)
	otherCredential := requestLogWindowRow("00000000-0000-4000-8000-000000000733", credentialID+1, now.Add(-time.Hour), 100)
	outside := requestLogWindowRow("00000000-0000-4000-8000-000000000734", credentialID, now.Add(-6*time.Hour), 200)
	for _, row := range []models.RequestLog{inside, partial, otherCredential, outside} {
		createRequestLogQueryRow(t, db, row)
	}

	result, err := service.QueryCredentialWindowUsage(t.Context(), CredentialWindowUsageQuery{
		CredentialID: credentialID,
		FromMS:       now.Add(-5 * time.Hour).UnixMilli(),
		ToMS:         now.UnixMilli(),
		Source:       CredentialWindowUsageSourceRequestLogs,
	})
	if err != nil {
		t.Fatalf("QueryCredentialWindowUsage() error = %v", err)
	}
	if result.Source != CredentialWindowUsageSourceRequestLogs || !result.DataComplete ||
		result.RequestCount != 2 || result.UncachedInputTokens != 24 ||
		result.CacheReadTokens != 7 || result.OutputTokens != 10 ||
		result.EstimatedCostNanoUSD != 19 || result.PartialCount != 1 ||
		result.UnpricedRequestCount != 1 || result.LastUsedAtMS == nil ||
		*result.LastUsedAtMS != partial.CompletedAtMS {
		t.Fatalf("exact window usage = %#v", result)
	}
}

func TestQueryCredentialWindowUsageUsesHourlyStatsForLongWindow(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	now := time.Date(2026, time.August, 14, 12, 25, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	credentialID := uint(41)
	first := usageStat(now.Add(-7*24*time.Hour).Truncate(time.Hour), 17, "codex-a", 2)
	first.CredentialID = credentialID
	last := usageStat(now.Add(-time.Hour).Truncate(time.Hour), 17, "codex-b", 3)
	last.CredentialID = credentialID
	other := usageStat(now.Add(-time.Hour).Truncate(time.Hour), 17, "codex-c", 100)
	other.CredentialID = credentialID + 1
	createUsageStats(t, db, first, last, other)

	from := now.Add(-7 * 24 * time.Hour).Add(10 * time.Minute)
	boundary := requestLogWindowRow(
		"00000000-0000-4000-8000-000000000735",
		credentialID,
		from.Add(5*time.Minute),
		40,
	)
	createRequestLogQueryRow(t, db, boundary)
	result, err := service.QueryCredentialWindowUsage(t.Context(), CredentialWindowUsageQuery{
		CredentialID: credentialID,
		FromMS:       from.UnixMilli(),
		ToMS:         now.UnixMilli(),
		Source:       CredentialWindowUsageSourceHourlyStats,
	})
	if err != nil {
		t.Fatalf("QueryCredentialWindowUsage() error = %v", err)
	}
	if result.Source != CredentialWindowUsageSourceHourlyStats || !result.DataComplete ||
		result.RequestCount != 4 || result.UncachedInputTokens != 70 ||
		result.EstimatedCostNanoUSD != 300_000_000 || result.LastUsedAtMS == nil ||
		*result.LastUsedAtMS != last.BucketStartMS {
		t.Fatalf("hourly window usage = %#v", result)
	}
}

func TestQueryCredentialWindowUsageUsesExactRequestLogsForBothBoundaries(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	now := time.Date(2026, time.August, 14, 14, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	credentialID := uint(42)
	from := time.Date(2026, time.August, 14, 10, 10, 0, 0, time.UTC)
	to := time.Date(2026, time.August, 14, 12, 25, 0, 0, time.UTC)

	fullHour := usageStat(time.Date(2026, time.August, 14, 11, 0, 0, 0, time.UTC), 17, "codex-a", 2)
	fullHour.CredentialID = credentialID
	endingHour := usageStat(time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC), 17, "codex-a", 100)
	endingHour.CredentialID = credentialID
	createUsageStats(t, db, fullHour, endingHour)

	startBoundary := requestLogWindowRow(
		"00000000-0000-4000-8000-000000000741",
		credentialID,
		from.Add(10*time.Minute),
		11,
	)
	endBoundary := requestLogWindowRow(
		"00000000-0000-4000-8000-000000000742",
		credentialID,
		to.Add(-5*time.Minute),
		13,
	)
	afterWindow := requestLogWindowRow(
		"00000000-0000-4000-8000-000000000743",
		credentialID,
		to.Add(5*time.Minute),
		1_000,
	)
	for _, row := range []models.RequestLog{startBoundary, endBoundary, afterWindow} {
		createRequestLogQueryRow(t, db, row)
	}

	result, err := service.QueryCredentialWindowUsage(t.Context(), CredentialWindowUsageQuery{
		CredentialID: credentialID,
		FromMS:       from.UnixMilli(),
		ToMS:         to.UnixMilli(),
		Source:       CredentialWindowUsageSourceHourlyStats,
	})
	if err != nil {
		t.Fatalf("QueryCredentialWindowUsage() error = %v", err)
	}
	if !result.DataComplete || result.RequestCount != 4 || result.UncachedInputTokens != 44 ||
		result.OutputTokens != 14 || result.LastUsedAtMS == nil ||
		*result.LastUsedAtMS != endBoundary.CompletedAtMS {
		t.Fatalf("exact boundary usage = %#v", result)
	}
}

func TestQueryCredentialWindowUsageTreatsLongTermHourlyStatsAsRetained(t *testing.T) {
	db := openRequestLogQueryDB(t)
	service := newRequestLogTestService(db)
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	credentialID := uint(43)
	from := now.Add(-40 * 24 * time.Hour)
	row := usageStat(from, 17, "codex-long-term", 2)
	row.CredentialID = credentialID
	createUsageStats(t, db, row)

	result, err := service.QueryCredentialWindowUsage(t.Context(), CredentialWindowUsageQuery{
		CredentialID: credentialID,
		FromMS:       from.UnixMilli(),
		ToMS:         now.UnixMilli(),
		Source:       CredentialWindowUsageSourceHourlyStats,
	})
	if err != nil {
		t.Fatalf("QueryCredentialWindowUsage() error = %v", err)
	}
	if !result.DataComplete || result.RequestCount != 2 || result.UncachedInputTokens != 20 {
		t.Fatalf("long-term hourly usage = %#v", result)
	}
}

func TestQueryCredentialWindowUsageUsesOneReadSnapshot(t *testing.T) {
	db, dsn := openRequestLogFileDB(t)
	service := newRequestLogTestService(db)
	now := time.Date(2026, time.August, 14, 12, 25, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	credentialID := uint(42)
	from := now.Add(-5 * time.Hour)
	hourly := usageStat(from.Add(time.Hour).Truncate(time.Hour), 17, "codex-a", 1)
	hourly.CredentialID = credentialID
	createUsageStats(t, db, hourly)

	writerDB, closeWriter := openUsageQueryWriterDB(t, dsn)
	defer closeWriter()
	inserted := false
	const callbackName = "test:credential_window_usage_snapshot_insert"
	if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if inserted || tx.Statement.Table != "usage_stats" {
			return
		}
		inserted = true
		lateBoundary := requestLogWindowRow(
			"00000000-0000-4000-8000-000000000736",
			credentialID,
			from.Add(5*time.Minute),
			40,
		)
		if err := writerDB.Create(&lateBoundary).Error; err != nil {
			t.Errorf("insert concurrent RequestLog: %v", err)
		}
	}); err != nil {
		t.Fatalf("register query callback: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Callback().Query().Remove(callbackName); err != nil {
			t.Errorf("remove query callback: %v", err)
		}
	})

	result, err := service.QueryCredentialWindowUsage(t.Context(), CredentialWindowUsageQuery{
		CredentialID: credentialID,
		FromMS:       from.UnixMilli(),
		ToMS:         now.UnixMilli(),
		Source:       CredentialWindowUsageSourceHourlyStats,
	})
	if err != nil {
		t.Fatalf("QueryCredentialWindowUsage() error = %v", err)
	}
	if !inserted || result.RequestCount != 1 {
		t.Fatalf("window usage did not retain one snapshot: inserted=%t result=%#v", inserted, result)
	}
}

func requestLogWindowRow(id string, credentialID uint, completedAt time.Time, input int64) models.RequestLog {
	return models.RequestLog{
		ID: id, CompletedAtMS: completedAt.UnixMilli(), AccessKeyID: 1, GroupID: 17,
		ChannelID: "codex", CredentialID: credentialID, Protocol: "anthropic", ClientModel: "codex-a",
		UpstreamModel: "codex-a", Status: string(telemetry.RequestStatusSuccess), StatusCode: 200,
		DurationMs: 1, UncachedInputTokens: input, OutputTokens: 5,
		UsageState: string(usage.StateComplete), CostState: string(pricing.CostStatePriced),
		PricingCompleteness: string(pricing.CompletenessComplete),
	}
}
