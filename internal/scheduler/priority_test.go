package scheduler

import (
	"errors"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestFilterHighestPriorityPrefersGroupThenCredential(t *testing.T) {
	t.Parallel()
	high, mid, low := 90, 50, 10
	got := filterHighestPriority([]weightedCredential{
		{meta: state.CredentialMeta{ID: 1}, groupPriority: mid, credentialPriority: high, weight: 1},
		{meta: state.CredentialMeta{ID: 2}, groupPriority: high, credentialPriority: low, weight: 1},
		{meta: state.CredentialMeta{ID: 3}, groupPriority: high, credentialPriority: mid, weight: 1},
		{meta: state.CredentialMeta{ID: 4}, groupPriority: high, credentialPriority: mid, weight: 2},
	})
	if len(got) != 2 || got[0].meta.ID != 3 || got[1].meta.ID != 4 {
		t.Fatalf("filterHighestPriority() = %#v, want credentials 3 and 4", got)
	}
}

func TestIteratorPrefersHigherGroupPriority(t *testing.T) {
	t.Parallel()
	snapshot := schedulerSnapshot()
	high, low := 80, 20
	group := snapshot.Groups[1]
	group.PriorityManual = &low
	snapshot.Groups[1] = group
	group = snapshot.Groups[2]
	group.PriorityManual = &high
	snapshot.Groups[2] = group

	source := fakeCredentialSource{keys: []state.CredentialMeta{
		{ID: 11, GroupID: 1, WeightManual: new(100)},
		{ID: 21, GroupID: 2, WeightManual: new(1)},
	}}
	source.progress = state.NewSchedulingState()
	for range 50 {
		selection, err := New(snapshot, source, Query{
			ClientProtocol: protocol.OpenAICompletions,
			Operation:      execution.OperationChatCompletion,
			ExternalModel:  modelPointer("gpt-4o"),
		}).Next()
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		if selection.CredentialID != 21 || selection.GroupID != 2 {
			t.Fatalf("selection = %#v, want higher-priority group credential 21", selection)
		}
	}
}

func TestIteratorFallsBackToLowerPriorityAfterTried(t *testing.T) {
	t.Parallel()
	snapshot := schedulerSnapshot()
	high, low := 80, 20
	group := snapshot.Groups[1]
	group.PriorityManual = &high
	snapshot.Groups[1] = group
	group = snapshot.Groups[2]
	group.PriorityManual = &low
	snapshot.Groups[2] = group

	source := fakeCredentialSource{keys: []state.CredentialMeta{
		{ID: 11, GroupID: 1},
		{ID: 21, GroupID: 2},
	}}
	source.progress = state.NewSchedulingState()
	iterator := New(snapshot, source, Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  modelPointer("gpt-4o"),
	})
	first, err := iterator.Next()
	if err != nil || first.CredentialID != 11 {
		t.Fatalf("first Next() = (%#v, %v), want credential 11", first, err)
	}
	second, err := iterator.Next()
	if err != nil || second.CredentialID != 21 {
		t.Fatalf("second Next() = (%#v, %v), want fallback credential 21", second, err)
	}
	if _, err := iterator.Next(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("third Next() error = %v, want ErrExhausted", err)
	}
}

func TestIteratorPrefersHigherCredentialPriorityWithinGroupTier(t *testing.T) {
	t.Parallel()
	high, low := 90, 10
	source := fakeCredentialSource{keys: []state.CredentialMeta{
		{ID: 11, GroupID: 1, PriorityManual: &low, WeightManual: new(100)},
		{ID: 12, GroupID: 1, PriorityManual: &high, WeightManual: new(1)},
	}}
	source.progress = state.NewSchedulingState()
	for range 40 {
		selection, err := New(schedulerSnapshot(), source, Query{
			ClientProtocol: protocol.OpenAICompletions,
			Operation:      execution.OperationChatCompletion,
			ExternalModel:  modelPointer("gpt-4o"),
		}).Next()
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		if selection.CredentialID != 12 {
			t.Fatalf("selection = %#v, want higher-priority credential 12", selection)
		}
	}
}

func TestIteratorPreferredCredentialDoesNotCrossPriorityTier(t *testing.T) {
	t.Parallel()
	snapshot := schedulerSnapshot()
	high, low := 80, 20
	group := snapshot.Groups[1]
	group.PriorityManual = &high
	snapshot.Groups[1] = group
	group = snapshot.Groups[2]
	group.PriorityManual = &low
	snapshot.Groups[2] = group

	source := fakeCredentialSource{keys: []state.CredentialMeta{
		{ID: 11, GroupID: 1},
		{ID: 21, GroupID: 2},
	}}
	source.progress = state.NewSchedulingState()
	selection, err := New(snapshot, source, Query{
		ClientProtocol:        protocol.OpenAICompletions,
		Operation:             execution.OperationChatCompletion,
		ExternalModel:         modelPointer("gpt-4o"),
		PreferredCredentialID: 21,
	}).Next()
	if err != nil || selection.CredentialID != 11 {
		t.Fatalf("Next() = (%#v, %v), want high-priority credential 11 over preferred 21", selection, err)
	}
}

func TestIteratorSamePriorityStillUsesWeights(t *testing.T) {
	t.Parallel()
	priority := 70
	snapshot := schedulerSnapshot()
	for _, groupID := range []uint{1, 2} {
		group := snapshot.Groups[groupID]
		group.PriorityManual = &priority
		snapshot.Groups[groupID] = group
	}
	group := snapshot.Groups[1]
	heavy := 100
	group.WeightManual = &heavy
	snapshot.Groups[1] = group
	group = snapshot.Groups[2]
	light := 50
	group.WeightManual = &light
	snapshot.Groups[2] = group

	source := fakeCredentialSource{keys: []state.CredentialMeta{
		{ID: 11, GroupID: 1, WeightManual: new(100)},
		{ID: 21, GroupID: 2, WeightManual: new(100)},
	}}
	source.progress = state.NewSchedulingState()
	counts := map[uint]int{}
	for range 12000 {
		selection, err := New(snapshot, source, Query{
			ClientProtocol: protocol.OpenAICompletions,
			Operation:      execution.OperationChatCompletion,
			ExternalModel:  modelPointer("gpt-4o"),
		}).Next()
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		counts[selection.CredentialID]++
	}
	ratio := float64(counts[11]) / float64(counts[21])
	if ratio < 1.85 || ratio > 2.15 {
		t.Fatalf("same-priority weighted counts = %#v, ratio = %.3f, want about 2:1", counts, ratio)
	}
}
