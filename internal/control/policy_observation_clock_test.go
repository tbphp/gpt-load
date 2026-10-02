package control

import (
	"testing"

	"gpt-load/internal/storage/models"
)

func TestPolicyObservationClearRejectsOldIdentity(t *testing.T) {
	fixture, _, credentialID := newSubscriptionCredentialFixture(t)
	ref, ok := fixture.registry.CredentialRef(credentialID)
	if !ok {
		t.Fatal("missing credential")
	}
	observed, reset, seconds, utilization := int64(1000), int64(5000), int64(18000), 0.95
	response := &CredentialObservationResponse{
		State: string(models.CredentialObservationFresh),
		Snapshot: &CredentialObservationSnapshot{QuotaWindows: []ObservationQuotaWindow{{
			ID: "short", Scope: "account", State: "available",
			ObservedAtMS: &observed, ResetAtMS: &reset, WindowSeconds: &seconds, Utilization: &utilization,
		}}},
	}
	fixture.service.applyCredentialQuotaObservation(credentialID, ref.IdentityGeneration, response)
	fixture.service.applyCredentialQuotaObservation(credentialID, ref.IdentityGeneration^1, nil)
	views := fixture.registry.Snapshot()
	if len(views) != 1 || len(views[0].QuotaWindows) != 1 {
		t.Fatalf("a different identity cleared the current sample: %#v", views)
	}
	fixture.service.applyCredentialQuotaObservation(credentialID, ref.IdentityGeneration, nil)
	if len(fixture.registry.Snapshot()[0].QuotaWindows) != 0 {
		t.Fatal("current identity could not clear its sample")
	}
}
