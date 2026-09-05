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
