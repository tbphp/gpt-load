package subscription

import (
	"testing"
	"time"

	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func TestPassiveQuotaAdmissionDoesNotWaitForPersistenceLock(t *testing.T) {
	manager, _, registry, _, row := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	ref, _ := registry.CredentialRef(row.ID)
	locked, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go manager.mutations.Do(row.ID, func() { close(locked); <-release })
	<-locked
	defer close(release)
	go func() {
		manager.RecordPassiveQuotaObservation(row.ID, ref.IdentityGeneration, 2000,
			[]providerobservation.QuotaWindow{{ID: "primary", State: "available"}})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("quota admission waited for the background persistence lock")
	}
}
