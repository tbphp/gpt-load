package cpa

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/gateway"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/usage"
)

type usageStreamFixtureBridge struct {
	providerBridge
	chunks <-chan providerStreamChunk
}

func (bridge usageStreamFixtureBridge) ExecuteStream(context.Context, string, providerCredential, providerRequest) (*providerStreamResponse, error) {
	return &providerStreamResponse{Headers: http.Header{"Content-Type": {"text/event-stream"}}, Chunks: bridge.chunks}, nil
}

func TestCPAAnthropicStreamUsageBoundaries(t *testing.T) {
	const start = "event: message_start\r\ndata: " + `{"type":"message_start","message":{"id":"msg_1","model":"upstream","content":[],"usage":{"input_tokens":902,"output_tokens":0}}}` + "\r\n\r\n"
	const delta = "event: message_delta\r\ndata: " + `{"type":"message_delta","usage":{"input_tokens":100,"cache_read_input_tokens":900,"output_tokens":10},"delta":{"stop_reason":"end_turn"}}` + "\r\n\r\n"
	const stop = "event: message_stop\r\ndata: " + `{"type":"message_stop"}` + "\r\n\r\n"
	for _, test := range []struct {
		name   string
		native bool
		wire   string
		err    error
		want   usage.Tokens
		state  usage.State
	}{
		{name: "combined events and model alias", wire: start + delta + stop,
			want: usage.Tokens{UncachedInput: 100, CacheRead: 900, Output: 10}, state: usage.StateComplete},
		{name: "native initial usage stays authoritative", native: true,
			wire: start + strings.Replace(delta, `"input_tokens":100,`, "", 1) + stop,
			want: usage.Tokens{UncachedInput: 902, CacheRead: 900, Output: 10}, state: usage.StateComplete},
		{name: "read failure cannot bill the estimate", wire: start, err: io.ErrUnexpectedEOF, state: usage.StatePartial},
		{name: "cancellation cannot bill the estimate", wire: start, err: context.Canceled, state: usage.StatePartial},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			spec := validSpec(t, row, keyService)
			kind := channel.ProviderCodex
			spec.ClientProtocol, spec.RouteMode = protocol.Anthropic, execution.RouteConverted
			spec.Operation, spec.Path = execution.OperationChatCompletion, "/v1/messages"
			spec.Body = []byte(`{"model":"client-model","max_tokens":32,"stream":true,"messages":[{"role":"user","content":"hello"}]}`)
			if test.native {
				adapter, keyService, row = newClaudeAdapterFixture(t)
				spec = validClaudeSpec(t, row, keyService)
				kind = channel.ProviderClaude
			}
			spec.ClientModel = "client-model"
			chunks := make(chan providerStreamChunk, 2)
			chunks <- providerStreamChunk{Payload: []byte(test.wire)}
			if test.err != nil {
				chunks <- providerStreamChunk{Err: test.err}
			}
			close(chunks)
			adapter.providers[kind] = usageStreamFixtureBridge{providerBridge: adapter.providers[kind], chunks: chunks}
			input := gateway.ForwardInput{
				Dialect: dialect.NewAnthropic(), Group: state.GroupView{ID: row.GroupID},
				Request:   &dialect.ParsedRequest{Method: spec.Method, Path: spec.Path, Body: spec.Body},
				RequestID: spec.RequestID, AttemptID: spec.AttemptID, AttemptSequence: spec.Sequence,
				ClientProtocol: spec.ClientProtocol, Operation: spec.Operation, ChannelID: spec.ChannelID,
				RouteMode: spec.RouteMode, TargetConfig: spec.TargetConfig, Credential: spec.Credential,
				ExternalModel: spec.ClientModel, UpstreamModelID: spec.UpstreamModel, ObserveUsage: true,
			}
			downstream := httptest.NewRecorder()
			result := gateway.NewExecutionForwarder(adapter).ForwardStream(t.Context(), input, downstream)
			if result.Err != nil || (result.ExecutionError != nil) != (test.err != nil) {
				t.Fatalf("forward error = %v, execution error = %+v, want failure=%t", result.Err, result.ExecutionError, test.err != nil)
			}
			if result.Usage.Tokens != test.want || result.Usage.State != test.state ||
				result.Usage.Diagnostics.Has(usage.DiagnosticInvalidEventSequence) {
				t.Fatalf("usage = %+v, want %+v/%s; wire=%s", result.Usage, test.want, test.state, downstream.Body)
			}
			if !strings.Contains(downstream.Body.String(), `"model":"client-model"`) {
				t.Fatalf("client model alias lost: %s", downstream.Body)
			}
		})
	}
}

// 经过真实 CPA 转换和网关统计，防止只修正客户端展示或只修正日志。
func TestCPAConvertedAnthropicStreamUsesFinalUsage(t *testing.T) {
	for _, provider := range []string{"codex", "grok", "antigravity"} {
		for _, thinking := range []bool{false, true} {
			name := provider + "/without thinking"
			if thinking {
				name = provider + "/with thinking"
			}
			t.Run(name, func(t *testing.T) {
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, convertedAnthropicUpstreamFixture(provider))
				}))
				defer server.Close()
				previousTransport := http.DefaultTransport
				http.DefaultTransport = server.Client().Transport
				defer func() { http.DefaultTransport = previousTransport }()

				canonical, identity, model := convertedAnthropicCredentialFixture(provider)
				adapter, _, _, _, row := newSubscriptionAdapterFixture(t, provider, canonical, identity)
				request := map[string]any{
					"model": model, "max_tokens": 8192, "stream": true,
					"messages": []any{map[string]any{"role": "user", "content": strings.Repeat("stable input text ", 300)}},
				}
				if thinking {
					request["thinking"] = map[string]any{"type": "adaptive"}
					request["output_config"] = map[string]any{"effort": "high"}
				}
				body, err := json.Marshal(request)
				if err != nil {
					t.Fatal(err)
				}
				target, err := json.Marshal(map[string]string{"base_url": server.URL})
				if err != nil {
					t.Fatal(err)
				}
				input := gateway.ForwardInput{
					Dialect: dialect.NewAnthropic(), Group: state.GroupView{ID: row.GroupID},
					Request:   &dialect.ParsedRequest{Method: http.MethodPost, Path: "/v1/messages", Body: body},
					RequestID: "request-1", AttemptID: "attempt-1", AttemptSequence: 1,
					ClientProtocol: protocol.Anthropic, Operation: execution.OperationChatCompletion,
					ChannelID: provider, RouteMode: execution.RouteConverted, TargetConfig: target,
					Credential: execution.NewCredentialSnapshot(row.ID, row.SecretVersion,
						stateloader.CredentialIdentityGeneration(row.IdentityFingerprint, provider, "subscription", json.RawMessage(`{}`)), canonical),
					ExternalModel: model, UpstreamModelID: model, ObserveUsage: true,
				}
				downstream := httptest.NewRecorder()
				result := gateway.NewExecutionForwarder(adapter).ForwardStream(t.Context(), input, downstream)
				if result.Err != nil || result.StatusCode != http.StatusOK {
					t.Fatalf("ForwardStream() = %v, status=%d; wire=%s", result.Err, result.StatusCode, downstream.Body)
				}
				want := usage.Tokens{UncachedInput: 100, CacheRead: 900, Output: 10}
				if result.Usage.Tokens != want || result.Usage.State != usage.StateComplete ||
					result.Usage.Diagnostics.Has(usage.DiagnosticInvalidEventSequence) {
					t.Errorf("gateway usage = %+v, want %+v without a regressing estimate", result.Usage, want)
				}
				assertConvertedAnthropicWireUsage(t, downstream.Body.String(), want)
			})
		}
	}
}

func assertConvertedAnthropicWireUsage(t *testing.T, wire string, want usage.Tokens) {
	t.Helper()
	extractor := dialect.NewAnthropic().NewUsageStreamExtractor()
	for _, line := range strings.Split(wire, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		if err := extractor.Observe(payload); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := extractor.Finalize()
	if got.Tokens != want || got.State != usage.StateComplete || got.Diagnostics.Has(usage.DiagnosticInvalidEventSequence) {
		t.Errorf("client usage = %+v, want %+v; wire=%s", got, want, wire)
	}
}

func convertedAnthropicCredentialFixture(provider string) ([]byte, string, string) {
	switch provider {
	case "codex":
		return []byte(`{"type":"codex","access_token":"test-access","refresh_token":"test-refresh","account_id":"test-account","expired":"2030-01-01T00:00:00Z"}`), "test-account", "gpt-5.6-sol"
	case "grok":
		return []byte(`{"type":"grok","access_token":"test-access","refresh_token":"test-refresh","account_id":"test-account","email":"test@example.com","token_type":"Bearer","expired":"2030-01-01T00:00:00Z","token_endpoint":"https://auth.x.ai/oauth/token"}`), "test-account", "grok-4.3"
	default:
		return []byte(`{"type":"antigravity","access_token":"test-access","refresh_token":"test-refresh","account_id":"test-account","email":"test@example.com","project_id":"test-project","expired":"2030-01-01T00:00:00Z"}`), "test-account", "gemini-live"
	}
}

func convertedAnthropicUpstreamFixture(provider string) string {
	if provider == "antigravity" {
		return `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}}` + "\n" +
			`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1000,"cachedContentTokenCount":900,"candidatesTokenCount":10,"thoughtsTokenCount":0,"totalTokenCount":1010}}}` + "\n"
	}
	return "data: " + `{"type":"response.created","response":{"id":"resp_1","model":"upstream-model","status":"in_progress","output":[]}}` + "\n\n" +
		"data: " + `{"type":"response.output_text.delta","response_id":"resp_1","output_index":0,"content_index":0,"delta":"ok"}` + "\n\n" +
		"data: " + `{"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","model":"upstream-model","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1000,"input_tokens_details":{"cached_tokens":900},"output_tokens":10,"total_tokens":1010}}}` + "\n\n"
}
