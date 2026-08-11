package dialect

import (
	"net/http"
	"testing"

	"gpt-load/internal/execution"
)

func TestInspectRequestIdentifiesExecutionOperationAndFeatures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		dialect    Dialect
		request    *ParsedRequest
		operation  execution.Operation
		features   []execution.Feature
		noFeatures []execution.Feature
	}{
		{
			name:    "OpenAI Chat",
			dialect: NewOpenAI(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/chat/completions", Body: []byte(`{
				"model":"gpt-4o","stream":true,"reasoning_effort":"high",
				"tools":[{"type":"function","function":{"name":"lookup"}}],
				"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.test/image.png"}}]}],
				"response_format":{"type":"json_schema","json_schema":{"name":"answer","schema":{"type":"object"}}}
			}`)},
			operation: execution.OperationChatCompletion,
			features: []execution.Feature{
				execution.FeatureStreaming,
				execution.FeatureTools,
				execution.FeatureReasoning,
				execution.FeatureMultimodal,
				execution.FeatureStructuredOutput,
			},
		},
		{
			name:    "Anthropic Messages",
			dialect: NewAnthropic(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/messages", Body: []byte(`{
				"model":"claude-sonnet-4","stream":true,
				"thinking":{"type":"enabled","budget_tokens":1024},
				"tools":[{"name":"lookup","input_schema":{"type":"object"}}],
				"messages":[{"role":"user","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"AA=="}}]}],
				"output_config":{"format":{"type":"json_schema","schema":{"type":"object"}}}
			}`)},
			operation: execution.OperationChatCompletion,
			features: []execution.Feature{
				execution.FeatureStreaming,
				execution.FeatureTools,
				execution.FeatureReasoning,
				execution.FeatureMultimodal,
				execution.FeatureStructuredOutput,
			},
		},
		{
			name:    "Gemini generateContent",
			dialect: NewGemini(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1beta/models/gemini-2.5-pro:streamGenerateContent", Body: []byte(`{
				"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"AA=="}}]}],
				"tools":[{"functionDeclarations":[{"name":"lookup"}]}],
				"generationConfig":{"thinkingConfig":{"thinkingLevel":"high"},"responseSchema":{"type":"OBJECT"}}
			}`)},
			operation: execution.OperationChatCompletion,
			features: []execution.Feature{
				execution.FeatureStreaming,
				execution.FeatureTools,
				execution.FeatureReasoning,
				execution.FeatureMultimodal,
				execution.FeatureStructuredOutput,
			},
		},
		{
			name:    "Responses create stored",
			dialect: NewOpenAIResponses(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Body: []byte(`{
				"model":"gpt-5","stream":true,"reasoning":{"effort":"high"},
				"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}],
				"input":[{"role":"user","content":[{"type":"input_file","file_id":"file_123"}]}],
				"text":{"format":{"type":"json_schema","name":"answer","schema":{"type":"object"}}}
			}`)},
			operation: execution.OperationResponsesCreate,
			features: []execution.Feature{
				execution.FeatureStreaming,
				execution.FeatureTools,
				execution.FeatureReasoning,
				execution.FeatureMultimodal,
				execution.FeatureStructuredOutput,
				execution.FeatureNativeResourceSemantics,
			},
		},
		{
			name:       "Responses create transient",
			dialect:    NewOpenAIResponses(),
			request:    &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Body: []byte(`{"model":"gpt-5","store":false}`)},
			operation:  execution.OperationResponsesCreate,
			noFeatures: []execution.Feature{execution.FeatureNativeResourceSemantics},
		},
		{
			name:       "Responses reasoning none is not a capability requirement",
			dialect:    NewOpenAIResponses(),
			request:    &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Body: []byte(`{"model":"gpt-5","reasoning":{"effort":"none"},"store":false}`)},
			operation:  execution.OperationResponsesCreate,
			noFeatures: []execution.Feature{execution.FeatureReasoning, execution.FeatureNativeResourceSemantics},
		},
		{
			name:    "OpenAI Chat tool history",
			dialect: NewOpenAI(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/chat/completions", Body: []byte(`{
				"model":"gpt-4o",
				"messages":[
					{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},
					{"role":"tool","tool_call_id":"call_1","content":"ok"}
				]
			}`)},
			operation: execution.OperationChatCompletion,
			features:  []execution.Feature{execution.FeatureTools},
		},
		{
			name:    "Anthropic tool and reasoning history",
			dialect: NewAnthropic(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/messages", Body: []byte(`{
				"model":"claude-sonnet-4",
				"messages":[{"role":"user","content":[
					{"type":"tool_result","tool_use_id":"tool_1","content":"ok"},
					{"type":"redacted_thinking","data":"opaque"}
				]}]
			}`)},
			operation: execution.OperationChatCompletion,
			features:  []execution.Feature{execution.FeatureTools, execution.FeatureReasoning},
		},
		{
			name:    "Gemini function history",
			dialect: NewGemini(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1beta/models/gemini-2.5-pro:generateContent", Body: []byte(`{
				"contents":[{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{"result":"ok"}}}]}]
			}`)},
			operation: execution.OperationChatCompletion,
			features:  []execution.Feature{execution.FeatureTools},
		},
		{
			name:    "Responses tool and reasoning history",
			dialect: NewOpenAIResponses(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Body: []byte(`{
				"model":"gpt-5","store":false,
				"input":[
					{"type":"reasoning","encrypted_content":"opaque"},
					{"type":"function_call_output","call_id":"call_1","output":"ok"}
				]
			}`)},
			operation: execution.OperationResponsesCreate,
			features:  []execution.Feature{execution.FeatureTools, execution.FeatureReasoning},
			noFeatures: []execution.Feature{
				execution.FeatureNativeResourceSemantics,
			},
		},
		{
			name:    "Responses previous response requires native semantics",
			dialect: NewOpenAIResponses(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Body: []byte(`{
				"model":"gpt-5","store":false,"previous_response_id":"resp_123"
			}`)},
			operation: execution.OperationResponsesCreate,
			features:  []execution.Feature{execution.FeatureNativeResourceSemantics},
		},
		{
			name:    "Responses conversation requires native semantics",
			dialect: NewOpenAIResponses(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Body: []byte(`{
				"model":"gpt-5","store":false,"conversation":"conv_123"
			}`)},
			operation: execution.OperationResponsesCreate,
			features:  []execution.Feature{execution.FeatureNativeResourceSemantics},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			metadata, err := test.dialect.InspectRequest(test.request)
			if err != nil {
				t.Fatalf("InspectRequest() error = %v", err)
			}
			if metadata.Operation != test.operation {
				t.Fatalf("Operation = %q, want %q", metadata.Operation, test.operation)
			}
			for _, feature := range test.features {
				if !metadata.RequiredFeatures.Has(feature) {
					t.Errorf("RequiredFeatures missing %q", feature)
				}
			}
			for _, feature := range test.noFeatures {
				if metadata.RequiredFeatures.Has(feature) {
					t.Errorf("RequiredFeatures unexpectedly contains %q", feature)
				}
			}
		})
	}
}

func TestOpenAIResponsesInspectRequestIdentifiesLifecycleOperations(t *testing.T) {
	t.Parallel()

	selected := NewOpenAIResponses()
	tests := []struct {
		name      string
		method    string
		path      string
		body      string
		operation execution.Operation
		model     string
	}{
		{name: "create", method: http.MethodPost, path: "/v1/responses", body: `{"model":"gpt-5"}`, operation: execution.OperationResponsesCreate, model: "gpt-5"},
		{name: "compact", method: http.MethodPost, path: "/v1/responses/compact", body: `{"model":"gpt-5","input":[]}`, operation: execution.OperationResponsesCompact, model: "gpt-5"},
		{name: "input tokens", method: http.MethodPost, path: "/v1/responses/input_tokens", body: `{"model":"gpt-5","input":[]}`, operation: execution.OperationResponsesInputTokens, model: "gpt-5"},
		{name: "retrieve", method: http.MethodGet, path: "/v1/responses/resp_123", operation: execution.OperationResponsesRetrieve},
		{name: "delete", method: http.MethodDelete, path: "/v1/responses/resp_123", operation: execution.OperationResponsesDelete},
		{name: "cancel", method: http.MethodPost, path: "/v1/responses/resp_123/cancel", operation: execution.OperationResponsesCancel},
		{name: "input items", method: http.MethodGet, path: "/v1/responses/resp_123/input_items", operation: execution.OperationResponsesInputItems},
		{name: "vendor extension", method: http.MethodPatch, path: "/v1/responses/vendor-extension/nested", body: `{"vendor":true}`, operation: execution.OperationResponsesPassthrough},
		{name: "resource head", method: http.MethodHead, path: "/v1/responses/resp_123", operation: execution.OperationResponsesPassthrough},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			metadata, err := selected.InspectRequest(&ParsedRequest{
				Method: test.method,
				Path:   test.path,
				Body:   []byte(test.body),
			})
			if err != nil {
				t.Fatalf("InspectRequest() error = %v", err)
			}
			if metadata.Operation != test.operation {
				t.Fatalf("Operation = %q, want %q", metadata.Operation, test.operation)
			}
			gotModel := ""
			if metadata.Model != nil {
				gotModel = *metadata.Model
			}
			if got := gotModel; got != test.model {
				t.Fatalf("Model = %q, want %q", got, test.model)
			}
			if !metadata.RequiredFeatures.Has(execution.FeatureNativeResourceSemantics) &&
				(test.operation == execution.OperationResponsesRetrieve ||
					test.operation == execution.OperationResponsesDelete ||
					test.operation == execution.OperationResponsesCancel ||
					test.operation == execution.OperationResponsesInputItems ||
					test.operation == execution.OperationResponsesPassthrough) {
				t.Fatal("resource operation must require native resource semantics")
			}
		})
	}
}
