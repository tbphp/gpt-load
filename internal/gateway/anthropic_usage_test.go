package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

func TestExecutionForwarderAnthropicUsageAuthority(t *testing.T) {
	sdkUsage := usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1200, CacheRead: 900, Output: 10}}
	for _, test := range []struct {
		name     string
		mode     execution.RouteMode
		observe  bool
		canceled bool
		want     usage.Result
	}{
		{name: "native fragmented stream", mode: execution.RouteNative, observe: true,
			want: usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 100, CacheRead: 900, Output: 10}}},
		{name: "canceled native stream", mode: execution.RouteNative, observe: true, canceled: true,
			want: usage.Result{State: usage.StatePartial, Tokens: usage.Tokens{UncachedInput: 100, CacheRead: 900, Output: 10}}},
		{name: "converted stream retains upstream accounting", mode: execution.RouteConverted, observe: true, want: sdkUsage},
		{name: "disabled wire observation retains executor accounting", mode: execution.RouteNative, want: sdkUsage},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := executionForwardInput()
			input.Dialect, input.ClientProtocol = dialect.NewAnthropic(), protocol.Anthropic
			input.RouteMode, input.RouteRequirement = test.mode, execution.RouteRequirementAny
			input.ObserveUsage = test.observe
			input.Request.Path = "/v1/messages"
			input.Request.Body = []byte(`{"model":"public","max_tokens":16,"stream":true,"messages":[{"role":"user","content":"hello"}]}`)
			wire := "data: " + `{"type":"message_start","message":{"id":"msg","model":"public","usage":{"input_tokens":1200,"output_tokens":0}}}` + "\r\n\r\n" +
				"data: " + `{"type":"message_delta","usage":{"input_tokens":100,"cache_read_input_tokens":900,"output_tokens":10},"delta":{"stop_reason":"end_turn"}}` + "\r\n\r\n"
			if !test.canceled {
				wire += "data: {\"type\":\"message_stop\"}\r\n\r\n"
			}
			executor := fakeExecutionExecutor{stream: func(_ context.Context, _ execution.AttemptSpec, sink execution.StreamSink) execution.StreamResult {
				header := http.Header{"Content-Type": {"text/event-stream"}}
				if err := sink(execution.StreamEvent{Sequence: 1, Kind: execution.StreamEventReady, StatusCode: http.StatusOK, Header: header}); err != nil {
					t.Fatal(err)
				}
				sequence := uint64(1)
				for start := 0; start < len(wire); start += 7 {
					sequence++
					if err := sink(execution.StreamEvent{Sequence: sequence, Kind: execution.StreamEventData, Data: []byte(wire[start:min(start+7, len(wire))])}); err != nil {
						t.Fatal(err)
					}
				}
				result := execution.StreamResult{
					DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
					StatusCode: http.StatusOK, Header: header,
					Usage: &execution.UsageEvidence{Normalized: sdkUsage},
				}
				if test.canceled {
					result.Error = &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled, Summary: "request canceled"}
				}
				return result
			}}
			downstream := httptest.NewRecorder()
			result := NewExecutionForwarder(executor).ForwardStream(t.Context(), input, downstream)
			if result.Usage != test.want {
				t.Fatalf("usage = %+v, want %+v", result.Usage, test.want)
			}
			if downstream.Body.String() != wire {
				t.Fatal("forwarded SSE changed")
			}
		})
	}
}

func TestExecutionForwarderAnthropicNonStreamUsageAuthority(t *testing.T) {
	sdkUsage := usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 100, Output: 10}}
	advisor := usage.ModelTokens{Model: "claude-opus-4-8", Tokens: usage.Tokens{UncachedInput: 300, Output: 400}}
	body := `{"id":"msg","type":"message","model":"public","content":[],"usage":{"input_tokens":100,"output_tokens":10,` +
		`"iterations":[{"type":"message","input_tokens":40,"output_tokens":4},` +
		`{"type":"advisor_message","model":"claude-opus-4-8","input_tokens":300,"output_tokens":400},` +
		`{"type":"compaction","input_tokens":5,"output_tokens":1},` +
		`{"type":"message","input_tokens":60,"output_tokens":6}]}}`
	for _, test := range []struct {
		name        string
		mode        execution.RouteMode
		observe     bool
		body        string
		wantTokens  usage.Tokens
		wantAdvisor bool
	}{
		{name: "native body carries iteration usage", mode: execution.RouteNative, observe: true, body: body,
			wantTokens: usage.Tokens{UncachedInput: 105, Output: 11}, wantAdvisor: true},
		{name: "native body without usage retains executor accounting", mode: execution.RouteNative, observe: true,
			body: `{"id":"msg","type":"message","model":"public","content":[]}`, wantTokens: sdkUsage.Tokens},
		{name: "converted body retains executor accounting", mode: execution.RouteConverted, observe: true, body: body, wantTokens: sdkUsage.Tokens},
		{name: "disabled observation retains executor accounting", mode: execution.RouteNative, body: body, wantTokens: sdkUsage.Tokens},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := executionForwardInput()
			input.Dialect, input.ClientProtocol = dialect.NewAnthropic(), protocol.Anthropic
			input.RouteMode, input.RouteRequirement = test.mode, execution.RouteRequirementAny
			input.ObserveUsage = test.observe
			input.Request.Path = "/v1/messages"
			input.Request.Body = []byte(`{"model":"public","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`)
			executor := fakeExecutionExecutor{unary: func(context.Context, execution.AttemptSpec) execution.AttemptResult {
				return execution.AttemptResult{
					DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
					StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
					Body: []byte(test.body), Usage: &execution.UsageEvidence{Normalized: sdkUsage},
				}
			}}
			result := NewExecutionForwarder(executor).Forward(t.Context(), input)
			if result.Err != nil || result.Usage.State != usage.StateComplete || result.Usage.Tokens != test.wantTokens {
				t.Fatalf("usage = %+v, err = %v, want tokens %+v", result.Usage, result.Err, test.wantTokens)
			}
			if !test.wantAdvisor {
				if result.Usage.Advisor != nil {
					t.Fatalf("advisor usage = %+v, want none", result.Usage.Advisor)
				}
				return
			}
			if result.Usage.Advisor == nil || len(result.Usage.Advisor.Models) != 1 || result.Usage.Advisor.Models[0] != advisor {
				t.Fatalf("advisor usage = %+v, want %+v", result.Usage.Advisor, advisor)
			}
		})
	}
}
