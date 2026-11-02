package cpa

import (
	"encoding/json"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestDevinRejectsKnownLossyInputsBeforeDispatch(t *testing.T) {
	bridge := newDevinProviderBridge()
	validator, ok := any(bridge).(providerRequestValidator)
	if !ok {
		t.Fatal("Devin must validate inputs its Connect-RPC backend cannot represent")
	}
	for _, test := range []struct {
		format, body string
		reject       bool
	}{
		{"openai", `{"messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"type":{"const":"input_audio"}}}}}]}`, false},
		{"openai", `{"messages":[{"role":"user","content":[{"type":"text","text":"hello"},{"type":"input_audio","input_audio":{"data":"AAAA","format":"wav"}}]}]}`, true},
		{"openai", `{"tool_choice":"required"}`, true},
		{"openai", `{"response_format":{"type":"json_schema","json_schema":{"name":"output"}}}`, true},
		{"openai-response", `{"tools":[{"type":"web_search"}],"input":"hello"}`, true},
		{"openai-response", `{"tools":[{"type":"namespace","name":"remote","tools":[{"type":"web_search"}]}],"input":"hello"}`, true},
		{"openai-response", `{"input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.test/image.png"}]}]}`, true},
		{"claude", `{"messages":[{"role":"user","content":[{"type":"text","text":"hello"},{"type":"document","source":{"type":"base64","data":"AAAA"}}]}]}`, true},
		{"claude", `{"tool_choice":{"type":"auto"},"tools":[{"name":"lookup","input_schema":{"type":"object"}}]}`, false},
		{"gemini", `{"contents":[{"role":"user","parts":[{"text":"hello"},{"inlineData":{"mimeType":"audio/wav","data":"AAAA"}}]}]}`, true},
		{"gemini", `{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"AAAA"}}]}]}`, false},
		{"gemini", `{"tools":[{"googleSearch":{}}]}`, true},
		{"openai", `{"messages":[{"role":"user","content":"first"},{"role":"system","content":"later instruction"}]}`, true},
	} {
		t.Run(test.format+test.body, func(t *testing.T) {
			err := validator.ValidateRequest(providerRequest{Format: test.format, Payload: []byte(test.body)})
			if (err != nil) != test.reject {
				t.Fatalf("rejection=%v want=%v", err, test.reject)
			}
		})
	}
}

func TestDevinErrorsPreserveCredentialQuotaScope(t *testing.T) {
	bridge := newDevinProviderBridge()
	_, evidence := bridge.ClassifyError(t.Context(), museQuotaTestError{grokProviderTestError{status: 429, code: "rate_limit_exceeded"}}, devinProviderCredential{})
	if evidence.ScopeHint != execution.ErrorScopeCredential || evidence.Hint != execution.FailureHintRateLimited {
		t.Fatal("Devin quota scope lost")
	}
}

func TestDevinConversionPreservesFunctionAllowlist(t *testing.T) {
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"allowed","parameters":{"type":"object"}},{"name":"forbidden","parameters":{"type":"object"}}]}],"toolConfig":{"functionCallingConfig":{"mode":"AUTO","allowedFunctionNames":["allowed"]}}}`)
	spec, failure := prepareConvertedFidelity(execution.AttemptSpec{RouteMode: execution.RouteConverted, ClientProtocol: protocol.Gemini, Operation: execution.OperationChatCompletion, Body: body}, channel.ProviderDevin)
	if failure != nil {
		t.Fatal(failure)
	}
	if strings.Contains(string(spec.Body), "forbidden") {
		t.Fatal("Devin conversion lost function allowlist")
	}
}

func TestDevinSupportsResponsesToolsAndEmptyControls(t *testing.T) {
	bridge := newDevinProviderBridge()
	for _, body := range []string{
		`{"input":"hi","tools":[{"type":"custom","name":"apply_patch","format":{"type":"text"}}]}`,
		`{"input":"hi","tools":[{"type":"namespace","name":"functions","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}]}`,
		`{"input":"hi","previous_response_id":"","previous_interaction_id":null,"stop":[]}`,
		`{"input":"hi","stop":"","stop_sequences":null}`,
	} {
		if err := bridge.ValidateRequest(providerRequest{Format: "openai-response", Payload: []byte(body)}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDevinGeminiFieldSpellingsPreserveEquivalentBehavior(t *testing.T) {
	for _, outer := range []string{"toolConfig", "tool_config"} {
		for _, inner := range []string{"functionCallingConfig", "function_calling_config"} {
			for _, allowedKey := range []string{"allowedFunctionNames", "allowed_function_names"} {
				name := outer + "/" + inner + "/" + allowedKey
				t.Run(name, func(t *testing.T) {
					config := map[string]any{"mode": "AUTO", allowedKey: []string{"allowed"}}
					body, err := json.Marshal(map[string]any{"tools": []any{map[string]any{"functionDeclarations": []any{map[string]string{"name": "allowed"}, map[string]string{"name": "forbidden"}}}}, outer: map[string]any{inner: config}})
					if err != nil {
						t.Fatal(err)
					}
					prepared, failure := prepareConvertedFidelity(execution.AttemptSpec{RouteMode: execution.RouteConverted, ClientProtocol: protocol.Gemini, Operation: execution.OperationChatCompletion, Body: body}, channel.ProviderDevin)
					if failure != nil {
						t.Fatal(failure)
					}
					if strings.Contains(string(prepared.Body), "forbidden") {
						t.Fatal("snake_case function whitelist was ignored")
					}
					if err := newDevinProviderBridge().ValidateRequest(providerRequest{Format: "gemini", Payload: prepared.Body}); err != nil {
						t.Fatal(err)
					}
					config["mode"] = "ANY"
					forced, err := json.Marshal(map[string]any{outer: map[string]any{inner: config}})
					if err != nil {
						t.Fatal(err)
					}
					if err := newDevinProviderBridge().ValidateRequest(providerRequest{Format: "gemini", Payload: forced}); err == nil {
						t.Fatal("unsupported forced tool mode was accepted")
					}
				})
			}
		}
	}
	for _, outer := range []string{"generationConfig", "generation_config"} {
		for _, inner := range []string{"responseMimeType", "response_mime_type"} {
			t.Run(outer+"/"+inner, func(t *testing.T) {
				raw, err := json.Marshal(map[string]any{outer: map[string]string{inner: "application/json"}})
				if err != nil {
					t.Fatal(err)
				}
				if err := newDevinProviderBridge().ValidateRequest(providerRequest{Format: "gemini", Payload: raw}); err == nil {
					t.Fatal("unsupported constrained output was accepted")
				}
			})
		}
	}
}
