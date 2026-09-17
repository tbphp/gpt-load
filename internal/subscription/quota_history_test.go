package subscription

import (
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func TestQuotaHistorySamplesLatestWindowIndependentlyAndSurvivesRestart(t *testing.T) {
	manager, db, registry, _, credential := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	newFlushableCredentialObservation(t, manager, credential.ID, models.CredentialObservationFresh,
		`{"quota_windows":[{"id":"primary","scope":"account","label":"Session","unit":"percent","state":"available"},{"id":"secondary","scope":"account","label":"Weekly","unit":"percent","state":"available"}]}`)
	ref, _ := registry.CredentialRef(credential.ID)
	record := func(at int64, id string, used float64) {
		manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, at,
			[]providerobservation.QuotaWindow{{ID: id, Utilization: &used}})
	}
	flush := func() {
		t.Helper()
		if remaining, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil || remaining {
			t.Fatalf("flush: remaining=%t error=%v", remaining, err)
		}
	}
	record(10_000, "primary", 0.1)
	record(20_000, "primary", 0.2)
	record(30_000, "secondary", 0.4)
	flush()
	record(79_999, "primary", 0.3)
	flush()
	record(80_000, "primary", 0.5)
	flush()
	// 新进程依然从持久化的最后观测时间限流，不会在一分钟内重复记点。
	manager.passiveQuota = newPassiveQuotaPending()
	record(90_000, "primary", 0.6)
	flush()
	record(1_000_000, "primary", 0.9)
	flush()
	var rows []models.CredentialQuotaHistory
	if err := db.Order("observed_at_ms").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || rows[0].ObservedAtMS != 20_000 || rows[0].UsedBasisPoints != 2000 ||
		rows[1].WindowID != "secondary" || rows[2].ObservedAtMS != 80_000 || rows[3].ObservedAtMS != 1_000_000 {
		t.Fatalf("unexpected real samples: %+v", rows)
	}
}

func TestQuotaHistoryIgnoresMissingPercentAndKeepsSamplesAfterWriteFailure(t *testing.T) {
	manager, db, registry, _, credential := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	newFlushableCredentialObservation(t, manager, credential.ID, models.CredentialObservationFresh, `{"quota_windows":[]}`)
	ref, _ := registry.CredentialRef(credential.ID)
	manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, 10_000,
		[]providerobservation.QuotaWindow{{ID: "primary", State: "available"}})
	if len(manager.passiveQuota.historyBatch(20)) != 0 {
		t.Fatal("missing percentage fabricated a sample")
	}
	used := 0.25
	manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, 20_000,
		[]providerobservation.QuotaWindow{{ID: "primary", Utilization: &used}})
	if err := db.Callback().Create().Before("gorm:create").Register("test:history_write_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "credential_quota_histories" {
			tx.AddError(testQuotaHistoryWriteError{})
		}
	}); err != nil {
		t.Fatal(err)
	}
	if remaining, err := manager.FlushPassiveQuotaObservations(t.Context()); err == nil || !remaining {
		t.Fatal("history failure must remain pending")
	}
	if err := db.Callback().Create().Remove("test:history_write_failure"); err != nil {
		t.Fatal(err)
	}
	if remaining, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil || remaining {
		t.Fatalf("retry: %t %v", remaining, err)
	}
	var count int64
	if err := db.Model(&models.CredentialQuotaHistory{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("count=%d error=%v", count, err)
	}
}

type testQuotaHistoryWriteError struct{}

func (testQuotaHistoryWriteError) Error() string { return "history write failed" }

func TestQuotaHistoryTimeCacheDoesNotStopSamplingWhenFull(t *testing.T) {
	pending := newPassiveQuotaPending()
	for index := 0; index < quotaHistoryCapacity; index++ {
		pending.rememberHistoryTime(quotaHistoryKey{credentialID: uint(index + 1), window: "session"}, int64(index))
	}
	key := quotaHistoryKey{credentialID: 5000, window: "new"}
	pending.rememberHistoryTime(key, 60_000)
	if len(pending.historyTimes) != quotaHistoryCapacity || pending.historyTimes[key] != 60_000 {
		t.Fatal("bounded time cache did not accept a new account")
	}
	used := 0.25
	pending.recordHistoryLocked(1, 1, 1, 120_000, []providerobservation.QuotaWindow{{ID: "primary", Utilization: &used}})
	if len(pending.historyBatch(20)) != 1 {
		t.Fatal("full time cache stopped history admission")
	}
}
