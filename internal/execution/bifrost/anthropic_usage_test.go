package bifrost

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/gateway"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

// 经过真实 SDK 和网关，避免正确的原始用量又被 SDK 的最大值结果覆盖。
func TestNativeAnthropicGatewayUsage(t *testing.T) {
	for _, channelID := range []channel.ID{channel.Anthropic, channel.NewAPI, channel.Sub2API, channel.CLIProxyAPI, channel.GPTLoad} {
		for _, test := range []struct {
			name    string
			start   string
			delta   string
			partial bool
			want    usage.Tokens
		}{
			{
				name:  "missing captured usage retains executor evidence",
				start: `{"output_tokens":0}`,
				delta: `{"input_tokens":100,"cache_read_input_tokens":900,"output_tokens":10}`,
				want:  usage.Tokens{UncachedInput: 100, CacheRead: 900, Output: 10},
			},
			{
				name:  "final input corrects initial estimate",
				start: `{"input_tokens":1200,"output_tokens":0}`,
				delta: `{"input_tokens":100,"cache_read_input_tokens":900,"output_tokens":10}`,
				want:  usage.Tokens{UncachedInput: 100, CacheRead: 900, Output: 10},
			},
			{
				name:  "all input cached",
				start: `{"input_tokens":1200,"output_tokens":0}`,
				delta: `{"input_tokens":0,"cache_read_input_tokens":1000,"output_tokens":10}`,
				want:  usage.Tokens{CacheRead: 1000, Output: 10},
			},
			{
				name:  "real initial usage survives omitted final fields",
				start: `{"input_tokens":100,"cache_read_input_tokens":900,"output_tokens":0}`,
				delta: `{"output_tokens":10}`,
				want:  usage.Tokens{UncachedInput: 100, CacheRead: 900, Output: 10},
			},
			{
				name:  "cache write corrections retain TTL split",
				start: `{"input_tokens":1200,"cache_creation_input_tokens":200,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":100}}`,
				delta: `{"input_tokens":100,"cache_read_input_tokens":800,"cache_creation_input_tokens":100,"cache_creation":{"ephemeral_5m_input_tokens":40,"ephemeral_1h_input_tokens":60},"output_tokens":10}`,
				want:  usage.Tokens{UncachedInput: 100, CacheRead: 800, CacheWrite5M: 40, CacheWrite1H: 60, Output: 10},
			},
			{
				name:  "compaction is billable but serving iterations are not counted twice",
				start: `{"input_tokens":1200,"output_tokens":0}`,
				delta: `{"input_tokens":100,"cache_read_input_tokens":900,"output_tokens":10,"iterations":[{"type":"compaction","input_tokens":50,"cache_read_input_tokens":100,"output_tokens":5},{"type":"message","input_tokens":100,"cache_read_input_tokens":900,"output_tokens":10},{"type":"fallback_message","input_tokens":100,"cache_read_input_tokens":900,"output_tokens":10}]}`,
				want:  usage.Tokens{UncachedInput: 150, CacheRead: 1000, Output: 15},
			},
			{
				name:    "interrupted stream keeps corrected partial usage",
				start:   `{"input_tokens":1200,"output_tokens":0}`,
				delta:   `{"input_tokens":100,"cache_read_input_tokens":900,"output_tokens":10}`,
				partial: true,
				want:    usage.Tokens{UncachedInput: 100, CacheRead: 900, Output: 10},
			},
		} {
			t.Run(string(channelID)+"/"+test.name, func(t *testing.T) {
				wire := fmt.Sprintf("event: message_start\r\ndata: "+`{"type":"message_start","message":{"id":"msg_usage","type":"message","role":"assistant","model":"test-model","content":[],"usage":%s}}`+"\r\n\r\n", test.start)
				wire += fmt.Sprintf("event: message_delta\r\ndata: "+`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":%s}`+"\r\n\r\n", test.delta)
				if !test.partial {
					wire += "event: message_stop\r\ndata: {\"type\":\"message_stop\"}\r\n\r\n"
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, wire)
				}))
				defer server.Close()
				registry := channel.NewRegistry()
				resolved, err := registry.Resolve(channelID, json.RawMessage(`{"base_url":"`+server.URL+`"}`))
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
				defer manager.Shutdown()
				input := gateway.ForwardInput{
					Dialect: dialect.NewAnthropic(), ClientProtocol: protocol.Anthropic,
					RequestID: "usage-request", AttemptID: "usage-attempt", AttemptSequence: 1,
					ChannelID: string(channelID), Operation: execution.OperationChatCompletion,
					RouteMode: execution.RouteNative, TargetConfig: resolved.TargetConfig,
					ExternalModel: "test-model", UpstreamModelID: "test-model", ObserveUsage: true,
					Credential: execution.NewCredentialSnapshot(17, 1, 1, []byte(`{"api_key":"local-test-key"}`)),
					Request: &dialect.ParsedRequest{
						Method: http.MethodPost, Path: "/v1/messages", Header: http.Header{"Content-Type": {"application/json"}},
						Body: []byte(`{"model":"test-model","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hello"}]}`),
					},
				}
				downstream := httptest.NewRecorder()
				result := gateway.NewExecutionForwarder(manager).ForwardStream(t.Context(), input, downstream)
				if !test.partial && (result.Err != nil || result.ExecutionError != nil) {
					t.Fatalf("forward failed: %v / %+v", result.Err, result.ExecutionError)
				}
				wantState := usage.StateComplete
				if test.partial {
					wantState = usage.StatePartial
				}
				if result.Usage.Tokens != test.want || result.Usage.State != wantState || result.Usage.Diagnostics.Has(usage.DiagnosticInvalidEventSequence) {
					t.Fatalf("usage = %+v, want %+v/%s", result.Usage, test.want, wantState)
				}
				if downstream.Body.String() != wire {
					t.Fatal("native response wire changed")
				}
			})
		}
	}
}

func TestTypedAnthropicStreamUsesFinalUsage(t *testing.T) {
	for _, test := range []struct {
		name     string
		channel  channel.ID
		protocol protocol.Protocol
		path     string
		body     string
		mode     execution.RouteMode
	}{
		{"DeepSeek native Anthropic", channel.DeepSeek, protocol.Anthropic, "/v1/messages", `{"model":"test-model","stream":true,"max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`, execution.RouteNative},
		{"Anthropic to Responses", channel.Anthropic, protocol.OpenAIResponses, "/v1/responses", `{"model":"test-model","stream":true,"input":"hello"}`, execution.RouteConverted},
		{"Anthropic to Gemini", channel.Anthropic, protocol.Gemini, "/v1beta/models/test-model:streamGenerateContent", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`, execution.RouteConverted},
		{"Anthropic to Chat Completions", channel.Anthropic, protocol.OpenAICompletions, "/v1/chat/completions", `{"model":"test-model","stream":true,"messages":[{"role":"user","content":"hello"}]}`, execution.RouteConverted},
	} {
		for _, sample := range []struct {
			name, initial, final string
			partial              bool
			want                 usage.Tokens
		}{
			{"corrected input", `{"input_tokens":1200,"output_tokens":0}`, `{"input_tokens":100,"cache_read_input_tokens":900,"output_tokens":10}`, false, usage.Tokens{UncachedInput: 100, CacheRead: 900, Output: 10}},
			{"explicit zero", `{"input_tokens":1200,"output_tokens":0}`, `{"input_tokens":0,"cache_read_input_tokens":1000,"output_tokens":10}`, false, usage.Tokens{CacheRead: 1000, Output: 10}},
			{"omitted final input", `{"input_tokens":100,"cache_read_input_tokens":900,"output_tokens":0}`, `{"output_tokens":10}`, false, usage.Tokens{UncachedInput: 100, CacheRead: 900, Output: 10}},
			{"cache TTL and compaction", `{"input_tokens":1200,"output_tokens":0}`, `{"input_tokens":100,"cache_read_input_tokens":800,"cache_creation_input_tokens":100,"cache_creation":{"ephemeral_5m_input_tokens":40,"ephemeral_1h_input_tokens":60},"output_tokens":10,"iterations":[{"type":"compaction","input_tokens":50,"output_tokens":5},{"type":"message","input_tokens":100,"output_tokens":10}]}`, false, usage.Tokens{UncachedInput: 150, CacheRead: 800, CacheWrite5M: 40, CacheWrite1H: 60, Output: 15}},
			{"truncated", `{"input_tokens":1200,"output_tokens":0}`, `{"input_tokens":100,"cache_read_input_tokens":900,"output_tokens":10}`, true, usage.Tokens{UncachedInput: 100, CacheRead: 900, Output: 10}},
		} {
			t.Run(test.name+"/"+sample.name, func(t *testing.T) {
				wire := "event: message_start\ndata: " + `{"type":"message_start","message":{"id":"msg_usage","type":"message","role":"assistant","model":"test-model","content":[],"usage":{"input_tokens":1200,"output_tokens":0}}}` + "\n\n" +
					"event: content_block_start\ndata: " + `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
					"event: content_block_delta\ndata: " + `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"OK"}}` + "\n\n" +
					"event: content_block_stop\ndata: " + `{"type":"content_block_stop","index":0}` + "\n\n" +
					"event: message_delta\ndata: " + `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":100,"cache_read_input_tokens":900,"output_tokens":10}}` + "\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
				wire = strings.Replace(wire, `{"input_tokens":1200,"output_tokens":0}`, sample.initial, 1)
				wire = strings.Replace(wire, `{"input_tokens":100,"cache_read_input_tokens":900,"output_tokens":10}`, sample.final, 1)
				if sample.partial {
					wire = strings.TrimSuffix(wire, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
				}

				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, wire)
				}))
				defer server.Close()
				registry := channel.NewRegistry()
				resolved, err := registry.Resolve(test.channel, json.RawMessage(`{"base_url":"`+server.URL+`"}`))
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
				defer manager.Shutdown()
				selected := map[protocol.Protocol]dialect.Dialect{
					protocol.Gemini: dialect.NewGemini(), protocol.Anthropic: dialect.NewAnthropic(), protocol.OpenAIResponses: dialect.NewOpenAIResponses(), protocol.OpenAICompletions: dialect.NewOpenAI(),
				}[test.protocol]
				input := gateway.ForwardInput{
					Dialect: selected, ClientProtocol: test.protocol, RequestID: "typed-usage", AttemptID: "typed-usage-1", AttemptSequence: 1,
					ChannelID: string(test.channel), Operation: execution.OperationChatCompletion, RouteMode: test.mode,
					TargetConfig: resolved.TargetConfig, ExternalModel: "test-model", UpstreamModelID: "test-model", ObserveUsage: true,
					Credential: execution.NewCredentialSnapshot(17, 1, 1, []byte(`{"api_key":"local-test-key"}`)),
					Request:    &dialect.ParsedRequest{Method: http.MethodPost, Path: test.path, Body: []byte(test.body)},
				}
				if test.protocol == protocol.OpenAIResponses {
					input.Operation = execution.OperationResponsesCreate
				}
				downstream := httptest.NewRecorder()
				result := gateway.NewExecutionForwarder(manager).ForwardStream(t.Context(), input, downstream)
				want := sample.want
				wantState := usage.StateComplete
				if sample.partial {
					wantState = usage.StatePartial
				}
				if (!sample.partial && (result.Err != nil || result.ExecutionError != nil)) || result.Usage.Tokens != want || result.Usage.State != wantState {
					t.Fatalf("usage = %+v, error=%v/%+v, want %+v; wire=%s", result.Usage, result.Err, result.ExecutionError, want, downstream.Body.String())
				}
				assertNoPrivateLeak(t, downstream.Body.String(), "raw_response", "raw_request", "local-test-key")
			})
		}
	}
}
