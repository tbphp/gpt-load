package subscription

import (
	"encoding/json"
	"testing"
	"time"

	"gpt-load/internal/policy"
	"gpt-load/internal/storage/models"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func TestPassiveQuotaPreservesWindowTimesAndPublishesResetInvalidation(t *testing.T) {
	manager, db, registry, _, credential := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	newFlushableCredentialObservation(t, manager, credential.ID, models.CredentialObservationFresh,
		`{"quota_windows":[{"id":"short","scope":"account","state":"available","utilization":0.8,"window_seconds":18000,"reset_at_ms":5000,"observed_at_ms":1000},{"id":"long","scope":"account","state":"available","utilization":0.5,"window_seconds":604800,"reset_at_ms":9000,"observed_at_ms":1000}]}`)
	ref, ok := registry.CredentialRef(credential.ID)
	if !ok {
		t.Fatal("missing credential")
	}
	utilization, injectedTime := 0.95, int64(99999)
	manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, 2000, []providerobservation.QuotaWindow{
		{ID: "short", Utilization: &utilization, ObservedAtMS: &injectedTime},
	})
	if _, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil {
		t.Fatal(err)
	}
	var stored models.CredentialObservation
	if err := db.Take(&stored, "credential_id = ?", credential.ID).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot providerobservation.Snapshot
	if err := json.Unmarshal(stored.SnapshotJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.QuotaWindows) != 2 || snapshot.QuotaWindows[0].ObservedAtMS == nil ||
		*snapshot.QuotaWindows[0].ObservedAtMS != 2000 || snapshot.QuotaWindows[1].ObservedAtMS == nil ||
		*snapshot.QuotaWindows[1].ObservedAtMS != 1000 {
		t.Fatalf("sample time was forged or propagated to an untouched window: %s", stored.SnapshotJSON)
	}
	newReset := int64(7000)
	manager.RecordPassiveQuotaObservation(credential.ID, ref.IdentityGeneration, 3000, []providerobservation.QuotaWindow{
		{ID: "short", ResetAtMS: &newReset},
	})
	if _, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil {
		t.Fatal(err)
	}
	views := registry.Snapshot()
	if len(views) != 1 || len(views[0].QuotaWindows) != 2 {
		t.Fatalf("missing quota windows: %#v", views)
	}
	facts := views[0].QuotaWindows
	if facts[0].State != policy.FactStateUnknown || !facts[0].ObservedAt.IsZero() || facts[0].ResetAt.UnixMilli() != newReset {
		t.Fatalf("reset-only update did not invalidate the old sample: %#v", facts[0])
	}
	if facts[1].State != policy.FactStateMeasured || facts[1].ObservedAt.UnixMilli() != 1000 {
		t.Fatalf("untouched sample changed: %#v", facts[1])
	}
}
