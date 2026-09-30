package control

import "testing"

func TestCredentialPlanHintDoesNotInventObservation(t *testing.T) {
	hint := withCredentialPlan(nil, "pro")
	if hint == nil || hint.Snapshot.Plan.Name != "Pro 20x" || hint.State != "unavailable" || hint.ObservedAtMS != nil || len(hint.Snapshot.QuotaWindows) != 0 {
		t.Fatalf("unexpected hint: %+v", hint)
	}
	current := &CredentialObservationResponse{State: "success", Snapshot: &CredentialObservationSnapshot{Plan: ObservationPlanSummary{Name: "Plus"}}}
	if withCredentialPlan(current, "pro") != current {
		t.Fatal("token overwrote live observation")
	}
	if withCredentialPlan(nil, "") != nil {
		t.Fatal("missing plan fabricated data")
	}
	empty := &CredentialObservationResponse{State: "unavailable", Snapshot: &CredentialObservationSnapshot{}}
	result := withCredentialPlan(empty, "team")
	if result.Snapshot.Plan.Name != "Team" || empty.Snapshot.Plan.Name != "" {
		t.Fatal("shared observation mutated")
	}
}
