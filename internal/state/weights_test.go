package state

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"gpt-load/internal/channel"
)

func TestCompileCopiesAndValidatesGroupManualWeight(t *testing.T) {
	weight := 25
	input := CompileInput{ChannelRegistry: channel.NewRegistry(), Groups: []GroupConfig{{
		ID: 1, Name: "weighted", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
		Models: []ModelConfig{{ID: "gpt-weighted"}}, WeightManual: &weight, Enabled: true,
	}}}

	snapshot, err := Compile(input)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	weight = 90
	if got := snapshot.Groups[1].WeightManual; got == nil || *got != 25 {
		t.Fatalf("GroupView.WeightManual = %v, want independent value 25", got)
	}

	for _, invalid := range []int{-1, 101} {
		input.Groups[0].WeightManual = &invalid
		if _, err := Compile(input); err == nil {
			t.Errorf("Compile() with group weight %d error = nil, want error", invalid)
		}
	}
}

func TestKeyRegistryCollectCandidatesExcludesRuntimeUnavailableKeys(t *testing.T) {
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{
		{ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "active"},
		{ID: 2, GroupID: 10, Status: CredentialStatusActive, CooldownUntil: now.Add(time.Second), Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cooling"},
		{ID: 3, GroupID: 10, Status: CredentialStatusActive, Blacklisted: true, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "blacklisted"},
		{ID: 4, GroupID: 10, Status: CredentialStatusActive, CooldownUntil: now, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "expired"},
		{ID: 5, GroupID: 10, Status: CredentialStatusDisabled, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "disabled"},
		{ID: 6, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "excluded"},
	})

	got := registry.CollectCredentialCandidates([]uint{10}, func(keyID uint) bool {
		return keyID == 6
	}, now)
	want := []CredentialMeta{
		{ID: 1, GroupID: 10, Version: 1, IdentityGeneration: 1, WeightAuto: DefaultWeight},
		{ID: 4, GroupID: 10, Version: 1, IdentityGeneration: 1, WeightAuto: DefaultWeight},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CollectCandidates() = %#v, want %#v", got, want)
	}
}

func TestKeyRuntimeViewClassifiesAvailability(t *testing.T) {
	now := time.Date(2026, time.July, 24, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		view CredentialRuntimeView
		want CredentialRuntimeState
	}{
		{
			name: "disabled wins",
			view: CredentialRuntimeView{
				Status: CredentialStatusDisabled, Blacklisted: true,
				CooldownUntil: now.Add(time.Minute),
			},
			want: CredentialRuntimeDisabled,
		},
		{
			name: "blacklist wins cooldown",
			view: CredentialRuntimeView{
				Status: CredentialStatusActive, Blacklisted: true,
				CooldownUntil: now.Add(time.Minute),
			},
			want: CredentialRuntimeBlacklisted,
		},
		{
			name: "future cooldown",
			view: CredentialRuntimeView{
				Status: CredentialStatusActive, CooldownUntil: now.Add(time.Nanosecond),
			},
			want: CredentialRuntimeCooldown,
		},
		{
			name: "cooldown equality is available",
			view: CredentialRuntimeView{
				Status: CredentialStatusActive, CooldownUntil: now,
			},
			want: CredentialRuntimeAvailable,
		},
		{
			name: "active",
			view: CredentialRuntimeView{Status: CredentialStatusActive},
			want: CredentialRuntimeAvailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.view.RuntimeState(now); got != test.want {
				t.Fatalf("RuntimeState() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCollectCandidatesUsesRuntimeViewBoundary(t *testing.T) {
	now := time.Date(2026, time.July, 24, 10, 0, 0, 0, time.UTC)
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{
		{ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "active"},
		{
			ID: 2, GroupID: 10, Status: CredentialStatusActive,
			CooldownUntil: now.Add(time.Nanosecond), Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cooling",
		},
		{ID: 3, GroupID: 10, Status: CredentialStatusActive, Blacklisted: true, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "blacklisted"},
		{ID: 4, GroupID: 10, Status: CredentialStatusActive, CooldownUntil: now, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "boundary"},
		{ID: 5, GroupID: 10, Status: CredentialStatusDisabled, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "disabled"},
	})
	views := registry.Snapshot()
	availableIDs := make([]uint, 0)
	for _, view := range views {
		if view.RuntimeState(now) == CredentialRuntimeAvailable {
			availableIDs = append(availableIDs, view.ID)
		}
	}
	candidates := registry.CollectCredentialCandidates([]uint{10}, nil, now)
	candidateIDs := make([]uint, 0, len(candidates))
	for _, candidate := range candidates {
		candidateIDs = append(candidateIDs, candidate.ID)
	}
	if !reflect.DeepEqual(candidateIDs, availableIDs) {
		t.Fatalf("CollectCandidates IDs = %v, RuntimeView IDs = %v", candidateIDs, availableIDs)
	}
}

func TestKeyRegistrySetCooldownNeverShortensDeadline(t *testing.T) {
	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{{
		ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher",
	}})

	if ok := registry.SetCooldown(1, now.Add(time.Hour)); !ok {
		t.Fatal("SetCooldown(long deadline) = false, want true")
	}
	if ok := registry.SetCooldown(1, now.Add(time.Minute)); !ok {
		t.Fatal("SetCooldown(short deadline) = false, want true")
	}
	if got := registry.CollectCredentialCandidates([]uint{10}, nil, now.Add(2*time.Minute)); len(got) != 0 {
		t.Fatalf("CollectCandidates() before longest cooldown expires = %#v, want none", got)
	}
	if got := registry.CollectCredentialCandidates([]uint{10}, nil, now.Add(time.Hour)); len(got) != 1 {
		t.Fatalf("CollectCandidates() at longest cooldown boundary = %#v, want key 1", got)
	}
}

func TestKeyRegistrySetCooldownConcurrentWritersKeepLatestDeadline(t *testing.T) {
	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{{
		ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher",
	}})

	const writers = 32
	start := make(chan struct{})
	var wait sync.WaitGroup
	for offset := 1; offset <= writers; offset++ {
		deadline := now.Add(time.Duration(offset) * time.Minute)
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			if ok := registry.SetCooldown(1, deadline); !ok {
				t.Errorf("SetCooldown(%v) = false, want true", deadline)
			}
		}()
	}
	close(start)
	wait.Wait()

	latest := now.Add(writers * time.Minute)
	if got := registry.CollectCredentialCandidates([]uint{10}, nil, latest.Add(-time.Nanosecond)); len(got) != 0 {
		t.Fatalf("CollectCandidates() before latest cooldown expires = %#v, want none", got)
	}
	if got := registry.CollectCredentialCandidates([]uint{10}, nil, latest); len(got) != 1 {
		t.Fatalf("CollectCandidates() at latest cooldown boundary = %#v, want key 1", got)
	}
}

func TestKeyRegistryDefaultsAndSetsAutoWeight(t *testing.T) {
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{{
		ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher",
	}})

	assertAutoWeight := func(want int) {
		t.Helper()
		got := registry.CollectCredentialCandidates([]uint{10}, nil, time.Time{})
		if len(got) != 1 || got[0].WeightAuto != want {
			t.Fatalf("CollectCandidates() = %#v, want WeightAuto %d", got, want)
		}
	}
	assertAutoWeight(DefaultWeight)
	for _, weight := range []int{1, MaxWeight} {
		if ok := registry.SetAutoWeight(1, weight); !ok {
			t.Fatalf("SetAutoWeight(1, %d) = false, want true", weight)
		}
		assertAutoWeight(weight)
	}
	for _, invalid := range []int{0, MaxWeight + 1} {
		if ok := registry.SetAutoWeight(1, invalid); ok {
			t.Errorf("SetAutoWeight(1, %d) = true, want false", invalid)
		}
	}
	if ok := registry.SetAutoWeight(99, DefaultWeight); ok {
		t.Error("SetAutoWeight(missing key) = true, want false")
	}
}

func TestKeyRegistryClearFailureAndRecover(t *testing.T) {
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{{
		ID: 1, GroupID: 10, Status: CredentialStatusActive, Blacklisted: true,
		FailureCount: 3, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher",
	}})

	if ok := registry.ClearFailure(1); !ok {
		t.Fatal("ClearFailure(1) = false, want true")
	}
	entry := registryEntry(t, registry, 1)
	if entry.FailureCount != 0 || !entry.Blacklisted {
		t.Fatalf("entry after ClearFailure() = %#v, want zero failures and retained blacklist", entry)
	}

	if _, ok := registry.IncrFailure(1); !ok {
		t.Fatal("IncrFailure(1) = false, want true")
	}
	if ok := registry.Recover(1); !ok {
		t.Fatal("Recover(1) = false, want true")
	}
	entry = registryEntry(t, registry, 1)
	if entry.FailureCount != 0 || entry.Blacklisted {
		t.Fatalf("entry after Recover() = %#v, want zero failures and no blacklist", entry)
	}

	if registry.ClearFailure(99) || registry.Recover(99) {
		t.Error("mutation of missing key succeeded")
	}
}

func TestKeyRegistryRecoverIfMatchRestoresMatchingBlacklistedActiveKey(t *testing.T) {
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{{
		ID: 1, GroupID: 10, Status: CredentialStatusActive, Blacklisted: true,
		FailureCount: 3, WeightAuto: 17, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one",
	}})

	if ok := registry.RecoverIfMatch(CredentialRef{ID: 1, GroupID: 10, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one"}, DefaultWeight); !ok {
		t.Fatal("RecoverIfMatch() = false, want true")
	}
	if got, want := registryEntry(t, registry, 1), (CredentialEntry{
		ID: 1, GroupID: 10, Status: CredentialStatusActive, WeightAuto: DefaultWeight,
		FailureGeneration: 1, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one",
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("entry after RecoverIfMatch() = %#v, want %#v", got, want)
	}
}

func TestKeyRegistryRecoverIfMatchRejectsStaleGeneration(t *testing.T) {
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{{
		ID: 1, GroupID: 10, Status: CredentialStatusActive, Blacklisted: true,
		FailureCount: 3, WeightAuto: 17, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one",
	}})

	stale := registry.BlacklistedCredentials()[0]
	if stale.FailureGeneration != 0 {
		t.Fatalf("captured FailureGeneration = %d, want 0", stale.FailureGeneration)
	}
	if _, ok := registry.IncrFailure(1); !ok {
		t.Fatal("IncrFailure(1) = false, want true")
	}
	before := registryEntry(t, registry, 1)
	if ok := registry.RecoverIfMatch(stale, DefaultWeight); ok {
		t.Fatal("RecoverIfMatch(stale ref) = true, want false")
	}
	if got := registryEntry(t, registry, 1); !reflect.DeepEqual(got, before) {
		t.Fatalf("entry after stale RecoverIfMatch() = %#v, want unchanged %#v", got, before)
	}

	fresh := registry.BlacklistedCredentials()[0]
	if fresh.FailureGeneration != 1 {
		t.Fatalf("fresh FailureGeneration = %d, want 1", fresh.FailureGeneration)
	}
	if ok := registry.RecoverIfMatch(fresh, DefaultWeight); !ok {
		t.Fatal("RecoverIfMatch(fresh ref) = false, want true")
	}
	if got, want := registryEntry(t, registry, 1), (CredentialEntry{
		ID: 1, GroupID: 10, Status: CredentialStatusActive, WeightAuto: DefaultWeight,
		FailureGeneration: 2, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one",
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("entry after fresh RecoverIfMatch() = %#v, want %#v", got, want)
	}
}

func TestKeyRegistryRecoverIfMatchRejectsNonMatchingOrInvalidRecoveryWithoutMutation(t *testing.T) {
	tests := []struct {
		name   string
		entry  CredentialEntry
		ref    CredentialRef
		weight int
	}{
		{
			name: "disabled", entry: CredentialEntry{ID: 1, GroupID: 10, Status: CredentialStatusDisabled, Blacklisted: true, FailureCount: 3, WeightAuto: 17, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one"},
			ref: CredentialRef{ID: 1, GroupID: 10, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one"}, weight: DefaultWeight,
		},
		{
			name: "not blacklisted", entry: CredentialEntry{ID: 1, GroupID: 10, Status: CredentialStatusActive, FailureCount: 3, WeightAuto: 17, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one"},
			ref: CredentialRef{ID: 1, GroupID: 10, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one"}, weight: DefaultWeight,
		},
		{
			name: "group mismatch", entry: CredentialEntry{ID: 1, GroupID: 10, Status: CredentialStatusActive, Blacklisted: true, FailureCount: 3, WeightAuto: 17, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one"},
			ref: CredentialRef{ID: 1, GroupID: 11, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one"}, weight: DefaultWeight,
		},
		{
			name: "cipher mismatch", entry: CredentialEntry{ID: 1, GroupID: 10, Status: CredentialStatusActive, Blacklisted: true, FailureCount: 3, WeightAuto: 17, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one"},
			ref: CredentialRef{ID: 1, GroupID: 10, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-replaced"}, weight: DefaultWeight,
		},
		{
			name: "missing", entry: CredentialEntry{ID: 2, GroupID: 10, Status: CredentialStatusActive, Blacklisted: true, FailureCount: 3, WeightAuto: 17, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-two"},
			ref: CredentialRef{ID: 1, GroupID: 10, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one"}, weight: DefaultWeight,
		},
		{
			name: "weight too low", entry: CredentialEntry{ID: 1, GroupID: 10, Status: CredentialStatusActive, Blacklisted: true, FailureCount: 3, WeightAuto: 17, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one"},
			ref: CredentialRef{ID: 1, GroupID: 10, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one"}, weight: 0,
		},
		{
			name: "weight too high", entry: CredentialEntry{ID: 1, GroupID: 10, Status: CredentialStatusActive, Blacklisted: true, FailureCount: 3, WeightAuto: 17, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one"},
			ref: CredentialRef{ID: 1, GroupID: 10, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one"}, weight: MaxWeight + 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := NewCredentialRegistry()
			mustReplaceKeyEntries(t, registry, []CredentialEntry{test.entry})
			before := registryEntry(t, registry, test.entry.ID)

			if ok := registry.RecoverIfMatch(test.ref, test.weight); ok {
				t.Fatal("RecoverIfMatch() = true, want false")
			}
			if got := registryEntry(t, registry, test.entry.ID); !reflect.DeepEqual(got, before) {
				t.Fatalf("entry after rejected RecoverIfMatch() = %#v, want unchanged %#v", got, before)
			}
		})
	}
}

func TestKeyRegistryBlacklistedKeysReturnsActiveSortedRefs(t *testing.T) {
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{
		{ID: 3, GroupID: 20, Status: CredentialStatusActive, Blacklisted: true, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-three"},
		{ID: 2, GroupID: 10, Status: CredentialStatusActive, Blacklisted: true, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-two"},
		{ID: 1, GroupID: 10, Status: CredentialStatusActive, Blacklisted: true, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one"},
		{ID: 4, GroupID: 10, Status: CredentialStatusDisabled, Blacklisted: true, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-disabled"},
		{ID: 5, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-healthy"},
	})

	want := []CredentialRef{
		{ID: 1, GroupID: 10, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one"},
		{ID: 2, GroupID: 10, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-two"},
		{ID: 3, GroupID: 20, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-three"},
	}
	if got := registry.BlacklistedCredentials(); !reflect.DeepEqual(got, want) {
		t.Fatalf("BlacklistedCredentials() = %#v, want %#v", got, want)
	}
}

func TestValidateKeyEntriesRejectsInvalidWeights(t *testing.T) {
	manualTooLow := -1
	manualTooHigh := MaxWeight + 1
	tests := []struct {
		name  string
		entry CredentialEntry
	}{
		{name: "manual below range", entry: CredentialEntry{WeightManual: &manualTooLow}},
		{name: "manual above range", entry: CredentialEntry{WeightManual: &manualTooHigh}},
		{name: "auto below range", entry: CredentialEntry{WeightAuto: -1}},
		{name: "auto above range", entry: CredentialEntry{WeightAuto: MaxWeight + 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.entry.ID = 1
			test.entry.GroupID = 10
			test.entry.Status = CredentialStatusActive
			test.entry.EncryptedValue = "cipher"
			if err := ValidateCredentialEntries([]CredentialEntry{test.entry}); err == nil {
				t.Fatal("ValidateCredentialEntries() error = nil, want error")
			}
		})
	}
}

func registryEntry(t *testing.T, registry *CredentialRegistry, keyID uint) CredentialEntry {
	t.Helper()
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	groupID, ok := registry.credentialGroups[keyID]
	if !ok {
		t.Fatalf("key %d missing", keyID)
	}
	return *registry.buckets[groupID][keyID]
}
