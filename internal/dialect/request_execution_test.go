package dialect

import (
	"net/http"
	"testing"

	"gpt-load/internal/execution"
)

func TestInspectRequestIdentifiesOperationAndNativeRouteRequirement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		dialect     Dialect
		request     *ParsedRequest
		operation   execution.Operation
		requirement execution.RouteRequirement
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
			operation:   execution.OperationResponsesCreate,
			requirement: execution.RouteRequirementNative,
		},
		{
			name:      "Responses create transient",
			dialect:   NewOpenAIResponses(),
			request:   &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Body: []byte(`{"model":"gpt-5","store":false}`)},
			operation: execution.OperationResponsesCreate,
		},
		{
			name:      "Responses reasoning none stays convertible",
			dialect:   NewOpenAIResponses(),
			request:   &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Body: []byte(`{"model":"gpt-5","reasoning":{"effort":"none"},"store":false}`)},
			operation: execution.OperationResponsesCreate,
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
		},
		{
			name:    "OpenAI Chat file ID requires native semantics",
			dialect: NewOpenAI(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/chat/completions", Body: []byte(`{
				"model":"gpt-4o","messages":[{"role":"user","content":[{"type":"file","file":{"file_id":"file_123"}}]}]
			}`)},
			operation:   execution.OperationChatCompletion,
			requirement: execution.RouteRequirementNative,
		},
		{
			name:    "OpenAI Chat inline file stays convertible",
			dialect: NewOpenAI(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/chat/completions", Body: []byte(`{
				"model":"gpt-4o","messages":[{"role":"user","content":[{"type":"file","file":{"filename":"input.txt","file_data":"data:text/plain;base64,SGVsbG8="}}]}]
			}`)},
			operation: execution.OperationChatCompletion,
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
		},
		{
			name:    "Gemini function history",
			dialect: NewGemini(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1beta/models/gemini-2.5-pro:generateContent", Body: []byte(`{
				"contents":[{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{"result":"ok"}}}]}]
			}`)},
			operation: execution.OperationChatCompletion,
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
		},
		{
			name:    "Responses previous response requires native semantics",
			dialect: NewOpenAIResponses(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Body: []byte(`{
				"model":"gpt-5","store":false,"previous_response_id":"resp_123"
			}`)},
			operation:   execution.OperationResponsesCreate,
			requirement: execution.RouteRequirementNative,
		},
		{
			name:    "Responses conversation requires native semantics",
			dialect: NewOpenAIResponses(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Body: []byte(`{
				"model":"gpt-5","store":false,"conversation":"conv_123"
			}`)},
			operation:   execution.OperationResponsesCreate,
			requirement: execution.RouteRequirementNative,
		},
		{
			name:    "Responses background requires native semantics",
			dialect: NewOpenAIResponses(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Body: []byte(`{
				"model":"gpt-5","store":false,"background":true
			}`)},
			operation:   execution.OperationResponsesCreate,
			requirement: execution.RouteRequirementNative,
		},
		{
			name:    "Responses item reference requires native semantics",
			dialect: NewOpenAIResponses(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Body: []byte(`{
				"model":"gpt-5","store":false,"input":[{"type":"item_reference","id":"item_123"}]
			}`)},
			operation:   execution.OperationResponsesCreate,
			requirement: execution.RouteRequirementNative,
		},
		{
			name:    "Responses input file reference requires native semantics",
			dialect: NewOpenAIResponses(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Body: []byte(`{
				"model":"gpt-5","store":false,"input":[{"role":"user","content":[{"type":"input_file","file_id":"file_123"}]}]
			}`)},
			operation:   execution.OperationResponsesCreate,
			requirement: execution.RouteRequirementNative,
		},
		{
			name:    "Responses file search reference requires native semantics",
			dialect: NewOpenAIResponses(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Body: []byte(`{
				"model":"gpt-5","store":false,"tools":[{"type":"file_search","vector_store_ids":["vs_123"]}]
			}`)},
			operation:   execution.OperationResponsesCreate,
			requirement: execution.RouteRequirementNative,
		},
		{
			name:    "Responses code interpreter container requires native semantics",
			dialect: NewOpenAIResponses(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Body: []byte(`{
				"model":"gpt-5","store":false,"tools":[{"type":"code_interpreter","container":"cntr_123"}]
			}`)},
			operation:   execution.OperationResponsesCreate,
			requirement: execution.RouteRequirementNative,
		},
		{
			name:    "Responses unrelated resource-like metadata stays convertible",
			dialect: NewOpenAIResponses(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Body: []byte(`{
				"model":"gpt-5","store":false,"metadata":{"file_id":"business-file","container_id":"business-container"}
			}`)},
			operation: execution.OperationResponsesCreate,
		},
		{
			name:    "Gemini cached content requires native semantics",
			dialect: NewGemini(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1beta/models/gemini-2.5-pro:generateContent", Body: []byte(`{
				"contents":[{"role":"user","parts":[{"text":"hello"}]}],"cachedContent":"cachedContents/abc"
			}`)},
			operation:   execution.OperationChatCompletion,
			requirement: execution.RouteRequirementNative,
		},
		{
			name:    "Gemini Files API URI requires native semantics",
			dialect: NewGemini(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1beta/models/gemini-2.5-pro:generateContent", Body: []byte(`{
				"contents":[{"role":"user","parts":[{"fileData":{"mimeType":"application/pdf","fileUri":"https://generativelanguage.googleapis.com/v1beta/files/abc123"}}]}]
			}`)},
			operation:   execution.OperationChatCompletion,
			requirement: execution.RouteRequirementNative,
		},
		{
			name:    "Gemini public file URL stays convertible",
			dialect: NewGemini(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1beta/models/gemini-2.5-pro:generateContent", Body: []byte(`{
				"contents":[{"role":"user","parts":[{"fileData":{"mimeType":"application/pdf","fileUri":"https://cdn.example.test/input.pdf"}}]}]
			}`)},
			operation: execution.OperationChatCompletion,
		},
		{
			name:    "Anthropic container requires native semantics",
			dialect: NewAnthropic(),
			request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/messages", Body: []byte(`{
				"model":"claude-sonnet-4","container":{"id":"container_123"},"messages":[{"role":"user","content":"hello"}]
			}`)},
			operation:   execution.OperationChatCompletion,
			requirement: execution.RouteRequirementNative,
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
			wantRequirement := test.requirement.Normalize()
			if metadata.RouteRequirement != wantRequirement {
				t.Errorf("RouteRequirement = %q, want %q", metadata.RouteRequirement, wantRequirement)
			}
		})
	}
}

func TestOpenAIResponsesInspectRequestIdentifiesLifecycleOperations(t *testing.T) {
	t.Parallel()

	selected := NewOpenAIResponses()
	tests := []struct {
		name        string
		method      string
		path        string
		body        string
		operation   execution.Operation
		model       string
		requirement execution.RouteRequirement
	}{
		{name: "create", method: http.MethodPost, path: "/v1/responses", body: `{"model":"gpt-5"}`, operation: execution.OperationResponsesCreate, model: "gpt-5", requirement: execution.RouteRequirementNative},
		{name: "compact", method: http.MethodPost, path: "/v1/responses/compact", body: `{"model":"gpt-5","input":[]}`, operation: execution.OperationResponsesCompact, model: "gpt-5"},
		{name: "input tokens", method: http.MethodPost, path: "/v1/responses/input_tokens", body: `{"model":"gpt-5","input":[]}`, operation: execution.OperationResponsesInputTokens, model: "gpt-5"},
		{name: "retrieve", method: http.MethodGet, path: "/v1/responses/resp_123", operation: execution.OperationResponsesRetrieve, requirement: execution.RouteRequirementNative},
		{name: "delete", method: http.MethodDelete, path: "/v1/responses/resp_123", operation: execution.OperationResponsesDelete, requirement: execution.RouteRequirementNative},
		{name: "cancel", method: http.MethodPost, path: "/v1/responses/resp_123/cancel", operation: execution.OperationResponsesCancel, requirement: execution.RouteRequirementNative},
		{name: "input items", method: http.MethodGet, path: "/v1/responses/resp_123/input_items", operation: execution.OperationResponsesInputItems, requirement: execution.RouteRequirementNative},
		{name: "vendor extension", method: http.MethodPatch, path: "/v1/responses/vendor-extension/nested", body: `{"vendor":true}`, operation: execution.OperationResponsesPassthrough, requirement: execution.RouteRequirementNative},
		{name: "resource head", method: http.MethodHead, path: "/v1/responses/resp_123", operation: execution.OperationResponsesPassthrough, requirement: execution.RouteRequirementNative},
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
			wantRequirement := test.requirement.Normalize()
			if metadata.RouteRequirement != wantRequirement {
				t.Fatalf("RouteRequirement = %q, want %q", metadata.RouteRequirement, wantRequirement)
			}
		})
	}
}
