package subscription

import (
	"encoding/json"
	"testing"
	"time"

	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription/providers/codex"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func TestFlushPassiveQuotaSourcesPreservesCardWindowsAndHealth(t *testing.T) {
	manager, db, registry, _, row := newCredentialManagerFixture(t,
		credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	raw, err := codex.NormalizeQuota([]byte(codexSourceQuotaPayload), nil)
	if err != nil {
		t.Fatal(err)
	}
	initial := newFlushableCredentialObservation(t, manager, row.ID, models.CredentialObservationFresh, string(raw))
	ref, ok := registry.CredentialRef(row.ID)
	if !ok {
		t.Fatal("credential ref is unavailable")
	}
	observedAt := time.UnixMilli(2000)
	patches := codex.NormalizePassiveQuotaWindows(map[string]string{
		"X-Codex-Active-Limit":                     "premium",
		"X-Codex-Primary-Used-Percent":             "0",
		"X-Codex-Primary-Window-Minutes":           "300",
		"X-Codex-Secondary-Used-Percent":           "100",
		"X-Codex-Secondary-Window-Minutes":         "10080",
		"X-Codex-Secondary-Reset-At":               "1800000000",
		"X-Codex-Bengalfox-Primary-Used-Percent":   "30",
		"X-Codex-Bengalfox-Primary-Window-Minutes": "300",
	}, observedAt)
	manager.RecordPassiveQuotaObservation(row.ID, ref.IdentityGeneration, observedAt.UnixMilli(), patches)
	pending, err := manager.FlushPassiveQuotaObservations(t.Context())
	if err != nil || pending || len(manager.DirtyPassiveQuotaObservations(1)) != 0 {
		t.Fatalf("flush pending=%v error=%v", pending, err)
	}
	var stored models.CredentialObservation
	if err := db.Take(&stored, "credential_id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ObservedAtMS == nil || *stored.ObservedAtMS != 2000 ||
		stored.ObservationVersion != initial.ObservationVersion || stored.State != initial.State {
		t.Fatalf("unexpected observation metadata: %#v", stored)
	}
	var snapshot providerobservation.Snapshot
	if err := json.Unmarshal(stored.SnapshotJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.QuotaWindows) != 2 {
		t.Fatalf("passive update changed the card's window set: %s", stored.SnapshotJSON)
	}
	weekly, spark := snapshot.QuotaWindows[0], snapshot.QuotaWindows[1]
	if weekly.ID != "primary" || weekly.SourceID != "codex" ||
		weekly.WindowSeconds == nil || *weekly.WindowSeconds != 604800 ||
		weekly.Used == nil || *weekly.Used != 100 || weekly.State != "exhausted" ||
		spark.SourceID != "codex_bengalfox" || spark.Used == nil || *spark.Used != 30 {
		t.Fatalf("quota sources were not refreshed independently: %s", stored.SnapshotJSON)
	}
	views := registry.Snapshot()
	if len(views) != 1 || views[0].ObservedQuotaRemaining() == nil || *views[0].ObservedQuotaRemaining() != 0 {
		t.Fatalf("Spark or the untracked 5h window polluted account health: %#v", views)
	}
}

func TestPassiveQuotaWindowsKeepIndependentFreshness(t *testing.T) {
	raw := []byte(`{"plan_summary":{},"quota_windows":[{"id":"primary","source_id":"codex","scope":"account","unit":"percent","used":100,"utilization":1,"state":"exhausted","window_seconds":18000},{"id":"spark-primary","source_id":"codex_bengalfox","scope":"Spark","unit":"percent","used":80,"utilization":0.8,"state":"available","window_seconds":18000}]}`)
	storedAt, period := int64(1000), int64(18000)
	for _, test := range []struct {
		source string
		at     int64
		used   float64
	}{
		{source: "codex_bengalfox", at: 3000, used: 90},
		{source: "codex", at: 2000, used: 25},
		{source: "codex", at: 1500, used: 95},
		{source: "codex", at: 2000, used: 95},
	} {
		merged, observedAt, err := mergePassiveQuotaSamples(raw, &storedAt, PassiveQuotaObservation{
			ObservedAtMS: test.at,
			Windows: []providerobservation.QuotaWindow{{
				SourceID: test.source, WindowSeconds: &period, State: "available",
				Used: floatPointer(test.used), Utilization: floatPointer(test.used / 100),
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if test.source == "codex" && test.used == 25 && (!merged.Recovered || merged.RecoveredAtMS != 2000) {
			t.Fatalf("account recovery borrowed another window's time: %#v", merged)
		}
		raw, storedAt = merged.Encoded, observedAt
	}
	var snapshot struct {
		providerobservation.Snapshot
		WindowTimes []providerobservation.QuotaWindowObservationTimes `json:"quota_window_observed_at_ms"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.QuotaWindows) != 2 || len(snapshot.WindowTimes) != 2 {
		t.Fatalf("windows changed: %s", raw)
	}
	account, feature := snapshot.QuotaWindows[0], snapshot.QuotaWindows[1]
	if account.Used == nil || *account.Used != 25 || snapshot.WindowTimes[0].Usage != 2000 ||
		feature.Used == nil || *feature.Used != 90 || snapshot.WindowTimes[1].Usage != 3000 || storedAt != 3000 {
		t.Fatalf("independent window freshness was lost: %s, row time=%d", raw, storedAt)
	}
}

func TestPassiveQuotaPartialSamplesKeepIndependentFields(t *testing.T) {
	// 旧版内部时间数组必须仍能读取；后续不同字段按各自的证据时间合并。
	raw := []byte(`{"quota_windows":[{"id":"primary","source_id":"codex","scope":"account","unit":"percent","used":100,"utilization":1,"state":"exhausted","reset_at_ms":6000,"window_seconds":18000}],"quota_window_observed_at_ms":[1000]}`)
	storedAt, period := int64(1000), int64(18000)
	for _, test := range []struct {
		name, state, wantState string
		at, reset              int64
		used                   *float64
		wantUsed               float64
		wantMatched            bool
	}{
		{name: "new reset", at: 3000, reset: 9000, wantUsed: 100, wantState: "exhausted", wantMatched: true},
		{name: "earlier usage still newer than its baseline", at: 2000, used: floatPointer(25), state: "available", wantUsed: 25, wantState: "available", wantMatched: true},
		{name: "new denial", at: 2500, state: "exhausted", wantUsed: 25, wantState: "exhausted", wantMatched: true},
		{name: "usage cannot replace newer denial", at: 2400, used: floatPointer(30), state: "available", wantUsed: 30, wantState: "exhausted", wantMatched: true},
		{name: "older reset", at: 1500, reset: 7000, wantUsed: 30, wantState: "exhausted"},
		{name: "tied reset", at: 3000, reset: 7000, wantUsed: 30, wantState: "exhausted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			patch := providerobservation.QuotaWindow{SourceID: "codex", WindowSeconds: &period, State: test.state}
			if test.used != nil {
				patch.Used, patch.Utilization = test.used, floatPointer(*test.used/100)
			}
			if test.reset != 0 {
				patch.ResetAtMS = &test.reset
			}
			merged, observedAt, err := mergePassiveQuotaSamples(raw, &storedAt, PassiveQuotaObservation{
				ObservedAtMS: test.at, Windows: []providerobservation.QuotaWindow{patch},
			})
			if err != nil {
				t.Fatal(err)
			}
			var snapshot providerobservation.Snapshot
			if err := json.Unmarshal(merged.Encoded, &snapshot); err != nil || len(snapshot.QuotaWindows) != 1 {
				t.Fatalf("readback: %v, snapshot=%s", err, merged.Encoded)
			}
			window := snapshot.QuotaWindows[0]
			if merged.Matched != test.wantMatched || window.Used == nil || *window.Used != test.wantUsed ||
				window.State != test.wantState || window.ResetAtMS == nil || *window.ResetAtMS != 9000 || observedAt != 3000 {
				t.Errorf("field freshness was lost: matched=%t, at=%d, snapshot=%s", merged.Matched, observedAt, merged.Encoded)
			}
			if test.at == 2000 && (!merged.Recovered || merged.RecoveredAtMS != 2000) {
				t.Errorf("usage recovery borrowed metadata time: recovered=%t, at=%d", merged.Recovered, merged.RecoveredAtMS)
			}
			raw, storedAt = merged.Encoded, observedAt
		})
	}
}
