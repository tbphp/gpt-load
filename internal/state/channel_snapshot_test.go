package state

import (
	"encoding/json"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestCompileBuildsOperationAwareChannelCandidates(t *testing.T) {
	t.Parallel()

	input := CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []GroupConfig{{ConnectionType: "api_key", ID: 7, Name: "anthropic", ChannelID: channel.Anthropic,
			Params:  json.RawMessage(`{}`),
			Models:  []ModelConfig{{ID: "claude-upstream", Alias: "claude-public"}},
			Enabled: true,
		}},
		Credentials: []CredentialConfig{{
			ID: 31, GroupID: 7, Status: CredentialStatusActive,
			Version: 1, IdentityGeneration: 1, Fingerprint: "fingerprint-31",
		}},
	}

	snapshot, err := Compile(input)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	tests := []struct {
		clientProtocol protocol.Protocol
		operation      execution.Operation
		wantMode       channel.RouteMode
	}{
		{protocol.Anthropic, execution.OperationChatCompletion, channel.RouteNative},
		{protocol.OpenAICompletions, execution.OperationChatCompletion, channel.RouteConverted},
		{protocol.Gemini, execution.OperationChatCompletion, channel.RouteConverted},
		{protocol.OpenAIResponses, execution.OperationResponsesCreate, channel.RouteConverted},
	}
	for _, test := range tests {
		got := snapshot.ExecutionCandidates[test.clientProtocol][test.operation]["claude-public"]
		if len(got) != 1 {
			t.Fatalf("ExecutionCandidates[%q][%q] = %#v, want one", test.clientProtocol, test.operation, got)
		}
		if got[0].GroupID != 7 || got[0].UpstreamModelID != "claude-upstream" || got[0].Mode != test.wantMode {
			t.Errorf("candidate = %#v, want group 7, upstream model and mode %q", got[0], test.wantMode)
		}
		if got[0].ResolvedTarget.ChannelID != channel.Anthropic || got[0].ResolvedTarget.CatalogProviderID != "anthropic" {
			t.Errorf("candidate target = %#v", got[0].ResolvedTarget)
		}
	}

	view := snapshot.Groups[7]
	if view.ChannelID != channel.Anthropic || string(view.Params) != `{}` || view.ResolvedTarget.ChannelID != channel.Anthropic {
		t.Fatalf("GroupView channel fields = %#v", view)
	}
	if len(view.ClientProtocols) != 4 {
		t.Fatalf("GroupView.ClientProtocols = %#v, want registry-derived protocols", view.ClientProtocols)
	}
}

func TestCompileSelectsNativeVertexGeminiModePerModel(t *testing.T) {
	t.Parallel()

	snapshot, err := Compile(CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []GroupConfig{{ConnectionType: "api_key", ID: 9, Name: "vertex", ChannelID: channel.GoogleVertex,
			Params: json.RawMessage(`{}`),
			Models: []ModelConfig{
				{ID: "gemini-2.5-pro", Alias: "gemini-public"},
				{ID: "claude-sonnet-4", Alias: "claude-public"},
			},
			Enabled: true,
		}},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	geminiTarget := snapshot.ExecutionCandidates[protocol.Gemini][execution.OperationChatCompletion]["gemini-public"]
	if len(geminiTarget) != 1 || geminiTarget[0].Mode != channel.RouteNative {
		t.Fatalf("Vertex Gemini candidates = %#v, want native", geminiTarget)
	}
	claudeTarget := snapshot.ExecutionCandidates[protocol.Gemini][execution.OperationChatCompletion]["claude-public"]
	if len(claudeTarget) != 1 || claudeTarget[0].Mode != channel.RouteConverted {
		t.Fatalf("Vertex Claude candidates = %#v, want converted", claudeTarget)
	}
	if got := string(snapshot.Groups[9].Params); got != `{"location":"global"}` {
		t.Fatalf("Vertex group params = %s, want default global location", got)
	}
}

func TestCompileIndexesNativeResponsesResourceOperationsWithoutModels(t *testing.T) {
	t.Parallel()

	snapshot, err := Compile(CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []GroupConfig{
			{ConnectionType: "api_key", ID: 1, Name: "openai", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`), Enabled: true},
			{ConnectionType: "api_key", ID: 2, Name: "compatible", ChannelID: channel.OpenAICompatible, Params: json.RawMessage(`{"base_url":"https://proxy.example/v1"}`), Enabled: true},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	for _, operation := range []execution.Operation{
		execution.OperationResponsesRetrieve,
		execution.OperationResponsesDelete,
		execution.OperationResponsesCancel,
		execution.OperationResponsesInputItems,
	} {
		got := snapshot.ExecutionCandidates[protocol.OpenAIResponses][operation][NoModelRouteKey]
		if len(got) != 1 || got[0].GroupID != 1 || got[0].Mode != channel.RouteNative {
			t.Errorf("resource candidates for %q = %#v, want native OpenAI only", operation, got)
		}
	}
	if got := snapshot.ExecutionCandidates[protocol.OpenAIResponses][execution.OperationResponsesCreate]; len(got) != 0 {
		t.Fatalf("model-less Responses create candidates = %#v, want none", got)
	}
}

func TestCompileIndexesAllNativeResponsesExtensions(t *testing.T) {
	t.Parallel()

	snapshot, err := Compile(CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []GroupConfig{
			{ConnectionType: "api_key", ID: 1, ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`), Models: []ModelConfig{{ID: "official-upstream", Alias: "public"}}, Enabled: true},
			{ConnectionType: "api_key", ID: 2, ChannelID: channel.OpenAICompatible, Params: json.RawMessage(`{"base_url":"https://proxy.example/v1"}`), Models: []ModelConfig{{ID: "compatible-upstream", Alias: "public"}}, Enabled: true},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	for _, operation := range []execution.Operation{
		execution.OperationResponsesCompact,
		execution.OperationResponsesInputTokens,
	} {
		got := snapshot.ExecutionCandidates[protocol.OpenAIResponses][operation]["public"]
		if len(got) != 1 || got[0].UpstreamModelID != "official-upstream" {
			t.Errorf("model candidates for %q = %#v", operation, got)
		}
	}

	noModel := snapshot.ExecutionCandidates[protocol.OpenAIResponses][execution.OperationResponsesPassthrough][NoModelRouteKey]
	if len(noModel) != 1 || noModel[0].GroupID != 1 {
		t.Fatalf("model-less passthrough candidates = %#v", noModel)
	}
	withModel := snapshot.ExecutionCandidates[protocol.OpenAIResponses][execution.OperationResponsesPassthrough]["public"]
	if len(withModel) != 1 || withModel[0].UpstreamModelID != "official-upstream" {
		t.Fatalf("model passthrough candidates = %#v", withModel)
	}
}

func TestCompileOrdersNativeTargetsBeforeConvertedTargets(t *testing.T) {
	t.Parallel()

	snapshot, err := Compile(CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []GroupConfig{
			{ConnectionType: "api_key", ID: 1, ChannelID: channel.Anthropic, Params: json.RawMessage(`{}`), Models: []ModelConfig{{ID: "converted", Alias: "public"}}, Enabled: true},
			{ConnectionType: "api_key", ID: 2, ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`), Models: []ModelConfig{{ID: "native", Alias: "public"}}, Enabled: true},
		},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	targets := snapshot.ExecutionCandidates[protocol.OpenAICompletions][execution.OperationChatCompletion]["public"]
	if len(targets) != 2 || targets[0].GroupID != 2 || targets[0].Mode != channel.RouteNative ||
		targets[1].GroupID != 1 || targets[1].Mode != channel.RouteConverted {
		t.Fatalf("targets = %#v, want native before converted", targets)
	}
}

func TestCompileChannelSnapshotOwnsInputData(t *testing.T) {
	t.Parallel()

	weight := 12
	params := json.RawMessage(`{"base_url":"https://proxy.example/v1/"}`)
	input := CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []GroupConfig{{ConnectionType: "api_key", ID: 1, ChannelID: channel.OpenAICompatible, Params: params,
			Models:       []ModelConfig{{ID: "upstream", Alias: "public"}},
			WeightManual: &weight, Enabled: true,
		}},
	}
	snapshot, err := Compile(input)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	params[2] = 'X'
	input.Groups[0].Models[0] = ModelConfig{ID: "changed", Alias: "changed"}
	weight = 99

	view := snapshot.Groups[1]
	if string(view.Params) != `{"base_url":"https://proxy.example/v1"}` {
		t.Fatalf("GroupView.Params = %s", view.Params)
	}
	if len(view.Models) != 1 || view.Models[0].ID != "upstream" || view.Models[0].Alias != "public" {
		t.Fatalf("GroupView.Models = %#v", view.Models)
	}
	if view.WeightManual == nil || *view.WeightManual != 12 {
		t.Fatalf("GroupView.WeightManual = %v", view.WeightManual)
	}
	target := snapshot.ExecutionCandidates[protocol.OpenAICompletions][execution.OperationChatCompletion]["public"][0]
	if string(target.ResolvedTarget.TargetConfig) != `{"base_url":"https://proxy.example/v1"}` {
		t.Fatalf("candidate TargetConfig = %s", target.ResolvedTarget.TargetConfig)
	}
	if target.UpstreamModelID != "upstream" {
		t.Fatalf("candidate upstream model = %q", target.UpstreamModelID)
	}
}

func TestCompileRejectsInvalidChannelAndCredentialConfiguration(t *testing.T) {
	t.Parallel()

	invalidWeight := -1
	tests := []struct {
		name    string
		input   CompileInput
		wantErr string
	}{
		{
			name:    "missing registry",
			input:   CompileInput{Groups: []GroupConfig{{ConnectionType: "api_key", ID: 1, ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`)}}},
			wantErr: "channel registry is required",
		},
		{
			name:    "unknown channel",
			input:   CompileInput{ChannelRegistry: channel.NewRegistry(), Groups: []GroupConfig{{ConnectionType: "api_key", ID: 1, ChannelID: channel.ID("unknown"), Params: json.RawMessage(`{}`)}}},
			wantErr: "unknown channel",
		},
		{
			name:    "invalid params",
			input:   CompileInput{ChannelRegistry: channel.NewRegistry(), Groups: []GroupConfig{{ConnectionType: "api_key", ID: 1, ChannelID: channel.OpenAICompatible, Params: json.RawMessage(`{}`)}}},
			wantErr: "params.base_url",
		},
		{
			name: "credential unknown group",
			input: CompileInput{ChannelRegistry: channel.NewRegistry(), Credentials: []CredentialConfig{{
				ID: 1, GroupID: 99, Status: CredentialStatusActive,
				Version: 1, IdentityGeneration: 1, Fingerprint: "fingerprint-1",
			}}},
			wantErr: "unknown group",
		},
		{
			name: "duplicate credential",
			input: CompileInput{ChannelRegistry: channel.NewRegistry(), Groups: []GroupConfig{{ConnectionType: "api_key", ID: 1, ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`)}}, Credentials: []CredentialConfig{
				{ID: 1, GroupID: 1, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "fingerprint-1"},
				{ID: 1, GroupID: 1, Status: CredentialStatusDisabled, Version: 2, IdentityGeneration: 2, Fingerprint: "fingerprint-2"},
			}},
			wantErr: "duplicate credential id",
		},
		{
			name: "invalid credential status",
			input: CompileInput{ChannelRegistry: channel.NewRegistry(), Groups: []GroupConfig{{ConnectionType: "api_key", ID: 1, ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`)}}, Credentials: []CredentialConfig{{
				ID: 1, GroupID: 1, Status: CredentialStatus("revoked"),
				Version: 1, IdentityGeneration: 1, Fingerprint: "fingerprint-1",
			}}},
			wantErr: "invalid status",
		},
		{
			name: "invalid credential weight",
			input: CompileInput{ChannelRegistry: channel.NewRegistry(), Groups: []GroupConfig{{ConnectionType: "api_key", ID: 1, ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`)}}, Credentials: []CredentialConfig{{
				ID: 1, GroupID: 1, Status: CredentialStatusActive, WeightManual: &invalidWeight,
				Version: 1, IdentityGeneration: 1, Fingerprint: "fingerprint-1",
			}}},
			wantErr: "manual weight",
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
