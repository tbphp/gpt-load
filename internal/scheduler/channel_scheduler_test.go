package scheduler

import (
	"encoding/json"
	"errors"
	"math/rand"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestIteratorExhaustsNativeTierBeforeConvertedTier(t *testing.T) {
	t.Parallel()

	snapshot := channelSchedulerSnapshot(t)
	convertedWeight := 100
	nativeWeight := 1
	converted := snapshot.Groups[1]
	converted.WeightManual = &convertedWeight
	snapshot.Groups[1] = converted
	native := snapshot.Groups[2]
	native.WeightManual = &nativeWeight
	snapshot.Groups[2] = native

	iterator := New(snapshot, fakeCredentialSource{keys: []state.CredentialMeta{
		{ID: 11, GroupID: 1, WeightAuto: state.DefaultWeight},
		{ID: 21, GroupID: 2, WeightAuto: state.DefaultWeight},
	}}, Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  modelPointer("public"),
		AccessKey:      state.AccessKeyView{Status: state.AccessKeyStatusActive},
	}, rand.New(zeroRandSource{}))

	first, err := iterator.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	if first.CredentialID != 21 || first.GroupID != 2 ||
		first.ChannelID != channel.OpenAI || first.RouteMode != channel.RouteNative ||
		first.ResolvedTarget.ChannelID != channel.OpenAI || first.UpstreamModelID == nil || *first.UpstreamModelID != "native-model" {
		t.Fatalf("first Selection = %#v, want native OpenAI credential", first)
	}

	second, err := iterator.Next()
	if err != nil {
		t.Fatalf("second Next() error = %v", err)
	}
	if second.CredentialID != 11 || second.GroupID != 1 || second.ChannelID != channel.Anthropic ||
		second.RouteMode != channel.RouteConverted || second.UpstreamModelID == nil || *second.UpstreamModelID != "converted-model" {
		t.Fatalf("second Selection = %#v, want converted Anthropic credential", second)
	}
	if _, err := iterator.Next(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("third Next() error = %v, want ErrExhausted", err)
	}
}

func TestIteratorSkipGroupAndAllowedCredentialIDsApplyAcrossRouteTiers(t *testing.T) {
	t.Parallel()

	allowed := map[uint]struct{}{11: {}, 21: {}}
	iterator := New(channelSchedulerSnapshot(t), fakeCredentialSource{keys: []state.CredentialMeta{
		{ID: 11, GroupID: 1}, {ID: 12, GroupID: 1}, {ID: 21, GroupID: 2},
	}}, Query{
		ClientProtocol:       protocol.OpenAICompletions,
		Operation:            execution.OperationChatCompletion,
		ExternalModel:        modelPointer("public"),
		AllowedCredentialIDs: allowed,
	}, rand.New(zeroRandSource{}))
	delete(allowed, 11)
	allowed[12] = struct{}{}
	iterator.SkipGroup(2)

	selection, err := iterator.Next()
	if err != nil || selection.CredentialID != 11 || selection.GroupID != 1 || selection.RouteMode != channel.RouteConverted {
		t.Fatalf("Next() = (%#v, %v), want frozen allowed converted credential 11", selection, err)
	}
	if _, err := iterator.Next(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("second Next() error = %v, want ErrExhausted", err)
	}
}

func TestIteratorRoutesModelLessResponsesResourceOperation(t *testing.T) {
	t.Parallel()

	snapshot, err := state.Compile(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ID: 7, Name: "responses", ChannelID: channel.OpenAI,
			Params: json.RawMessage(`{}`), Enabled: true,
		}},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	selection, err := New(snapshot, fakeCredentialSource{keys: []state.CredentialMeta{{ID: 71, GroupID: 7}}}, Query{
		ClientProtocol: protocol.OpenAIResponses,
		Operation:      execution.OperationResponsesRetrieve,
		ExternalModel:  nil,
	}, rand.New(zeroRandSource{})).Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if selection.CredentialID != 71 || selection.GroupID != 7 || selection.UpstreamModelID != nil ||
		selection.RouteMode != channel.RouteNative {
		t.Fatalf("Selection = %#v", selection)
	}
}

func TestCandidateGroupIDsForQueryUsesExecutionRoutesAndAccessKeyFilters(t *testing.T) {
	t.Parallel()

	snapshot := channelSchedulerSnapshot(t)
	query := Query{
		ClientProtocol: protocol.OpenAICompletions,
		Operation:      execution.OperationChatCompletion,
		ExternalModel:  modelPointer("public"),
		AccessKey: state.AccessKeyView{
			Status: state.AccessKeyStatusActive,
			Filters: state.FilterSet{
				Groups:    map[uint]struct{}{1: {}, 2: {}},
				Protocols: map[protocol.Protocol]struct{}{protocol.OpenAICompletions: {}},
				Models:    map[string]struct{}{"public": {}},
			},
		},
	}
	if got := CandidateGroupIDsForQuery(snapshot, query); !slices.Equal(got, []uint{2, 1}) {
		t.Fatalf("CandidateGroupIDsForQuery() = %#v, want native then converted [2 1]", got)
	}

	query.AccessKey.Filters.Groups = map[uint]struct{}{1: {}}
	if got := CandidateGroupIDsForQuery(snapshot, query); !slices.Equal(got, []uint{1}) {
		t.Fatalf("CandidateGroupIDsForQuery() with group filter = %#v, want [1]", got)
	}

	query.AccessKey.Filters.Protocols = map[protocol.Protocol]struct{}{protocol.Anthropic: {}}
	if got := CandidateGroupIDsForQuery(snapshot, query); len(got) != 0 {
		t.Fatalf("CandidateGroupIDsForQuery() with protocol filter = %#v, want empty", got)
	}
}

func TestCandidateGroupIDsForQuerySupportsModelLessResourceOperation(t *testing.T) {
	t.Parallel()

	snapshot, err := state.Compile(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{
			{
				ID: 7, Name: "official", ChannelID: channel.OpenAI,
				Params: json.RawMessage(`{}`), Enabled: true,
			},
			{
				ID: 8, Name: "compatible", ChannelID: channel.OpenAICompatible,
				Params: json.RawMessage(`{"base_url":"https://compatible.example/v1"}`), Enabled: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	required := schedulerFeatureSet(t, execution.FeatureNativeResourceSemantics)
	got := CandidateGroupIDsForQuery(snapshot, Query{
		ClientProtocol:   protocol.OpenAIResponses,
		Operation:        execution.OperationResponsesRetrieve,
		RequiredFeatures: required,
		ExternalModel:    nil,
		AccessKey:        state.AccessKeyView{Status: state.AccessKeyStatusActive},
	})
	if !slices.Equal(got, []uint{7}) {
		t.Fatalf("CandidateGroupIDsForQuery() = %#v, want native Responses group [7]", got)
	}
}

func TestOpenRouterRoutesResponsesWithReasoningOptOut(t *testing.T) {
	t.Parallel()

	snapshot, err := state.Compile(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ID: 9, Name: "openrouter", ChannelID: channel.OpenRouter,
			Params: json.RawMessage(`{}`), Enabled: true,
			Models: []state.ModelConfig{{ID: "openai/gpt-5.6-luna", Alias: "gpt-5.6-luna"}},
		}},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	request := &dialect.ParsedRequest{
		Method: http.MethodPost,
		Path:   "/v1/responses",
		Body:   []byte(`{"model":"gpt-5.6-luna","reasoning":{"effort":"none"},"store":false}`),
	}
	metadata, err := dialect.NewOpenAIResponses().InspectRequest(request)
	if err != nil {
		t.Fatalf("InspectRequest() error = %v", err)
	}
	if metadata.RequiredFeatures.Has(execution.FeatureReasoning) {
		t.Fatal("reasoning effort none must not require reasoning capability")
	}
	selection, err := New(snapshot, fakeCredentialSource{keys: []state.CredentialMeta{{
		ID: 91, GroupID: 9,
	}}}, Query{
		ClientProtocol:   protocol.OpenAIResponses,
		Operation:        metadata.Operation,
		RequiredFeatures: metadata.RequiredFeatures,
		ExternalModel:    modelPointer("gpt-5.6-luna"),
		AccessKey:        state.AccessKeyView{Status: state.AccessKeyStatusActive},
	}, rand.New(zeroRandSource{})).Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if selection.GroupID != 9 || selection.ChannelID != channel.OpenRouter ||
		selection.RouteMode != channel.RouteNative || selection.UpstreamModelID == nil ||
		*selection.UpstreamModelID != "openai/gpt-5.6-luna" {
		t.Fatalf("Selection = %#v, want native OpenRouter target", selection)
	}
}

func TestCandidateGroupIDsForQueryRejectsUnsupportedCapability(t *testing.T) {
	t.Parallel()

	got := CandidateGroupIDsForQuery(channelSchedulerSnapshot(t), Query{
		ClientProtocol:   protocol.OpenAICompletions,
		Operation:        execution.OperationChatCompletion,
		RequiredFeatures: schedulerFeatureSet(t, execution.FeatureReasoning),
		ExternalModel:    modelPointer("converted-only"),
		AccessKey:        state.AccessKeyView{Status: state.AccessKeyStatusActive},
	})
	if len(got) != 0 {
		t.Fatalf("CandidateGroupIDsForQuery() = %#v, want empty for unsupported capability", got)
	}
}

func TestCapabilityUnsupportedIsStableAndInspectionIsNeutral(t *testing.T) {
	t.Parallel()

	snapshot := channelSchedulerSnapshot(t)
	required := schedulerFeatureSet(t, execution.FeatureReasoning)
	query := Query{
		ClientProtocol:   protocol.OpenAICompletions,
		Operation:        execution.OperationChatCompletion,
		RequiredFeatures: required,
		ExternalModel:    modelPointer("converted-only"),
		AccessKey:        state.AccessKeyView{Status: state.AccessKeyStatusActive},
	}
	iterator := New(snapshot, fakeCredentialSource{keys: []state.CredentialMeta{{ID: 11, GroupID: 1}}}, query, rand.New(zeroRandSource{}))
	if iterator.StaticReason() != ReasonCapabilityUnsupported {
		t.Fatalf("StaticReason() = %q, want %q", iterator.StaticReason(), ReasonCapabilityUnsupported)
	}
	if _, err := iterator.Next(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("Next() error = %v, want ErrExhausted", err)
	}

	inspection, err := Inspect(snapshot, []state.CredentialRuntimeView{{
		ID: 11, GroupID: 1, Status: state.CredentialStatusActive, WeightAuto: state.DefaultWeight,
	}}, query, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if inspection.Routable || inspection.Reason != ReasonCapabilityUnsupported ||
		inspection.ClientProtocol != protocol.OpenAICompletions || inspection.Operation != execution.OperationChatCompletion ||
		len(inspection.RequiredFeatures) != 1 || inspection.RequiredFeatures[0] != execution.FeatureReasoning || len(inspection.Groups) != 1 {
		t.Fatalf("Inspection = %#v", inspection)
	}
	group := inspection.Groups[0]
	if group.ChannelID != channel.Anthropic || group.RouteMode != channel.RouteConverted || group.CapabilitySupported ||
		group.Reason != ReasonCapabilityUnsupported || len(group.Credentials) != 0 {
		t.Fatalf("GroupInspection = %#v", group)
	}
	encoded, err := json.Marshal(inspection)
	if err != nil {
		t.Fatalf("json.Marshal(Inspection) error = %v", err)
	}
	implementationName := "bif" + "rost"
	if strings.Contains(string(encoded), "target_config") || strings.Contains(string(encoded), "base_url") ||
		strings.Contains(strings.ToLower(string(encoded)), implementationName) {
		t.Fatalf("Inspection leaked execution implementation details: %s", encoded)
	}
}

func TestInspectionExplainsAllowedCredentialScope(t *testing.T) {
	t.Parallel()

	inspection, err := Inspect(channelSchedulerSnapshot(t), []CredentialRuntimeView{
		{ID: 11, GroupID: 1, Status: state.CredentialStatusActive},
		{ID: 12, GroupID: 1, Status: state.CredentialStatusActive},
		{ID: 21, GroupID: 2, Status: state.CredentialStatusActive},
	}, Query{
		ClientProtocol:       protocol.OpenAICompletions,
		Operation:            execution.OperationChatCompletion,
		ExternalModel:        modelPointer("public"),
		AccessKey:            state.AccessKeyView{Status: state.AccessKeyStatusActive},
		AllowedCredentialIDs: map[uint]struct{}{11: {}},
	}, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if !inspection.Routable || len(inspection.Groups) != 2 {
		t.Fatalf("Inspection = %#v", inspection)
	}
	native, converted := inspection.Groups[0], inspection.Groups[1]
	if native.RouteMode != channel.RouteNative || native.Routable ||
		native.Reason != ReasonNoAvailableCredential || len(native.Credentials) != 1 ||
		native.Credentials[0].Reason != ReasonCredentialNotAllowed {
		t.Fatalf("native GroupInspection = %#v", native)
	}
	if converted.RouteMode != channel.RouteConverted || !converted.Routable || len(converted.Credentials) != 2 ||
		!converted.Credentials[0].Available || converted.Credentials[0].CredentialID != 11 ||
		converted.Credentials[1].Reason != ReasonCredentialNotAllowed {
		t.Fatalf("converted GroupInspection = %#v", converted)
	}
}

func channelSchedulerSnapshot(t *testing.T) *state.ConfigSnapshot {
	t.Helper()
	snapshot, err := state.Compile(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{
			{
				ID: 1, Name: "converted", ChannelID: channel.Anthropic, Params: json.RawMessage(`{}`),
				Models: []state.ModelConfig{
					{ID: "converted-model", Alias: "public"},
					{ID: "converted-only", Alias: "converted-only"},
				},
				Enabled: true,
			},
			{
				ID: 2, Name: "native", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
				Models: []state.ModelConfig{{ID: "native-model", Alias: "public"}}, Enabled: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	return snapshot
}

func schedulerFeatureSet(t *testing.T, features ...execution.Feature) execution.FeatureSet {
	t.Helper()
	set, err := execution.NewFeatureSet(features...)
	if err != nil {
		t.Fatalf("NewFeatureSet() error = %v", err)
	}
	return set
}
