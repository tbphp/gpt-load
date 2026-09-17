package cpa

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"

	"github.com/tidwall/gjson"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestSubscriptionConvertedFunctionAllowlistsPreserveConstraints(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name          string
		providerKind  channel.ProviderKind
		protocol      protocol.Protocol
		upstreamModel string
		body          string
		toolsPath     string
	}{
		{
			name: "Responses to Claude", providerKind: channel.ProviderClaude, protocol: protocol.OpenAIResponses,
			body:      `{"model":"client-model","input":"hello","store":false,"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}},{"type":"function","name":"summarize","parameters":{"type":"object"}}],"tool_choice":{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"lookup"}]}}`,
			toolsPath: "tools",
		},
		{
			name: "Responses to Antigravity", providerKind: channel.ProviderAntigravity, protocol: protocol.OpenAIResponses,
			upstreamModel: "gemini-2.5-pro",
			body:          `{"model":"client-model","input":"hello","store":false,"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}},{"type":"function","name":"summarize","parameters":{"type":"object"}}],"tool_choice":{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"lookup"}]}}`,
			toolsPath:     "tools",
		},
		{
			name: "Chat to Claude", providerKind: channel.ProviderClaude, protocol: protocol.OpenAICompletions,
			body:      `{"model":"client-model","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}},{"type":"function","function":{"name":"summarize","parameters":{"type":"object"}}}],"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[{"type":"function","function":{"name":"lookup"}}]}}}`,
			toolsPath: "tools",
		},
		{
			name: "Chat to Antigravity", providerKind: channel.ProviderAntigravity, protocol: protocol.OpenAICompletions,
			upstreamModel: "gemini-2.5-pro",
			body:          `{"model":"client-model","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}},{"type":"function","function":{"name":"summarize","parameters":{"type":"object"}}}],"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[{"type":"function","function":{"name":"lookup"}}]}}}`,
			toolsPath:     "tools",
		},
		{
			name: "Chat to Codex", providerKind: channel.ProviderCodex, protocol: protocol.OpenAICompletions,
			body:      `{"model":"client-model","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}},{"type":"function","function":{"name":"summarize","parameters":{"type":"object"}}}],"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[{"type":"function","function":{"name":"lookup"}}]}}}`,
			toolsPath: "tools",
		},
		{
			name: "Chat to Grok", providerKind: channel.ProviderGrok, protocol: protocol.OpenAICompletions,
			body:      `{"model":"client-model","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}},{"type":"function","function":{"name":"summarize","parameters":{"type":"object"}}}],"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[{"type":"function","function":{"name":"lookup"}}]}}}`,
			toolsPath: "tools",
		},
		{
			name: "Gemini to Claude", providerKind: channel.ProviderClaude, protocol: protocol.Gemini,
			body:      `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object"}},{"name":"summarize","parameters":{"type":"object"}}]}],"toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["lookup"]}}}`,
			toolsPath: "tools.0.functionDeclarations",
		},
		{
			name: "Gemini to Codex", providerKind: channel.ProviderCodex, protocol: protocol.Gemini,
			body:      `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object"}},{"name":"summarize","parameters":{"type":"object"}}]}],"toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["lookup"]}}}`,
			toolsPath: "tools.0.functionDeclarations",
		},
		{
			name: "Gemini to Grok", providerKind: channel.ProviderGrok, protocol: protocol.Gemini,
			body:      `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{"type":"object"}},{"name":"summarize","parameters":{"type":"object"}}]}],"toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["lookup"]}}}`,
			toolsPath: "tools.0.functionDeclarations",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := execution.AttemptSpec{
				ClientProtocol: test.protocol,
				RouteMode:      execution.RouteConverted,
				Operation:      execution.OperationChatCompletion,
				UpstreamModel:  test.upstreamModel,
				Body:           []byte(test.body),
			}
			if test.protocol == protocol.OpenAIResponses {
				spec.Operation = execution.OperationResponsesCreate
			}
			original := append([]byte(nil), spec.Body...)
			prepared, evidence := prepareConvertedFidelity(spec, test.providerKind)
			if evidence != nil {
				t.Fatalf("valid function allowlist rejected: %+v", evidence)
			}
			if got := int(gjson.GetBytes(prepared.Body, test.toolsPath+".#").Int()); got != 1 {
				t.Fatalf("prepared tools = %d, want 1: %s", got, prepared.Body)
			}
			if got := subscriptionPreparedToolName(test.protocol, prepared.Body); got != "lookup" {
				t.Fatalf("prepared tool = %q, want lookup: %s", got, prepared.Body)
			}
			if got := subscriptionPreparedToolMode(test.protocol, prepared.Body); got != "required" {
				t.Fatalf("prepared tool mode = %q, want required: %s", got, prepared.Body)
			}
			if !bytes.Equal(spec.Body, original) {
				t.Fatalf("original body mutated: %s", spec.Body)
			}
		})
	}
}

func TestSubscriptionConvertedFunctionAllowlistsRejectInvalidConstraints(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		protocol protocol.Protocol
		body     string
	}{
		{"Responses empty", protocol.OpenAIResponses, `{"input":"hello","tools":[{"type":"function","name":"lookup"}],"tool_choice":{"type":"allowed_tools","mode":"required","tools":[]}}`},
		{"Responses missing", protocol.OpenAIResponses, `{"input":"hello","tools":[{"type":"function","name":"lookup"}],"tool_choice":{"type":"allowed_tools","mode":"required"}}`},
		{"Responses invalid mode", protocol.OpenAIResponses, `{"input":"hello","tools":[{"type":"function","name":"lookup"}],"tool_choice":{"type":"allowed_tools","mode":"none","tools":[{"type":"function","name":"lookup"}]}}`},
		{"Responses duplicate", protocol.OpenAIResponses, `{"input":"hello","tools":[{"type":"function","name":"lookup"}],"tool_choice":{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"lookup"},{"type":"function","name":"lookup"}]}}`},
		{"Chat empty", protocol.OpenAICompletions, `{"messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup"}}],"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[]}}}`},
		{"Chat missing", protocol.OpenAICompletions, `{"messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup"}}],"tool_choice":{"type":"allowed_tools"}}`},
		{"Gemini missing function", protocol.Gemini, `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"tools":[{"functionDeclarations":[{"name":"lookup"}]}],"toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["missing"]}}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := execution.AttemptSpec{
				ClientProtocol: test.protocol,
				RouteMode:      execution.RouteConverted,
				Operation:      execution.OperationChatCompletion,
				Body:           []byte(test.body),
			}
			if test.protocol == protocol.OpenAIResponses {
				spec.Operation = execution.OperationResponsesCreate
			}
			_, evidence := prepareConvertedFidelity(spec, channel.ProviderClaude)
			if evidence == nil || evidence.Kind != execution.ErrorKindConversionUnsupported ||
				evidence.Code != execution.ErrorCodeCriticalSemanticLoss {
				t.Fatalf("invalid allowlist was not rejected: %+v", evidence)
			}
		})
	}
}

func subscriptionPreparedToolName(clientProtocol protocol.Protocol, body []byte) string {
	switch clientProtocol {
	case protocol.OpenAICompletions:
		return gjson.GetBytes(body, "tools.0.function.name").String()
	case protocol.Gemini:
		return gjson.GetBytes(body, "tools.0.functionDeclarations.0.name").String()
	default:
		return gjson.GetBytes(body, "tools.0.name").String()
	}
}

func subscriptionPreparedToolMode(clientProtocol protocol.Protocol, body []byte) string {
	if clientProtocol == protocol.Gemini {
		mode := gjson.GetBytes(body, "toolConfig.functionCallingConfig.mode").String()
		if mode == "ANY" {
			return "required"
		}
		return "auto"
	}
	return gjson.GetBytes(body, "tool_choice").String()
}

func TestSubscriptionConvertedToolCompatibilityKeepsInstructionPreparation(t *testing.T) {
	t.Parallel()
	body := []byte(`{"model":"client-model","input":[{"role":"user","content":"start"},{"role":"assistant","content":"reply"},{"role":"system","content":"instruction"},{"role":"user","content":"continue"}],"tools":[{"type":"function","name":"lookup"},{"type":"function","name":"summarize"}],"tool_choice":{"type":"allowed_tools","mode":"auto","tools":[{"type":"function","name":"lookup"}]}}`)
	spec := execution.AttemptSpec{
		ClientProtocol: protocol.OpenAIResponses,
		RouteMode:      execution.RouteConverted,
		Operation:      execution.OperationResponsesCreate,
		Body:           body,
	}
	_, evidence := prepareConvertedFidelity(spec, channel.ProviderClaude)
	if evidence == nil || evidence.Kind != execution.ErrorKindConversionUnsupported ||
		evidence.Code != execution.ErrorCodeCriticalSemanticLoss {
		t.Fatalf("tool adaptation skipped instruction protection: %+v", evidence)
	}
}

func TestSubscriptionConvertedToolCompatibilityDoesNotApplyToNativeRoutes(t *testing.T) {
	t.Parallel()
	body := []byte(`{"input":"hello","tools":[{"type":"function","name":"lookup"},{"type":"function","name":"summarize"}],"tool_choice":{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"lookup"}]}}`)
	spec := execution.AttemptSpec{
		ClientProtocol: protocol.OpenAIResponses,
		RouteMode:      execution.RouteNative,
		Operation:      execution.OperationResponsesCreate,
		Body:           body,
	}
	prepared, evidence := prepareConvertedFidelity(spec, channel.ProviderCodex)
	if evidence != nil || !bytes.Equal(prepared.Body, body) {
		t.Fatalf("native request changed: evidence=%+v body=%s", evidence, prepared.Body)
	}
}

func TestSubscriptionConvertedResponsesKeepsNativeCodexAllowlist(t *testing.T) {
	t.Parallel()
	body := []byte(`{"input":"hello","tools":[{"type":"function","name":"lookup"},{"type":"function","name":"summarize"}],"tool_choice":{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"lookup"}]}}`)
	spec := execution.AttemptSpec{
		ClientProtocol: protocol.OpenAIResponses,
		RouteMode:      execution.RouteConverted,
		Operation:      execution.OperationResponsesCreate,
		Body:           body,
	}
	for _, providerKind := range []channel.ProviderKind{channel.ProviderCodex, channel.ProviderGrok} {
		prepared, evidence := prepareConvertedFidelity(spec, providerKind)
		if evidence != nil || !bytes.Equal(prepared.Body, body) {
			t.Fatalf("%s native Responses allowlist changed: evidence=%+v body=%s", providerKind, evidence, prepared.Body)
		}
	}
}

func TestSubscriptionConvertedFunctionAllowlistModes(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"auto", "required"} {
		t.Run(mode, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"input":"hello","tools":[{"type":"function","name":"lookup"},{"type":"function","name":"summarize"}],"tool_choice":{"type":"allowed_tools","mode":%q,"tools":[{"type":"function","name":"lookup"}]}}`, mode))
			spec := execution.AttemptSpec{ClientProtocol: protocol.OpenAIResponses, RouteMode: execution.RouteConverted, Operation: execution.OperationResponsesCreate, Body: body}
			prepared, evidence := prepareConvertedFidelity(spec, channel.ProviderClaude)
			if evidence != nil || gjson.GetBytes(prepared.Body, "tool_choice").String() != mode {
				t.Fatalf("mode %q was not preserved: evidence=%+v body=%s", mode, evidence, prepared.Body)
			}
		})
	}
}

func TestAdapterRejectsAntigravityClaudeRequiredFunctionAllowlistsBeforeDispatch(t *testing.T) {
	for _, test := range []struct {
		name      string
		protocol  protocol.Protocol
		operation execution.Operation
		path      string
		body      string
	}{
		{
			name: "Responses", protocol: protocol.OpenAIResponses,
			operation: execution.OperationResponsesCreate, path: "/v1/responses",
			body: `{"model":"client-model","input":"hello","tools":[{"type":"function","name":"lookup"}],"tool_choice":{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"lookup"}]}}`,
		},
		{
			name: "Chat", protocol: protocol.OpenAICompletions,
			operation: execution.OperationChatCompletion, path: "/v1/chat/completions",
			body: `{"model":"client-model","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup"}}],"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[{"type":"function","function":{"name":"lookup"}}]}}}`,
		},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", test.name, stream), func(t *testing.T) {
				registry := channel.NewRegistry()
				target, err := registry.Resolve(channel.Antigravity, nil)
				if err != nil {
					t.Fatal(err)
				}
				preparer := &fakeCredentialPreparer{}
				adapter := NewAdapter(nil, registry)
				adapter.credentials = preparer
				spec := execution.NewAttemptSpec(execution.AttemptSpec{
					RequestID: "allowlist-request", AttemptID: "allowlist-attempt", Sequence: 1,
					ChannelID: string(channel.Antigravity), TargetConfig: target.TargetConfig,
					RouteMode: execution.RouteConverted, ClientProtocol: test.protocol, Operation: test.operation,
					ClientModel: "client-model", UpstreamModel: "claude-sonnet-4-6",
					Method: http.MethodPost, Path: test.path, Body: []byte(test.body),
					Credential: execution.NewCredentialSnapshot(1, 1, 1, []byte(`{}`)),
				})
				var dispatchState execution.DispatchState
				var evidence *execution.ErrorEvidence
				if stream {
					result := adapter.ExecuteStream(t.Context(), spec, func(execution.StreamEvent) error { return nil })
					dispatchState, evidence = result.DispatchState, result.Error
				} else {
					result := adapter.Execute(t.Context(), spec)
					dispatchState, evidence = result.DispatchState, result.Error
				}
				if preparer.calls != 0 || dispatchState != execution.DispatchNotSent || evidence == nil ||
					evidence.Kind != execution.ErrorKindConversionUnsupported || evidence.Code != execution.ErrorCodeCriticalSemanticLoss {
					t.Fatalf("required allowlist reached dispatch: credential_calls=%d state=%s evidence=%+v", preparer.calls, dispatchState, evidence)
				}
			})
		}
	}
}

func TestSubscriptionGeminiAllowlistUsesClaudeSDKAliasPriority(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		body     string
		modePath string
	}{
		{
			name:     "snake case",
			body:     `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"tools":[{"functionDeclarations":[{"name":"lookup"},{"name":"summarize"},{"name":"remove_record"}]}],"tool_config":{"function_calling_config":{"mode":"ANY","allowed_function_names":["lookup","summarize"]}}}`,
			modePath: "tool_config.function_calling_config.mode",
		},
		{
			name:     "snake allowed names in camel config",
			body:     `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"tools":[{"functionDeclarations":[{"name":"lookup"},{"name":"summarize"},{"name":"remove_record"}]}],"toolConfig":{"functionCallingConfig":{"mode":"ANY","allowed_function_names":["lookup","summarize"]}}}`,
			modePath: "toolConfig.functionCallingConfig.mode",
		},
		{
			name:     "snake top level takes priority",
			body:     `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"tools":[{"functionDeclarations":[{"name":"lookup"},{"name":"summarize"},{"name":"remove_record"}]}],"tool_config":{"function_calling_config":{"mode":"ANY","allowed_function_names":["lookup","summarize"]}},"toolConfig":{"functionCallingConfig":{"mode":"AUTO","allowedFunctionNames":["remove_record"]}}}`,
			modePath: "tool_config.function_calling_config.mode",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := execution.AttemptSpec{
				ClientProtocol: protocol.Gemini,
				RouteMode:      execution.RouteConverted,
				Operation:      execution.OperationChatCompletion,
				Body:           []byte(test.body),
			}
			prepared, evidence := prepareConvertedFidelity(spec, channel.ProviderClaude)
			if evidence != nil {
				t.Fatalf("valid Gemini allowlist rejected: %+v", evidence)
			}
			declarations := gjson.GetBytes(prepared.Body, "tools.0.functionDeclarations").Array()
			if len(declarations) != 2 || declarations[0].Get("name").String() != "lookup" ||
				declarations[1].Get("name").String() != "summarize" {
				t.Fatalf("prepared declarations = %s, want lookup and summarize", prepared.Body)
			}
			if mode := gjson.GetBytes(prepared.Body, test.modePath).String(); mode != "ANY" {
				t.Fatalf("prepared mode = %q, want ANY: %s", mode, prepared.Body)
			}
		})
	}
}

func TestSubscriptionTokenCountUsesGenerationToolConstraints(t *testing.T) {
	t.Parallel()
	const geminiBody = `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"tools":[{"functionDeclarations":[{"name":"lookup"},{"name":"summarize"},{"name":"remove_record"}]}],"toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["lookup","summarize"]}}}`
	const responsesBody = `{"input":"hello","tools":[{"type":"function","name":"lookup"},{"type":"function","name":"summarize"},{"type":"function","name":"remove_record"}],"tool_choice":{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"lookup"},{"type":"function","name":"summarize"}]}}`
	for _, test := range []struct {
		name                string
		providerKind        channel.ProviderKind
		clientProtocol      protocol.Protocol
		generationOperation execution.Operation
		countOperation      execution.Operation
		upstreamModel       string
		body                string
		toolsPath           string
	}{
		{"Gemini to Claude", channel.ProviderClaude, protocol.Gemini, execution.OperationChatCompletion, execution.OperationCountTokens, "claude-sonnet-4-6", geminiBody, "tools.0.functionDeclarations"},
		{"Gemini to Codex", channel.ProviderCodex, protocol.Gemini, execution.OperationChatCompletion, execution.OperationCountTokens, "gpt-5.2", geminiBody, "tools.0.functionDeclarations"},
		{"Gemini to Grok", channel.ProviderGrok, protocol.Gemini, execution.OperationChatCompletion, execution.OperationCountTokens, "grok-4.3", geminiBody, "tools.0.functionDeclarations"},
		{"Responses to Claude", channel.ProviderClaude, protocol.OpenAIResponses, execution.OperationResponsesCreate, execution.OperationResponsesInputTokens, "claude-sonnet-4-6", responsesBody, "tools"},
		{"Responses to Antigravity", channel.ProviderAntigravity, protocol.OpenAIResponses, execution.OperationResponsesCreate, execution.OperationResponsesInputTokens, "gemini-2.5-pro", responsesBody, "tools"},
	} {
		t.Run(test.name, func(t *testing.T) {
			generation := execution.AttemptSpec{
				ClientProtocol: test.clientProtocol, RouteMode: execution.RouteConverted,
				Operation: test.generationOperation, UpstreamModel: test.upstreamModel, Body: []byte(test.body),
			}
			want, evidence := prepareConvertedFidelity(generation, test.providerKind)
			if evidence != nil {
				t.Fatalf("generation constraints rejected: %+v", evidence)
			}
			count := generation
			count.Operation = test.countOperation
			got, evidence := prepareConvertedFidelity(count, test.providerKind)
			if evidence != nil {
				t.Fatalf("token-count constraints rejected: %+v", evidence)
			}
			if !bytes.Equal(got.Body, want.Body) || gjson.GetBytes(got.Body, test.toolsPath+".#").Int() != 2 {
				t.Fatalf("token-count constraints differ from generation: got=%s want=%s", got.Body, want.Body)
			}
		})
	}
}
