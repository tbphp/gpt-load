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
