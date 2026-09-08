package scheduler

import (
	"errors"
	"sync"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestIteratorSharesProgressAcrossRequestsAndCandidateSets(t *testing.T) {
	registry := state.NewCredentialRegistry()
	entries := make([]state.CredentialEntry, 0, 3)
	for _, id := range []uint{11, 12, 13} {
		entries = append(entries, state.CredentialEntry{
			ID: id, GroupID: 1, Version: 1, IdentityGeneration: 1,
			Status: state.CredentialStatusActive, Fingerprint: "fixture", EncryptedValue: "cipher",
		})
	}
	if err := registry.ReplaceCredentials(entries); err != nil {
		t.Fatal(err)
	}
	snapshot := schedulerSnapshot()
	counts := make(map[uint]int)
	for request := range 600 {
		allowed := map[uint]struct{}{11: {}, 12: {}}
		if request%2 != 0 {
			allowed = map[uint]struct{}{12: {}, 13: {}}
		}
		iterator := New(snapshot, registry, Query{
			ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion,
			ExternalModel: modelPointer("gpt-4o"), AllowedCredentialIDs: allowed,
		})
		selected, err := iterator.Next()
		if err != nil {
			t.Fatal(err)
		}
		counts[selected.CredentialID]++
	}
	for _, id := range []uint{11, 12, 13} {
		if counts[id] != 200 {
			t.Fatalf("shared distribution = %v, want 200 allocations per credential", counts)
		}
	}
}

func newFairnessRegistry(t *testing.T) *state.CredentialRegistry {
	t.Helper()
	r := state.NewCredentialRegistry()
	for _, id := range []uint{11, 12} {
		if err := r.ApplyCredentialImport(1, []state.CredentialEntry{{
			ID: id, GroupID: 1, Version: 1, IdentityGeneration: 1,
			Status: state.CredentialStatusActive, Fingerprint: "fixture", EncryptedValue: "cipher",
		}}); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func fairnessQuery(preferred uint) Query {
	return Query{ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion,
		ExternalModel: modelPointer("gpt-4o"), PreferredCredentialID: preferred}
}

func fairnessPick(t *testing.T, snapshot *state.ConfigSnapshot, registry *state.CredentialRegistry, preferred uint) uint {
	t.Helper()
	selection, err := New(snapshot, registry, fairnessQuery(preferred)).Next()
	if err != nil {
		t.Fatal(err)
	}
	return selection.CredentialID
}

func TestFairnessLimitsContinuousAllocationsWithoutDiscardingHistory(t *testing.T) {
	r := newFairnessRegistry(t)
	snapshot := schedulerSnapshot()
	for range 1000 {
		if got := fairnessPick(t, snapshot, r, 11); got != 11 {
			t.Fatalf("affinity selected %d", got)
		}
	}
	for range 100 {
		if got := fairnessPick(t, snapshot, r, 0); got != 12 {
			t.Fatalf("catchup selected %d", got)
		}
	}
	if got := fairnessPick(t, snapshot, r, 0); got != 11 {
		t.Fatalf("continuous limit selected %d, want temporary alternative", got)
	}
	if got := fairnessPick(t, snapshot, r, 0); got != 12 {
		t.Fatalf("normal selection resumed with %d, want lagging member", got)
	}
	r.SchedulingState().WithLock(func(d *state.SchedulingLedger) {
		if d.Sequence != 1102 || d.Members[11].Progress.Compare(d.Members[12].Progress) <= 0 {
			t.Fatalf("history discarded or allocation missed: sequence=%d", d.Sequence)
		}
	})
}

func TestFairnessLifecycleRetainsOrCalibratesProgress(t *testing.T) {
	for _, test := range []struct {
		name      string
		change    func(*testing.T, *state.CredentialRegistry)
		calibrate bool
	}{
		{"normal refresh", func(t *testing.T, r *state.CredentialRegistry) {
			r.SetCredentialAuthState(12, state.CredentialAuthStateRefreshing)
			r.SetCredentialAuthState(12, state.CredentialAuthStateReady)
			if !r.ReplaceCredentialSecretIfMatch(12, 1, 2, "refreshed", "cipher-new") {
				t.Fatal("refresh failed")
			}
		}, false},
		{"disable and enable", func(t *testing.T, r *state.CredentialRegistry) {
			if err := r.SetCredentialStatus(12, state.CredentialStatusDisabled); err != nil {
				t.Fatal(err)
			}
			if err := r.SetCredentialStatus(12, state.CredentialStatusActive); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"zero weight", func(t *testing.T, r *state.CredentialRegistry) {
			zero := 0
			if err := r.UpdateCredentialConfig(12, state.CredentialStatusActive, &zero); err != nil {
				t.Fatal(err)
			}
			if err := r.UpdateCredentialConfig(12, state.CredentialStatusActive, nil); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"blacklist", func(_ *testing.T, r *state.CredentialRegistry) { r.SetBlacklisted(12); r.Recover(12) }, true},
		{"cooldown", func(t *testing.T, r *state.CredentialRegistry) {
			until := time.Now().Add(time.Hour)
			r.SetCooldown(12, until)
			if !r.ClearCooldownIfMatch(12, until) {
				t.Fatal("clear cooldown failed")
			}
		}, true},
		{"identity replacement", func(t *testing.T, r *state.CredentialRegistry) {
			if err := r.ApplyCredentialImport(1, []state.CredentialEntry{{ID: 12, GroupID: 1, Version: 2, IdentityGeneration: 2,
				Status: state.CredentialStatusActive, Fingerprint: "new-account", EncryptedValue: "cipher-new"}}); err != nil {
				t.Fatal(err)
			}
		}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := newFairnessRegistry(t)
			snapshot := schedulerSnapshot()
			for range 1000 {
				fairnessPick(t, snapshot, r, 11)
			}
			test.change(t, r)
			if got := fairnessPick(t, snapshot, r, 0); got != 12 {
				t.Fatalf("first=%d", got)
			}
			want := uint(12)
			if test.calibrate {
				want = 11
			}
			if got := fairnessPick(t, snapshot, r, 0); got != want {
				t.Fatalf("second=%d want=%d", got, want)
			}
		})
	}
}

func TestFairnessDoesNotRestoreDeletedIdentityFromOldRequest(t *testing.T) {
	r := newFairnessRegistry(t)
	snapshot := schedulerSnapshot()
	ref, _ := r.CredentialRef(12)
	query := fairnessQuery(0)
	query.AllowedCredentialRefs = map[uint]state.CredentialRef{12: ref}
	iterator := New(snapshot, r, query)
	r.RemoveCredential(12)
	if err := r.ApplyCredentialImport(1, []state.CredentialEntry{{ID: 12, GroupID: 1, Version: 2, IdentityGeneration: 2,
		Status: state.CredentialStatusActive, Fingerprint: "new-account", EncryptedValue: "cipher-new"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := iterator.Next(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("old identity selected: %v", err)
	}
	r.SchedulingState().WithLock(func(d *state.SchedulingLedger) {
		if d.Sequence != 0 || d.Members[12].IdentityGeneration != 2 {
			t.Fatal("old request modified new member")
		}
	})
}

func TestFairnessConcurrentAllocationsAreAtomic(t *testing.T) {
	r := newFairnessRegistry(t)
	snapshot := schedulerSnapshot()
	results := make(chan uint, 600)
	var workers sync.WaitGroup
	for range 12 {
		workers.Go(func() {
			for range 50 {
				selection, err := New(snapshot, r, fairnessQuery(0)).Next()
				if err != nil {
					t.Error(err)
					return
				}
				results <- selection.CredentialID
			}
		})
	}
	workers.Wait()
	close(results)
	counts := map[uint]int{}
	for id := range results {
		counts[id]++
	}
	if counts[11] != 300 || counts[12] != 300 {
		t.Fatalf("concurrent distribution=%v", counts)
	}
}

func TestFairnessNewBatchUsesOneExistingBaseline(t *testing.T) {
	r := newFairnessRegistry(t)
	r.RemoveCredential(12)
	snapshot := schedulerSnapshot()
	for range 1000 {
		fairnessPick(t, snapshot, r, 0)
	}
	for _, id := range []uint{12, 13} {
		if err := r.ApplyCredentialImport(1, []state.CredentialEntry{{ID: id, GroupID: 1, Version: 1, IdentityGeneration: 1,
			Status: state.CredentialStatusActive, Fingerprint: "fixture", EncryptedValue: "cipher"}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []uint{12, 13, 11} {
		if got := fairnessPick(t, snapshot, r, 0); got != want {
			t.Fatalf("batch join selected %d, want %d", got, want)
		}
	}
	var watermark state.SchedulingProgress
	r.SchedulingState().WithLock(func(d *state.SchedulingLedger) { watermark = d.Watermark })
	for _, id := range []uint{11, 12, 13} {
		r.RemoveCredential(id)
	}
	if err := r.ApplyCredentialImport(1, []state.CredentialEntry{{ID: 14, GroupID: 1, Version: 1, IdentityGeneration: 1,
		Status: state.CredentialStatusActive, Fingerprint: "fixture", EncryptedValue: "cipher"}}); err != nil {
		t.Fatal(err)
	}
	fairnessPick(t, snapshot, r, 0)
	r.SchedulingState().WithLock(func(d *state.SchedulingLedger) {
		if d.Members[14].Progress.Compare(watermark) <= 0 {
			t.Fatal("all-new pool fell back to zero")
		}
	})
}

func TestFairnessNewMemberDoesNotInheritExistingCatchupDebt(t *testing.T) {
	r := newFairnessRegistry(t)
	snapshot := schedulerSnapshot()
	for range 1000 {
		fairnessPick(t, snapshot, r, 11)
	}
	if err := r.ApplyCredentialImport(1, []state.CredentialEntry{{ID: 13, GroupID: 1, Version: 1, IdentityGeneration: 1,
		Status: state.CredentialStatusActive, Fingerprint: "new-member", EncryptedValue: "cipher"}}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if got := fairnessPick(t, snapshot, r, 0); got != 12 {
			t.Fatalf("new member inherited old catchup debt: selected %d, want existing lagging member 12", got)
		}
	}
	r.SchedulingState().WithLock(func(d *state.SchedulingLedger) {
		if d.Members[13].Progress != d.Members[11].Progress {
			t.Fatal("new member did not join at the existing frontier")
		}
	})
}

func TestFairnessGroupToggleIsObservedBetweenRequests(t *testing.T) {
	r := newFairnessRegistry(t)
	manager := state.NewManager()
	manager.SetSchedulingState(r.SchedulingState())
	publish := func(enabled bool) *state.ConfigSnapshot {
		snapshot, err := manager.Publish(state.CompileInput{ChannelRegistry: channel.NewRegistry(), Groups: []state.GroupConfig{{
			ID: 1, Name: "fixture", ChannelID: channel.OpenAI, ConnectionType: "api_key", Enabled: enabled,
			Models: []state.ModelConfig{{ID: "gpt-4o"}},
		}}})
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	snapshot := publish(true)
	for range 1000 {
		fairnessPick(t, snapshot, r, 11)
	}
	publish(false)
	snapshot = publish(true)
	for _, want := range []uint{12, 11} {
		if got := fairnessPick(t, snapshot, r, 0); got != want {
			t.Fatalf("group recovery selected %d want %d", got, want)
		}
	}
}

func TestFairnessInitiallyDisabledGroupJoinsCurrentProgress(t *testing.T) {
	registry := state.NewCredentialRegistry()
	manager := state.NewManager()
	manager.SetSchedulingState(registry.SchedulingState())
	publish := func(enabled bool) *state.ConfigSnapshot {
		snapshot, err := manager.Publish(state.CompileInput{ChannelRegistry: channel.NewRegistry(), Groups: []state.GroupConfig{
			{ID: 1, Name: "active", ChannelID: channel.OpenAI, ConnectionType: "api_key", Enabled: true,
				Models: []state.ModelConfig{{ID: "gpt-4o"}}},
			{ID: 2, Name: "initially-disabled", ChannelID: channel.OpenAI, ConnectionType: "api_key", Enabled: enabled,
				Models: []state.ModelConfig{{ID: "gpt-4o"}}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	// 与启动加载顺序一致：先发布分组，再加载凭据。
	snapshot := publish(false)
	if err := registry.ReplaceCredentials([]state.CredentialEntry{
		{ID: 11, GroupID: 1, Version: 1, IdentityGeneration: 1, Status: state.CredentialStatusActive,
			Fingerprint: "fixture-1", EncryptedValue: "cipher"},
		{ID: 12, GroupID: 2, Version: 1, IdentityGeneration: 1, Status: state.CredentialStatusActive,
			Fingerprint: "fixture-2", EncryptedValue: "cipher"},
	}); err != nil {
		t.Fatal(err)
	}
	for range 1000 {
		fairnessPick(t, snapshot, registry, 0)
	}
	snapshot = publish(true)
	counts := map[uint]int{}
	for range 101 {
		counts[fairnessPick(t, snapshot, registry, 0)]++
	}
	if counts[11] != 50 || counts[12] != 51 {
		t.Fatalf("group activation distribution = %v, want 50/51 without historical catchup", counts)
	}
}

func TestFairnessHeavyWeightIsNotLimitedToOneHundred(t *testing.T) {
	r := newFairnessRegistry(t)
	snapshot := schedulerSnapshot()
	heavy, light := 100, 1
	group := snapshot.Groups[1]
	group.WeightManual = &heavy
	snapshot.Groups[1] = group
	group = snapshot.Groups[2]
	group.WeightManual = &light
	snapshot.Groups[2] = group
	if err := r.UpdateCredentialConfig(11, state.CredentialStatusActive, &heavy); err != nil {
		t.Fatal(err)
	}
	r.RemoveCredential(12)
	if err := r.ApplyCredentialImport(2, []state.CredentialEntry{{ID: 12, GroupID: 2, Version: 1, IdentityGeneration: 1,
		Status: state.CredentialStatusActive, WeightManual: &light, Fingerprint: "fixture", EncryptedValue: "cipher"}}); err != nil {
		t.Fatal(err)
	}
	counts := map[uint]int{}
	for range 20002 {
		counts[fairnessPick(t, snapshot, r, 0)]++
	}
	if counts[12] < 1 || counts[12] > 3 {
		t.Fatalf("10000:1 weights distorted by streak limit: %v", counts)
	}
}

func TestFairnessChargesRetriesButKeepsTriedRequestLocal(t *testing.T) {
	r := newFairnessRegistry(t)
	snapshot := schedulerSnapshot()
	// 模拟首个候选失败、第二个成功，且健康机制未触发跨请求暂退。
	for range 10 {
		iterator := New(snapshot, r, fairnessQuery(0))
		for _, want := range []uint{11, 12} {
			selection, err := iterator.Next()
			if err != nil || selection.CredentialID != want {
				t.Fatalf("retry selection=%d err=%v, want=%d", selection.CredentialID, err, want)
			}
		}
		if _, err := iterator.Next(); !errors.Is(err, ErrExhausted) {
			t.Fatalf("same request retried a credential again: %v", err)
		}
	}
	r.SchedulingState().WithLock(func(d *state.SchedulingLedger) {
		if d.Sequence != 20 || d.Members[11].Progress != d.Members[12].Progress {
			t.Fatal("attempt accounting lost retries")
		}
	})
}
