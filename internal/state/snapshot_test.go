package state

import (
	"encoding/json"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"gpt-load/internal/accessquota"
	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
)

func TestCompileIndexesExternalModelsAndPreservesUpstreamIDs(t *testing.T) {
	t.Parallel()

	input := CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []GroupConfig{
			{ConnectionType: "api_key", ID: 1, Name: "one", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
				Models: []ModelConfig{
					{ID: "provider-a", Alias: "public"},
					{ID: "provider-a", Alias: "secondary"},
					{ID: "plain"},
				},
				Enabled: true,
			},
			{ConnectionType: "api_key", ID: 2, Name: "two", ChannelID: channel.OpenAICompatible,
				Params:  json.RawMessage(`{"base_url":"https://proxy.example/v1"}`),
				Models:  []ModelConfig{{ID: "provider-b", Alias: "public"}},
				Enabled: true,
			},
		},
	}

	snapshot, err := Compile(input)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	index := snapshot.ExecutionCandidates[protocol.OpenAICompletions][execution.OperationChatCompletion]
	public := index["public"]
	if len(public) != 2 || public[0].UpstreamModelID != "provider-a" || public[1].UpstreamModelID != "provider-b" {
		t.Fatalf("public targets = %#v", public)
	}
	if got := index["secondary"]; len(got) != 1 || got[0].UpstreamModelID != "provider-a" {
		t.Fatalf("secondary targets = %#v", got)
	}
	if got := index["plain"]; len(got) != 1 || got[0].UpstreamModelID != "plain" {
		t.Fatalf("plain targets = %#v", got)
	}
	if _, exists := index["provider-a"]; exists {
		t.Fatal("aliased upstream id entered external index")
	}
}

func TestCompileIndexesOpenAIImagesOperationsForAllConfiguredModels(t *testing.T) {
	t.Parallel()

	snapshot, err := Compile(CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []GroupConfig{
			{ConnectionType: "api_key", ID: 1, ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
				Models: []ModelConfig{{ID: "official-image", Alias: "public"}}, Enabled: true},
			{ConnectionType: "api_key", ID: 2, ChannelID: channel.OpenAICompatible,
				Params: json.RawMessage(`{"base_url":"https://proxy.example/api/v4"}`),
				Models: []ModelConfig{{ID: "compatible-image", Alias: "public"}}, Enabled: true},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	for _, operation := range []execution.Operation{execution.OperationImagesGenerate, execution.OperationImagesEdit} {
		got := snapshot.ExecutionCandidates[protocol.OpenAIImages][operation]["public"]
		if len(got) != 2 || got[0].GroupID != 1 || got[1].GroupID != 2 ||
			got[0].Mode != channel.RouteNative || got[1].Mode != channel.RouteNative {
			t.Errorf("Images candidates for %q = %#v", operation, got)
		}
	}
}

func TestCompileIndexesOpenAIEmbeddingsForAllConfiguredModels(t *testing.T) {
	t.Parallel()

	snapshot, err := Compile(CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []GroupConfig{
			{ConnectionType: "api_key", ID: 1, ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
				Models: []ModelConfig{{ID: "text-embedding-3-small", Alias: "public"}}, Enabled: true},
			{ConnectionType: "api_key", ID: 2, ChannelID: channel.OpenAICompatible,
				Params: json.RawMessage(`{"base_url":"https://proxy.example/api/v4"}`),
				Models: []ModelConfig{{ID: "Qwen/Qwen3-Embedding-8B", Alias: "public"}}, Enabled: true},
			{ConnectionType: "api_key", ID: 3, ChannelID: channel.OpenRouter, Params: json.RawMessage(`{}`),
				Models: []ModelConfig{{ID: "openai/text-embedding-3-small", Alias: "public"}}, Enabled: true},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	got := snapshot.ExecutionCandidates[protocol.OpenAIEmbeddings][execution.OperationEmbeddingsCreate]["public"]
	if len(got) != 3 || got[0].GroupID != 1 || got[1].GroupID != 2 || got[2].GroupID != 3 {
		t.Fatalf("Embeddings candidates = %#v", got)
	}
	for _, target := range got {
		if target.Mode != channel.RouteNative {
			t.Fatalf("Embeddings target = %#v, want native", target)
		}
	}
}

func TestCompileSubscriptionPublishesOnlyVerifiedCodexOperations(t *testing.T) {
	t.Parallel()
	snapshot, err := Compile(CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []GroupConfig{{
			ID: 1, Name: "subscription", ChannelID: channel.Codex, ConnectionType: "subscription",
			Params: json.RawMessage(`{}`), Models: []ModelConfig{{ID: "gpt-5", Alias: "public"}}, Enabled: true,
		}},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if got := snapshot.ExecutionCandidates[protocol.OpenAICompletions][execution.OperationChatCompletion]["public"]; len(got) != 1 {
		t.Fatalf("chat targets = %#v", got)
	}
	if got := snapshot.ExecutionCandidates[protocol.OpenAIResponses][execution.OperationResponsesCreate]["public"]; len(got) != 1 {
		t.Fatalf("responses create targets = %#v", got)
	}
	if got := snapshot.ExecutionCandidates[protocol.OpenAIResponses][execution.OperationResponsesInputTokens]["public"]; len(got) != 1 {
		t.Fatalf("responses input token targets = %#v", got)
	}
	if got := snapshot.ExecutionCandidates[protocol.Anthropic][execution.OperationCountTokens]["public"]; len(got) != 1 {
		t.Fatalf("Anthropic count token targets = %#v", got)
	}
	if got := snapshot.ExecutionCandidates[protocol.Gemini][execution.OperationCountTokens]["public"]; len(got) != 1 {
		t.Fatalf("Gemini count token targets = %#v", got)
	}
	if got := snapshot.ExecutionCandidates[protocol.OpenAIEmbeddings]; len(got) != 0 {
		t.Fatalf("subscription Embeddings targets = %#v, want none", got)
	}
	for _, operation := range []execution.Operation{
		execution.OperationResponsesRetrieve, execution.OperationResponsesDelete,
		execution.OperationResponsesCancel, execution.OperationResponsesCompact,
		execution.OperationResponsesInputItems,
		execution.OperationResponsesPassthrough,
	} {
		if got := snapshot.ExecutionCandidates[protocol.OpenAIResponses][operation]; len(got) != 0 {
			t.Fatalf("unsupported subscription operation %q was published: %#v", operation, got)
		}
	}
}

func TestCompileBuildsManagementCatalogsWithoutChangingActiveIndexes(t *testing.T) {
	t.Parallel()

	disabledWeight := 20
	input := CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []GroupConfig{
			{ConnectionType: "api_key", ID: 2, Name: "disabled", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
				Models: []ModelConfig{{ID: "provider-disabled", Alias: "public"}}, WeightManual: &disabledWeight,
			},
			{ConnectionType: "api_key", ID: 1, Name: "active", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
				Models: []ModelConfig{{ID: "provider-active", Alias: "public"}}, Enabled: true,
			},
		},
		AccessKeys: []AccessKeyConfig{
			{ID: 11, Name: "active-client", KeyHash: "active-hash", Status: AccessKeyStatusActive, Filters: FilterSet{Groups: map[uint]struct{}{1: {}}}, RPMLimit: 10},
			{ID: 12, Name: "disabled-client", KeyHash: "disabled-hash", Status: AccessKeyStatusDisabled, Filters: FilterSet{Models: map[string]struct{}{"public": {}}}, RPMLimit: 20},
		},
	}

	snapshot, err := Compile(input)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if len(snapshot.Groups) != 1 || snapshot.Groups[1].Name != "active" {
		t.Fatalf("active Groups = %#v", snapshot.Groups)
	}
	active := snapshot.ExecutionCandidates[protocol.OpenAICompletions][execution.OperationChatCompletion]["public"]
	if len(active) != 1 || active[0].GroupID != 1 {
		t.Fatalf("active candidates = %#v", active)
	}
	routes := snapshot.ExecutionRouteCatalog[protocol.OpenAICompletions][execution.OperationChatCompletion]["public"]
	if len(routes) != 2 || routes[0].GroupID != 1 || routes[1].GroupID != 2 {
		t.Fatalf("route catalog = %#v", routes)
	}
	if got := snapshot.GroupCatalog[2]; got.Enabled || got.WeightManual == nil || *got.WeightManual != 20 {
		t.Fatalf("disabled group catalog = %#v", got)
	}
	if _, ok := snapshot.AccessKeysByHash["disabled-hash"]; ok {
		t.Fatal("disabled access key entered active hash index")
	}
	if got := snapshot.AccessKeysByID[12]; got.Status != AccessKeyStatusDisabled || got.RPMLimit != 20 {
		t.Fatalf("disabled access key catalog = %#v", got)
	}
}

func TestCompileCarriesSettingsAndValidationModel(t *testing.T) {
	t.Parallel()

	snapshot, err := Compile(CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		SystemSettings:  config.Settings{"first_byte_timeout": json.Number("20")},
		Groups: []GroupConfig{{ConnectionType: "api_key", ID: 1, Name: "one", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
			ValidationModel: "  probe-model  ",
			Models:          []ModelConfig{{ID: "real-model", Alias: "public-model"}},
			Settings:        config.Settings{"request_timeout": json.Number("30")}, Enabled: true,
		}},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	view := snapshot.Groups[1]
	if view.ValidationModel != "probe-model" || view.Timeouts.FirstByte.Seconds() != 20 || view.Timeouts.Request.Seconds() != 30 {
		t.Fatalf("group runtime view = %#v", view)
	}
}

func TestCompileOwnsInputData(t *testing.T) {
	t.Parallel()

	weight := 25
	expiresAtMS := int64(1_900_000_000_000)
	filters := FilterSet{
		Groups:    map[uint]struct{}{1: {}},
		Protocols: map[protocol.Protocol]struct{}{protocol.OpenAICompletions: {}},
		Models:    map[string]struct{}{"public": {}},
	}
	input := CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []GroupConfig{{ConnectionType: "api_key", ID: 1, ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
			Models: []ModelConfig{{ID: "upstream", Alias: "public"}}, WeightManual: &weight, Enabled: true,
		}},
		AccessKeys: []AccessKeyConfig{{
			ID: 1, KeyHash: "hash", Status: AccessKeyStatusActive, Filters: filters,
			ExpiresAtMS: &expiresAtMS,
			AllowedPeerCIDRs: []netip.Prefix{
				netip.MustParsePrefix("192.0.2.0/24"),
			},
			CostLimitRules: []accessquota.Rule{{
				ID: 7, Revision: 1, Kind: accessquota.KindTotal, LimitNanoUSD: 100,
			}},
		}},
	}
	snapshot, err := Compile(input)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	input.Groups[0].Models[0] = ModelConfig{ID: "changed"}
	weight = 99
	filters.Groups[2] = struct{}{}
	filters.Protocols[protocol.Gemini] = struct{}{}
	filters.Models["changed"] = struct{}{}
	expiresAtMS = 1
	input.AccessKeys[0].AllowedPeerCIDRs[0] = netip.MustParsePrefix("198.51.100.0/24")
	input.AccessKeys[0].CostLimitRules[0].LimitNanoUSD = 1

	view := snapshot.Groups[1]
	if !reflect.DeepEqual(view.Models, []ModelConfig{{ID: "upstream", Alias: "public"}}) || view.WeightManual == nil || *view.WeightManual != 25 {
		t.Fatalf("group view changed with input = %#v", view)
	}
	gotFilters := snapshot.AccessKeysByID[1].Filters
	if _, ok := gotFilters.Groups[2]; ok {
		t.Fatal("group filter retained caller mutation")
	}
	if _, ok := gotFilters.Protocols[protocol.Gemini]; ok {
		t.Fatal("protocol filter retained caller mutation")
	}
	if _, ok := gotFilters.Models["changed"]; ok {
		t.Fatal("model filter retained caller mutation")
	}
	if got := snapshot.AccessKeysByID[1].CostLimitRules; len(got) != 1 || got[0].LimitNanoUSD != 100 {
		t.Fatalf("cost limit rules changed with input = %#v", got)
	}
	accessKey := snapshot.AccessKeysByID[1]
	if accessKey.ExpiresAtMS == nil || *accessKey.ExpiresAtMS != 1_900_000_000_000 {
		t.Fatalf("access key expiry changed with input = %#v", accessKey.ExpiresAtMS)
	}
	if !reflect.DeepEqual(accessKey.AllowedPeerCIDRs, []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}) {
		t.Fatalf("access key CIDRs changed with input = %#v", accessKey.AllowedPeerCIDRs)
	}
}

func TestCompileRejectsInvalidCoreConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   CompileInput
		wantErr string
	}{
		{
			name:    "duplicate external model",
			input:   CompileInput{ChannelRegistry: channel.NewRegistry(), Groups: []GroupConfig{{ConnectionType: "api_key", ID: 1, ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`), Models: []ModelConfig{{ID: "a"}, {ID: "b", Alias: "a"}}, Enabled: true}}},
			wantErr: "duplicate external model",
		},
		{
			name: "duplicate group id",
			input: CompileInput{ChannelRegistry: channel.NewRegistry(), Groups: []GroupConfig{
				{ConnectionType: "api_key", ID: 1, ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`)},
				{ConnectionType: "api_key", ID: 1, ChannelID: channel.Anthropic, Params: json.RawMessage(`{}`)},
			}},
			wantErr: "duplicate group id",
		},
		{
			name: "duplicate access key hash",
			input: CompileInput{AccessKeys: []AccessKeyConfig{
				{ID: 1, KeyHash: "same", Status: AccessKeyStatusActive},
				{ID: 2, KeyHash: "same", Status: AccessKeyStatusActive},
			}},
			wantErr: "duplicate access key hash",
		},
		{
			name: "invalid cost limit rules",
			input: CompileInput{AccessKeys: []AccessKeyConfig{{
				ID: 1, KeyHash: "hash", Status: AccessKeyStatusActive,
				CostLimitRules: []accessquota.Rule{
					{ID: 1, Revision: 1, Kind: accessquota.KindTotal, LimitNanoUSD: 100},
					{ID: 2, Revision: 1, Kind: accessquota.KindTotal, LimitNanoUSD: 200},
				},
			}}},
			wantErr: "cost limit",
		},
		{
			name: "negative access key expiry",
			input: CompileInput{AccessKeys: []AccessKeyConfig{{
				ID: 1, KeyHash: "hash", Status: AccessKeyStatusActive,
				ExpiresAtMS: int64Pointer(-1),
			}}},
			wantErr: "expiry",
		},
		{
			name: "unsafe access key expiry",
			input: CompileInput{AccessKeys: []AccessKeyConfig{{
				ID: 1, KeyHash: "hash", Status: AccessKeyStatusActive,
				ExpiresAtMS: int64Pointer(9_007_199_254_740_992),
			}}},
			wantErr: "expiry",
		},
		{
			name: "invalid allowed peer cidr",
			input: CompileInput{AccessKeys: []AccessKeyConfig{{
				ID: 1, KeyHash: "hash", Status: AccessKeyStatusActive,
				AllowedPeerCIDRs: []netip.Prefix{{}},
			}}},
			wantErr: "CIDR",
		},
		{
			name: "non canonical allowed peer cidr",
			input: CompileInput{AccessKeys: []AccessKeyConfig{{
				ID: 1, KeyHash: "hash", Status: AccessKeyStatusActive,
				AllowedPeerCIDRs: []netip.Prefix{netip.MustParsePrefix("192.0.2.99/24")},
			}}},
			wantErr: "CIDR",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Compile(test.input)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Compile() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func int64Pointer(value int64) *int64 { return &value }
