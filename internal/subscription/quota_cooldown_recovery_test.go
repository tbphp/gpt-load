package subscription

import (
	"testing"
	"time"

	"gpt-load/internal/storage/models"
	providerobservation "gpt-load/internal/subscription/providers/observation"
)

func TestPassiveQuotaRecoveryClearsBothLevelsOnce(t *testing.T) {
	manager, _, registry, _, row := newCredentialManagerFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	newFlushableCredentialObservation(t, manager, row.ID, models.CredentialObservationFresh,
		`{"quota_windows":[{"id":"primary","source_id":"codex","scope":"account","unit":"percent","state":"exhausted","used":100,"window_seconds":604800}]}`)
	now := time.Now()
	ref, _ := registry.CredentialRef(row.ID)
	registry.SetQuotaCooldown(ref, "", now.Add(time.Hour), now)
	registry.SetQuotaCooldown(ref, "a", now.Add(time.Hour), now)
	period, used, utilization := int64(604800), 95.0, 0.95
	patches := []providerobservation.QuotaWindow{{ID: "secondary", SourceID: "codex", WindowSeconds: &period, State: "available", Used: &used, Utilization: &utilization}}
	manager.RecordPassiveQuotaObservation(row.ID, ref.IdentityGeneration, 2000, patches)
	if _, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil {
		t.Fatal(err)
	}
	view := registry.Snapshot()[0]
	if !view.CooldownUntil.IsZero() || len(view.ModelCooldowns) != 0 {
		t.Fatalf("cooldowns survived recovery: %#v", view)
	}
	after, _ := registry.CredentialRef(row.ID)
	registry.SetQuotaCooldown(after, "a", now.Add(time.Hour), now)
	manager.RecordPassiveQuotaObservation(row.ID, ref.IdentityGeneration, 3000, patches)
	if _, err := manager.FlushPassiveQuotaObservations(t.Context()); err != nil {
		t.Fatal(err)
	}
	latest, _ := registry.CredentialRef(row.ID)
	if latest.QuotaCooldownGeneration != after.QuotaCooldownGeneration || registry.Snapshot()[0].ModelCooldowns["a"].IsZero() {
		t.Fatal("same quota cleared cooldown again")
	}
	if accepted, _ := registry.SetQuotaCooldown(ref, "", now.Add(time.Hour), now); accepted {
		t.Fatal("old account rejection was accepted")
	}
}

func TestPassiveQuotaRecoveryComparesPersistedAndFinalSamples(t *testing.T) {
	for _, test := range []struct {
		name          string
		precedingUsed float64
		finalUsed     float64
		wantRecovered bool
	}{
		{name: "accumulated decrease", precedingUsed: 77, finalUsed: 74, wantRecovered: true},
		{name: "intermediate recovery consumed", precedingUsed: 20, finalUsed: 79, wantRecovered: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			storedAt, period := int64(1000), int64(18000)
			window := func(used float64) []providerobservation.QuotaWindow {
				utilization := used / 100
				return []providerobservation.QuotaWindow{{ID: "primary", SourceID: "codex", WindowSeconds: &period, Unit: "percent", State: "available", Used: &used, Utilization: &utilization}}
			}
			merged, observedAt, err := mergePassiveQuotaSamples(
				[]byte(`{"quota_windows":[{"id":"primary","source_id":"codex","scope":"account","unit":"percent","state":"available","used":80,"window_seconds":18000}]}`),
				&storedAt, PassiveQuotaObservation{
					Preceding:    &PassiveQuotaSample{ObservedAtMS: 2000, Windows: window(test.precedingUsed)},
					ObservedAtMS: 3000, Windows: window(test.finalUsed),
				})
			if err != nil {
				t.Fatal(err)
			}
			if !merged.Matched || !merged.Changed || observedAt != 3000 || len(merged.Windows) != 1 ||
				merged.Windows[0].Used == nil || *merged.Windows[0].Used != test.finalUsed {
				t.Fatalf("merged quota = %#v, observed at %d", merged, observedAt)
			}
			if merged.Recovered != test.wantRecovered {
				t.Fatalf("recovered = %t, want %t for 80 -> %g -> %g", merged.Recovered, test.wantRecovered, test.precedingUsed, test.finalUsed)
			}
		})
	}
}
