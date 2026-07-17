package scheduler

import (
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestFilterTargetsAppliesAccessKeyDimensions(t *testing.T) {
	snapshot := schedulerSnapshot()
	tests := []struct {
		name       string
		protocol   protocol.Protocol
		model      string
		filters    state.FilterSet
		wantGroups []uint
	}{
		{name: "unrestricted", protocol: protocol.OpenAI, model: "gpt-4o", wantGroups: []uint{1, 2}},
		{
			name:       "group filter",
			protocol:   protocol.OpenAI,
			model:      "gpt-4o",
			filters:    state.FilterSet{Groups: map[uint]struct{}{2: {}}},
			wantGroups: []uint{2},
		},
		{
			name:       "protocol allowed",
			protocol:   protocol.OpenAI,
			model:      "gpt-4o",
			filters:    state.FilterSet{Protocols: map[protocol.Protocol]struct{}{protocol.OpenAI: {}}},
			wantGroups: []uint{1, 2},
		},
		{
			name:       "protocol denied",
			protocol:   protocol.OpenAI,
			model:      "gpt-4o",
			filters:    state.FilterSet{Protocols: map[protocol.Protocol]struct{}{protocol.Anthropic: {}}},
			wantGroups: []uint{},
		},
		{
			name:       "model allowed",
			protocol:   protocol.OpenAI,
			model:      "gpt-4o",
			filters:    state.FilterSet{Models: map[string]struct{}{"gpt-4o": {}}},
			wantGroups: []uint{1, 2},
		},
		{
			name:       "model denied",
			protocol:   protocol.OpenAI,
			model:      "gpt-4o",
			filters:    state.FilterSet{Models: map[string]struct{}{"gpt-4o-mini": {}}},
			wantGroups: []uint{},
		},
		{name: "unknown model", protocol: protocol.OpenAI, model: "missing", wantGroups: []uint{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targets := filterTargets(snapshot, Query{
				Protocol:      tt.protocol,
				ExternalModel: tt.model,
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
	got := filterTargets(snapshot, Query{Protocol: protocol.OpenAI, ExternalModel: "gpt-4o"})
	if len(got) != 1 || got[0].target.GroupID != 1 {
		t.Fatalf("targets = %#v, want only group 1", got)
	}
}

type fakeKeySource struct {
	keys []state.KeyMeta
}

func (source fakeKeySource) CollectCandidates(groupIDs []uint, excluded func(uint) bool) []state.KeyMeta {
	allowed := make(map[uint]struct{}, len(groupIDs))
	for _, groupID := range groupIDs {
		allowed[groupID] = struct{}{}
	}
	result := make([]state.KeyMeta, 0, len(source.keys))
	for _, key := range source.keys {
		if _, ok := allowed[key.GroupID]; !ok || (excluded != nil && excluded(key.ID)) {
			continue
		}
		result = append(result, key)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func TestIteratorNextNeverRepeatsAndExhausts(t *testing.T) {
	source := fakeKeySource{keys: []state.KeyMeta{
		{ID: 11, GroupID: 1},
		{ID: 12, GroupID: 1},
		{ID: 21, GroupID: 2},
	}}
	iterator := New(
		schedulerSnapshot(),
		source,
		Query{Protocol: protocol.OpenAI, ExternalModel: "gpt-4o"},
		rand.New(rand.NewSource(7)),
	)

	seen := make(map[uint]struct{})
	for range 3 {
		selection, err := iterator.Next()
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		if _, duplicate := seen[selection.KeyID]; duplicate {
			t.Fatalf("key %d selected twice", selection.KeyID)
		}
		seen[selection.KeyID] = struct{}{}
		if selection.Group.ID != selection.GroupID || selection.UpstreamModelID == "" {
			t.Fatalf("invalid selection: %#v", selection)
		}
	}
	if _, err := iterator.Next(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("Next() after pool exhaustion error = %v, want ErrExhausted", err)
	}
}

func TestIteratorUsesEqualWeightsInM1(t *testing.T) {
	heavy, light := 100, 1
	source := fakeKeySource{keys: []state.KeyMeta{
		{ID: 1, GroupID: 1, WeightManual: &heavy},
		{ID: 2, GroupID: 2, WeightManual: &light},
	}}
	counts := map[uint]int{}
	random := rand.New(rand.NewSource(99))
	for range 2000 {
		iterator := New(schedulerSnapshot(), source, Query{Protocol: protocol.OpenAI, ExternalModel: "gpt-4o"}, random)
		selection, err := iterator.Next()
		if err != nil {
			t.Fatalf("Next() error = %v", err)
		}
		counts[selection.KeyID]++
	}
	for _, keyID := range []uint{1, 2} {
		if counts[keyID] < 800 || counts[keyID] > 1200 {
			t.Fatalf("equal-weight counts = %#v, key %d outside deterministic tolerance", counts, keyID)
		}
	}
}

func TestIteratorReadsRegistryChangesBetweenNextCalls(t *testing.T) {
	registry := state.NewKeyRegistry()
	if err := registry.Replace([]state.KeyEntry{{
		ID: 11, GroupID: 1, Status: state.KeyStatusActive, EncryptedValue: "cipher-one",
	}}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	iterator := New(
		schedulerSnapshot(),
		registry,
		Query{Protocol: protocol.OpenAI, ExternalModel: "gpt-4o"},
		rand.New(rand.NewSource(1)),
	)
	first, err := iterator.Next()
	if err != nil || first.KeyID != 11 {
		t.Fatalf("first Next() = (%#v, %v)", first, err)
	}
	if err := registry.ApplyImport(1, []state.KeyEntry{{
		ID: 12, GroupID: 1, Status: state.KeyStatusActive, EncryptedValue: "cipher-two",
	}}); err != nil {
		t.Fatalf("ApplyImport() error = %v", err)
	}
	second, err := iterator.Next()
	if err != nil || second.KeyID != 12 {
		t.Fatalf("second Next() = (%#v, %v), want newly added key 12", second, err)
	}
}

func TestIteratorPropertyNeverEscapesAccessFilters(t *testing.T) {
	snapshot := schedulerSnapshot()
	source := fakeKeySource{keys: []state.KeyMeta{
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
			filters.Protocols = map[protocol.Protocol]struct{}{protocol.OpenAI: {}}
		}
		if generator.Intn(2) == 1 {
			filters.Models = map[string]struct{}{"gpt-4o": {}}
		}
		query := Query{
			Protocol:      protocol.OpenAI,
			ExternalModel: "gpt-4o",
			AccessKey:     state.AccessKeyView{ID: uint(caseIndex + 1), Filters: filters},
		}
		frozenGroups := make(map[uint]struct{})
		for _, target := range filterTargets(snapshot, query) {
			frozenGroups[target.target.GroupID] = struct{}{}
		}
		iterator := New(snapshot, source, query, rand.New(rand.NewSource(int64(caseIndex+1))))

		for {
			selection, err := iterator.Next()
			if errors.Is(err, ErrExhausted) {
				break
			}
			if err != nil {
				t.Fatalf("case %d Next() error = %v", caseIndex, err)
			}
			if _, ok := frozenGroups[selection.GroupID]; !ok {
				t.Fatalf("case %d selection %#v escaped frozen target groups %#v", caseIndex, selection, frozenGroups)
			}
			if len(filters.Groups) > 0 {
				if _, ok := filters.Groups[selection.GroupID]; !ok {
					t.Fatalf("case %d selection %#v escaped group filter %#v", caseIndex, selection, filters.Groups)
				}
			}
			if selection.UpstreamModelID == "" || selection.GroupID == 0 {
				t.Fatalf("case %d invalid selection %#v", caseIndex, selection)
			}
		}
	}
}

func TestIteratorExhaustsForNilOrEmptyDependencies(t *testing.T) {
	tests := []struct {
		name     string
		iterator *Iterator
	}{
		{name: "nil snapshot", iterator: New(nil, fakeKeySource{}, Query{}, rand.New(rand.NewSource(1)))},
		{name: "nil key source", iterator: New(schedulerSnapshot(), nil, Query{Protocol: protocol.OpenAI, ExternalModel: "gpt-4o"}, rand.New(rand.NewSource(1)))},
		{name: "nil random", iterator: New(schedulerSnapshot(), fakeKeySource{}, Query{Protocol: protocol.OpenAI, ExternalModel: "gpt-4o"}, nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.iterator.Next(); !errors.Is(err, ErrExhausted) {
				t.Fatalf("Next() error = %v, want ErrExhausted", err)
			}
		})
	}
}

func schedulerSnapshot() *state.ConfigSnapshot {
	return &state.ConfigSnapshot{
		Candidates: map[protocol.Protocol]map[string][]state.RouteTarget{
			protocol.OpenAI: {
				"gpt-4o": {
					{GroupID: 1, UpstreamModelID: "gpt-4o"},
					{GroupID: 2, UpstreamModelID: "provider-gpt-4o"},
				},
			},
		},
		Groups: map[uint]state.GroupView{
			1: {ID: 1, Name: "one", UpstreamURL: "https://one.example"},
			2: {ID: 2, Name: "two", UpstreamURL: "https://two.example"},
		},
	}
}
