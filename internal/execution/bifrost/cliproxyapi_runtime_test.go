package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/provideradapter"
)

func TestCLIProxyAPIModelDiscoveryUsesOpenAIEndpoint(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/team-a/v1/models" {
			t.Errorf("upstream target = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+testAPIKey {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"object":"list","data":[{"id":"cpa-model"}]}`)
	}))
	defer server.Close()

	manager, resolved := newCLIProxyAPIManagerForTest(t, server.URL+"/team-a")
	spec := utilitySpec(channel.CLIProxyAPI, protocol.OpenAICompletions, execution.OperationListModels, http.MethodGet, "/v1/models", nil)
	spec.TargetConfig = resolved.TargetConfig
	spec = freezeTestAttempt(spec)
	result := manager.Execute(context.Background(), spec)
	if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != http.StatusOK ||
		!bytes.Contains(result.Body, []byte(`"id":"cpa-model"`)) {
		t.Fatalf("result = %+v, validation=%v, body=%s", result, err, result.Body)
	}
}

func newCLIProxyAPIManagerForTest(t *testing.T, baseURL string) (*RuntimeManager, channel.ResolvedTarget) {
	t.Helper()

	registry := channel.NewRegistry()
	resolved, err := registry.Resolve(
		channel.CLIProxyAPI,
		json.RawMessage(`{"base_url":"`+baseURL+`"}`),
	)
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
	return manager, resolved
}

func TestCLIProxyAPIReportedModelsPreserveWire(t *testing.T) {
	for _, shape := range []struct {
		name                         string
		protocol                     protocol.Protocol
		operation                    execution.Operation
		path, request, unary, stream string
	}{
		{"chat", protocol.OpenAICompletions, execution.OperationChatCompletion, "/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"}]}`, `{"id":"c","model":"reported","choices":[]}`, "data: {\"model\":\"reported\",\"choices\":[]}\n\ndata: [DONE]\n\n"},
		{"responses", protocol.OpenAIResponses, execution.OperationResponsesCreate, "/v1/responses", `{"input":"hi"}`, `{"id":"r","object":"response","model":"reported","output":[]}`, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"object\":\"response\",\"status\":\"completed\",\"model\":\"reported\"}}\n\n"},
		{"anthropic", protocol.Anthropic, execution.OperationChatCompletion, "/v1/messages", `{"messages":[{"role":"user","content":"hi"}],"max_tokens":8}`, `{"id":"m","type":"message","model":"reported","content":[]}`, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"model\":\"reported\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
		{"gemini", protocol.Gemini, execution.OperationChatCompletion, "/v1beta/models/requested:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`, `{"modelVersion":"reported","candidates":[]}`, "data: {\"modelVersion\":\"reported\",\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n"},
	} {
		for _, stream := range []bool{false, true} {
			for _, missing := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream=%t/missing=%t", shape.name, stream, missing), func(t *testing.T) {
					wire := shape.unary
					if stream {
						wire = shape.stream
					}
					wantModel := "reported"
					if missing {
						wire = strings.ReplaceAll(wire, `"model":"reported",`, "")
						wire = strings.ReplaceAll(wire, `,"model":"reported"`, "")
						wire = strings.ReplaceAll(wire, `"modelVersion":"reported",`, "")
						wantModel = ""
					}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						contentType := "application/json"
						if stream {
							contentType = "text/event-stream"
						}
						w.Header().Set("Content-Type", contentType)
						_, _ = io.WriteString(w, wire)
					}))
					defer server.Close()
					manager, resolved := newCLIProxyAPIManagerForTest(t, server.URL)
					spec := compatibleSpec(server.URL)
					spec.ChannelID = string(channel.CLIProxyAPI)
					spec.TargetConfig = resolved.TargetConfig
					spec.ClientProtocol = shape.protocol
					spec.Operation = shape.operation
					spec.Path = shape.path
					if stream && shape.protocol == protocol.Gemini {
						spec.Path = strings.ReplaceAll(spec.Path, ":generateContent", ":streamGenerateContent")
					}
					spec.Body = []byte(shape.request)
					spec.ClientModel = "requested"
					spec.UpstreamModel = "requested"
					spec = freezeTestAttempt(spec)
					var model string
					var body []byte
					if stream {
						var output bytes.Buffer
						result := manager.ExecuteStream(t.Context(), spec, func(e execution.StreamEvent) error {
							if e.Kind == execution.StreamEventData {
								output.Write(e.Data)
							}
							return nil
						})
						if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != 200 {
							t.Fatalf("execution=%+v validation=%v", result, err)
						}
						model = result.Model
						body = output.Bytes()
					} else {
						result := manager.Execute(t.Context(), spec)
						if err := result.Validate(); err != nil || result.Error != nil || result.StatusCode != 200 {
							t.Fatalf("execution=%+v validation=%v", result, err)
						}
						model = result.Model
						body = result.Body
					}
					if string(body) != wire {
						t.Fatalf("wire changed: %s", body)
					}
					if model != wantModel {
						t.Fatalf("reported model=%q want=%q", model, wantModel)
					}
				})
			}
		}
	}
}
