package subscription

import (
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func TestQuotaHistoryRecordsDisplayedPercentChangesAndSurvivesRestart(t *testing.T) {
	manager, db, registry, _, credential := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	newFlushableCredentialObservation(t, manager, credential.ID, models.CredentialObservationFresh,
		`{"quota_windows":[{"id":"primary","scope":"account","label":"Session","unit":"percent","state":"available"},{"id":"secondary","scope":"account","label":"Weekly","unit":"percent","state":"available"}]}`)
	ref, _ := registry.CredentialRef(credential.ID)
	seconds := int64(604_800)
	record := func(at int64, id string, used float64) {
		manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, at,
			[]providerobservation.QuotaWindow{{ID: id, WindowSeconds: &seconds, Utilization: &used}})
	}
	flush := func() {
		t.Helper()
		if remaining, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil || remaining {
			t.Fatalf("flush: remaining=%t error=%v", remaining, err)
		}
	}
	record(10_000, "primary", 0.1)
	// 剩余 89.51% 与 89.50% 都显示为 90%，不应新增历史点。
	record(20_000, "primary", 0.1049)
	record(30_000, "secondary", 0.4)
	flush()
	record(40_000, "primary", 0.105)
	flush()
	// 剩余 89.49% 显示为 89%，即使相隔不到十五分钟也必须记录。
	record(50_000, "primary", 0.1051)
	flush()
	// 重启后使用持久化的最后显示值去重。
	manager.passiveQuota = newPassiveQuotaPending()
	record(60_000, "primary", 0.1052)
	flush()
	record(70_000, "primary", 0.095)
	flush()
	var rows []models.CredentialQuotaHistory
	if err := db.Order("observed_at_ms").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || rows[0].ObservedAtMS != 10_000 || rows[0].UsedBasisPoints != 1000 ||
		rows[1].WindowID != "secondary" || rows[2].ObservedAtMS != 50_000 || rows[2].UsedBasisPoints != 1051 ||
		rows[3].ObservedAtMS != 70_000 || rows[3].UsedBasisPoints != 950 {
		t.Fatalf("unexpected real samples: %+v", rows)
	}
}

func TestQuotaHistoryIgnoresMissingPercentAndKeepsSamplesAfterWriteFailure(t *testing.T) {
	manager, db, registry, _, credential := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	newFlushableCredentialObservation(t, manager, credential.ID, models.CredentialObservationFresh, `{"quota_windows":[]}`)
	ref, _ := registry.CredentialRef(credential.ID)
	seconds := int64(604_800)
	manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, 10_000,
		[]providerobservation.QuotaWindow{{ID: "primary", WindowSeconds: &seconds, State: "available"}})
	if len(manager.passiveQuota.historyBatch(20)) != 0 {
		t.Fatal("missing percentage fabricated a sample")
	}
	used := 0.25
	manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, 20_000,
		[]providerobservation.QuotaWindow{{ID: "primary", WindowSeconds: &seconds, Utilization: &used}})
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
	// 写入失败期间继续观测，每次显示值变化都必须保留。
	for index, utilization := range []float64{0.9, 0.01, 0.02} {
		manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, 30_000+int64(index)*10_000,
			[]providerobservation.QuotaWindow{{ID: "primary", WindowSeconds: &seconds, Utilization: &utilization}})
	}
	if err := db.Callback().Create().Remove("test:history_write_failure"); err != nil {
		t.Fatal(err)
	}
	if remaining, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil || remaining {
		t.Fatalf("retry: %t %v", remaining, err)
	}
	var count int64
	if err := db.Model(&models.CredentialQuotaHistory{}).Count(&count).Error; err != nil || count != 4 {
		t.Fatalf("count=%d error=%v", count, err)
	}
	var rows []models.CredentialQuotaHistory
	if err := db.Order("observed_at_ms").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if rows[0].ObservedAtMS != 20_000 || rows[1].ObservedAtMS != 30_000 || rows[2].ObservedAtMS != 40_000 ||
		rows[2].UsedBasisPoints != 100 || rows[3].ObservedAtMS != 50_000 || rows[3].UsedBasisPoints != 200 {
		t.Fatalf("retry lost real changed samples: %+v", rows)
	}
}

type testQuotaHistoryWriteError struct{}

func (testQuotaHistoryWriteError) Error() string { return "history write failed" }

func TestQuotaHistoryUsesOneWindowForNamedWebsocketAndHTTPObservations(t *testing.T) {
	manager, db, registry, _, credential := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	newFlushableCredentialObservation(t, manager, credential.ID, models.CredentialObservationFresh,
		`{"quota_windows":[{"id":"spark-primary","source_id":"codex_spark","scope":"Spark","label":"Spark","unit":"percent","window_seconds":604800,"state":"available"}]}`)
	ref, _ := registry.CredentialRef(credential.ID)
	seconds := int64(604_800)
	for index, at := range []int64{10_000, 20_000, 3_610_000} {
		used := float64(index+2) / 10
		window := providerobservation.QuotaWindow{ID: "primary", WindowSeconds: &seconds, Utilization: &used}
		if index == 0 {
			window.SourceName = "Spark"
		} else {
			window.SourceID = "codex_spark"
		}
		manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, at, []providerobservation.QuotaWindow{window})
		if remaining, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil || remaining {
			t.Fatalf("flush: remaining=%t error=%v", remaining, err)
		}
	}
	var rows []models.CredentialQuotaHistory
	if err := db.Order("observed_at_ms").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].WindowKey != rows[1].WindowKey || rows[1].WindowKey != rows[2].WindowKey || rows[0].ObservedAtMS != 10_000 || rows[1].ObservedAtMS != 20_000 || rows[2].ObservedAtMS != 3_610_000 || rows[0].UsedBasisPoints != 2000 || rows[1].UsedBasisPoints != 3000 || rows[2].UsedBasisPoints != 4000 {
		t.Fatalf("one source was split or changed values were dropped: %+v", rows)
	}
}

func TestQuotaHistoryKeepsNamedWebsocketSourcesSeparate(t *testing.T) {
	manager, db, registry, _, credential := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	newFlushableCredentialObservation(t, manager, credential.ID, models.CredentialObservationFresh,
		`{"quota_windows":[{"id":"spark-primary","source_id":"codex_spark","scope":"Spark","label":"Spark","unit":"percent","window_seconds":604800,"state":"available"},{"id":"other-primary","source_id":"other","scope":"Other","label":"Other","unit":"percent","window_seconds":604800,"state":"available"}]}`)
	ref, _ := registry.CredentialRef(credential.ID)
	seconds, sparkUsed, otherUsed := int64(604_800), 0.2, 0.5
	manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, 10_000, []providerobservation.QuotaWindow{
		{ID: "primary", SourceName: "Spark", WindowSeconds: &seconds, Utilization: &sparkUsed},
		{ID: "primary", SourceName: "Other", WindowSeconds: &seconds, Utilization: &otherUsed},
	})
	if remaining, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil || remaining {
		t.Fatalf("flush: remaining=%t error=%v", remaining, err)
	}
	var rows []models.CredentialQuotaHistory
	if err := db.Order("source_id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].WindowKey == rows[1].WindowKey || rows[0].SourceID != "codex_spark" || rows[0].UsedBasisPoints != 2000 || rows[1].SourceID != "other" || rows[1].UsedBasisPoints != 5000 {
		t.Fatalf("different named sources replaced each other: %+v", rows)
	}
}

func TestQuotaHistoryOnlyPersistsWindowsOfAtLeastOneDay(t *testing.T) {
	manager, db, registry, _, credential := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	newFlushableCredentialObservation(t, manager, credential.ID, models.CredentialObservationFresh, `{"quota_windows":[]}`)
	ref, _ := registry.CredentialRef(credential.ID)
	fiveHours, belowDay, oneDay, oneWeek := int64(18_000), int64(86_399), int64(86_400), int64(604_800)
	used := 0.25
	manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, 10_000, []providerobservation.QuotaWindow{
		{ID: "unknown", Utilization: &used},
		{ID: "five-hours", WindowSeconds: &fiveHours, Utilization: &used},
		{ID: "below-day", WindowSeconds: &belowDay, Utilization: &used},
		{ID: "one-day", WindowSeconds: &oneDay, Utilization: &used},
		{ID: "one-week", WindowSeconds: &oneWeek, Utilization: &used},
	})
	if remaining, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil || remaining {
		t.Fatalf("flush: remaining=%t error=%v", remaining, err)
	}
	var rows []models.CredentialQuotaHistory
	if err := db.Order("window_seconds").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].WindowID != "one-day" || rows[1].WindowID != "one-week" {
		t.Fatalf("short or unknown periods entered history: %+v", rows)
	}
}

func TestQuotaHistoryRecordsEveryDisplayedChangeAndDropsOutOfOrderObservation(t *testing.T) {
	manager, db, registry, _, credential := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	newFlushableCredentialObservation(t, manager, credential.ID, models.CredentialObservationFresh, `{"quota_windows":[]}`)
	ref, _ := registry.CredentialRef(credential.ID)
	seconds := int64(604_800)
	record := func(at int64, used float64) {
		manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, at,
			[]providerobservation.QuotaWindow{{ID: "primary", WindowSeconds: &seconds, Utilization: &used}})
	}
	flush := func() {
		t.Helper()
		if remaining, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil || remaining {
			t.Fatalf("flush: remaining=%t error=%v", remaining, err)
		}
	}
	record(10_000, 0.5)
	flush()
	record(20_000, 0.9)
	flush()
	record(30_000, 0.01)
	// 未刷新的相邻显示值变化都必须保留。
	record(40_000, 0.02)
	flush()
	// 乱序观测不能替换较新的额度值。
	record(50_000, 0.015)
	record(45_000, 0)
	flush()
	record(929_999, 0.3)
	flush()
	record(930_000, 0.4)
	flush()
	// 重启后仍从持久化的最后显示值继续去重。
	manager.passiveQuota = newPassiveQuotaPending()
	record(940_000, 0.39)
	flush()
	var rows []models.CredentialQuotaHistory
	if err := db.Order("observed_at_ms").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	wantTimes := []int64{10_000, 20_000, 30_000, 40_000, 50_000, 929_999, 930_000, 940_000}
	wantUsed := []int64{5000, 9000, 100, 200, 150, 3000, 4000, 3900}
	if len(rows) != len(wantTimes) {
		t.Fatalf("unexpected changed history: %+v", rows)
	}
	for index, row := range rows {
		if row.ObservedAtMS != wantTimes[index] || row.UsedBasisPoints != wantUsed[index] {
			t.Fatalf("point %d lost real observation: %+v", index, row)
		}
	}
}

func TestQuotaHistoryKeepsConsecutiveDisplayedChangesAndBoundsPendingMemory(t *testing.T) {
	pending := newPassiveQuotaPending()
	seconds := int64(604_800)
	for index, used := range []float64{0.9, 0.5, 0.1} {
		pending.nextVersion++
		pending.recordHistorySampleLocked(1, 1, 1, int64(index+1)*10_000, pending.nextVersion,
			[]providerobservation.QuotaWindow{{ID: "primary", WindowSeconds: &seconds, Utilization: &used}})
	}
	samples := pending.historyBatch(20)
	if len(samples) != 3 {
		t.Fatalf("consecutive displayed changes replaced pending points: %+v", samples)
	}
	stale := samples[0]
	stale.version--
	pending.ackHistory(stale)
	if len(pending.historyBatch(20)) != 3 {
		t.Fatal("stale acknowledgement discarded a newer sample")
	}
	pending.ackHistory(samples[0])
	if len(pending.historyBatch(20)) != 2 {
		t.Fatal("current acknowledgement did not discard the sample")
	}
	for index := 0; index < quotaHistoryCapacity+10; index++ {
		used := 0.5
		pending.nextVersion++
		pending.recordHistorySampleLocked(1, uint(index+2), 1, int64(index+4)*10_000, pending.nextVersion,
			[]providerobservation.QuotaWindow{{ID: "primary", WindowSeconds: &seconds, Utilization: &used}})
	}
	if len(pending.history) != quotaHistoryCapacity || len(pending.historyStates) != quotaHistoryCapacity {
		t.Fatalf("unbounded history memory: queued=%d states=%d", len(pending.history), len(pending.historyStates))
	}
}

func TestQuotaHistoryPreservesWebsocketHandshakeSample(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(fmt.Sprint("reset=", reset), func(t *testing.T) {
			manager, db, registry, _, credential := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			newFlushableCredentialObservation(t, manager, credential.ID, models.CredentialObservationFresh, `{"quota_windows":[]}`)
			ref, _ := registry.CredentialRef(credential.ID)
			seconds := int64(604_800)
			window := func(used float64) []providerobservation.QuotaWindow {
				return []providerobservation.QuotaWindow{{ID: "primary", WindowSeconds: &seconds, Utilization: &used}}
			}
			if reset {
				manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, 10_000, window(.9))
				if _, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			headerUsed, eventUsed := 0.0, 0.3
			if reset {
				headerUsed, eventUsed = .95, .01
			}
			manager.RecordPassiveQuotaPair(credential.ID, ref.IdentityGeneration,
				PassiveQuotaSample{ObservedAtMS: 20_000, Windows: window(headerUsed)},
				PassiveQuotaSample{ObservedAtMS: 30_000, Windows: window(eventUsed)})
			if remaining, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil || remaining {
				t.Fatalf("flush: %t %v", remaining, err)
			}
			var rows []models.CredentialQuotaHistory
			if err := db.Order("observed_at_ms").Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if reset {
				if len(rows) != 3 || rows[1].ObservedAtMS != 20_000 || rows[1].UsedBasisPoints != 9500 || rows[2].ObservedAtMS != 30_000 || rows[2].UsedBasisPoints != 100 {
					t.Fatalf("lost reset boundary: %+v", rows)
				}
			} else if len(rows) != 2 || rows[0].ObservedAtMS != 20_000 || rows[0].UsedBasisPoints != 0 || rows[1].ObservedAtMS != 30_000 || rows[1].UsedBasisPoints != 3000 {
				t.Fatalf("lost websocket changed observation: %+v", rows)
			}
		})
	}
}

func TestQuotaHistoryDetectsReboundAcrossHTTPAndNamedWebsocket(t *testing.T) {
	for _, namedFirst := range []bool{false, true} {
		t.Run(fmt.Sprint("named-first=", namedFirst), func(t *testing.T) {
			manager, db, registry, _, credential := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			newFlushableCredentialObservation(t, manager, credential.ID, models.CredentialObservationFresh,
				`{"quota_windows":[{"id":"spark-primary","source_id":"codex_spark","scope":"Spark","label":"Spark","unit":"percent","window_seconds":604800,"state":"available"}]}`)
			ref, _ := registry.CredentialRef(credential.ID)
			seconds := int64(604_800)
			for index, used := range []float64{.2, .9, .8} {
				window := providerobservation.QuotaWindow{ID: "primary", WindowSeconds: &seconds, Utilization: &used}
				if (index%2 == 0) == namedFirst {
					window.SourceName = "Spark"
				} else {
					window.SourceID = "codex_spark"
				}
				manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, int64(index+1)*10_000, []providerobservation.QuotaWindow{window})
				if remaining, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil || remaining {
					t.Fatalf("flush: %t %v", remaining, err)
				}
			}
			var rows []models.CredentialQuotaHistory
			if err := db.Order("observed_at_ms").Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if len(rows) != 3 || rows[1].ObservedAtMS != 20_000 || rows[1].UsedBasisPoints != 9000 || rows[2].ObservedAtMS != 30_000 || rows[2].UsedBasisPoints != 8000 || rows[0].WindowKey != rows[2].WindowKey {
				t.Fatalf("cross-transport rebound lost: %+v", rows)
			}
		})
	}
}

func TestQuotaHistoryDetectsReboundAfterSourceCacheEviction(t *testing.T) {
	for _, burst := range []bool{false, true} {
		t.Run(fmt.Sprint("burst=", burst), func(t *testing.T) {
			manager, db, registry, _, credential := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			newFlushableCredentialObservation(t, manager, credential.ID, models.CredentialObservationFresh,
				`{"quota_windows":[{"id":"spark-primary","source_id":"codex_spark","scope":"Spark","label":"Spark","unit":"percent","window_seconds":604800,"state":"available"}]}`)
			ref, _ := registry.CredentialRef(credential.ID)
			seconds := int64(604_800)
			for index, used := range []float64{.2, .9, .8} {
				window := providerobservation.QuotaWindow{ID: "primary", SourceID: "codex_spark", WindowSeconds: &seconds, Utilization: &used}
				if index == 2 {
					manager.passiveQuota.mu.Lock()
					clear(manager.passiveQuota.historySources)
					manager.passiveQuota.mu.Unlock()
					window.SourceID, window.SourceName = "", "Spark"
					if burst {
						used = .01
					}
				}
				manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, int64(index+1)*10_000, []providerobservation.QuotaWindow{window})
				if index == 2 && burst {
					used = .95
					manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, 40_000, []providerobservation.QuotaWindow{window})
				}
				for attempt := 0; attempt < 3; attempt++ {
					remaining, err := manager.FlushPassiveQuotaObservations(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					if !remaining {
						break
					}
					if attempt == 2 {
						t.Fatal("history did not drain")
					}
				}
			}
			var rows []models.CredentialQuotaHistory
			if err := db.Order("observed_at_ms").Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if !burst && (len(rows) != 3 || rows[1].ObservedAtMS != 20_000 || rows[1].UsedBasisPoints != 9000 || rows[2].ObservedAtMS != 30_000 || rows[2].UsedBasisPoints != 8000) {
				t.Fatalf("changed values were lost after source cache eviction: %+v", rows)
			}
			if burst && (len(rows) != 4 || rows[1].ObservedAtMS != 20_000 || rows[1].UsedBasisPoints != 9000 || rows[2].ObservedAtMS != 30_000 || rows[2].UsedBasisPoints != 100 || rows[3].ObservedAtMS != 40_000 || rows[3].UsedBasisPoints != 9500) {
				t.Fatalf("burst changes were lost after source cache eviction: %+v", rows)
			}
		})
	}
}
