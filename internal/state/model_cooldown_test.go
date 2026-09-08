package state

import (
	"sync"
	"testing"
	"time"
)

type modelCooldownRegistry interface {
	SetModelCooldown(CredentialRef, string, time.Time, time.Time) (bool, bool)
	ModelCooldowns(uint, time.Time) map[string]time.Time
	ClearModelCooldowns(uint) bool
}

func modelCooldownFixture(t *testing.T) (*CredentialRegistry, modelCooldownRegistry, time.Time) {
	t.Helper()
	r := NewCredentialRegistry()
	mustReplaceKeyEntries(t, r, []CredentialEntry{{ID: 1, GroupID: 10, Version: 1, IdentityGeneration: 1,
		Status: CredentialStatusActive, AuthState: CredentialAuthStateReady, Fingerprint: "fixture", EncryptedValue: "fixture"}})
	api, ok := any(r).(modelCooldownRegistry)
	if !ok {
		t.Fatal("registry does not support model-scoped cooldowns")
	}
	return r, api, time.Now().UTC().Truncate(time.Millisecond)
}

func TestModelCooldownMergesConcurrentLimitsWithoutAffectingAccount(t *testing.T) {
	r, api, now := modelCooldownFixture(t)
	ref, _ := r.CredentialRef(1)
	var wg sync.WaitGroup
	for i := 1; i <= 20; i++ {
		wg.Go(func() { api.SetModelCooldown(ref, "model-a", now.Add(time.Duration(i)*time.Minute), now) })
	}
	wg.Wait()
	r.ClearFailure(1)
	limits := api.ModelCooldowns(1, now)
	if len(limits) != 1 || !limits["model-a"].Equal(now.Add(20*time.Minute)) {
		t.Fatalf("limits = %v, want longest deadline for model-a", limits)
	}
	limits["model-a"] = now
	if !api.ModelCooldowns(1, now)["model-a"].After(now) {
		t.Fatal("caller mutated stored cooldown")
	}
	if r.Snapshot()[0].RuntimeState(now) != CredentialRuntimeAvailable || len(r.CollectCredentialCandidates([]uint{10}, nil, now)) != 1 {
		t.Fatal("model limit changed whole-account availability")
	}
	if len(api.ModelCooldowns(1, now.Add(20*time.Minute))) != 0 {
		t.Fatal("expired model limit remained active")
	}
}

func TestModelCooldownManualRecoveryRejectsEarlierInFlightResults(t *testing.T) {
	r, api, now := modelCooldownFixture(t)
	before, _ := r.CredentialRef(1)
	api.SetModelCooldown(before, "model-a", now.Add(time.Hour), now)
	api.SetModelCooldown(before, "model-b", now.Add(time.Hour), now)
	if !api.ClearModelCooldowns(1) || len(api.ModelCooldowns(1, now)) != 0 {
		t.Fatal("manual recovery did not clear all model limits")
	}
	if accepted, _ := api.SetModelCooldown(before, "model-a", now.Add(time.Hour), now); accepted {
		t.Fatal("pre-recovery request restored stale cooldown")
	}
	after, _ := r.CredentialRef(1)
	if accepted, _ := api.SetModelCooldown(after, "model-b", now.Add(time.Hour), now); !accepted {
		t.Fatal("post-recovery request could not record new limit")
	}
}

func TestModelCooldownSurvivesIdentityPreservingRebuilds(t *testing.T) {
	r, api, now := modelCooldownFixture(t)
	before, _ := r.CredentialRef(1)
	api.SetModelCooldown(before, "model-a", now.Add(time.Hour), now)
	if !r.ReplaceCredentialSecretIfMatch(1, 1, 2, "rotated", "rotated") {
		t.Fatal("rotate")
	}
	entries := []CredentialEntry{{ID: 1, GroupID: 10, Version: 3, IdentityGeneration: 1,
		Status: CredentialStatusDisabled, Fingerprint: "new-secret", EncryptedValue: "new-secret"}}
	if _, err := r.ReconcileGroup(10, entries); err != nil {
		t.Fatal(err)
	}
	if len(api.ModelCooldowns(1, now)) != 1 {
		t.Fatal("same account reconciliation lost cooldown")
	}
	if err := r.ReplaceCredentials(entries); err != nil {
		t.Fatal(err)
	}
	if len(api.ModelCooldowns(1, now)) != 1 {
		t.Fatal("same account reload lost cooldown")
	}
	entries[0].IdentityGeneration = 2
	if _, err := r.ReconcileGroup(10, entries); err != nil {
		t.Fatal(err)
	}
	if len(api.ModelCooldowns(1, now)) != 0 {
		t.Fatal("new target inherited old model cooldown")
	}
	if accepted, _ := api.SetModelCooldown(before, "model-a", now.Add(time.Hour), now); accepted {
		t.Fatal("old target accepted")
	}
}

func TestModelCooldownCheckpointAndBatchSnapshotsAreDetached(t *testing.T) {
	r, api, now := modelCooldownFixture(t)
	ref, _ := r.CredentialRef(1)
	api.SetModelCooldown(ref, "model-a", now.Add(time.Hour), now)
	checkpoint := r.CaptureRuntimeCheckpoint()
	entries, err := r.SnapshotGroupCredentialEntriesExact(10, []uint{1})
	if err != nil {
		t.Fatal(err)
	}
	api.SetModelCooldown(ref, "model-b", now.Add(time.Hour), now)
	if err := r.RestoreGroupCredentialEntriesExact(10, entries); err != nil {
		t.Fatal(err)
	}
	if len(api.ModelCooldowns(1, now)) != 1 {
		t.Fatal("batch snapshot shared mutable model map")
	}
	api.ClearModelCooldowns(1)
	r.RestoreRuntimeCheckpoint(checkpoint)
	if len(api.ModelCooldowns(1, now)) != 1 {
		t.Fatal("checkpoint lost cooldown")
	}
	entries[0].IdentityGeneration = 2
	if _, err := r.ReconcileGroup(10, entries); err != nil {
		t.Fatal(err)
	}
	r.RestoreRuntimeCheckpoint(checkpoint)
	if len(api.ModelCooldowns(1, now)) != 0 {
		t.Fatal("checkpoint restored wrong target cooldown")
	}
}
