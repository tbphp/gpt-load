package bifrost

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/provideradapter"
)

func TestOpenCodePresetsPreserveNativeProtocols(t *testing.T) {
	for _, id := range []channel.ID{"opencode_go", "opencode_zen"} {
		for _, test := range []struct {
			protocol protocol.Protocol
			path     string
			body     string
			response string
			stream   string
		}{
			{
				protocol: protocol.OpenAICompletions, path: "/v1/chat/completions",
				body:     `{"model":"client-model","messages":[{"role":"user","content":"hello"}]}`,
				response: `{"id":"chat_1","object":"chat.completion","model":"upstream-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
				stream:   "data: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\ndata: [DONE]\n\n",
			},
			{
				protocol: protocol.OpenAIResponses, path: "/v1/responses",
				body:     `{"model":"client-model","input":"hello","store":false}`,
				response: `{"id":"resp_1","object":"response","status":"completed","model":"upstream-model","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
				stream:   openAIResponsesStreamFixture,
			},
			{
				protocol: protocol.Anthropic, path: "/v1/messages",
				body:     `{"model":"client-model","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`,
				response: `{"id":"msg_1","type":"message","role":"assistant","model":"upstream-model","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`,
				stream:   anthropicResponsesStreamFixture,
			},
		} {
			for _, mode := range []string{"unary", "stream", "probe"} {
				t.Run(string(id)+"/"+string(test.protocol)+"/"+mode, func(t *testing.T) {
					prefix := "/zen"
					if id == "opencode_go" {
						prefix += "/go"
					}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method != http.MethodPost || r.URL.Path != prefix+test.path {
							t.Errorf("unexpected target %s %s", r.Method, r.URL.Path)
						}
						wantAuth, wantAPIKey := "Bearer "+testAPIKey, ""
						if test.protocol == protocol.Anthropic {
							wantAuth, wantAPIKey = "", testAPIKey
						}
						if r.Header.Get("Authorization") != wantAuth || r.Header.Get("X-Api-Key") != wantAPIKey {
							t.Error("selected credential did not replace client authentication")
						}
						body, err := io.ReadAll(r.Body)
						if err != nil {
							t.Error(err)
							return
						}
						var payload map[string]json.RawMessage
						if err := json.Unmarshal(body, &payload); err != nil {
							t.Error(err)
							return
						}
						if string(payload["model"]) != `"upstream-model"` || payload["api_key"] != nil {
							t.Errorf("unexpected upstream body %s", body)
						}
						if mode != "probe" {
							if !bytes.Contains(payload["vendor_extension"], []byte(`1.2300`)) {
								t.Errorf("native extension changed: %s", body)
							}
							if r.Header.Get("User-Agent") != "coding-client/1.0" || r.Header.Get("X-Opencode-Session") != "conversation-one" {
								t.Error("coding client identity or conversation header was lost")
							}
						}
						if mode == "stream" {
							w.Header().Set("Content-Type", "text/event-stream")
							_, _ = io.WriteString(w, test.stream)
							w.(http.Flusher).Flush()
						} else {
							w.Header().Set("Content-Type", "application/json")
							_, _ = io.WriteString(w, test.response)
						}
					}))
					defer server.Close()
					manager, target := newOpenCodeManagerForTest(t, id, server.URL+prefix)
					operation := execution.OperationChatCompletion
					if test.protocol == protocol.OpenAIResponses {
						operation = execution.OperationResponsesCreate
					}
					body := strings.TrimSuffix(test.body, "}") + `,"vendor_extension":{"value":1.2300},"api_key":"client-secret"}`
					if mode == "stream" {
						body = strings.TrimSuffix(body, "}") + `,"stream":true}`
					}
					if mode == "probe" {
						operation = execution.OperationProbe
					}
					spec := utilitySpec(id, test.protocol, operation, http.MethodPost, test.path, []byte(body))
					spec.ClientModel, spec.UpstreamModel = "client-model", "upstream-model"
					spec.TargetConfig = target.TargetConfig
					spec.Header.Set("Content-Type", "application/json")
					spec.Header.Set("Authorization", "Bearer client-secret")
					spec.Header.Set("X-Api-Key", "client-secret")
					spec.Header.Set("User-Agent", "coding-client/1.0")
					spec.Header.Set("X-Opencode-Session", "conversation-one")
					if mode == "stream" {
						spec.ClientModel = spec.UpstreamModel
					}
					if mode == "probe" {
						spec.Method, spec.Path = "", ""
						spec.Body = nil
					}
					spec = freezeTestAttempt(spec)
					if mode == "stream" {
						var data bytes.Buffer
						result := manager.ExecuteStream(t.Context(), spec, func(event execution.StreamEvent) error {
							if event.Kind == execution.StreamEventData {
								data.Write(event.Data)
							}
							return nil
						})
						if data.String() != test.stream {
							t.Errorf("native SSE changed: %q", data.String())
						}
						if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK || result.UpstreamProtocol != test.protocol || result.Usage == nil {
							t.Fatalf("stream result=%+v error=%+v contract=%v", result, result.Error, err)
						}
						return
					}
					result := manager.Execute(t.Context(), spec)
					if mode != "probe" {
						var got, want map[string]any
						if err := json.Unmarshal(result.Body, &got); err != nil {
							t.Fatal(err)
						}
						if err := json.Unmarshal([]byte(test.response), &want); err != nil {
							t.Fatal(err)
						}
						want["model"] = "client-model"
						if !reflect.DeepEqual(got, want) {
							t.Errorf("native response or model alias changed: %s", result.Body)
						}
					}
					if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK || result.UpstreamProtocol != test.protocol {
						t.Fatalf("result=%+v error=%+v contract=%v", result, result.Error, err)
					}
					if mode != "probe" && result.Usage == nil {
						t.Error("native usage was lost")
					}
				})
			}
		}
	}
}

func TestOpenCodePresetsDiscoverModelsAtGatewayRoot(t *testing.T) {
	for _, id := range []channel.ID{"opencode_go", "opencode_zen"} {
		t.Run(string(id), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/custom-root/v1/models" || r.Header.Get("Authorization") != "Bearer "+testAPIKey {
					t.Errorf("unexpected model discovery target %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"model-one"}]}`)
			}))
			defer server.Close()
			manager, target := newOpenCodeManagerForTest(t, id, server.URL+"/custom-root")
			spec := utilitySpec(id, protocol.OpenAICompletions, execution.OperationListModels, http.MethodGet, "/v1/models", nil)
			spec.TargetConfig = target.TargetConfig
			result := manager.Execute(t.Context(), freezeTestAttempt(spec))
			if err := result.Validate(); err != nil || result.Error != nil || !bytes.Contains(result.Body, []byte(`"id":"model-one"`)) {
				t.Fatalf("models result=%+v contract=%v", result, err)
			}
		})
	}
}

func newOpenCodeManagerForTest(t *testing.T, id channel.ID, baseURL string) (*RuntimeManager, channel.ResolvedTarget) {
	t.Helper()
	registry := channel.NewRegistry()
	target, err := registry.Resolve(id, json.RawMessage(`{"base_url":"`+baseURL+`"}`))
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
	if err := manager.Reconcile([]provideradapter.RuntimeTarget{{Target: target}}); err != nil {
		t.Fatal(err)
	}
	return manager, target
}
