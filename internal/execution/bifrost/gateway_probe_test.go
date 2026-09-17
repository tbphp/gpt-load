package bifrost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/provideradapter"
)

func TestMultiProtocolGatewayProbesUseSelectedProtocol(t *testing.T) {
	t.Parallel()

	for _, id := range []channel.ID{channel.NewAPI, channel.GPTLoad, channel.CLIProxyAPI, channel.Sub2API} {
		for _, selected := range []protocol.Protocol{protocol.OpenAIResponses, protocol.Anthropic, protocol.Gemini} {
			t.Run(string(id)+"/"+string(selected), func(t *testing.T) {
				var calls atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method != http.MethodPost || r.URL.Path != "/team-a"+gatewayProbePath(selected) || r.URL.RawQuery != "" {
						t.Errorf("probe target = %s %s", r.Method, r.URL.String())
					}
					credentialHeader := "Authorization"
					credentialValue := "Bearer " + testAPIKey
					switch selected {
					case protocol.Anthropic:
						credentialHeader, credentialValue = "X-Api-Key", testAPIKey
						if r.Header.Get("Anthropic-Version") == "" {
							t.Error("Anthropic-Version is missing")
						}
					case protocol.Gemini:
						credentialHeader, credentialValue = "X-Goog-Api-Key", testAPIKey
					}
					for _, header := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key"} {
						want := ""
						if header == credentialHeader {
							want = credentialValue
						}
						if r.Header.Get(header) != want {
							t.Errorf("incorrect %s credential header", header)
						}
					}
					var payload map[string]any
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Errorf("decode probe request: %v", err)
						return
					}
					if selected != protocol.Gemini && payload["model"] != "probe-upstream" {
						t.Errorf("probe model = %v", payload["model"])
					}
					switch selected {
					case protocol.OpenAIResponses:
						if payload["store"] != false || payload["max_output_tokens"] != float64(16) || payload["input"] == nil {
							t.Errorf("Responses probe = %#v", payload)
						}
					case protocol.Anthropic:
						if payload["max_tokens"] != float64(1) || payload["messages"] == nil {
							t.Errorf("Anthropic probe = %#v", payload)
						}
					case protocol.Gemini:
						config, _ := payload["generationConfig"].(map[string]any)
						if config["maxOutputTokens"] != float64(1) || payload["contents"] == nil {
							t.Errorf("Gemini probe = %#v", payload)
						}
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, gatewayProbeResponse(selected))
				}))
				defer server.Close()
				manager, spec := gatewayProbeForTest(t, id, selected, server.URL+"/team-a")
				result := manager.Execute(t.Context(), spec)
				if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK ||
					result.UpstreamProtocol != selected || calls.Load() != 1 {
					t.Fatalf("calls = %d; result = %+v; validation = %v", calls.Load(), result, err)
				}
			})
		}
	}
}

func TestGatewayProtocolProbeResponseValidation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		selected protocol.Protocol
		body     string
		valid    bool
	}{
		{"responses-completed", protocol.OpenAIResponses, `{"object":"response","status":"completed","output":[]}`, true},
		{"responses-output-limit", protocol.OpenAIResponses, `{"object":"response","status":"incomplete","output":[],"incomplete_details":{"reason":"max_output_tokens"}}`, true},
		{"responses-reasoning-only", protocol.OpenAIResponses, `{"object":"response","status":"completed","output":[{"type":"reasoning","summary":[]}]}`, true},
		{"responses-failed", protocol.OpenAIResponses, `{"object":"response","status":"failed","output":[]}`, false},
		{"anthropic-empty-content", protocol.Anthropic, `{"type":"message","content":[]}`, true},
		{"anthropic-empty-text", protocol.Anthropic, `{"type":"message","content":[{"type":"text","text":""}]}`, true},
		{"gemini-output-limit", protocol.Gemini, `{"candidates":[{"finishReason":"MAX_TOKENS"}]}`, true},
		{"gemini-empty-parts", protocol.Gemini, `{"candidates":[{"content":{"parts":[]},"finishReason":"MAX_TOKENS"}]}`, true},
		{"gemini-separate-payload-parts", protocol.Gemini, `{"candidates":[{"content":{"parts":[{"text":"pong"},{"functionCall":{"name":"clock","args":{}}}]}}]}`, true},
		{"gemini-no-candidates", protocol.Gemini, `{"candidates":[]}`, false},
		{"gemini-missing-content-parts", protocol.Gemini, `{"candidates":[{"content":{}}]}`, false},
		{"gemini-null-part", protocol.Gemini, `{"candidates":[{"content":{"parts":[null]}}]}`, false},
		{"gemini-scalar-part", protocol.Gemini, `{"candidates":[{"content":{"parts":[1]}}]}`, false},
		{"gemini-empty-part", protocol.Gemini, `{"candidates":[{"content":{"parts":[{}]}}]}`, false},
		{"gemini-scalar-content", protocol.Gemini, `{"candidates":[{"content":1,"finishReason":"MAX_TOKENS"}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validGatewayProtocolProbeResponse(tc.selected, []byte(tc.body)); got != tc.valid {
				t.Errorf("valid = %v, want %v", got, tc.valid)
			}
		})
	}
}

func TestMultiProtocolGatewayProbeRejectsInvalidSuccessResponses(t *testing.T) {
	t.Parallel()

	for _, selected := range []protocol.Protocol{protocol.OpenAIResponses, protocol.Anthropic, protocol.Gemini} {
		bodies := []string{`{}`, `{"error":{"message":"upstream rejected probe"}}`, `null`, `[]`}
		var response map[string]json.RawMessage
		if err := json.Unmarshal([]byte(gatewayProbeResponse(selected)), &response); err != nil {
			t.Fatal(err)
		}
		key := map[protocol.Protocol]string{protocol.OpenAIResponses: "output", protocol.Anthropic: "content", protocol.Gemini: "candidates"}[selected]
		validItems := string(response[key])
		for _, items := range []string{`[null]`, `[1]`, `["invalid"]`, `[[]]`, `[{}]`, `[{"type":null}]`, `[{"type":1}]`, `[{"type":""}]`, validItems[:len(validItems)-1] + `,null]`} {
			response[key] = json.RawMessage(items)
			body, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			bodies = append(bodies, string(body))
		}
		for _, body := range bodies {
			t.Run(string(selected)+"/"+body, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, body)
				}))
				defer server.Close()
				manager, spec := gatewayProbeForTest(t, channel.NewAPI, selected, server.URL)
				result := manager.Execute(t.Context(), spec)
				if err := result.Validate(); err != nil || result.Error == nil {
					t.Fatalf("invalid probe response %s succeeded: %+v; validation = %v", body, result, err)
				}
			})
		}
	}
}

func TestMultiProtocolGatewayProbePayloadValidation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		selected protocol.Protocol
		payload  string
		valid    bool
	}{
		{"missing-text", protocol.Anthropic, `{"type":"text"}`, false},
		{"null-text", protocol.Anthropic, `{"type":"text","text":null}`, false},
		{"number-text", protocol.Anthropic, `{"type":"text","text":123}`, false},
		{"empty-text", protocol.Anthropic, `{"type":"text","text":""}`, true},
		{"thinking", protocol.Anthropic, `{"type":"thinking","thinking":"","signature":"sig"}`, true},
		{"missing-thinking", protocol.Anthropic, `{"type":"thinking","signature":"sig"}`, false},
		{"redacted-thinking", protocol.Anthropic, `{"type":"redacted_thinking","data":"opaque"}`, true},
		{"tool-use", protocol.Anthropic, `{"type":"tool_use","id":"tool_1","name":"clock","input":{}}`, true},
		{"missing-tool-input", protocol.Anthropic, `{"type":"tool_use","id":"tool_1","name":"clock"}`, false},
		{"missing-message-content", protocol.OpenAIResponses, `{"type":"message"}`, false},
		{"null-text", protocol.OpenAIResponses, `{"type":"message","content":[{"type":"output_text","text":null}]}`, false},
		{"number-text", protocol.OpenAIResponses, `{"type":"message","content":[{"type":"output_text","text":123}]}`, false},
		{"empty-message-content", protocol.OpenAIResponses, `{"type":"message","content":[]}`, true},
		{"empty-text", protocol.OpenAIResponses, `{"type":"message","content":[{"type":"output_text","text":"","annotations":[]}]}`, true},
		{"refusal", protocol.OpenAIResponses, `{"type":"message","content":[{"type":"refusal","refusal":""}]}`, true},
		{"missing-refusal", protocol.OpenAIResponses, `{"type":"message","content":[{"type":"refusal"}]}`, false},
		{"reasoning-summary", protocol.OpenAIResponses, `{"type":"reasoning","summary":[{"type":"summary_text","text":""}]}`, true},
		{"missing-summary-text", protocol.OpenAIResponses, `{"type":"reasoning","summary":[{"type":"summary_text"}]}`, false},
		{"function-call", protocol.OpenAIResponses, `{"type":"function_call","call_id":"call_1","name":"clock","arguments":""}`, true},
		{"missing-function-arguments", protocol.OpenAIResponses, `{"type":"function_call","call_id":"call_1","name":"clock"}`, false},
		{"file-search", protocol.OpenAIResponses, `{"type":"file_search_call","id":"fs_1","status":"completed","queries":[]}`, true},
		{"missing-search-queries", protocol.OpenAIResponses, `{"type":"file_search_call","id":"fs_1","status":"completed"}`, false},
		{"scalar-search-query", protocol.OpenAIResponses, `{"type":"file_search_call","id":"fs_1","status":"completed","queries":[1]}`, false},
		{"web-search", protocol.OpenAIResponses, `{"type":"web_search_call","id":"ws_1","status":"completed","action":{"type":"search","query":"ping"}}`, true},
		{"missing-search-action", protocol.OpenAIResponses, `{"type":"web_search_call","id":"ws_1","status":"completed"}`, false},
		{"mcp-list-tools", protocol.OpenAIResponses, `{"type":"mcp_list_tools","id":"mcp_1","server_label":"test","tools":[]}`, true},
		{"missing-mcp-tools", protocol.OpenAIResponses, `{"type":"mcp_list_tools","id":"mcp_1","server_label":"test"}`, false},
		{"local-shell", protocol.OpenAIResponses, `{"type":"local_shell_call","id":"shell_1","call_id":"call_1","status":"completed","action":{"type":"exec","command":["pwd"],"env":{"PATH":"/usr/bin"}}}`, true},
		{"scalar-shell-env", protocol.OpenAIResponses, `{"type":"local_shell_call","id":"shell_1","call_id":"call_1","status":"completed","action":{"type":"exec","command":["pwd"],"env":{"PATH":123}}}`, false},
		{"code-interpreter-null-output", protocol.OpenAIResponses, `{"type":"code_interpreter_call","id":"ci_1","status":"incomplete","container_id":"container_1","code":null,"outputs":null}`, true},
		{"code-interpreter-null-log", protocol.OpenAIResponses, `{"type":"code_interpreter_call","id":"ci_1","status":"completed","container_id":"container_1","code":"","outputs":[{"type":"logs","logs":null}]}`, false},
		{"mcp-null-tool", protocol.OpenAIResponses, `{"type":"mcp_list_tools","id":"mcp_1","server_label":"test","tools":[null]}`, false},
		{"null-image-result", protocol.OpenAIResponses, `{"type":"image_generation_call","id":"ig_1","status":"completed","result":null}`, false},
		{"null-text", protocol.Gemini, `{"text":null}`, false},
		{"number-text", protocol.Gemini, `{"text":123}`, false},
		{"unknown-payload", protocol.Gemini, `{"unexpected":true}`, false},
		{"empty-text", protocol.Gemini, `{"text":""}`, true},
		{"text-with-metadata", protocol.Gemini, `{"text":"","thought":true,"thoughtSignature":"c2ln"}`, true},
		{"mixed-payload", protocol.Gemini, `{"text":"pong","functionCall":{"name":"clock","args":{}}}`, false},
		{"inline-data", protocol.Gemini, `{"inlineData":{"mimeType":"image/png","data":"AA=="}}`, true},
		{"missing-inline-data", protocol.Gemini, `{"inlineData":{"mimeType":"image/png"}}`, false},
		{"function-call", protocol.Gemini, `{"functionCall":{"name":"clock","args":{}}}`, true},
		{"missing-function-name", protocol.Gemini, `{"functionCall":{"args":{}}}`, false},
	} {
		t.Run(string(tc.selected)+"/"+tc.name, func(t *testing.T) {
			body := `{"type":"message","content":[` + tc.payload + `]}`
			switch tc.selected {
			case protocol.OpenAIResponses:
				body = `{"object":"response","status":"completed","output":[` + tc.payload + `]}`
			case protocol.Gemini:
				body = `{"candidates":[{"content":{"parts":[` + tc.payload + `]}}]}`
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			manager, spec := gatewayProbeForTest(t, channel.NewAPI, tc.selected, server.URL)
			result := manager.Execute(t.Context(), spec)
			if err := result.Validate(); err != nil || (result.Error == nil) != tc.valid {
				t.Fatalf("valid = %v, want %v; upstream error = %v; validation = %v", result.Error == nil, tc.valid, result.Error, err)
			}
		})
	}
}

func TestMultiProtocolGatewayProbeFailuresDoNotSucceed(t *testing.T) {
	t.Parallel()

	for _, selected := range []protocol.Protocol{protocol.OpenAIResponses, protocol.Anthropic, protocol.Gemini} {
		for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusOK} {
			t.Run(string(selected)+"/"+http.StatusText(status), func(t *testing.T) {
				var calls atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.URL.Path != gatewayProbePath(selected) {
						t.Errorf("wrong protocol target: %s", r.URL.Path)
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					if status == http.StatusOK {
						_, _ = io.WriteString(w, `not-json`)
					} else {
						_, _ = io.WriteString(w, `{"error":{"message":"probe rejected","type":"invalid_request_error"}}`)
					}
				}))
				defer server.Close()
				manager, spec := gatewayProbeForTest(t, channel.NewAPI, selected, server.URL)
				result := manager.Execute(t.Context(), spec)
				if err := result.Validate(); err != nil || result.Error == nil || calls.Load() != 1 {
					t.Fatalf("calls = %d; result = %+v; validation = %v", calls.Load(), result, err)
				}
				if status != http.StatusOK && result.Error.StatusCode != status {
					t.Errorf("error status = %d, want %d", result.Error.StatusCode, status)
				}
			})
		}
	}
}

func TestMultiProtocolGatewayProbeHonorsCancellationAndTimeout(t *testing.T) {
	t.Parallel()

	for _, selected := range []protocol.Protocol{protocol.OpenAIResponses, protocol.Anthropic, protocol.Gemini} {
		t.Run(string(selected), func(t *testing.T) {
			entered := make(chan struct{}, 1)
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				entered <- struct{}{}
				<-release
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, gatewayProbeResponse(selected))
			}))
			defer server.Close()
			defer close(release)
			manager, spec := gatewayProbeForTest(t, channel.NewAPI, selected, server.URL)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			completed := make(chan execution.AttemptResult, 1)
			go func() { completed <- manager.Execute(ctx, spec) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("probe did not reach upstream")
			}
			cancel()
			select {
			case result := <-completed:
				if err := result.Validate(); err != nil || result.Error == nil {
					t.Fatalf("canceled probe = %+v; validation = %v", result, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("probe ignored cancellation")
			}
			spec.Timeouts.Request = 20 * time.Millisecond
			result := manager.Execute(t.Context(), spec)
			if err := result.Validate(); err != nil || result.Error == nil || result.Error.Kind != execution.ErrorKindTimeout {
				t.Fatalf("timed out probe = %+v; validation = %v", result, err)
			}
		})
	}
}

func gatewayProbeForTest(t *testing.T, id channel.ID, selected protocol.Protocol, baseURL string) (*RuntimeManager, execution.AttemptSpec) {
	t.Helper()
	registry := channel.NewRegistry()
	params, err := json.Marshal(map[string]string{"base_url": baseURL})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := registry.Resolve(id, params)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newRuntimeManager(runtimeOptions{allowPrivateNetwork: true}, registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Shutdown)
	if err := manager.Reconcile([]provideradapter.RuntimeTarget{{Target: resolved}}); err != nil {
		t.Fatal(err)
	}
	spec := utilitySpec(id, selected, execution.OperationProbe, "", "", nil)
	spec.TargetConfig = resolved.TargetConfig
	spec.ClientModel, spec.UpstreamModel = "probe-client", "probe-upstream"
	return manager, freezeTestAttempt(spec)
}

func gatewayProbePath(selected protocol.Protocol) string {
	switch selected {
	case protocol.OpenAIResponses:
		return "/v1/responses"
	case protocol.Anthropic:
		return "/v1/messages"
	case protocol.Gemini:
		return "/v1beta/models/probe-upstream:generateContent"
	default:
		panic("unexpected test protocol")
	}
}

func gatewayProbeResponse(selected protocol.Protocol) string {
	switch selected {
	case protocol.OpenAIResponses:
		return `{"id":"resp_1","object":"response","status":"completed","model":"probe-upstream","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"pong","annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	case protocol.Anthropic:
		return `{"id":"msg_1","type":"message","role":"assistant","model":"probe-upstream","content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
	case protocol.Gemini:
		return `{"candidates":[{"content":{"role":"model","parts":[{"text":"pong"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2},"modelVersion":"probe-upstream"}`
	default:
		panic("unexpected test protocol")
	}
}
