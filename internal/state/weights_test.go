package state

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"gpt-load/internal/protocol"
)

func TestCompileCopiesAndValidatesGroupManualWeight(t *testing.T) {
	weight := 25
	input := CompileInput{Groups: []GroupConfig{{
		ID: 1, Name: "weighted", UpstreamURL: "https://weighted.example.com",
		Protocols: []protocol.Protocol{protocol.OpenAI},
		Models:    []ModelConfig{{ID: "gpt-weighted"}}, WeightManual: &weight, Enabled: true,
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
	registry := NewKeyRegistry()
	mustReplaceKeyEntries(t, registry, []KeyEntry{
		{ID: 1, GroupID: 10, Status: KeyStatusActive, EncryptedValue: "active"},
		{ID: 2, GroupID: 10, Status: KeyStatusActive, CooldownUntil: now.Add(time.Second), EncryptedValue: "cooling"},
		{ID: 3, GroupID: 10, Status: KeyStatusActive, Blacklisted: true, EncryptedValue: "blacklisted"},
		{ID: 4, GroupID: 10, Status: KeyStatusActive, CooldownUntil: now, EncryptedValue: "expired"},
		{ID: 5, GroupID: 10, Status: KeyStatusDisabled, EncryptedValue: "disabled"},
		{ID: 6, GroupID: 10, Status: KeyStatusActive, EncryptedValue: "excluded"},
	})

	got := registry.CollectCandidates([]uint{10}, func(keyID uint) bool {
		return keyID == 6
	}, now)
	want := []KeyMeta{
		{ID: 1, GroupID: 10, WeightAuto: DefaultWeight},
		{ID: 4, GroupID: 10, WeightAuto: DefaultWeight},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CollectCandidates() = %#v, want %#v", got, want)
	}
}

func TestKeyRuntimeViewClassifiesAvailability(t *testing.T) {
	now := time.Date(2026, time.July, 24, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		view KeyRuntimeView
		want KeyRuntimeState
	}{
		{
			name: "disabled wins",
			view: KeyRuntimeView{
				Status: KeyStatusDisabled, Blacklisted: true,
				CooldownUntil: now.Add(time.Minute),
			},
			want: KeyRuntimeDisabled,
		},
		{
			name: "blacklist wins cooldown",
			view: KeyRuntimeView{
				Status: KeyStatusActive, Blacklisted: true,
				CooldownUntil: now.Add(time.Minute),
			},
			want: KeyRuntimeBlacklisted,
		},
		{
			name: "future cooldown",
			view: KeyRuntimeView{
				Status: KeyStatusActive, CooldownUntil: now.Add(time.Nanosecond),
			},
			want: KeyRuntimeCooldown,
		},
		{
			name: "cooldown equality is available",
			view: KeyRuntimeView{
				Status: KeyStatusActive, CooldownUntil: now,
			},
			want: KeyRuntimeAvailable,
		},
		{
			name: "active",
			view: KeyRuntimeView{Status: KeyStatusActive},
			want: KeyRuntimeAvailable,
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
	registry := NewKeyRegistry()
	mustReplaceKeyEntries(t, registry, []KeyEntry{
		{ID: 1, GroupID: 10, Status: KeyStatusActive, EncryptedValue: "active"},
		{
			ID: 2, GroupID: 10, Status: KeyStatusActive,
			CooldownUntil: now.Add(time.Nanosecond), EncryptedValue: "cooling",
		},
		{ID: 3, GroupID: 10, Status: KeyStatusActive, Blacklisted: true, EncryptedValue: "blacklisted"},
		{ID: 4, GroupID: 10, Status: KeyStatusActive, CooldownUntil: now, EncryptedValue: "boundary"},
		{ID: 5, GroupID: 10, Status: KeyStatusDisabled, EncryptedValue: "disabled"},
	})
	views := registry.Snapshot()
	availableIDs := make([]uint, 0)
	for _, view := range views {
		if view.RuntimeState(now) == KeyRuntimeAvailable {
			availableIDs = append(availableIDs, view.ID)
		}
	}
	candidates := registry.CollectCandidates([]uint{10}, nil, now)
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
	registry := NewKeyRegistry()
	mustReplaceKeyEntries(t, registry, []KeyEntry{{
		ID: 1, GroupID: 10, Status: KeyStatusActive, EncryptedValue: "cipher",
	}})

	if ok := registry.SetCooldown(1, now.Add(time.Hour)); !ok {
		t.Fatal("SetCooldown(long deadline) = false, want true")
	}
	if ok := registry.SetCooldown(1, now.Add(time.Minute)); !ok {
		t.Fatal("SetCooldown(short deadline) = false, want true")
	}
	if got := registry.CollectCandidates([]uint{10}, nil, now.Add(2*time.Minute)); len(got) != 0 {
		t.Fatalf("CollectCandidates() before longest cooldown expires = %#v, want none", got)
	}
	if got := registry.CollectCandidates([]uint{10}, nil, now.Add(time.Hour)); len(got) != 1 {
		t.Fatalf("CollectCandidates() at longest cooldown boundary = %#v, want key 1", got)
	}
}

func TestKeyRegistrySetCooldownConcurrentWritersKeepLatestDeadline(t *testing.T) {
	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	registry := NewKeyRegistry()
	mustReplaceKeyEntries(t, registry, []KeyEntry{{
		ID: 1, GroupID: 10, Status: KeyStatusActive, EncryptedValue: "cipher",
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
	if got := registry.CollectCandidates([]uint{10}, nil, latest.Add(-time.Nanosecond)); len(got) != 0 {
		t.Fatalf("CollectCandidates() before latest cooldown expires = %#v, want none", got)
	}
	if got := registry.CollectCandidates([]uint{10}, nil, latest); len(got) != 1 {
		t.Fatalf("CollectCandidates() at latest cooldown boundary = %#v, want key 1", got)
	}
}

func TestKeyRegistryDefaultsAndSetsAutoWeight(t *testing.T) {
	registry := NewKeyRegistry()
	mustReplaceKeyEntries(t, registry, []KeyEntry{{
		ID: 1, GroupID: 10, Status: KeyStatusActive, EncryptedValue: "cipher",
	}})

	assertAutoWeight := func(want int) {
		t.Helper()
		got := registry.CollectCandidates([]uint{10}, nil, time.Time{})
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
	registry := NewKeyRegistry()
	mustReplaceKeyEntries(t, registry, []KeyEntry{{
		ID: 1, GroupID: 10, Status: KeyStatusActive, Blacklisted: true,
		FailureCount: 3, EncryptedValue: "cipher",
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
	registry := NewKeyRegistry()
	mustReplaceKeyEntries(t, registry, []KeyEntry{{
		ID: 1, GroupID: 10, Status: KeyStatusActive, Blacklisted: true,
		FailureCount: 3, WeightAuto: 17, EncryptedValue: "cipher-one",
	}})

	if ok := registry.RecoverIfMatch(KeyRef{ID: 1, GroupID: 10, EncryptedValue: "cipher-one"}, DefaultWeight); !ok {
		t.Fatal("RecoverIfMatch() = false, want true")
	}
	if got, want := registryEntry(t, registry, 1), (KeyEntry{
		ID: 1, GroupID: 10, Status: KeyStatusActive, WeightAuto: DefaultWeight, EncryptedValue: "cipher-one",
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("entry after RecoverIfMatch() = %#v, want %#v", got, want)
	}
}

func TestKeyRegistryRecoverIfMatchRejectsNonMatchingOrInvalidRecoveryWithoutMutation(t *testing.T) {
	tests := []struct {
		name   string
		entry  KeyEntry
		ref    KeyRef
		weight int
	}{
		{
			name: "disabled", entry: KeyEntry{ID: 1, GroupID: 10, Status: KeyStatusDisabled, Blacklisted: true, FailureCount: 3, WeightAuto: 17, EncryptedValue: "cipher-one"},
			ref: KeyRef{ID: 1, GroupID: 10, EncryptedValue: "cipher-one"}, weight: DefaultWeight,
		},
		{
			name: "not blacklisted", entry: KeyEntry{ID: 1, GroupID: 10, Status: KeyStatusActive, FailureCount: 3, WeightAuto: 17, EncryptedValue: "cipher-one"},
			ref: KeyRef{ID: 1, GroupID: 10, EncryptedValue: "cipher-one"}, weight: DefaultWeight,
		},
		{
			name: "group mismatch", entry: KeyEntry{ID: 1, GroupID: 10, Status: KeyStatusActive, Blacklisted: true, FailureCount: 3, WeightAuto: 17, EncryptedValue: "cipher-one"},
			ref: KeyRef{ID: 1, GroupID: 11, EncryptedValue: "cipher-one"}, weight: DefaultWeight,
		},
		{
			name: "cipher mismatch", entry: KeyEntry{ID: 1, GroupID: 10, Status: KeyStatusActive, Blacklisted: true, FailureCount: 3, WeightAuto: 17, EncryptedValue: "cipher-one"},
			ref: KeyRef{ID: 1, GroupID: 10, EncryptedValue: "cipher-replaced"}, weight: DefaultWeight,
		},
		{
			name: "missing", entry: KeyEntry{ID: 2, GroupID: 10, Status: KeyStatusActive, Blacklisted: true, FailureCount: 3, WeightAuto: 17, EncryptedValue: "cipher-two"},
			ref: KeyRef{ID: 1, GroupID: 10, EncryptedValue: "cipher-one"}, weight: DefaultWeight,
		},
		{
			name: "weight too low", entry: KeyEntry{ID: 1, GroupID: 10, Status: KeyStatusActive, Blacklisted: true, FailureCount: 3, WeightAuto: 17, EncryptedValue: "cipher-one"},
			ref: KeyRef{ID: 1, GroupID: 10, EncryptedValue: "cipher-one"}, weight: 0,
		},
		{
			name: "weight too high", entry: KeyEntry{ID: 1, GroupID: 10, Status: KeyStatusActive, Blacklisted: true, FailureCount: 3, WeightAuto: 17, EncryptedValue: "cipher-one"},
			ref: KeyRef{ID: 1, GroupID: 10, EncryptedValue: "cipher-one"}, weight: MaxWeight + 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := NewKeyRegistry()
			mustReplaceKeyEntries(t, registry, []KeyEntry{test.entry})
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
	registry := NewKeyRegistry()
	mustReplaceKeyEntries(t, registry, []KeyEntry{
		{ID: 3, GroupID: 20, Status: KeyStatusActive, Blacklisted: true, EncryptedValue: "cipher-three"},
		{ID: 2, GroupID: 10, Status: KeyStatusActive, Blacklisted: true, EncryptedValue: "cipher-two"},
		{ID: 1, GroupID: 10, Status: KeyStatusActive, Blacklisted: true, EncryptedValue: "cipher-one"},
		{ID: 4, GroupID: 10, Status: KeyStatusDisabled, Blacklisted: true, EncryptedValue: "cipher-disabled"},
		{ID: 5, GroupID: 10, Status: KeyStatusActive, EncryptedValue: "cipher-healthy"},
	})

	want := []KeyRef{
		{ID: 1, GroupID: 10, EncryptedValue: "cipher-one"},
		{ID: 2, GroupID: 10, EncryptedValue: "cipher-two"},
		{ID: 3, GroupID: 20, EncryptedValue: "cipher-three"},
	}
	if got := registry.BlacklistedKeys(); !reflect.DeepEqual(got, want) {
		t.Fatalf("BlacklistedKeys() = %#v, want %#v", got, want)
	}
}

func TestValidateKeyEntriesRejectsInvalidWeights(t *testing.T) {
	manualTooLow := -1
	manualTooHigh := MaxWeight + 1
	tests := []struct {
		name  string
		entry KeyEntry
	}{
		{name: "manual below range", entry: KeyEntry{WeightManual: &manualTooLow}},
		{name: "manual above range", entry: KeyEntry{WeightManual: &manualTooHigh}},
		{name: "auto below range", entry: KeyEntry{WeightAuto: -1}},
		{name: "auto above range", entry: KeyEntry{WeightAuto: MaxWeight + 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.entry.ID = 1
			test.entry.GroupID = 10
			test.entry.Status = KeyStatusActive
			test.entry.EncryptedValue = "cipher"
			if err := ValidateKeyEntries([]KeyEntry{test.entry}); err == nil {
				t.Fatal("ValidateKeyEntries() error = nil, want error")
			}
		})
	}
}

func registryEntry(t *testing.T, registry *KeyRegistry, keyID uint) KeyEntry {
	t.Helper()
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	groupID, ok := registry.keyGroups[keyID]
	if !ok {
		t.Fatalf("key %d missing", keyID)
	}
	return *registry.buckets[groupID][keyID]
}
