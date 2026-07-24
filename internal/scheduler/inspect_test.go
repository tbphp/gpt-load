package scheduler

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func inspectNow() time.Time {
	return time.Date(2026, time.July, 24, 10, 0, 0, 0, time.UTC)
}

func inspectSnapshot(t *testing.T) *state.ConfigSnapshot {
	t.Helper()
	zero := 0
	snapshot, err := state.Compile(state.CompileInput{
		Groups: []state.GroupConfig{
			{
				ID: 2, Name: "disabled", Protocols: []protocol.Protocol{protocol.OpenAI},
				Models:  []state.ModelConfig{{ID: "provider-disabled", Alias: "public"}},
				Enabled: false,
			},
			{
				ID: 1, Name: "active", Protocols: []protocol.Protocol{protocol.OpenAI},
				Models:  []state.ModelConfig{{ID: "provider-active", Alias: "public"}},
				Enabled: true,
			},
			{
				ID: 3, Name: "weight-zero", Protocols: []protocol.Protocol{protocol.OpenAI},
				Models:       []state.ModelConfig{{ID: "provider-zero", Alias: "public"}},
				WeightManual: &zero, Enabled: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	return snapshot
}

func TestInspectAppliesTopLevelReasonPriority(t *testing.T) {
	snapshot := inspectSnapshot(t)
	tests := []struct {
		name   string
		query  Query
		reason ReasonCode
	}{
		{
			name: "access key disabled",
			query: Query{
				Protocol: protocol.OpenAI, ExternalModel: "public",
				AccessKey: state.AccessKeyView{Status: state.AccessKeyStatusDisabled},
			},
			reason: ReasonAccessKeyDisabled,
		},
		{
			name: "protocol filtered before model",
			query: Query{
				Protocol: protocol.OpenAI, ExternalModel: "public",
				AccessKey: state.AccessKeyView{
					Status: state.AccessKeyStatusActive,
					Filters: state.FilterSet{
						Protocols: map[protocol.Protocol]struct{}{protocol.Anthropic: {}},
						Models:    map[string]struct{}{"other": {}},
					},
				},
			},
			reason: ReasonProtocolFiltered,
		},
		{
			name: "model filtered",
			query: Query{
				Protocol: protocol.OpenAI, ExternalModel: "public",
				AccessKey: state.AccessKeyView{
					Status:  state.AccessKeyStatusActive,
					Filters: state.FilterSet{Models: map[string]struct{}{"other": {}}},
				},
			},
			reason: ReasonModelFiltered,
		},
		{
			name: "no route target",
			query: Query{
				Protocol: protocol.OpenAI, ExternalModel: "missing",
				AccessKey: state.AccessKeyView{Status: state.AccessKeyStatusActive},
			},
			reason: ReasonNoRouteTarget,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Inspect(snapshot, nil, test.query, inspectNow())
			if err != nil {
				t.Fatalf("Inspect() error = %v", err)
			}
			if got.Routable || got.Reason != test.reason || len(got.Groups) != 0 {
				t.Fatalf("Inspection = %#v, want reason %q and empty groups", got, test.reason)
			}
		})
	}
}

func TestInspectExplainsGroupsAndKeysInStableOrder(t *testing.T) {
	now := inspectNow()
	snapshot := inspectSnapshot(t)
	zero := 0
	keys := []state.KeyRuntimeView{
		{
			ID: 32, GroupID: 3, Status: state.KeyStatusActive,
			WeightAuto: state.DefaultWeight,
		},
		{
			ID: 12, GroupID: 1, Status: state.KeyStatusActive,
			WeightAuto: state.DefaultWeight, CooldownUntil: now.Add(time.Minute),
		},
		{
			ID: 11, GroupID: 1, Status: state.KeyStatusActive,
			WeightManual: &zero, WeightAuto: state.DefaultWeight,
		},
		{
			ID: 13, GroupID: 1, Status: state.KeyStatusActive,
			WeightAuto: 40,
		},
	}
	before := append([]state.KeyRuntimeView(nil), keys...)
	got, err := Inspect(snapshot, keys, Query{
		Protocol: protocol.OpenAI, ExternalModel: "public",
		AccessKey: state.AccessKeyView{
			Status:  state.AccessKeyStatusActive,
			Filters: state.FilterSet{Groups: map[uint]struct{}{1: {}, 3: {}}},
		},
	}, now)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if !reflect.DeepEqual(keys, before) {
		t.Fatalf("Inspect() mutated input: got=%#v want=%#v", keys, before)
	}
	if !got.Routable || got.Reason != "" || len(got.Groups) != 3 {
		t.Fatalf("Inspection = %#v", got)
	}
	if got.Groups[0].GroupID != 1 || got.Groups[1].GroupID != 2 ||
		got.Groups[2].GroupID != 3 {
		t.Fatalf("group order = %#v", got.Groups)
	}
	if got.Groups[1].Included || got.Groups[1].Reason != ReasonGroupDisabled ||
		len(got.Groups[1].Keys) != 0 {
		t.Fatalf("disabled group = %#v", got.Groups[1])
	}
	active := got.Groups[0]
	if !active.Included || !active.Routable || len(active.Keys) != 3 ||
		active.Keys[0].KeyID != 11 || active.Keys[1].KeyID != 12 ||
		active.Keys[2].KeyID != 13 {
		t.Fatalf("active group = %#v", active)
	}
	if active.Keys[0].Reason != ReasonKeyWeightZero ||
		active.Keys[1].Reason != ReasonKeyCooldown ||
		!active.Keys[2].Available || active.Keys[2].EffectiveWeight != 50*40 {
		t.Fatalf("key explanations = %#v", active.Keys)
	}
	if got.Groups[2].Reason != ReasonGroupWeightZero ||
		got.Groups[2].Keys[0].Reason != ReasonGroupWeightZero {
		t.Fatalf("weight-zero group = %#v", got.Groups[2])
	}
}

func TestInspectEligiblePoolMatchesIteratorInitialWeightedPool(t *testing.T) {
	now := inspectNow()
	snapshot := inspectSnapshot(t)
	manual := 25
	zero := 0
	registry := state.NewKeyRegistry()
	if err := registry.Replace([]state.KeyEntry{
		{ID: 11, GroupID: 1, Status: state.KeyStatusActive, WeightAuto: 40, EncryptedValue: "one"},
		{
			ID: 12, GroupID: 1, Status: state.KeyStatusActive,
			WeightManual: &manual, WeightAuto: 80, EncryptedValue: "two",
		},
		{
			ID: 13, GroupID: 1, Status: state.KeyStatusActive,
			CooldownUntil: now.Add(time.Minute), EncryptedValue: "cooldown",
		},
		{
			ID: 14, GroupID: 1, Status: state.KeyStatusActive,
			Blacklisted: true, EncryptedValue: "blacklisted",
		},
		{
			ID: 15, GroupID: 1, Status: state.KeyStatusDisabled,
			EncryptedValue: "disabled",
		},
		{
			ID: 16, GroupID: 1, Status: state.KeyStatusActive,
			WeightManual: &zero, EncryptedValue: "key-zero",
		},
		{ID: 21, GroupID: 2, Status: state.KeyStatusActive, EncryptedValue: "disabled-group"},
		{ID: 31, GroupID: 3, Status: state.KeyStatusActive, EncryptedValue: "zero-group"},
	}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	query := Query{
		Protocol: protocol.OpenAI, ExternalModel: "public",
		AccessKey: state.AccessKeyView{Status: state.AccessKeyStatusActive},
	}
	inspection, err := Inspect(snapshot, registry.Snapshot(), query, now)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	inspectPool := make(map[uint]int64)
	for _, group := range inspection.Groups {
		for _, key := range group.Keys {
			if key.Available && key.EffectiveWeight > 0 {
				inspectPool[key.KeyID] = key.EffectiveWeight
			}
		}
	}
	wantPool := map[uint]int64{11: 50 * 40, 12: 50 * 25}
	if !reflect.DeepEqual(inspectPool, wantPool) {
		t.Fatalf("Inspector pool = %#v, want %#v", inspectPool, wantPool)
	}

	iterator := newWithClock(
		snapshot,
		registry,
		query,
		rand.New(rand.NewSource(1)),
		func() time.Time { return now },
	)
	weighted, _ := iterator.weightedPool(now)
	iteratorPool := make(map[uint]int64, len(weighted))
	for _, key := range weighted {
		iteratorPool[key.meta.ID] = key.weight
	}
	if !reflect.DeepEqual(inspectPool, iteratorPool) {
		t.Fatalf("Inspector pool = %#v, Iterator pool = %#v", inspectPool, iteratorPool)
	}
	for _, group := range inspection.Groups {
		for _, key := range group.Keys {
			if key.Reason != "" {
				if _, selected := iteratorPool[key.KeyID]; selected {
					t.Fatalf("excluded key %d entered Iterator pool", key.KeyID)
				}
			}
		}
	}
}

func TestInspectRejectsCatalogRegistryMismatch(t *testing.T) {
	_, err := Inspect(inspectSnapshot(t), []state.KeyRuntimeView{{
		ID: 99, GroupID: 999, Status: state.KeyStatusActive,
	}}, Query{
		Protocol: protocol.OpenAI, ExternalModel: "public",
		AccessKey: state.AccessKeyView{Status: state.AccessKeyStatusActive},
	}, inspectNow())
	if !errors.Is(err, ErrInconsistentSnapshot) {
		t.Fatalf("Inspect() error = %v, want ErrInconsistentSnapshot", err)
	}
}

func TestInspectReportsNoKeysForIncludedGroup(t *testing.T) {
	got, err := Inspect(inspectSnapshot(t), nil, Query{
		Protocol:      protocol.OpenAI,
		ExternalModel: "public",
		AccessKey: state.AccessKeyView{
			Status:  state.AccessKeyStatusActive,
			Filters: state.FilterSet{Groups: map[uint]struct{}{1: {}}},
		},
	}, inspectNow())
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if got.Routable || got.Reason != ReasonNoAvailableKey ||
		len(got.Groups) != 3 ||
		!got.Groups[0].Included ||
		got.Groups[0].Reason != ReasonNoKeys ||
		got.Groups[0].Keys == nil ||
		len(got.Groups[0].Keys) != 0 {
		t.Fatalf("no-key Inspection = %#v", got)
	}
}

func TestInspectUsesExactKeyReasonPriority(t *testing.T) {
	now := inspectNow()
	zero := 0
	snapshot := inspectSnapshot(t)
	keys := []state.KeyRuntimeView{
		{
			ID: 11, GroupID: 1, Status: state.KeyStatusDisabled,
			WeightManual: &zero, Blacklisted: true,
			CooldownUntil: now.Add(time.Minute),
		},
		{
			ID: 12, GroupID: 1, Status: state.KeyStatusActive,
			WeightManual: &zero, Blacklisted: true,
		},
		{
			ID: 13, GroupID: 1, Status: state.KeyStatusActive,
			Blacklisted: true, CooldownUntil: now.Add(time.Minute),
		},
		{
			ID: 14, GroupID: 1, Status: state.KeyStatusActive,
			CooldownUntil: now.Add(time.Minute),
		},
	}
	got, err := Inspect(snapshot, keys, Query{
		Protocol: protocol.OpenAI, ExternalModel: "public",
		AccessKey: state.AccessKeyView{
			Status:  state.AccessKeyStatusActive,
			Filters: state.FilterSet{Groups: map[uint]struct{}{1: {}}},
		},
	}, now)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	want := []ReasonCode{
		ReasonKeyDisabled,
		ReasonKeyWeightZero,
		ReasonKeyBlacklisted,
		ReasonKeyCooldown,
	}
	for index, reason := range want {
		if got.Groups[0].Keys[index].Reason != reason {
			t.Fatalf("key %d reason = %q, want %q", index, got.Groups[0].Keys[index].Reason, reason)
		}
	}
	if got.Routable || got.Reason != ReasonNoAvailableKey ||
		got.Groups[0].Reason != ReasonNoAvailableKey {
		t.Fatalf("unavailable Inspection = %#v", got)
	}
}

func TestInspectSummarizesStaticGroupExclusions(t *testing.T) {
	group := func(id uint, enabled bool) state.GroupConfig {
		return state.GroupConfig{
			ID: id, Name: fmt.Sprintf("group-%d", id),
			Protocols: []protocol.Protocol{protocol.OpenAI},
			Models:    []state.ModelConfig{{ID: fmt.Sprintf("provider-%d", id), Alias: "public"}},
			Enabled:   enabled,
		}
	}
	tests := []struct {
		name          string
		groups        []state.GroupConfig
		allowedGroups map[uint]struct{}
		want          ReasonCode
	}{
		{
			name:   "all disabled",
			groups: []state.GroupConfig{group(1, false), group(2, false)},
			want:   ReasonGroupDisabled,
		},
		{
			name:          "all filtered",
			groups:        []state.GroupConfig{group(1, true), group(2, true)},
			allowedGroups: map[uint]struct{}{999: {}},
			want:          ReasonGroupFiltered,
		},
		{
			name:          "mixed disabled and filtered",
			groups:        []state.GroupConfig{group(1, false), group(2, true)},
			allowedGroups: map[uint]struct{}{999: {}},
			want:          ReasonNoAvailableGroup,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot, err := state.Compile(state.CompileInput{Groups: test.groups})
			if err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
			result, err := Inspect(snapshot, nil, Query{
				Protocol:      protocol.OpenAI,
				ExternalModel: "public",
				AccessKey: state.AccessKeyView{
					Status:  state.AccessKeyStatusActive,
					Filters: state.FilterSet{Groups: test.allowedGroups},
				},
			}, inspectNow())
			if err != nil {
				t.Fatalf("Inspect() error = %v", err)
			}
			if result.Routable || result.Reason != test.want ||
				len(result.Groups) != 2 {
				t.Fatalf("Inspection = %#v, want reason %q", result, test.want)
			}
		})
	}
}
