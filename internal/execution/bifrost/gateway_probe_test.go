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
		{"responses-failed", protocol.OpenAIResponses, `{"object":"response","status":"failed","output":[]}`, false},
		{"anthropic-empty-content", protocol.Anthropic, `{"type":"message","content":[]}`, true},
		{"gemini-no-candidates", protocol.Gemini, `{"candidates":[]}`, false},
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
		for _, body := range []string{`{}`, `{"error":{"message":"upstream rejected probe"}}`, `null`, `[]`} {
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
