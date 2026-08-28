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

func TestFlushPassiveQuotaObservationsDiscardsSampleOlderThanStoredObservation(t *testing.T) {
	manager, db, registry, _, row := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	// A newer active observation is already stored: observed at 5000, used 90.
	if err := manager.db.AutoMigrate(&models.CredentialObservation{}); err != nil {
		t.Fatal(err)
	}
	newer := int64(5000)
	stored := models.CredentialObservation{
		CredentialID: row.ID, IdentityFingerprint: "identity", SchemaVersion: 1, ObservationVersion: 2,
		SnapshotJSON: models.JSON(
			`{"plan_summary":{},"quota_windows":[{"id":"primary","label":"5h","scope":"account","unit":"percent","state":"available","used":90,"utilization":0.9,"reset_at_ms":1800000000000}]}`,
		),
		State: models.CredentialObservationFresh, ObservedAtMS: &newer, UpdatedAtMS: 5000,
	}
	if err := manager.db.Create(&stored).Error; err != nil {
		t.Fatal(err)
	}
	ref, _ := registry.CredentialRef(row.ID)
	// A passive sample captured before that active refresh completed.
	oldUsed, oldUtilization := 10.0, 0.1
	manager.RecordPassiveQuotaObservation(row.ID, ref.IdentityGeneration, 2000, []providerobservation.QuotaWindow{
		{ID: "primary", Label: "5h", Scope: "account", Unit: "percent", State: "available",
			Used: &oldUsed, Utilization: &oldUtilization},
	})

	if _, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil {
		t.Fatalf("FlushPassiveQuotaObservations() error = %v", err)
	}
	if dirty := manager.DirtyPassiveQuotaObservations(1); len(dirty) != 0 {
		t.Fatalf("stale sample was not acknowledged and will retry forever: %#v", dirty)
	}

	var persisted models.CredentialObservation
	if err := db.Take(&persisted, "credential_id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.ObservedAtMS == nil || *persisted.ObservedAtMS != 5000 {
		t.Fatalf("observed_at_ms = %#v, want the newer 5000 preserved", persisted.ObservedAtMS)
	}
	if !strings.Contains(string(persisted.SnapshotJSON), `"used":90`) {
		t.Fatalf("snapshot_json = %s, want the newer used:90 preserved", persisted.SnapshotJSON)
	}
}

func TestFlushPassiveQuotaObservationsAppliesSampleAtSameObservationTime(t *testing.T) {
	manager, db, registry, _, row := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	newFlushableCredentialObservation(t, manager, row.ID, models.CredentialObservationFresh,
		`{"plan_summary":{},"quota_windows":[{"id":"primary","label":"5h","scope":"account","unit":"percent","state":"available","reset_at_ms":1800000000000}]}`,
	)
	ref, _ := registry.CredentialRef(row.ID)
	used, utilization := 42.0, 0.42
	// newFlushableCredentialObservation stores observed_at_ms = 1000.
	manager.RecordPassiveQuotaObservation(row.ID, ref.IdentityGeneration, 1000, []providerobservation.QuotaWindow{
		{ID: "primary", Label: "5h", Scope: "account", Unit: "percent", State: "available",
			Used: &used, Utilization: &utilization},
	})

	if _, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil {
		t.Fatalf("FlushPassiveQuotaObservations() error = %v", err)
	}
	var persisted models.CredentialObservation
	if err := db.Take(&persisted, "credential_id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(persisted.SnapshotJSON), `"used":42`) {
		t.Fatalf("snapshot_json = %s, want an equally-timed sample to still merge", persisted.SnapshotJSON)
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
