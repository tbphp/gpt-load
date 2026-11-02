package cpa

import (
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
