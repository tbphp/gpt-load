package bifrost

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestConvertedOpenRouterPreservesCacheMarkers(t *testing.T) {
	t.Parallel()

	wire := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" {
			t.Errorf("upstream request = %s %s", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		wire <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, openAIResponsesConvertedFixture)
	}))
	defer server.Close()
	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
	runtime.baseURLs[channel.OpenRouter] = server.URL
	spec := convertedSpec(channel.OpenRouter, protocol.Anthropic, execution.OperationChatCompletion, "/v1/messages", []byte(`{"model":"client-model","max_tokens":128,"system":[{"type":"text","text":"shared prefix","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hello"}]}`))
	spec.UpstreamModel = "anthropic/claude-sonnet-4.6"
	result := runtime.Execute(t.Context(), spec)
	if err := result.Validate(); err != nil || result.Error != nil || result.UpstreamProtocol != protocol.OpenAIResponses {
		t.Fatalf("result = %+v; validation = %v", result, err)
	}
	var request struct {
		Input []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(<-wire, &request); err != nil {
		t.Fatal(err)
	}
	if len(request.Input) != 2 || request.Input[0].Role != "system" {
		t.Fatalf("upstream input = %+v", request.Input)
	}
	var blocks []struct {
		Text                  string `json:"text"`
		PromptCacheBreakpoint *struct {
			Mode string `json:"mode"`
		} `json:"prompt_cache_breakpoint"`
	}
	if err := json.Unmarshal(request.Input[0].Content, &blocks); err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || blocks[0].Text != "shared prefix" ||
		blocks[0].PromptCacheBreakpoint == nil || blocks[0].PromptCacheBreakpoint.Mode != "explicit" {
		t.Fatalf("cache marker not preserved: %s", request.Input[0].Content)
	}
}

func TestConvertedOpenRouterReportsOutputLimit(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" {
			t.Errorf("upstream request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_1","object":"response","created_at":123,"status":"incomplete","model":"claude-upstream","incomplete_details":{"reason":"max_output_tokens"},"output":[],"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}`)
	}))
	defer server.Close()
	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
	runtime.baseURLs[channel.OpenRouter] = server.URL
	spec := convertedSpec(channel.OpenRouter, protocol.Anthropic, execution.OperationChatCompletion, "/v1/messages", []byte(`{"model":"client-model","max_tokens":128,"messages":[{"role":"user","content":"hello"}]}`))
	spec.UpstreamModel = "anthropic/claude-sonnet-4.6"
	result := runtime.Execute(t.Context(), spec)
	if err := result.Validate(); err != nil || result.Error != nil || result.UpstreamProtocol != protocol.OpenAIResponses {
		t.Fatalf("result = %+v; validation = %v", result, err)
	}
	var response struct {
		Type       string `json:"type"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(result.Body, &response); err != nil {
		t.Fatal(err)
	}
	if response.Type != "message" || response.StopReason != "max_tokens" {
		t.Fatalf("truncated response = %s", result.Body)
	}
}

func TestConvertedAnthropicPreservesNamespaceTools(t *testing.T) {
	t.Parallel()

	wire := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			t.Errorf("upstream request = %s %s", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		wire <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-upstream","content":[{"type":"tool_use","id":"tool_1","name":"demo__clock","input":{}}],"stop_reason":"tool_use","stop_sequence":null,"usage":{"input_tokens":4,"output_tokens":2}}`)
	}))
	defer server.Close()
	runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true, anthropicBaseURL: server.URL})
	spec := convertedSpec(channel.Anthropic, protocol.OpenAIResponses, execution.OperationResponsesCreate, "/v1/responses", []byte(`{"model":"client-model","input":"hello","tools":[{"type":"namespace","name":"demo","tools":[{"type":"function","name":"clock","description":"Return time","parameters":{"type":"object","properties":{}}}]}],"tool_choice":"auto"}`))
	spec.UpstreamModel = "claude-sonnet-4.6"
	result := runtime.Execute(t.Context(), spec)
	if err := result.Validate(); err != nil || result.Error != nil || result.UpstreamProtocol != protocol.Anthropic {
		t.Fatalf("result = %+v; validation = %v", result, err)
	}
	var request struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(<-wire, &request); err != nil {
		t.Fatal(err)
	}
	if len(request.Tools) != 1 || request.Tools[0].Name != "demo__clock" || request.Tools[0].Description != "Return time" {
		t.Fatalf("namespace tools not preserved: %+v", request.Tools)
	}
	var response struct {
		Output []struct {
			Type      string `json:"type"`
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"output"`
	}
	if err := json.Unmarshal(result.Body, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 1 || response.Output[0].Type != "function_call" ||
		response.Output[0].Name != "clock" || response.Output[0].Namespace != "demo" {
		t.Fatalf("namespace tool response not restored: %s", result.Body)
	}
}
