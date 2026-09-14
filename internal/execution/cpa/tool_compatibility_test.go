package cpa

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/tidwall/gjson"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestSubscriptionConvertedFunctionAllowlistsPreserveConstraints(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		providerKind channel.ProviderKind
		protocol     protocol.Protocol
		body         string
		toolsPath    string
	}{
		{
			name: "Responses to Claude", providerKind: channel.ProviderClaude, protocol: protocol.OpenAIResponses,
			body:      `{"model":"client-model","input":"hello","store":false,"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}},{"type":"function","name":"summarize","parameters":{"type":"object"}}],"tool_choice":{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"lookup"}]}}`,
			toolsPath: "tools",
		},
		{
			name: "Responses to Antigravity", providerKind: channel.ProviderAntigravity, protocol: protocol.OpenAIResponses,
			body:      `{"model":"client-model","input":"hello","store":false,"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}},{"type":"function","name":"summarize","parameters":{"type":"object"}}],"tool_choice":{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"lookup"}]}}`,
			toolsPath: "tools",
		},
		{
			name: "Chat to Claude", providerKind: channel.ProviderClaude, protocol: protocol.OpenAICompletions,
			body:      `{"model":"client-model","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}},{"type":"function","function":{"name":"summarize","parameters":{"type":"object"}}}],"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[{"type":"function","function":{"name":"lookup"}}]}}}`,
			toolsPath: "tools",
		},
		{
			name: "Chat to Antigravity", providerKind: channel.ProviderAntigravity, protocol: protocol.OpenAICompletions,
			body:      `{"model":"client-model","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}},{"type":"function","function":{"name":"summarize","parameters":{"type":"object"}}}],"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[{"type":"function","function":{"name":"lookup"}}]}}}`,
			toolsPath: "tools",
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
