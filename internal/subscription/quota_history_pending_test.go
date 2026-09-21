package subscription

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription/providers/codex"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func newQuotaHistoryExtendedFixture(t *testing.T) (*CredentialManager, uint, uint64) {
	manager, _, registry, _, credential := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	newFlushableCredentialObservation(t, manager, credential.ID, models.CredentialObservationFresh, `{"quota_windows":[{"id":"spark-primary","source_id":"codex_bengalfox","scope":"Spark","label":"Spark","unit":"percent","window_seconds":604800,"state":"available"}]}`)
	ref, _ := registry.CredentialRef(credential.ID)
	return manager, credential.ID, ref.IdentityGeneration
}

func drainQuotaHistoryObservations(t *testing.T, manager *CredentialManager) {
	t.Helper()
	for attempt := 0; attempt < 10; attempt++ {
		remaining, err := manager.FlushPassiveQuotaObservations(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !remaining {
			return
		}
	}
	t.Fatal("history did not drain")
}

func TestQuotaHistoryPreservesFourMixedObservations(t *testing.T) {
	for _, mode := range []string{"cold", "warm", "evicted"} {
		for _, namedFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/named-first=%t", mode, namedFirst), func(t *testing.T) {
				manager, id, identity := newQuotaHistoryExtendedFixture(t)
				seconds := int64(604800)
				if mode == "warm" {
					manager.passiveQuota.rememberHistorySources(id, identity, 0, []providerobservation.QuotaWindow{{ID: "spark-primary", SourceID: "codex_bengalfox", Scope: "Spark", WindowSeconds: &seconds}})
				}
				for index, used := range []float64{.2, .22, .21, .23} {
					window := providerobservation.QuotaWindow{ID: "primary", WindowSeconds: &seconds, Utilization: &used}
					if (index != 2) == namedFirst {
						window.SourceName = "Spark"
					} else {
						window.SourceID = "codex_bengalfox"
					}
					manager.RecordPassiveQuotaObservation(id, identity, int64(index+1)*10000, []providerobservation.QuotaWindow{window})
					if index == 0 && mode == "evicted" {
						drainQuotaHistoryObservations(t, manager)
						manager.passiveQuota.mu.Lock()
						clear(manager.passiveQuota.historySources)
						manager.passiveQuota.mu.Unlock()
					}
				}
				drainQuotaHistoryObservations(t, manager)
				var rows []models.CredentialQuotaHistory
				if err := manager.db.Order("observed_at_ms").Find(&rows).Error; err != nil {
					t.Fatal(err)
				}
				if len(rows) != 4 || rows[1].ObservedAtMS != 20000 || rows[1].UsedBasisPoints != 2200 || rows[2].ObservedAtMS != 30000 || rows[2].UsedBasisPoints != 2100 || rows[3].ObservedAtMS != 40000 || rows[3].UsedBasisPoints != 2300 {
					t.Fatalf("four changed observations were not retained: %+v", rows)
				}
			})
		}
	}
}

func TestQuotaHistoryPrefersNamedCopiesAcrossThreeEvents(t *testing.T) {
	for _, mode := range []string{"cold", "warm", "evicted"} {
		t.Run(mode, func(t *testing.T) {
			manager, id, identity := newQuotaHistoryExtendedFixture(t)
			seconds := int64(604800)
			if mode == "warm" {
				manager.passiveQuota.rememberHistorySources(id, identity, 0, []providerobservation.QuotaWindow{{ID: "spark-primary", SourceID: "codex_bengalfox", Scope: "Spark", WindowSeconds: &seconds}})
			}
			for index, pair := range [][2]int{{2, 2}, {1, 3}, {4, 4}} {
				at := int64(index+1) * 10000
				payload := fmt.Sprintf(`{"type":"codex.rate_limits","metered_limit_name":"codex_bengalfox","rate_limits":{"secondary":{"used_percent":%d,"window_minutes":10080,"reset_at":1800000001}},"additional_rate_limits":{"Spark":{"primary":{"used_percent":%d,"window_minutes":10080,"reset_at":1800000000}}}}`, pair[0], pair[1])
				windows := codex.NormalizeWebsocketQuotaWindows([]byte(payload), time.UnixMilli(at))
				manager.RecordPassiveQuotaObservation(id, identity, at, windows)
				if index == 0 && mode == "evicted" {
					drainQuotaHistoryObservations(t, manager)
					manager.passiveQuota.mu.Lock()
					clear(manager.passiveQuota.historySources)
					manager.passiveQuota.mu.Unlock()
				}
			}
			drainQuotaHistoryObservations(t, manager)
			var rows []models.CredentialQuotaHistory
			if err := manager.db.Order("observed_at_ms").Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if len(rows) != 3 || rows[0].ObservedAtMS != 10000 || rows[0].UsedBasisPoints != 200 || rows[1].ObservedAtMS != 20000 || rows[1].UsedBasisPoints != 300 || rows[2].ObservedAtMS != 30000 || rows[2].UsedBasisPoints != 400 {
				t.Fatalf("named copies did not retain the canonical changed series: %+v", rows)
			}
		})
	}
}

func TestQuotaHistoryPendingBufferIsBoundedAndSnapshotsKeepUpdating(t *testing.T) {
	manager, id, identity := newQuotaHistoryExtendedFixture(t)
	seconds, used := int64(604_800), .25
	for index := 0; index <= quotaHistoryCapacity; index++ {
		manager.RecordPassiveQuotaObservation(id, identity, 10_000+int64(index),
			[]providerobservation.QuotaWindow{{ID: "primary", SourceID: "codex_bengalfox", WindowSeconds: &seconds, Utilization: &used}})
	}
	pending := manager.passiveQuota
	if pending.historyObservationCount != quotaHistoryCapacity || len(pending.historyStates) != 0 || len(pending.history) != 0 {
		t.Fatalf("pending observations are unbounded or sampled before normalization: pending=%d states=%d history=%d", pending.historyObservationCount, len(pending.historyStates), len(pending.history))
	}
	if snapshot := manager.DirtyPassiveQuotaObservations(1); len(snapshot) != 1 || snapshot[0].ObservedAtMS != 10_000+quotaHistoryCapacity {
		t.Fatalf("full history buffer stopped live snapshot updates: %+v", snapshot)
	}
	drainQuotaHistoryObservations(t, manager)
	var rows []models.CredentialQuotaHistory
	if err := manager.db.Order("observed_at_ms").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if pending.historyObservationCount != 0 || len(rows) != 1 || rows[0].ObservedAtMS != 10_000 {
		t.Fatalf("pending buffer did not drain through display-value sampling: pending=%d rows=%+v", pending.historyObservationCount, rows)
	}
}

func TestQuotaHistorySourceLookupFailureKeepsCompleteObservations(t *testing.T) {
	manager, id, identity := newQuotaHistoryExtendedFixture(t)
	seconds := int64(604_800)
	for index, used := range []float64{.2, .22, .21, .23} {
		window := providerobservation.QuotaWindow{ID: "primary", SourceID: "codex_bengalfox", WindowSeconds: &seconds, Utilization: &used}
		if index == 2 {
			window.SourceID, window.SourceName = "", "Spark"
		}
		manager.RecordPassiveQuotaObservation(id, identity, int64(index+1)*10_000, []providerobservation.QuotaWindow{window})
	}
	const callbackName = "test:quota_history_source_lookup_failure"
	if err := manager.db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "credential_observations" && len(tx.Statement.Selects) == 1 && tx.Statement.Selects[0] == "snapshot_json" {
			tx.AddError(errors.New("forced history source lookup failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	if remaining, err := manager.FlushPassiveQuotaObservations(t.Context()); err == nil || !remaining || manager.passiveQuota.historyObservationCount != 4 {
		t.Fatalf("source lookup failure discarded evidence: remaining=%t error=%v pending=%d", remaining, err, manager.passiveQuota.historyObservationCount)
	}
	if err := manager.db.Callback().Query().Remove(callbackName); err != nil {
		t.Fatal(err)
	}
	drainQuotaHistoryObservations(t, manager)
	var rows []models.CredentialQuotaHistory
	if err := manager.db.Order("observed_at_ms").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || rows[1].ObservedAtMS != 20_000 || rows[1].UsedBasisPoints != 2200 || rows[2].ObservedAtMS != 30_000 || rows[2].UsedBasisPoints != 2100 || rows[3].ObservedAtMS != 40_000 || rows[3].UsedBasisPoints != 2300 {
		t.Fatalf("source lookup retry lost changed observations: %+v", rows)
	}
}

func TestQuotaHistoryReplaysArrivalsDuringSourceLookupBeforeDirectSampling(t *testing.T) {
	manager, id, identity := newQuotaHistoryExtendedFixture(t)
	seconds := int64(604_800)
	for index, used := range []float64{.2, .22, .21} {
		window := providerobservation.QuotaWindow{ID: "primary", SourceID: "codex_bengalfox", WindowSeconds: &seconds, Utilization: &used}
		if index == 2 {
			window.SourceID, window.SourceName = "", "Spark"
		}
		manager.RecordPassiveQuotaObservation(id, identity, int64(index+1)*10_000, []providerobservation.QuotaWindow{window})
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once, released sync.Once
	unblock := func() { released.Do(func() { close(release) }) }
	defer unblock()
	if err := manager.db.Callback().Query().Before("gorm:query").Register("test:block_history_source_lookup", func(tx *gorm.DB) {
		if tx.Statement.Table == "credential_observations" && len(tx.Statement.Selects) == 1 && tx.Statement.Selects[0] == "snapshot_json" {
			once.Do(func() {
				close(entered)
				select {
				case <-release:
				case <-tx.Statement.Context.Done():
					tx.AddError(tx.Statement.Context.Err())
				}
			})
		}
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := manager.FlushPassiveQuotaObservations(t.Context())
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("source lookup did not start")
	}
	used := .23
	manager.RecordPassiveQuotaObservation(id, identity, 40_000,
		[]providerobservation.QuotaWindow{{ID: "primary", SourceID: "codex_bengalfox", WindowSeconds: &seconds, Utilization: &used}})
	unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	drainQuotaHistoryObservations(t, manager)
	var rows []models.CredentialQuotaHistory
	if err := manager.db.Order("observed_at_ms").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || rows[1].ObservedAtMS != 20_000 || rows[2].ObservedAtMS != 30_000 || rows[3].ObservedAtMS != 40_000 {
		t.Fatalf("new arrival overtook unresolved changed observations: %+v", rows)
	}
	if manager.passiveQuota.historyObservationCount != 0 {
		t.Fatal("resolved account continued buffering observations")
	}
}

func TestQuotaHistoryWaitsForCompleteSourceMapping(t *testing.T) {
	for _, dualCopies := range []bool{false, true} {
		t.Run(fmt.Sprintf("dual-copies=%t", dualCopies), func(t *testing.T) {
			manager, id, identity := newQuotaHistoryExtendedFixture(t)
			missing := models.JSON(`{"quota_windows":[{"id":"secondary","source_id":"codex","scope":"account","unit":"percent","window_seconds":604800,"state":"available"}]}`)
			if err := manager.db.Model(&models.CredentialObservation{}).Where("credential_id = ?", id).Update("snapshot_json", missing).Error; err != nil {
				t.Fatal(err)
			}
			lookups := 0
			if err := manager.db.Callback().Query().Before("gorm:query").Register("test:count_incomplete_source_lookups", func(tx *gorm.DB) {
				if tx.Statement.Table == "credential_observations" && len(tx.Statement.Selects) == 1 && tx.Statement.Selects[0] == "snapshot_json" {
					lookups++
				}
			}); err != nil {
				t.Fatal(err)
			}
			seconds := int64(604800)
			for index, pair := range [][2]int{{2, 2}, {1, 3}, {4, 4}} {
				at := int64(index+1) * 10000
				used := float64(pair[1]) / 100
				windows := []providerobservation.QuotaWindow{{ID: "primary", SourceName: "Spark", WindowSeconds: &seconds, Utilization: &used}}
				if dualCopies {
					payload := fmt.Sprintf(`{"type":"codex.rate_limits","metered_limit_name":"codex_bengalfox","rate_limits":{"secondary":{"used_percent":%d,"window_minutes":10080,"reset_at":1800000001}},"additional_rate_limits":{"Spark":{"primary":{"used_percent":%d,"window_minutes":10080,"reset_at":1800000000}}}}`, pair[0], pair[1])
					windows = codex.NormalizeWebsocketQuotaWindows([]byte(payload), time.UnixMilli(at))
				}
				manager.RecordPassiveQuotaObservation(id, identity, at, windows)
			}
			if remaining, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil || remaining {
				t.Fatalf("incomplete mapping must wait without a retry loop: remaining=%t error=%v", remaining, err)
			}
			var count int64
			if err := manager.db.Model(&models.CredentialQuotaHistory{}).Count(&count).Error; err != nil || count != 0 || manager.passiveQuota.historyObservationCount != 3 {
				t.Fatalf("unresolved events must remain buffered, not persisted: rows=%d buffered=%d error=%v", count, manager.passiveQuota.historyObservationCount, err)
			}
			complete := models.JSON(`{"quota_windows":[{"id":"secondary","source_id":"codex","scope":"account","unit":"percent","window_seconds":604800,"state":"available"},{"id":"spark-primary","source_id":"codex_bengalfox","scope":"Spark","unit":"percent","window_seconds":604800,"state":"available"}]}`)
			if err := manager.db.Model(&models.CredentialObservation{}).Where("credential_id = ?", id).Update("snapshot_json", complete).Error; err != nil {
				t.Fatal(err)
			}
			used := .05
			manager.RecordPassiveQuotaObservation(id, identity, 40000, []providerobservation.QuotaWindow{{ID: "primary", SourceID: "codex_bengalfox", WindowSeconds: &seconds, Utilization: &used}})
			// The retry interval must also apply when another request wakes the worker.
			if _, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil || manager.passiveQuota.historyObservationCount != 4 || lookups != 1 {
				t.Fatalf("retry interval bypassed: buffered=%d lookups=%d error=%v", manager.passiveQuota.historyObservationCount, lookups, err)
			}
			// Simulate the next eligible worker wake without sleeping.
			manager.passiveQuota.mu.Lock()
			manager.passiveQuota.historyRetryAt[quotaHistoryCredential{credentialID: id, identity: identity}] = time.Now().Add(-time.Second)
			manager.passiveQuota.mu.Unlock()
			drainQuotaHistoryObservations(t, manager)
			var rows []models.CredentialQuotaHistory
			if err := manager.db.Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if len(rows) != 4 || rows[0].SourceID != "codex_bengalfox" || rows[0].ObservedAtMS != 10000 || rows[0].UsedBasisPoints != 200 || rows[1].ObservedAtMS != 20000 || rows[1].UsedBasisPoints != 300 || rows[2].ObservedAtMS != 30000 || rows[2].UsedBasisPoints != 400 || rows[3].ObservedAtMS != 40000 || rows[3].UsedBasisPoints != 500 || manager.passiveQuota.historyObservationCount != 0 {
				t.Fatalf("resolved history did not retain the canonical changed series: %+v", rows)
			}
			if len(manager.passiveQuota.historyRetryAt) != 0 {
				t.Fatal("resolved observations retained retry state")
			}
		})
	}
}

func TestQuotaHistoryKeepsLaterResolvedEventsBehindUnresolvedEvent(t *testing.T) {
	manager, id, identity := newQuotaHistoryExtendedFixture(t)
	if err := manager.db.Model(&models.CredentialObservation{}).Where("credential_id = ?", id).Update("snapshot_json", models.JSON(`{"quota_windows":[]}`)).Error; err != nil {
		t.Fatal(err)
	}
	seconds := int64(604800)
	for index, used := range []float64{.2, .22, .21, .23} {
		window := providerobservation.QuotaWindow{ID: "primary", SourceID: "codex_bengalfox", WindowSeconds: &seconds, Utilization: &used}
		if index == 2 {
			window.SourceID, window.SourceName = "", "Spark"
		}
		manager.RecordPassiveQuotaObservation(id, identity, int64(index+1)*10000, []providerobservation.QuotaWindow{window})
	}
	if _, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil || manager.passiveQuota.historyObservationCount != 2 {
		t.Fatalf("unresolved observation and subsequent event must stay in order: buffered=%d error=%v", manager.passiveQuota.historyObservationCount, err)
	}
	if err := manager.db.Model(&models.CredentialObservation{}).Where("credential_id = ?", id).Update("snapshot_json", models.JSON(`{"quota_windows":[{"id":"spark-primary","source_id":"codex_bengalfox","scope":"Spark","unit":"percent","window_seconds":604800,"state":"available"}]}`)).Error; err != nil {
		t.Fatal(err)
	}
	manager.passiveQuota.mu.Lock()
	manager.passiveQuota.historyRetryAt[quotaHistoryCredential{credentialID: id, identity: identity}] = time.Now().Add(-time.Second)
	manager.passiveQuota.mu.Unlock()
	drainQuotaHistoryObservations(t, manager)
	var rows []models.CredentialQuotaHistory
	if err := manager.db.Order("observed_at_ms").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || rows[1].ObservedAtMS != 20000 || rows[1].UsedBasisPoints != 2200 || rows[2].ObservedAtMS != 30000 || rows[2].UsedBasisPoints != 2100 || rows[3].ObservedAtMS != 40000 || rows[3].UsedBasisPoints != 2300 {
		t.Fatalf("later resolved observation overtook quota changes: %+v", rows)
	}
}
