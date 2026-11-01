package cpa

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/telemetry"
)

func TestCodexTransientCapacityUsesExistingCandidatePolicy(t *testing.T) {
	for _, test := range []struct {
		name     string
		body     string
		capacity bool
	}{
		{"turn limit", `{"error":{"type":"rate_limit_error","code":"turn_rate_limit_exceeded","message":"Codex is at capacity"}}`, true},
		{"capacity without code", `{"error":{"type":"rate_limit_error","message":"Codex is at capacity"}}`, true},
		{"quota takes precedence", `{"error":{"type":"usage_limit_reached","code":"turn_rate_limit_exceeded","message":"usage limit reached"}}`, false},
		{"generic rate limit", `{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"The model is at capacity"}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter, _, _, keys, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusTooManyRequests,
					Header: http.Header{"Content-Type": {"application/json"}, "Retry-After": {"256"}},
					Body:   io.NopCloser(strings.NewReader(test.body)), Request: r}, nil
			})))
			result := adapter.Execute(ctx, validSpec(t, row, keys))
			if result.Error == nil || result.Error.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("result = %+v", result)
			}
			decision := health.JudgeExecution(health.ExecutionAttempt{
				DispatchState: result.DispatchState, StatusCode: result.StatusCode, Evidence: result.Error, Now: time.Now(),
			}, health.DecisionContext{Method: http.MethodPost, Operation: execution.OperationResponsesCreate})
			if test.capacity {
				if result.Error.Hint != execution.FailureHintCandidateUnavailable ||
					result.Error.ReplaySafety != execution.ReplaySafetyRejectedBeforeProcessing ||
					decision.Retry != health.RetryNextCandidate || decision.Effect != health.EffectNone || decision.QuotaCooldown {
					t.Fatalf("capacity evidence=%+v decision=%+v", result.Error, decision)
				}
			} else if result.Error.Hint != execution.FailureHintRateLimited || !decision.QuotaCooldown {
				t.Fatalf("rate limit lost quota policy: evidence=%+v decision=%+v", result.Error, decision)
			}
		})
	}
}

func TestCodexGatewayPreservesIncompleteAcrossProtocols(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, terminal := range []string{"completed", "incomplete"} {
		for _, test := range []struct {
			name string
			path string
			body string
		}{
			{"Responses", "/v1/responses", `{"model":"gpt-5","stream":true,"input":"hello"}`},
			{"Chat", "/v1/chat/completions", `{"model":"gpt-5","stream":true,"messages":[{"role":"user","content":"hello"}]}`},
			{"Anthropic", "/v1/messages", `{"model":"gpt-5","stream":true,"max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`},
			{"Gemini", "/v1beta/models/gpt-5:streamGenerateContent", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`},
		} {
			t.Run(terminal+"/"+test.name, func(t *testing.T) {
				var calls atomic.Int32
				upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: "+`{"type":"response.created","response":{"id":"resp_1","object":"response","status":"in_progress","created_at":1,"model":"gpt-5","output":[]}}`+"\n\n")
					_, _ = io.WriteString(w, "data: "+`{"type":"response.output_text.delta","response_id":"resp_1","output_index":0,"item_id":"msg_1","content_index":0,"delta":"partial answer"}`+"\n\n")
					details := ""
					if terminal == "incomplete" {
						details = `,"incomplete_details":{"reason":"max_output_tokens"}`
					}
					_, _ = fmt.Fprintf(w, "data: "+`{"type":"response.%s","response":{"id":"resp_1","object":"response","status":"%s","model":"gpt-5"%s,"output":[],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`+"\n\n", terminal, terminal, details)
				}))
				defer upstream.Close()
				engine, sink := newCodexBootstrapGateway(t, upstream.URL, nil)
				request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
				request = request.WithContext(context.WithValue(t.Context(), "cliproxy.roundtripper", upstream.Client().Transport))
				request.Header.Set("Authorization", "Bearer gl-client")
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, request)
				if calls.Load() != 1 || len(sink.events) != 1 || response.Code != http.StatusOK ||
					!strings.Contains(response.Body.String(), "partial answer") || strings.Contains(response.Body.String(), "event: error") {
					t.Fatalf("calls=%d logs=%d response=%d %s", calls.Load(), len(sink.events), response.Code, response.Body)
				}
				event := sink.events[0]
				wantStatus, wantCode := telemetry.RequestStatusSuccess, ""
				if terminal == "incomplete" {
					wantStatus, wantCode = telemetry.RequestStatusIncomplete, "upstream_response_incomplete"
				}
				if event.Status != wantStatus || event.ErrorCode != wantCode || len(event.Attempts) != 1 ||
					event.Attempts[0].Effect != telemetry.EffectNone || event.Attempts[0].WillRetry ||
					event.Usage.Result.Tokens.UncachedInput != 2 || event.Usage.Result.Tokens.Output != 1 {
					t.Fatalf("status=%s code=%s attempts=%+v usage=%+v", event.Status, event.ErrorCode, event.Attempts, event.Usage.Result)
				}
			})
		}
	}
}
