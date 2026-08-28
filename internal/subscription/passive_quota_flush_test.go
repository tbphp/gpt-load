package subscription

import (
	"strings"
	"testing"
	"time"

	"gpt-load/internal/storage/models"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func newFlushableCredentialObservation(
	t *testing.T,
	manager *CredentialManager,
	credentialID uint,
	state models.CredentialObservationState,
	snapshotJSON string,
) models.CredentialObservation {
	t.Helper()
	if err := manager.db.AutoMigrate(&models.CredentialObservation{}); err != nil {
		t.Fatal(err)
	}
	observedAt := int64(1000)
	row := models.CredentialObservation{
		CredentialID: credentialID, IdentityFingerprint: "identity", SchemaVersion: 1,
		ObservationVersion: 1, SnapshotJSON: models.JSON(snapshotJSON), State: state,
		ObservedAtMS: &observedAt, UpdatedAtMS: 1000,
	}
	if err := manager.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row
}

func TestFlushPassiveQuotaObservationsUpdatesExistingWindowAndProjectsFreshToRegistry(t *testing.T) {
	manager, db, registry, _, row := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	newFlushableCredentialObservation(t, manager, row.ID, models.CredentialObservationFresh,
		`{"plan_summary":{"name":"Pro 20x"},"quota_windows":[{"id":"primary","label":"Session","scope":"account","unit":"percent","state":"available","window_seconds":300,"reset_at_ms":1800000000000}],"reset_credits_available":3}`,
	)
	ref, ok := registry.CredentialRef(row.ID)
	if !ok {
		t.Fatal("credential ref is unavailable")
	}
	used := 55.0
	utilization := 0.55
	manager.RecordPassiveQuotaObservation(row.ID, ref.IdentityGeneration, 2000, []providerobservation.QuotaWindow{
		{ID: "primary", Label: "Session", Scope: "account", Unit: "percent", State: "available", Used: &used, Utilization: &utilization},
	})

	remaining, err := manager.FlushPassiveQuotaObservations(t.Context())
	if err != nil {
		t.Fatalf("FlushPassiveQuotaObservations() error = %v", err)
	}
	if remaining {
		t.Fatal("FlushPassiveQuotaObservations() reported remaining pending after flushing everything")
	}
	if dirty := manager.DirtyPassiveQuotaObservations(1); len(dirty) != 0 {
		t.Fatalf("pending observation was not acknowledged: %#v", dirty)
	}

	var stored models.CredentialObservation
	if err := db.Take(&stored, "credential_id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ObservedAtMS == nil || *stored.ObservedAtMS != 2000 {
		t.Fatalf("stored observed_at_ms = %#v, want 2000", stored.ObservedAtMS)
	}
	snapshot := string(stored.SnapshotJSON)
	if !strings.Contains(snapshot, `"used":55`) || !strings.Contains(snapshot, `"window_seconds":300`) ||
		!strings.Contains(snapshot, `"Pro 20x"`) || !strings.Contains(snapshot, `"reset_credits_available":3`) {
		t.Fatalf("stored snapshot_json = %s", snapshot)
	}

	views := registry.Snapshot()
	if len(views) != 1 || views[0].ObservedQuotaRemaining() == nil {
		t.Fatalf("registry was not projected from the fresh flush: %#v", views)
	}
}

func TestFlushPassiveQuotaObservationsSkipsUnknownWindowID(t *testing.T) {
	manager, db, registry, _, row := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	newFlushableCredentialObservation(t, manager, row.ID, models.CredentialObservationFresh,
		`{"plan_summary":{},"quota_windows":[{"id":"primary","scope":"account","unit":"percent","state":"available"}]}`,
	)
	ref, _ := registry.CredentialRef(row.ID)
	used := 10.0
	manager.RecordPassiveQuotaObservation(row.ID, ref.IdentityGeneration, 2000, []providerobservation.QuotaWindow{
		{ID: "secondary", Scope: "account", State: "available", Used: &used},
	})

	if _, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil {
		t.Fatalf("FlushPassiveQuotaObservations() error = %v", err)
	}
	if dirty := manager.DirtyPassiveQuotaObservations(1); len(dirty) != 0 {
		t.Fatalf("pending observation with no matching window was not acknowledged: %#v", dirty)
	}

	var stored models.CredentialObservation
	if err := db.Take(&stored, "credential_id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ObservedAtMS == nil || *stored.ObservedAtMS != 1000 {
		t.Fatalf("stored observed_at_ms = %#v, want unchanged 1000", stored.ObservedAtMS)
	}
	if strings.Contains(string(stored.SnapshotJSON), `"secondary"`) {
		t.Fatalf("an unknown window ID was created: %s", stored.SnapshotJSON)
	}
}

func TestFlushPassiveQuotaObservationsDiscardsWithoutExistingRow(t *testing.T) {
	manager, _, registry, _, row := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	if err := manager.db.AutoMigrate(&models.CredentialObservation{}); err != nil {
		t.Fatal(err)
	}
	ref, _ := registry.CredentialRef(row.ID)
	used := 10.0
	manager.RecordPassiveQuotaObservation(row.ID, ref.IdentityGeneration, 2000, []providerobservation.QuotaWindow{
		{ID: "primary", Scope: "account", State: "available", Used: &used},
	})

	if _, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil {
		t.Fatalf("FlushPassiveQuotaObservations() error = %v", err)
	}
	if dirty := manager.DirtyPassiveQuotaObservations(1); len(dirty) != 0 {
		t.Fatalf("pending observation for a credential with no observation row was not acknowledged: %#v", dirty)
	}
	var count int64
	if err := manager.db.Model(&models.CredentialObservation{}).Where("credential_id = ?", row.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("a new observation row was created, want none: count=%d", count)
	}
}

func TestFlushPassiveQuotaObservationsDiscardsOnIdentityGenerationMismatch(t *testing.T) {
	manager, db, _, _, row := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	newFlushableCredentialObservation(t, manager, row.ID, models.CredentialObservationFresh,
		`{"plan_summary":{},"quota_windows":[{"id":"primary","scope":"account","unit":"percent","state":"available"}]}`,
	)
	used := 10.0
	manager.RecordPassiveQuotaObservation(row.ID, 999999, 2000, []providerobservation.QuotaWindow{
		{ID: "primary", Scope: "account", State: "available", Used: &used},
	})

	if _, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil {
		t.Fatalf("FlushPassiveQuotaObservations() error = %v", err)
	}
	if dirty := manager.DirtyPassiveQuotaObservations(1); len(dirty) != 0 {
		t.Fatalf("pending observation with a stale identity generation was not acknowledged: %#v", dirty)
	}
	var stored models.CredentialObservation
	if err := db.Take(&stored, "credential_id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ObservedAtMS == nil || *stored.ObservedAtMS != 1000 {
		t.Fatalf("stored observed_at_ms = %#v, want unchanged 1000", stored.ObservedAtMS)
	}
}

func TestFlushPassiveQuotaObservationsDoesNotProjectStaleStateToRegistry(t *testing.T) {
	manager, _, registry, _, row := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	newFlushableCredentialObservation(t, manager, row.ID, models.CredentialObservationStale,
		`{"plan_summary":{},"quota_windows":[{"id":"primary","scope":"account","unit":"percent","state":"available"}]}`,
	)
	ref, _ := registry.CredentialRef(row.ID)
	used := 10.0
	manager.RecordPassiveQuotaObservation(row.ID, ref.IdentityGeneration, 2000, []providerobservation.QuotaWindow{
		{ID: "primary", Scope: "account", State: "available", Used: &used},
	})

	if _, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil {
		t.Fatalf("FlushPassiveQuotaObservations() error = %v", err)
	}
	views := registry.Snapshot()
	if len(views) != 1 || views[0].ObservedQuotaRemaining() != nil {
		t.Fatalf("a stale row's passive update was projected to the registry: %#v", views)
	}
}

func TestFlushPassiveQuotaObservationsPreservesUntouchedStateAndMetadata(t *testing.T) {
	manager, db, registry, _, row := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	existing := newFlushableCredentialObservation(t, manager, row.ID, models.CredentialObservationError,
		`{"plan_summary":{},"quota_windows":[{"id":"primary","scope":"account","unit":"percent","state":"available"}]}`,
	)
	existing.LastErrorCode = "observation_upstream_failed"
	if err := db.Save(&existing).Error; err != nil {
		t.Fatal(err)
	}
	ref, _ := registry.CredentialRef(row.ID)
	used := 10.0
	manager.RecordPassiveQuotaObservation(row.ID, ref.IdentityGeneration, 2000, []providerobservation.QuotaWindow{
		{ID: "primary", Scope: "account", State: "available", Used: &used},
	})

	if _, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil {
		t.Fatalf("FlushPassiveQuotaObservations() error = %v", err)
	}
	var stored models.CredentialObservation
	if err := db.Take(&stored, "credential_id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.State != models.CredentialObservationError || stored.LastErrorCode != "observation_upstream_failed" {
		t.Fatalf("stored observation = %#v, want state/last_error_code preserved", stored)
	}
}
