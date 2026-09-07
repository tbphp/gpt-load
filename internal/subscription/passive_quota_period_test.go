package subscription

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription/providers/codex"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func TestMergePassiveQuotaSnapshotChecksWindowPeriod(t *testing.T) {
	weekly, fiveHour := int64(7*24*60*60), int64(5*60*60)
	for _, test := range []struct {
		name         string
		id           string
		storedPeriod *int64
		patchPeriod  *int64
		wantMatched  bool
	}{
		{"weekly to five hour", "primary", &weekly, &fiveHour, false},
		{"five hour to weekly", "primary", &fiveHour, &weekly, false},
		{"secondary conflict", "secondary", &weekly, &fiveHour, false},
		{"additional window conflict", "spark-primary", &weekly, &fiveHour, false},
		{"same period", "primary", &weekly, &weekly, true},
		{"partial header omits period", "primary", &weekly, nil, true},
		{"fills unknown period", "primary", nil, &weekly, true},
		{"both periods unknown", "primary", nil, nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			used, limit, remaining, utilization := 100.0, 100.0, 0.0, 1.0
			resetAt := int64(1800000000000)
			previous := providerobservation.QuotaWindow{
				ID: test.id, Label: "Active window", Scope: "account", Unit: "percent",
				Used: &used, Limit: &limit, Remaining: &remaining, Utilization: &utilization,
				ResetAtMS: &resetAt, WindowSeconds: test.storedPeriod,
				State: "exhausted", IsPrimary: true,
			}
			raw, err := json.Marshal(providerobservation.Snapshot{
				Plan:         providerobservation.PlanSummary{Name: "Pro"},
				QuotaWindows: []providerobservation.QuotaWindow{previous},
			})
			if err != nil {
				t.Fatal(err)
			}
			patchUsed, patchRemaining, patchUtilization := 10.0, 90.0, 0.1
			patchResetAt := resetAt + 60000
			patch := providerobservation.QuotaWindow{
				ID: test.id, Used: &patchUsed, Limit: &limit,
				Remaining: &patchRemaining, Utilization: &patchUtilization,
				ResetAtMS: &patchResetAt, WindowSeconds: test.patchPeriod, State: "available",
			}

			merged, err := mergePassiveQuotaSnapshot(raw, []providerobservation.QuotaWindow{patch})
			if err != nil {
				t.Fatal(err)
			}
			if merged.Matched != test.wantMatched || merged.Changed != test.wantMatched {
				t.Fatalf("Matched=%v Changed=%v, want both %v", merged.Matched, merged.Changed, test.wantMatched)
			}
			want := previous
			if test.wantMatched {
				want.Used, want.Remaining, want.Utilization = &patchUsed, &patchRemaining, &patchUtilization
				want.ResetAtMS, want.State = &patchResetAt, "available"
				if test.patchPeriod != nil {
					want.WindowSeconds = test.patchPeriod
				}
			} else if !bytes.Equal(merged.Encoded, raw) {
				t.Fatalf("conflicting sample changed the snapshot: %s", merged.Encoded)
			}
			if !reflect.DeepEqual(merged.Windows, []providerobservation.QuotaWindow{want}) {
				t.Fatalf("merged windows = %#v, want %#v", merged.Windows, want)
			}
		})
	}
}

const passiveQuotaWeeklyAndSparkSnapshot = `{"plan_summary":{"name":"Pro"},"quota_windows":[{"id":"primary","label":"7d","label_key":"weekly","scope":"account","unit":"percent","used":100,"limit":100,"remaining":0,"utilization":1,"reset_at_ms":1800000000000,"window_seconds":604800,"state":"exhausted","is_primary":true},{"id":"gpt-5-3-codex-spark-primary","label":"GPT 5.3 Codex Spark · 5h","scope":"GPT 5.3 Codex Spark","unit":"percent","used":20,"limit":100,"remaining":80,"utilization":0.2,"reset_at_ms":1799900000000,"window_seconds":18000,"state":"available"}],"reset_credits_available":3}`

func TestMergePassiveQuotaSnapshotKeepsCompatibleWindowAlongsideConflict(t *testing.T) {
	var previous providerobservation.Snapshot
	if err := json.Unmarshal([]byte(passiveQuotaWeeklyAndSparkSnapshot), &previous); err != nil {
		t.Fatal(err)
	}
	fiveHour := int64(5 * 60 * 60)
	used, limit, remaining, utilization := 30.0, 100.0, 70.0, 0.3
	patch := providerobservation.QuotaWindow{
		ID: "primary", WindowSeconds: &fiveHour, State: "available",
		Used: &used, Limit: &limit, Remaining: &remaining, Utilization: &utilization,
	}
	sparkPatch := patch
	sparkPatch.ID = "gpt-5-3-codex-spark-primary"

	merged, err := mergePassiveQuotaSnapshot([]byte(passiveQuotaWeeklyAndSparkSnapshot),
		[]providerobservation.QuotaWindow{patch, sparkPatch})
	if err != nil {
		t.Fatal(err)
	}
	if !merged.Matched || !merged.Changed || len(merged.Windows) != 2 {
		t.Fatalf("merge = %#v, want only the compatible window updated", merged)
	}
	want := append([]providerobservation.QuotaWindow(nil), previous.QuotaWindows...)
	want[1].Used, want[1].Remaining, want[1].Utilization = &used, &remaining, &utilization
	if !reflect.DeepEqual(merged.Windows, want) {
		t.Fatalf("weekly or Spark window corrupted: %s", merged.Encoded)
	}
	var stored providerobservation.Snapshot
	if err := json.Unmarshal(merged.Encoded, &stored); err != nil {
		t.Fatal(err)
	}
	previous.QuotaWindows = want
	if !reflect.DeepEqual(stored, previous) {
		t.Fatalf("encoded snapshot differs from expected windows and metadata: %s", merged.Encoded)
	}
}

func TestFlushPassiveQuotaObservationsDiscardsConflictingPeriodWithoutAdvancingFreshness(t *testing.T) {
	manager, db, registry, _, row := newCredentialManagerFixture(t,
		credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	existing := newFlushableCredentialObservation(t, manager, row.ID,
		models.CredentialObservationFresh, passiveQuotaWeeklyAndSparkSnapshot)
	var snapshot providerobservation.Snapshot
	if err := json.Unmarshal(existing.SnapshotJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	if !registry.ApplyQuotaWindows(row.ID, snapshot.QuotaWindows) {
		t.Fatal("failed to initialize quota health projection")
	}
	before := registry.Snapshot()
	ref, ok := registry.CredentialRef(row.ID)
	if !ok {
		t.Fatal("credential ref is unavailable")
	}
	// 普通 7d 与 Spark 5h 都可能占用 primary；冲突样本不能刷新任何账号级状态。
	observedAt := time.UnixMilli(2000)
	windows := codex.NormalizePassiveQuotaWindows(map[string]string{
		"X-Codex-Primary-Used-Percent":   "10",
		"X-Codex-Primary-Window-Minutes": "300",
		"X-Codex-Primary-Reset-At":       "1799900000",
	}, observedAt)
	if len(windows) != 1 || windows[0].ID != "primary" {
		t.Fatalf("unexpected passive windows: %#v", windows)
	}
	manager.RecordPassiveQuotaObservation(row.ID, ref.IdentityGeneration, observedAt.UnixMilli(), windows)

	pending, err := manager.FlushPassiveQuotaObservations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if pending || len(manager.DirtyPassiveQuotaObservations(1)) != 0 {
		t.Fatal("conflicting sample must be acknowledged, not retried forever")
	}
	var stored models.CredentialObservation
	if err := db.Take(&stored, "credential_id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored, existing) {
		t.Fatalf("conflicting sample changed observation data or freshness: %#v", stored)
	}
	if after := registry.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatalf("conflicting sample changed quota health projection: %#v", after)
	}
}
