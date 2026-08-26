package scheduler

import (
	"encoding/json"
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func modelPointer(value string) *string {
	return &value
}

func TestFilterTargetsAppliesAccessKeyDimensions(t *testing.T) {
	snapshot := schedulerSnapshot()
	tests := []struct {
		name       string
		protocol   protocol.Protocol
		model      string
		filters    state.FilterSet
		wantGroups []uint
	}{
		{name: "unrestricted", protocol: protocol.OpenAICompletions, model: "gpt-4o", wantGroups: []uint{1, 2}},
		{
			name:       "group filter",
			protocol:   protocol.OpenAICompletions,
			model:      "gpt-4o",
			filters:    state.FilterSet{Groups: map[uint]struct{}{2: {}}},
			wantGroups: []uint{2},
		},
		{
			name:       "protocol allowed",
			protocol:   protocol.OpenAICompletions,
			model:      "gpt-4o",
			filters:    state.FilterSet{Protocols: map[protocol.Protocol]struct{}{protocol.OpenAICompletions: {}}},
			wantGroups: []uint{1, 2},
		},
		{
			name:       "protocol denied",
			protocol:   protocol.OpenAICompletions,
			model:      "gpt-4o",
			filters:    state.FilterSet{Protocols: map[protocol.Protocol]struct{}{protocol.Anthropic: {}}},
			wantGroups: []uint{},
		},
		{
			name:       "model allowed",
			protocol:   protocol.OpenAICompletions,
			model:      "gpt-4o",
			filters:    state.FilterSet{Models: map[string]struct{}{"gpt-4o": {}}},
			wantGroups: []uint{1, 2},
		},
		{
			name:       "model denied",
			protocol:   protocol.OpenAICompletions,
			model:      "gpt-4o",
			filters:    state.FilterSet{Models: map[string]struct{}{"gpt-4o-mini": {}}},
			wantGroups: []uint{},
		},
		{name: "unknown model", protocol: protocol.OpenAICompletions, model: "missing", wantGroups: []uint{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targets, _ := filterTargetsWithReason(snapshot, Query{
				ClientProtocol: tt.protocol, Operation: execution.OperationChatCompletion,
				ExternalModel: modelPointer(tt.model),
				AccessKey:     state.AccessKeyView{ID: 10, Filters: tt.filters},
			})
			got := make([]uint, 0, len(targets))
			for _, target := range targets {
				got = append(got, target.target.GroupID)
			}
			if !reflect.DeepEqual(got, tt.wantGroups) {
				t.Fatalf("groups = %#v, want %#v", got, tt.wantGroups)
			}
		})
	}
}

func TestFilterTargetsSkipsCandidateWithoutGroupView(t *testing.T) {
	snapshot := schedulerSnapshot()
	delete(snapshot.Groups, 2)
	got, _ := filterTargetsWithReason(
		snapshot,
		Query{
			ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion,
			ExternalModel: modelPointer("gpt-4o"),
		},
	)
	if len(got) != 1 || got[0].target.GroupID != 1 {
		t.Fatalf("targets = %#v, want only group 1", got)
	}
}

func TestIteratorRejectsProtocolOnlyGroupWithoutConfiguredModels(t *testing.T) {
	t.Parallel()

	snapshot, err := state.Compile(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{ConnectionType: "api_key", ID: 7, Name: "responses", ChannelID: channel.OpenAI,
			Params: json.RawMessage(`{}`), Enabled: true,
		}},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	source := fakeCredentialSource{keys: []state.CredentialMeta{{
		ID: 71, GroupID: 7, WeightAuto: state.DefaultWeight,
	}}}

	iterator := New(
		snapshot,
		source,
		Query{
			ClientProtocol: protocol.OpenAIResponses,
			Operation:      execution.OperationResponsesRetrieve,
			ExternalModel:  nil,
			AccessKey: state.AccessKeyView{
				Status: state.AccessKeyStatusActive,
			},
		},
		rand.New(zeroRandSource{}),
	)
	if iterator.StaticReason() != ReasonNoRouteTarget {
		t.Fatalf("StaticReason() = %q, want %q", iterator.StaticReason(), ReasonNoRouteTarget)
	}
	if _, err := iterator.Next(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("Next() error = %v, want ErrExhausted", err)
	}
}

type fakeCredentialSource struct {
	keys []state.CredentialMeta
}

type zeroRandSource struct{}

func (zeroRandSource) Int63() int64 { return 0 }
func (zeroRandSource) Seed(int64)   {}

func (source fakeCredentialSource) CollectCredentialCandidates(groupIDs []uint, excluded func(uint) bool, _ time.Time) []state.CredentialMeta {
	allowed := make(map[uint]struct{}, len(groupIDs))
	for _, groupID := range groupIDs {
		allowed[groupID] = struct{}{}
	}
	result := make([]state.CredentialMeta, 0, len(source.keys))
	for _, key := range source.keys {
		if _, ok := allowed[key.GroupID]; !ok || (excluded != nil && excluded(key.ID)) {
			continue
		}
		result = append(result, key)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func TestIteratorUsesInjectedTimeForCandidateEligibility(t *testing.T) {
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	registry := state.NewCredentialRegistry()
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 11, GroupID: 1, Status: state.CredentialStatusActive,
		CooldownUntil: now.Add(time.Second), Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher",
	}}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}

	query := Query{ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("gpt-4o")}
	cooling := newWithClock(schedulerSnapshot(), registry, query, rand.New(rand.NewSource(1)), func() time.Time {
		return now
	})
	if _, err := cooling.Next(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("Next() while cooling error = %v, want ErrExhausted", err)
	}

	expired := newWithClock(schedulerSnapshot(), registry, query, rand.New(rand.NewSource(1)), func() time.Time {
		return now.Add(time.Second)
	})
	selection, err := expired.Next()
	if err != nil || selection.CredentialID != 11 {
		t.Fatalf("Next() at cooldown boundary = (%#v, %v), want key 11", selection, err)
	}
}

func TestIteratorSkipGroupExcludesWholeGroup(t *testing.T) {
	source := fakeCredentialSource{keys: []state.CredentialMeta{
		{ID: 11, GroupID: 1}, {ID: 12, GroupID: 1}, {ID: 21, GroupID: 2},
	}}
	iterator := New(schedulerSnapshot(), source,
		Query{ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("gpt-4o")},
		rand.New(zeroRandSource{}))
	first, err := iterator.Next()
	if err != nil || first.CredentialID != 11 {
		t.Fatalf("first Next() = (%#v, %v), want key 11", first, err)
	}
	iterator.SkipGroup(1)
	iterator.SkipGroup(1)
	second, err := iterator.Next()
	if err != nil || second.CredentialID != 21 {
		t.Fatalf("second Next() = (%#v, %v), want key 21", second, err)
	}
	if _, err := iterator.Next(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("Next() after skip/exhaustion error = %v, want ErrExhausted", err)
	}
}

func TestIteratorSkipGroupIsRequestLocal(t *testing.T) {
	source := fakeCredentialSource{keys: []state.CredentialMeta{
		{ID: 11, GroupID: 1}, {ID: 21, GroupID: 2},
	}}
	query := Query{ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("gpt-4o")}
	first := New(schedulerSnapshot(), source, query, rand.New(zeroRandSource{}))
	first.SkipGroup(1)
	selection, err := first.Next()
	if err != nil || selection.GroupID != 2 {
		t.Fatalf("skipping iterator Next() = (%#v, %v), want group 2", selection, err)
	}
	second := New(schedulerSnapshot(), source, query, rand.New(zeroRandSource{}))
	selection, err = second.Next()
	if err != nil || selection.GroupID != 1 {
		t.Fatalf("fresh iterator Next() = (%#v, %v), want group 1", selection, err)
	}
}

func TestIteratorSkipGroupIgnoresNilReceiverAndZeroID(t *testing.T) {
	var nilIterator *Iterator
	nilIterator.SkipGroup(1)

	iterator := New(schedulerSnapshot(), fakeCredentialSource{keys: []state.CredentialMeta{
		{ID: 11, GroupID: 1}, {ID: 21, GroupID: 2},
	}}, Query{ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("gpt-4o")}, rand.New(zeroRandSource{}))
	iterator.SkipGroup(0)
	selection, err := iterator.Next()
	if err != nil || selection.GroupID != 1 {
		t.Fatalf("Next() after SkipGroup(0) = (%#v, %v), want group 1", selection, err)
	}
}

func TestIteratorSkipGroupExcludesKeysAddedAfterSkip(t *testing.T) {
	registry := state.NewCredentialRegistry()
	if err := registry.ReplaceCredentials([]state.CredentialEntry{
		{ID: 11, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "one"},
		{ID: 21, GroupID: 2, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "two"},
	}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	iterator := New(schedulerSnapshot(), registry,
		Query{ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("gpt-4o")},
		rand.New(zeroRandSource{}))
	iterator.SkipGroup(1)
	if err := registry.ApplyCredentialImport(1, []state.CredentialEntry{{
		ID: 12, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "new",
	}}); err != nil {
		t.Fatalf("ApplyImport() error = %v", err)
	}
	selection, err := iterator.Next()
	if err != nil || selection.CredentialID != 21 {
		t.Fatalf("Next() = (%#v, %v), want group 2 key 21", selection, err)
	}
	if _, err := iterator.Next(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("Next() error = %v, want ErrExhausted", err)
	}
}

func TestIteratorNextNeverRepeatsAndExhausts(t *testing.T) {
	source := fakeCredentialSource{keys: []state.CredentialMeta{
		{ID: 11, GroupID: 1},
		{ID: 12, GroupID: 1},
		{ID: 21, GroupID: 2},
	}}
	iterator := New(
		schedulerSnapshot(),
		source,
		Query{ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("gpt-4o")},
		rand.New(rand.NewSource(7)),
	)

	seen := make(map[uint]struct{})
	for range 3 {
		selection, err := iterator.Next()
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		if _, duplicate := seen[selection.CredentialID]; duplicate {
			t.Fatalf("key %d selected twice", selection.CredentialID)
		}
		seen[selection.CredentialID] = struct{}{}
		if selection.Group.ID != selection.GroupID ||
			selection.UpstreamModelID == nil ||
			*selection.UpstreamModelID == "" {
			t.Fatalf("invalid selection: %#v", selection)
		}
	}
	if _, err := iterator.Next(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("Next() after pool exhaustion error = %v, want ErrExhausted", err)
	}
}

func TestIteratorUsesEffectiveWeights(t *testing.T) {
	snapshot := schedulerSnapshot()
	heavyGroup, lightGroup := 100, 50
	group := snapshot.Groups[1]
	group.WeightManual = &heavyGroup
	snapshot.Groups[1] = group
	group = snapshot.Groups[2]
	group.WeightManual = &lightGroup
	snapshot.Groups[2] = group

	source := fakeCredentialSource{keys: []state.CredentialMeta{
		{ID: 1, GroupID: 1, WeightAuto: 100},
		{ID: 2, GroupID: 2, WeightAuto: 100},
	}}
	counts := map[uint]int{}
	random := rand.New(rand.NewSource(99))
	for range 12000 {
		iterator := New(snapshot, source, Query{ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("gpt-4o")}, random)
		selection, err := iterator.Next()
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		counts[selection.CredentialID]++
	}
	ratio := float64(counts[1]) / float64(counts[2])
	if ratio < 1.85 || ratio > 2.15 {
		t.Fatalf("weighted counts = %#v, ratio = %.3f, want about 2:1", counts, ratio)
	}
}

func TestIteratorUsesKeyWeights(t *testing.T) {
	manualWeight := 100
	source := fakeCredentialSource{keys: []state.CredentialMeta{
		{ID: 1, GroupID: 1, WeightManual: &manualWeight, WeightAuto: 1},
		{ID: 2, GroupID: 1, WeightAuto: 50},
	}}
	counts := map[uint]int{}
	random := rand.New(rand.NewSource(99))
	for range 12000 {
		iterator := New(
			schedulerSnapshot(),
			source,
			Query{ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("gpt-4o")},
			random,
		)
		selection, err := iterator.Next()
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		counts[selection.CredentialID]++
	}
	ratio := float64(counts[1]) / float64(counts[2])
	if ratio < 1.85 || ratio > 2.15 {
		t.Fatalf("weighted counts = %#v, ratio = %.3f, want about 2:1", counts, ratio)
	}
}

func TestEffectiveWeightCombinesGroupAndKeyWeights(t *testing.T) {
	groupManual := 20
	keyManual := 40
	zero := 0
	tests := []struct {
		name  string
		group state.GroupView
		key   state.CredentialMeta
		want  int64
	}{
		{name: "defaults", group: state.GroupView{}, key: state.CredentialMeta{}, want: 50 * 50},
		{name: "group and auto key", group: state.GroupView{WeightManual: &groupManual}, key: state.CredentialMeta{WeightAuto: 30}, want: 20 * 30},
		{name: "manual key overrides auto", group: state.GroupView{WeightManual: &groupManual}, key: state.CredentialMeta{WeightManual: &keyManual, WeightAuto: 90}, want: 20 * 40},
		{name: "zero group", group: state.GroupView{WeightManual: &zero}, key: state.CredentialMeta{WeightAuto: 30}},
		{name: "zero key", group: state.GroupView{WeightManual: &groupManual}, key: state.CredentialMeta{WeightManual: &zero, WeightAuto: 30}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := effectiveWeight(
				test.group.WeightManual,
				test.key.WeightManual,
				test.key.WeightAuto,
			); got != test.want {
				t.Fatalf("effectiveWeight() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestIteratorExcludesZeroManualWeights(t *testing.T) {
	t.Run("group", func(t *testing.T) {
		snapshot := schedulerSnapshot()
		zero := 0
		group := snapshot.Groups[1]
		group.WeightManual = &zero
		snapshot.Groups[1] = group
		source := fakeCredentialSource{keys: []state.CredentialMeta{
			{ID: 11, GroupID: 1, WeightAuto: state.DefaultWeight},
			{ID: 21, GroupID: 2, WeightAuto: state.DefaultWeight},
		}}

		random := rand.New(rand.NewSource(1))
		for range 200 {
			iterator := New(snapshot, source, Query{ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("gpt-4o")}, random)
			selection, err := iterator.Next()
			if err != nil || selection.CredentialID != 21 {
				t.Fatalf("Next() = (%#v, %v), want enabled group key 21", selection, err)
			}
		}
	})

	t.Run("key", func(t *testing.T) {
		zero := 0
		source := fakeCredentialSource{keys: []state.CredentialMeta{
			{ID: 11, GroupID: 1, WeightManual: &zero, WeightAuto: state.DefaultWeight},
			{ID: 12, GroupID: 1, WeightAuto: state.DefaultWeight},
		}}

		random := rand.New(rand.NewSource(1))
		for range 200 {
			iterator := New(schedulerSnapshot(), source, Query{ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("gpt-4o")}, random)
			selection, err := iterator.Next()
			if err != nil || selection.CredentialID != 12 {
				t.Fatalf("Next() = (%#v, %v), want enabled key 12", selection, err)
			}
		}
	})
}

func TestIteratorExhaustsWhenEffectiveWeightPoolIsEmpty(t *testing.T) {
	t.Run("group disabled by weight", func(t *testing.T) {
		snapshot := schedulerSnapshot()
		zero := 0
		for _, groupID := range []uint{1, 2} {
			group := snapshot.Groups[groupID]
			group.WeightManual = &zero
			snapshot.Groups[groupID] = group
		}
		iterator := New(
			snapshot,
			fakeCredentialSource{keys: []state.CredentialMeta{{ID: 11, GroupID: 1, WeightAuto: state.DefaultWeight}}},
			Query{ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("gpt-4o")},
			rand.New(rand.NewSource(1)),
		)
		if _, err := iterator.Next(); !errors.Is(err, ErrExhausted) {
			t.Fatalf("Next() error = %v, want ErrExhausted", err)
		}
	})

	t.Run("keys disabled by weight", func(t *testing.T) {
		zero := 0
		iterator := New(
			schedulerSnapshot(),
			fakeCredentialSource{keys: []state.CredentialMeta{
				{ID: 11, GroupID: 1, WeightManual: &zero, WeightAuto: state.DefaultWeight},
				{ID: 21, GroupID: 2, WeightManual: &zero, WeightAuto: state.DefaultWeight},
			}},
			Query{ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("gpt-4o")},
			rand.New(rand.NewSource(1)),
		)
		if _, err := iterator.Next(); !errors.Is(err, ErrExhausted) {
			t.Fatalf("Next() error = %v, want ErrExhausted", err)
		}
	})
}

func TestIteratorUsesDefaultWeights(t *testing.T) {
	iterator := New(
		schedulerSnapshot(),
		fakeCredentialSource{keys: []state.CredentialMeta{{ID: 11, GroupID: 1}}},
		Query{ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("gpt-4o")},
		rand.New(rand.NewSource(1)),
	)
	selection, err := iterator.Next()
	if err != nil || selection.CredentialID != 11 {
		t.Fatalf("Next() with default weights = (%#v, %v), want key 11", selection, err)
	}
}

func TestIteratorPrefersEligibleCredentialThenResumesWeightedCandidates(t *testing.T) {
	t.Parallel()

	iterator := New(
		schedulerSnapshot(),
		fakeCredentialSource{keys: []state.CredentialMeta{
			{ID: 11, GroupID: 1, WeightAuto: state.DefaultWeight},
			{ID: 12, GroupID: 1, WeightAuto: state.DefaultWeight},
		}},
		Query{
			ClientProtocol:        protocol.OpenAICompletions,
			Operation:             execution.OperationChatCompletion,
			ExternalModel:         modelPointer("gpt-4o"),
			PreferredCredentialID: 12,
		},
		rand.New(zeroRandSource{}),
	)

	first, err := iterator.Next()
	if err != nil || first.CredentialID != 12 {
		t.Fatalf("first Next() = (%#v, %v), want preferred credential 12", first, err)
	}
	second, err := iterator.Next()
	if err != nil || second.CredentialID != 11 {
		t.Fatalf("second Next() = (%#v, %v), want remaining weighted credential 11", second, err)
	}
}

func TestIteratorIgnoresIneligiblePreferredCredential(t *testing.T) {
	t.Parallel()

	zero := 0
	iterator := New(
		schedulerSnapshot(),
		fakeCredentialSource{keys: []state.CredentialMeta{
			{ID: 11, GroupID: 1, WeightAuto: state.DefaultWeight},
			{ID: 12, GroupID: 1, WeightManual: &zero, WeightAuto: state.DefaultWeight},
		}},
		Query{
			ClientProtocol:        protocol.OpenAICompletions,
			Operation:             execution.OperationChatCompletion,
			ExternalModel:         modelPointer("gpt-4o"),
			PreferredCredentialID: 12,
		},
		rand.New(zeroRandSource{}),
	)

	selection, err := iterator.Next()
	if err != nil || selection.CredentialID != 11 {
		t.Fatalf("Next() = (%#v, %v), want eligible weighted credential 11", selection, err)
	}
}

func TestIteratorReadsRegistryChangesBetweenNextCalls(t *testing.T) {
	registry := state.NewCredentialRegistry()
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 11, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one",
	}}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	iterator := New(
		schedulerSnapshot(),
		registry,
		Query{ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("gpt-4o")},
		rand.New(rand.NewSource(1)),
	)
	first, err := iterator.Next()
	if err != nil || first.CredentialID != 11 {
		t.Fatalf("first Next() = (%#v, %v)", first, err)
	}
	if err := registry.ApplyCredentialImport(1, []state.CredentialEntry{{
		ID: 12, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-two",
	}}); err != nil {
		t.Fatalf("ApplyImport() error = %v", err)
	}
	second, err := iterator.Next()
	if err != nil || second.CredentialID != 12 {
		t.Fatalf("second Next() = (%#v, %v), want newly added key 12", second, err)
	}
}

func TestIteratorRestrictsLiveCandidatesToAllowedCredentialIDs(t *testing.T) {
	registry := state.NewCredentialRegistry()
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 11, GroupID: 1, Status: state.CredentialStatusActive,
		Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-captured",
	}}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	allowed := map[uint]struct{}{11: {}}
	iterator := New(
		schedulerSnapshot(),
		registry,
		Query{
			ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("gpt-4o"),
			AllowedCredentialIDs: allowed,
		},
		rand.New(zeroRandSource{}),
	)

	delete(allowed, 11)
	allowed[12] = struct{}{}
	if err := registry.ApplyCredentialImport(1, []state.CredentialEntry{{
		ID: 12, GroupID: 1, Status: state.CredentialStatusActive,
		Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-imported-after-iterator",
	}}); err != nil {
		t.Fatalf("ApplyImport() error = %v", err)
	}

	first, err := iterator.Next()
	if err != nil || first.CredentialID != 11 {
		t.Fatalf("first Next() = (%#v, %v), want captured key 11", first, err)
	}
	if _, err := iterator.Next(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("second Next() error = %v, want ErrExhausted", err)
	}
}

func TestIteratorPropertyNeverEscapesAccessFilters(t *testing.T) {
	snapshot := schedulerSnapshot()
	source := fakeCredentialSource{keys: []state.CredentialMeta{
		{ID: 11, GroupID: 1}, {ID: 12, GroupID: 1},
		{ID: 21, GroupID: 2}, {ID: 22, GroupID: 2},
		{ID: 31, GroupID: 3},
	}}
	generator := rand.New(rand.NewSource(20260717))

	for caseIndex := range 300 {
		allowedGroup := uint(generator.Intn(2) + 1)
		filters := state.FilterSet{}
		if generator.Intn(2) == 1 {
			filters.Groups = map[uint]struct{}{allowedGroup: {}}
		}
		if generator.Intn(2) == 1 {
			filters.Protocols = map[protocol.Protocol]struct{}{protocol.OpenAICompletions: {}}
		}
		if generator.Intn(2) == 1 {
			filters.Models = map[string]struct{}{"gpt-4o": {}}
		}
		query := Query{
			ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion,
			ExternalModel: modelPointer("gpt-4o"),
			AccessKey:     state.AccessKeyView{ID: uint(caseIndex + 1), Filters: filters},
		}
		frozenGroups := make(map[uint]struct{})
		targets, _ := filterTargetsWithReason(snapshot, query)
		for _, target := range targets {
			frozenGroups[target.target.GroupID] = struct{}{}
		}
		iterator := New(snapshot, source, query, rand.New(rand.NewSource(int64(caseIndex+1))))

		skipped := make(map[uint]struct{})
		for {
			selection, err := iterator.Next()
			if errors.Is(err, ErrExhausted) {
				break
			}
			if err != nil {
				t.Fatalf("case %d Next() error = %v", caseIndex, err)
			}
			if _, blocked := skipped[selection.GroupID]; blocked {
				t.Fatalf("case %d selected skipped group %d", caseIndex, selection.GroupID)
			}
			if _, ok := frozenGroups[selection.GroupID]; !ok {
				t.Fatalf("case %d selection %#v escaped frozen target groups %#v", caseIndex, selection, frozenGroups)
			}
			if len(filters.Groups) > 0 {
				if _, ok := filters.Groups[selection.GroupID]; !ok {
					t.Fatalf("case %d selection %#v escaped group filter %#v", caseIndex, selection, filters.Groups)
				}
			}
			if selection.UpstreamModelID == nil ||
				*selection.UpstreamModelID == "" ||
				selection.GroupID == 0 {
				t.Fatalf("case %d invalid selection %#v", caseIndex, selection)
			}
			if generator.Intn(2) == 1 {
				skipped[selection.GroupID] = struct{}{}
				iterator.SkipGroup(selection.GroupID)
			}
		}
	}
}

func TestIteratorExhaustsForNilOrEmptyDependencies(t *testing.T) {
	tests := []struct {
		name     string
		iterator *Iterator
	}{
		{name: "nil snapshot", iterator: New(nil, fakeCredentialSource{}, Query{}, rand.New(rand.NewSource(1)))},
		{name: "nil key source", iterator: New(schedulerSnapshot(), nil, Query{ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("gpt-4o")}, rand.New(rand.NewSource(1)))},
		{name: "nil random", iterator: New(schedulerSnapshot(), fakeCredentialSource{}, Query{ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("gpt-4o")}, nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.iterator.Next(); !errors.Is(err, ErrExhausted) {
				t.Fatalf("Next() error = %v, want ErrExhausted", err)
			}
		})
	}
}

func TestNormalizeQueryDoesNotDefaultOpenAIImagesOperation(t *testing.T) {
	t.Parallel()

	normalized := normalizeQuery(Query{
		ClientProtocol: protocol.OpenAIImages,
		ExternalModel:  modelPointer("gpt-image-2"),
	})
	if normalized.operation != "" {
		t.Fatalf("operation = %q, want empty", normalized.operation)
	}
}

func schedulerSnapshot() *state.ConfigSnapshot {
	snapshot, err := state.Compile(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{
			{ConnectionType: "api_key", ID: 1, Name: "one", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
				Models: []state.ModelConfig{{ID: "gpt-4o", Alias: "gpt-4o"}}, Enabled: true,
			},
			{ConnectionType: "api_key", ID: 2, Name: "two", ChannelID: channel.OpenAICompatible,
				Params: json.RawMessage(`{"base_url":"https://two.example/v1"}`),
				Models: []state.ModelConfig{{ID: "provider-gpt-4o", Alias: "gpt-4o"}}, Enabled: true,
			},
		},
	})
	if err != nil {
		panic(err)
	}
	return snapshot
}
