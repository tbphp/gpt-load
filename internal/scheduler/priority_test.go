package scheduler

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func priorityFixture(t *testing.T, priorities ...int32) (*state.ConfigSnapshot, *state.CredentialRegistry) {
	t.Helper()
	input := state.CompileInput{ChannelRegistry: channel.NewRegistry()}
	entries := make([]state.CredentialEntry, 0, len(priorities))
	for index, priority := range priorities {
		id := uint(index + 1)
		input.Groups = append(input.Groups, state.GroupConfig{
			ID: id, Name: fmt.Sprintf("group-%d", id), ChannelID: channel.OpenAI,
			ConnectionType: "api_key", Params: json.RawMessage(`{}`), Enabled: true, Priority: priority,
			Models: []state.ModelConfig{{ID: "gpt-4o"}},
		})
		entries = append(entries, state.CredentialEntry{ID: id, GroupID: id, Version: 1, IdentityGeneration: 1,
			Status: state.CredentialStatusActive, Fingerprint: fmt.Sprint(id), EncryptedValue: "cipher"})
	}
	snapshot, err := state.Compile(input)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Revision = 1
	registry := state.NewCredentialRegistry()
	if err := registry.ReplaceCredentials(entries); err != nil {
		t.Fatal(err)
	}
	return snapshot, registry
}

func TestPriorityRetriesDescendAndStayAtLastTier(t *testing.T) {
	for _, test := range []struct {
		name            string
		priorities      []int32
		cooldown        uint
		skipFailedGroup bool
		want            []uint
	}{
		{"descend without exhausting peers", []int32{100, 100, 0, -100, -100}, 0, false, []uint{1, 3, 4, 5}},
		{"single tier retains retries", []int32{0, 0, 0}, 0, false, []uint{1, 2, 3}},
		{"group failure does not skip another tier", []int32{100, 0, -100}, 0, true, []uint{1, 2, 3}},
		{"unavailable middle tier", []int32{100, 0, -100}, 2, false, []uint{1, 3}},
		{"negative tiers", []int32{-1, -2, -2}, 0, false, []uint{1, 2, 3}},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot, registry := priorityFixture(t, test.priorities...)
			if test.cooldown != 0 {
				registry.SetCooldown(test.cooldown, time.Now().Add(time.Hour))
			}
			iterator := New(snapshot, registry, fairnessQuery(0))
			for _, want := range test.want {
				selected, err := iterator.Next()
				if err != nil || selected.CredentialID != want {
					t.Fatalf("selected %d, want %d, error=%v", selected.CredentialID, want, err)
				}
				if test.skipFailedGroup {
					iterator.SkipGroup(selected.GroupID)
				}
				iterator.AdvancePriority(selected)
			}
			if _, err := iterator.Next(); !errors.Is(err, ErrExhausted) {
				t.Fatalf("exhausted retry must not return to skipped high tiers: %v", err)
			}
		})
	}
}

func TestPriorityPreparationDoesNotAdvanceRetryTier(t *testing.T) {
	snapshot, registry := priorityFixture(t, 100, 100, 0)
	iterator := New(snapshot, registry, fairnessQuery(0))
	for _, want := range []uint{1, 2} {
		selection, err := iterator.Next()
		if err != nil || selection.CredentialID != want {
			t.Fatalf("preparation skip: selected %d, want %d, error=%v", selection.CredentialID, want, err)
		}
	}
}

func TestPriorityRetryDoesNotReturnToRecoveredHigherTier(t *testing.T) {
	snapshot, registry := priorityFixture(t, 100, 0, -100)
	until := time.Now().Add(time.Hour)
	registry.SetCooldown(1, until)
	iterator := New(snapshot, registry, fairnessQuery(0))
	selected, err := iterator.Next()
	if err != nil || selected.CredentialID != 2 {
		t.Fatalf("first selection %d, %v", selected.CredentialID, err)
	}
	registry.ClearCooldownIfMatch(1, until)
	iterator.AdvancePriority(selected)
	selected, err = iterator.Next()
	if err != nil || selected.CredentialID != 3 {
		t.Fatalf("retry selection %d, %v, want lower tier 3", selected.CredentialID, err)
	}
	if got := fairnessPick(t, snapshot, registry, 3); got != 1 {
		t.Fatalf("new request selected %d, want recovered high tier", got)
	}
}

func TestPriorityEqualTiersKeepWeightedDistribution(t *testing.T) {
	for _, priority := range []int32{0, 100, -100} {
		snapshot, registry := priorityFixture(t, priority, priority)
		for id, weight := range map[uint]int{1: 1, 2: 3} {
			group := snapshot.Groups[id]
			group.WeightManual = new(weight)
			snapshot.Groups[id] = group
		}
		counts := make(map[uint]int)
		for range 160 {
			counts[fairnessPick(t, snapshot, registry, 0)]++
		}
		if counts[1] != 40 || counts[2] != 120 {
			t.Fatalf("priority %d changed weighted allocation: %v", priority, counts)
		}
	}
}

func withPriorities(snapshot *state.ConfigSnapshot, priorities map[uint]int32) *state.ConfigSnapshot {
	next := *snapshot
	next.Revision++
	next.Groups = make(map[uint]state.GroupView, len(snapshot.Groups))
	next.GroupCatalog = make(map[uint]state.GroupCatalogView, len(snapshot.GroupCatalog))
	for id, group := range snapshot.Groups {
		if value, ok := priorities[id]; ok {
			group.Priority = value
		}
		next.Groups[id] = group
	}
	for id, group := range snapshot.GroupCatalog {
		if value, ok := priorities[id]; ok {
			group.Priority = value
		}
		next.GroupCatalog[id] = group
	}
	return &next
}

func TestPrioritySelectsHighestAvailableBeforeAffinity(t *testing.T) {
	snapshot, registry := priorityFixture(t, -100, -10, -50)
	if got := fairnessPick(t, snapshot, registry, 1); got != 2 {
		t.Fatalf("selected %d, want highest negative priority 2", got)
	}
	until := time.Now().Add(time.Hour)
	registry.SetCooldown(2, until)
	if got := fairnessPick(t, snapshot, registry, 1); got != 3 {
		t.Fatalf("selected %d, want available backup 3", got)
	}
	registry.ClearCooldownIfMatch(2, until)
	if got := fairnessPick(t, snapshot, registry, 3); got != 2 {
		t.Fatalf("recovered selection %d, want 2 despite backup affinity", got)
	}
	query := fairnessQuery(1)
	query.AllowedCredentialIDs = map[uint]struct{}{1: {}}
	selected, err := New(snapshot, registry, query).Next()
	if err != nil || selected.CredentialID != 1 {
		t.Fatalf("bound selection = %+v, %v", selected, err)
	}
}

func TestPriorityPrecedesNativePreferenceOnlyAcrossTiers(t *testing.T) {
	for _, strategy := range []string{"native_first", "weighted_mix"} {
		t.Run(strategy, func(t *testing.T) {
			snapshot := channelSchedulerSnapshot(t)
			setSnapshotRouteStrategy(t, snapshot, strategy)
			snapshot = withPriorities(snapshot, map[uint]int32{1: 100, 2: 0})
			source := fakeCredentialSource{keys: []state.CredentialMeta{{ID: 11, GroupID: 1}, {ID: 21, GroupID: 2}}}
			selected, err := New(snapshot, source, Query{ClientProtocol: protocol.OpenAICompletions,
				ExternalModel: modelPointer("public"), PreferredCredentialID: 21}).Next()
			if err != nil || selected.CredentialID != 11 || selected.RouteMode != channel.RouteConverted {
				t.Fatalf("priority selection = %+v, %v", selected, err)
			}
		})
	}
}

func TestPriorityChangeReentersAllMembers(t *testing.T) {
	for _, changes := range []map[uint]int32{{2: 100}, {1: 0}} {
		snapshot, registry := priorityFixture(t, 100, 0)
		ledger := registry.SchedulingState()
		ledger.SyncGroups(snapshot)
		ledger.WithLock(func(d *state.SchedulingLedger) {
			d.Members[1].Progress.Whole = 1000
			d.Watermark.Whole = 1000
			d.Sequence = 1000
		})
		next := withPriorities(snapshot, changes)
		ledger.SyncGroups(next)
		ledger.WithLock(func(d *state.SchedulingLedger) {
			for _, member := range d.Members {
				if !member.Pending {
					t.Fatal("priority change did not mark every member pending")
				}
			}
		})
		counts := make(map[uint]int)
		for range 100 {
			counts[fairnessPick(t, next, registry, 0)]++
		}
		if counts[1] != 50 || counts[2] != 50 {
			t.Fatalf("priority change caused catch-up burst: %v", counts)
		}
		ledger.SyncGroups(withPriorities(next, nil))
		ledger.WithLock(func(d *state.SchedulingLedger) {
			if d.Sequence != 1100 || d.Members[1].Pending || d.Members[2].Pending {
				t.Fatal("unchanged priorities reset history")
			}
		})
	}
}

func TestPriorityOldSnapshotCannotFinishReentry(t *testing.T) {
	snapshot, registry := priorityFixture(t, 100, 0, 0)
	query := fairnessQuery(0)
	query.AllowedCredentialIDs = map[uint]struct{}{2: {}, 3: {}}
	old := New(snapshot, registry, query)
	ledger := registry.SchedulingState()
	ledger.WithLock(func(d *state.SchedulingLedger) {
		d.Members[1].Progress.Whole, d.Watermark.Whole, d.Sequence = 1000, 1000, 1000
	})
	next := withPriorities(snapshot, map[uint]int32{3: 100})
	ledger.SyncGroups(next)
	query.AllowedCredentialIDs = map[uint]struct{}{2: {}}
	if _, err := New(next, registry, query).Next(); err != nil {
		t.Fatal(err)
	}
	query.AllowedCredentialIDs = map[uint]struct{}{1: {}}
	for range 1000 {
		if _, err := New(next, registry, query).Next(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := old.Next(); err != nil {
		t.Fatal(err)
	}
	ledger.WithLock(func(d *state.SchedulingLedger) {
		if !d.Members[3].Pending {
			t.Fatal("old snapshot cleared pending reentry against an obsolete pool")
		}
	})
	counts := make(map[uint]int)
	for range 100 {
		counts[fairnessPick(t, next, registry, 0)]++
	}
	if counts[1] != 50 || counts[3] != 50 {
		t.Fatalf("old snapshot caused catch-up burst: %v", counts)
	}
}
