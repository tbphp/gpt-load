package scheduler

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
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
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{
			{ConnectionType: "api_key", ID: 2, Name: "disabled", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
				Models:  []state.ModelConfig{{ID: "provider-disabled", Alias: "public"}},
				Enabled: false,
			},
			{ConnectionType: "api_key", ID: 1, Name: "active", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
				Models:  []state.ModelConfig{{ID: "provider-active", Alias: "public"}},
				Enabled: true,
			},
			{ConnectionType: "api_key", ID: 3, Name: "weight-zero", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
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
				ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("public"),
				AccessKey: state.AccessKeyView{Status: state.AccessKeyStatusDisabled},
			},
			reason: ReasonAccessKeyDisabled,
		},
		{
			name: "protocol filtered before model",
			query: Query{
				ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("public"),
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
				ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("public"),
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
				ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("missing"),
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

func TestInspectRequiresModelWhenAccessKeyHasModelFilter(t *testing.T) {
	t.Parallel()

	got, err := Inspect(inspectSnapshot(t), nil, Query{
		ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion,
		ExternalModel: nil,
		AccessKey: state.AccessKeyView{
			Status: state.AccessKeyStatusActive,
			Filters: state.FilterSet{
				Models: map[string]struct{}{"public": {}},
			},
		},
	}, inspectNow())
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if got.Routable || got.Reason != ReasonModelRequiredByFilter ||
		len(got.Groups) != 0 {
		t.Fatalf(
			"Inspection = %#v, want reason %q and empty groups",
			got,
			ReasonModelRequiredByFilter,
		)
	}
}

func TestInspectExplainsGroupsAndKeysInStableOrder(t *testing.T) {
	now := inspectNow()
	snapshot := inspectSnapshot(t)
	zero := 0
	keys := []state.CredentialRuntimeView{
		{
			ID: 32, GroupID: 3, Status: state.CredentialStatusActive,
			WeightAuto: state.DefaultWeight,
		},
		{
			ID: 12, GroupID: 1, Status: state.CredentialStatusActive,
			WeightAuto: state.DefaultWeight, CooldownUntil: now.Add(time.Minute),
		},
		{
			ID: 11, GroupID: 1, Status: state.CredentialStatusActive,
			WeightManual: &zero, WeightAuto: state.DefaultWeight,
		},
		{
			ID: 13, GroupID: 1, Status: state.CredentialStatusActive,
			WeightAuto: 40,
		},
	}
	before := append([]state.CredentialRuntimeView(nil), keys...)
	got, err := Inspect(snapshot, keys, Query{
		ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("public"),
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
		len(got.Groups[1].Credentials) != 0 {
		t.Fatalf("disabled group = %#v", got.Groups[1])
	}
	active := got.Groups[0]
	if !active.Included || !active.Routable || len(active.Credentials) != 3 ||
		active.Credentials[0].CredentialID != 11 || active.Credentials[1].CredentialID != 12 ||
		active.Credentials[2].CredentialID != 13 {
		t.Fatalf("active group = %#v", active)
	}
	if active.Credentials[0].Reason != ReasonCredentialWeightZero ||
		active.Credentials[1].Reason != ReasonCredentialCooldown ||
		!active.Credentials[2].Available || active.Credentials[2].EffectiveWeight != 50*40 {
		t.Fatalf("key explanations = %#v", active.Credentials)
	}
	if got.Groups[2].Reason != ReasonGroupWeightZero ||
		got.Groups[2].Credentials[0].Reason != ReasonGroupWeightZero {
		t.Fatalf("weight-zero group = %#v", got.Groups[2])
	}
}

func TestInspectEligiblePoolMatchesIteratorInitialWeightedPool(t *testing.T) {
	now := inspectNow()
	snapshot := inspectSnapshot(t)
	manual := 25
	zero := 0
	registry := state.NewCredentialRegistry()
	if err := registry.ReplaceCredentials([]state.CredentialEntry{
		{ID: 11, GroupID: 1, Status: state.CredentialStatusActive, WeightAuto: 40, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "one"},
		{
			ID: 12, GroupID: 1, Status: state.CredentialStatusActive,
			WeightManual: &manual, WeightAuto: 80, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "two",
		},
		{
			ID: 13, GroupID: 1, Status: state.CredentialStatusActive,
			CooldownUntil: now.Add(time.Minute), Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cooldown",
		},
		{
			ID: 14, GroupID: 1, Status: state.CredentialStatusActive,
			Blacklisted: true, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "blacklisted",
		},
		{
			ID: 15, GroupID: 1, Status: state.CredentialStatusDisabled,
			Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "disabled",
		},
		{
			ID: 16, GroupID: 1, Status: state.CredentialStatusActive,
			WeightManual: &zero, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "key-zero",
		},
		{ID: 21, GroupID: 2, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "disabled-group"},
		{ID: 31, GroupID: 3, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "zero-group"},
	}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	query := Query{
		ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("public"),
		AccessKey: state.AccessKeyView{Status: state.AccessKeyStatusActive},
	}
	inspection, err := Inspect(snapshot, registry.Snapshot(), query, now)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	inspectPool := make(map[uint]int64)
	for _, group := range inspection.Groups {
		for _, key := range group.Credentials {
			if key.Available && key.EffectiveWeight > 0 {
				inspectPool[key.CredentialID] = key.EffectiveWeight
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
	weighted, _ := iterator.weightedPoolForMode(channel.RouteNative, now)
	iteratorPool := make(map[uint]int64, len(weighted))
	for _, key := range weighted {
		iteratorPool[key.meta.ID] = key.weight
	}
	if !reflect.DeepEqual(inspectPool, iteratorPool) {
		t.Fatalf("Inspector pool = %#v, Iterator pool = %#v", inspectPool, iteratorPool)
	}
	for _, group := range inspection.Groups {
		for _, key := range group.Credentials {
			if key.Reason != "" {
				if _, selected := iteratorPool[key.CredentialID]; selected {
					t.Fatalf("excluded key %d entered Iterator pool", key.CredentialID)
				}
			}
		}
	}
}

func TestInspectEligiblePoolMatchesIteratorCredentialAuthorization(t *testing.T) {
	now := inspectNow()
	snapshot := inspectSnapshot(t)
	registry := state.NewCredentialRegistry()
	if err := registry.ReplaceCredentials([]state.CredentialEntry{
		{
			ID: 11, GroupID: 1, Status: state.CredentialStatusActive,
			AuthState: state.CredentialAuthStateReady, WeightAuto: state.DefaultWeight,
			Version: 1, IdentityGeneration: 1, Fingerprint: "ready", EncryptedValue: "ready",
		},
		{
			ID: 12, GroupID: 1, Status: state.CredentialStatusActive,
			AuthState: state.CredentialAuthStateRefreshing, WeightAuto: state.DefaultWeight,
			Version: 1, IdentityGeneration: 1, Fingerprint: "refreshing", EncryptedValue: "refreshing",
		},
	}); err != nil {
		t.Fatalf("ReplaceCredentials() error = %v", err)
	}
	query := Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  modelPointer("public"),
		AccessKey:      state.AccessKeyView{Status: state.AccessKeyStatusActive},
	}
	inspection, err := Inspect(snapshot, registry.Snapshot(), query, now)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	credentials := inspection.Groups[0].Credentials
	if len(credentials) != 2 || !credentials[0].Available ||
		credentials[1].Available ||
		credentials[1].Reason != ReasonCode("credential_auth_unavailable") {
		t.Fatalf("credential inspections = %#v", credentials)
	}

	iterator := newWithClock(
		snapshot,
		registry,
		query,
		rand.New(rand.NewSource(1)),
		func() time.Time { return now },
	)
	weighted, _ := iterator.weightedPoolForMode(channel.RouteNative, now)
	if len(weighted) != 1 || weighted[0].meta.ID != 11 {
		t.Fatalf("Iterator pool = %#v, want only ready credential 11", weighted)
	}
}

func TestInspectEligiblePoolMatchesIteratorWhenQuotaObservationsDiffer(t *testing.T) {
	now := inspectNow()
	snapshot, err := state.Compile(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ID: 7, Name: "codex", ChannelID: channel.Codex, ConnectionType: "subscription",
			Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-5", Alias: "public"}},
			Enabled: true,
		}},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	registry := state.NewCredentialRegistry()
	if err := registry.ReplaceCredentials([]state.CredentialEntry{
		{
			ID: 71, GroupID: 7, Status: state.CredentialStatusActive,
			AuthState: state.CredentialAuthStateReady, WeightAuto: state.DefaultWeight,
			Version: 1, IdentityGeneration: 1, Fingerprint: "low", EncryptedValue: "low",
		},
		{
			ID: 72, GroupID: 7, Status: state.CredentialStatusActive,
			AuthState: state.CredentialAuthStateReady, WeightAuto: state.DefaultWeight,
			Version: 1, IdentityGeneration: 1, Fingerprint: "high", EncryptedValue: "high",
		},
	}); err != nil {
		t.Fatalf("ReplaceCredentials() error = %v", err)
	}
	low, high := 0.2, 0.8
	resetAt := now.Add(time.Hour)
	freshUntil := now.Add(30 * time.Minute)
	if !registry.SetCredentialQuotaObservation(71, &low, resetAt, freshUntil) ||
		!registry.SetCredentialQuotaObservation(72, &high, resetAt, freshUntil) {
		t.Fatal("SetCredentialQuotaObservation() = false")
	}
	query := Query{
		ClientProtocol: protocol.OpenAIResponses,
		Operation:      execution.OperationResponsesCreate,
		ExternalModel:  modelPointer("public"),
		AccessKey:      state.AccessKeyView{Status: state.AccessKeyStatusActive},
	}
	inspection, err := Inspect(snapshot, registry.Snapshot(), query, now)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	credentials := inspection.Groups[0].Credentials
	if len(credentials) != 2 || !credentials[0].Available || !credentials[1].Available ||
		credentials[0].Reason != "" || credentials[1].Reason != "" {
		t.Fatalf("credential inspections = %#v", credentials)
	}

	iterator := newWithClock(
		snapshot,
		registry,
		query,
		rand.New(rand.NewSource(1)),
		func() time.Time { return now },
	)
	weighted, _ := iterator.weightedPoolForMode(channel.RouteNative, now)
	if len(weighted) != 2 || weighted[0].meta.ID != 71 || weighted[1].meta.ID != 72 {
		t.Fatalf("Iterator pool = %#v, want both weighted credentials", weighted)
	}
}

func TestInspectRejectsCatalogRegistryMismatch(t *testing.T) {
	_, err := Inspect(inspectSnapshot(t), []state.CredentialRuntimeView{{
		ID: 99, GroupID: 999, Status: state.CredentialStatusActive,
	}}, Query{
		ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("public"),
		AccessKey: state.AccessKeyView{Status: state.AccessKeyStatusActive},
	}, inspectNow())
	if !errors.Is(err, ErrInconsistentSnapshot) {
		t.Fatalf("Inspect() error = %v, want ErrInconsistentSnapshot", err)
	}
}

func TestInspectIgnoresFreshCredentialQuotaExhaustion(t *testing.T) {
	now := inspectNow()
	inspection, err := Inspect(inspectSnapshot(t), []state.CredentialRuntimeView{{
		ID: 11, GroupID: 1, Status: state.CredentialStatusActive,
		QuotaRemaining: floatPointer(0), QuotaResetAt: now.Add(time.Hour), QuotaFreshUntil: now.Add(30 * time.Minute),
	}}, Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  modelPointer("public"),
		AccessKey:      state.AccessKeyView{Status: state.AccessKeyStatusActive},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	credential := inspection.Groups[0].Credentials[0]
	if !credential.Available || credential.Reason != "" {
		t.Fatalf("credential inspection = %#v", credential)
	}
}

func floatPointer(value float64) *float64 { return &value }

func TestInspectReportsNoKeysForIncludedGroup(t *testing.T) {
	got, err := Inspect(inspectSnapshot(t), nil, Query{
		ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion,
		ExternalModel: modelPointer("public"),
		AccessKey: state.AccessKeyView{
			Status:  state.AccessKeyStatusActive,
			Filters: state.FilterSet{Groups: map[uint]struct{}{1: {}}},
		},
	}, inspectNow())
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if got.Routable || got.Reason != ReasonNoAvailableCredential ||
		len(got.Groups) != 3 ||
		!got.Groups[0].Included ||
		got.Groups[0].Reason != ReasonNoCredentials ||
		got.Groups[0].Credentials == nil ||
		len(got.Groups[0].Credentials) != 0 {
		t.Fatalf("no-key Inspection = %#v", got)
	}
}

func TestInspectUsesExactKeyReasonPriority(t *testing.T) {
	now := inspectNow()
	zero := 0
	snapshot := inspectSnapshot(t)
	keys := []state.CredentialRuntimeView{
		{
			ID: 11, GroupID: 1, Status: state.CredentialStatusDisabled,
			WeightManual: &zero, Blacklisted: true,
			CooldownUntil: now.Add(time.Minute),
		},
		{
			ID: 12, GroupID: 1, Status: state.CredentialStatusActive,
			WeightManual: &zero, Blacklisted: true,
		},
		{
			ID: 13, GroupID: 1, Status: state.CredentialStatusActive,
			Blacklisted: true, CooldownUntil: now.Add(time.Minute),
		},
		{
			ID: 14, GroupID: 1, Status: state.CredentialStatusActive,
			CooldownUntil: now.Add(time.Minute),
		},
	}
	got, err := Inspect(snapshot, keys, Query{
		ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("public"),
		AccessKey: state.AccessKeyView{
			Status:  state.AccessKeyStatusActive,
			Filters: state.FilterSet{Groups: map[uint]struct{}{1: {}}},
		},
	}, now)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	want := []ReasonCode{
		ReasonCredentialDisabled,
		ReasonCredentialWeightZero,
		ReasonCredentialBlacklisted,
		ReasonCredentialCooldown,
	}
	for index, reason := range want {
		if got.Groups[0].Credentials[index].Reason != reason {
			t.Fatalf("key %d reason = %q, want %q", index, got.Groups[0].Credentials[index].Reason, reason)
		}
	}
	if got.Routable || got.Reason != ReasonNoAvailableCredential ||
		got.Groups[0].Reason != ReasonNoAvailableCredential {
		t.Fatalf("unavailable Inspection = %#v", got)
	}
}

func TestInspectSummarizesStaticGroupExclusions(t *testing.T) {
	group := func(id uint, enabled bool) state.GroupConfig {
		return state.GroupConfig{ConnectionType: "api_key", ID: id, Name: fmt.Sprintf("group-%d", id),
			ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
			Models:  []state.ModelConfig{{ID: fmt.Sprintf("provider-%d", id), Alias: "public"}},
			Enabled: enabled,
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
			snapshot, err := state.Compile(state.CompileInput{
				ChannelRegistry: channel.NewRegistry(),
				Groups:          test.groups,
			})
			if err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
			result, err := Inspect(snapshot, nil, Query{
				ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion,
				ExternalModel: modelPointer("public"),
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
