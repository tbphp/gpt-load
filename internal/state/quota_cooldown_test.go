package state

import (
	"testing"
	"time"
)

func TestQuotaRecoveryClearsBothLevelsAndPreservesOtherHealth(t *testing.T) {
	r, _, now := modelCooldownFixture(t)
	entry, _ := r.entryLocked(1)
	entry.QuotaCooldownUntil = now.Add(48 * time.Hour)
	entry.QuotaModelCooldowns = map[string]time.Time{"a": now.Add(48 * time.Hour), "b": now.Add(time.Hour)}
	entry.CooldownUntil = now.Add(time.Minute)
	entry.ModelCooldowns = map[string]time.Time{"a": now.Add(2 * time.Minute)}
	entry.Blacklisted, entry.FailureCount = true, 3
	entry.Status, entry.AuthState = CredentialStatusDisabled, CredentialAuthStateReauthorizationRequired
	if !r.RestoreQuotaCooldowns(1) {
		t.Fatal("quota recovery was not applied")
	}
	if !entry.QuotaCooldownUntil.IsZero() || len(entry.QuotaModelCooldowns) != 0 || entry.QuotaCooldownGeneration != 1 {
		t.Fatalf("quota cooldowns were retained: %#v", entry)
	}
	if !entry.CooldownUntil.Equal(now.Add(time.Minute)) || !entry.ModelCooldowns["a"].Equal(now.Add(2*time.Minute)) ||
		!entry.Blacklisted || entry.FailureCount != 3 || entry.Status != CredentialStatusDisabled || entry.AuthState != CredentialAuthStateReauthorizationRequired {
		t.Fatal("quota recovery changed unrelated health state")
	}
}

func TestQuotaRecoveryWithoutCooldownStillAdvancesGeneration(t *testing.T) {
	r, _, _ := modelCooldownFixture(t)
	if !r.RestoreQuotaCooldowns(1) {
		t.Fatal("recovery without existing cooldown was skipped")
	}
	entry, _ := r.entryLocked(1)
	if entry.QuotaCooldownGeneration != 1 {
		t.Fatal("in-flight quota failures were not invalidated")
	}
}

func TestObservedQuotaRecoveryPreservesRejectionAcrossRegistryRebuilds(t *testing.T) {
	r, _, now := modelCooldownFixture(t)
	ref, _ := r.CredentialRef(1)
	r.SetQuotaCooldown(ref, "", now.Add(time.Hour), now)
	r.SetQuotaCooldown(ref, "model", now.Add(time.Hour), now)
	entries := []CredentialEntry{{ID: 1, GroupID: 10, Version: 2, IdentityGeneration: 1,
		Status: CredentialStatusActive, AuthState: CredentialAuthStateReady,
		Fingerprint: "rotated", EncryptedValue: "rotated"}}
	for _, rebuild := range []func() error{
		func() error { _, err := r.ReconcileGroup(10, entries); return err },
		func() error { return r.ReplaceCredentials(entries) },
	} {
		if err := rebuild(); err != nil {
			t.Fatal(err)
		}
		if r.RestoreObservedQuotaCooldowns(1, now.UnixMilli()) {
			t.Fatal("registry rebuild lost the rejection time or accepted a same-millisecond sample")
		}
		view := r.Snapshot()[0]
		after, _ := r.CredentialRef(1)
		if view.CooldownUntil.IsZero() || view.ModelCooldowns["model"].IsZero() ||
			after.QuotaCooldownGeneration != ref.QuotaCooldownGeneration {
			t.Fatal("rejected recovery changed quota cooldown state")
		}
	}
	entries[0].IdentityGeneration = 2
	if _, err := r.ReconcileGroup(10, entries); err != nil {
		t.Fatal(err)
	}
	if !r.RestoreObservedQuotaCooldowns(1, now.UnixMilli()) {
		t.Fatal("new target inherited the old target's rejection time")
	}
}

func TestQuotaCooldownRecoveryPreservesCheckpointIdentityAndOtherCauses(t *testing.T) {
	r, _, now := modelCooldownFixture(t)
	before, _ := r.CredentialRef(1)
	r.SetQuotaCooldown(before, "", now.Add(time.Hour), now)
	r.SetQuotaCooldown(before, "a", now.Add(time.Hour), now)
	r.SetModelCooldown(before, "a", now.Add(time.Minute), now)
	checkpoint := r.CaptureRuntimeCheckpoint()
	r.RestoreQuotaCooldowns(1)
	if accepted, _ := r.SetQuotaCooldown(before, "", now.Add(time.Hour), now); accepted {
		t.Fatal("old account limit was restored")
	}
	if accepted, _ := r.SetQuotaCooldown(before, "a", now.Add(time.Hour), now); accepted {
		t.Fatal("old model limit was restored")
	}
	if accepted, _ := r.SetModelCooldown(before, "other", now.Add(time.Minute), now); !accepted {
		t.Fatal("quota recovery invalidated unrelated in-flight errors")
	}
	r.RestoreRuntimeCheckpoint(checkpoint)
	view := r.Snapshot()[0]
	if !view.CooldownUntil.Equal(now.Add(time.Hour)) || !view.ModelCooldowns["a"].Equal(now.Add(time.Hour)) {
		t.Fatal("checkpoint lost quota cooldowns")
	}
	r.RestoreQuotaCooldowns(1)
	view = r.Snapshot()[0]
	if !view.CooldownUntil.IsZero() || !view.ModelCooldowns["a"].Equal(now.Add(time.Minute)) {
		t.Fatal("quota recovery did not preserve the shorter non-quota cooldown")
	}
	if len(checkpoint[0].QuotaModelCooldowns) != 1 {
		t.Fatal("checkpoint shares live maps")
	}
	after, _ := r.CredentialRef(1)
	r.SetQuotaCooldown(after, "", now.Add(time.Hour), now)
	r.SetQuotaCooldown(after, "a", now.Add(time.Hour), now)
	r.RestoreRuntimeState(1)
	r.ClearModelCooldowns(1)
	view = r.Snapshot()[0]
	if !view.CooldownUntil.IsZero() || len(view.ModelCooldowns) != 0 {
		t.Fatal("explicit restoration left quota cooldowns")
	}
}
