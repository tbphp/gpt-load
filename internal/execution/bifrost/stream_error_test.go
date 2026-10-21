package bifrost

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gpt-load/internal/execution"
	"gpt-load/internal/health"
)

func TestConvertedChatStreamPreservesErrorStatusSource(t *testing.T) {
	for _, test := range []struct {
		name       string
		httpStatus int
		payload    string
		status     int
		hint       execution.FailureHint
		effect     health.Effect
		retry      health.RetryDirective
	}{
		{
			name: "explicit SSE authentication status", httpStatus: http.StatusOK,
			payload: `{"type":"error","status_code":401,"error":{"message":"request refused"}}`,
			status:  http.StatusUnauthorized, hint: execution.FailureHintInvalidCredential,
			effect: health.EffectRecordCredentialFailure, retry: health.RetryNextCandidate,
		},
		{
			name: "explicit SSE rate limit status", httpStatus: http.StatusOK,
			payload: `{"type":"error","status_code":429,"error":{"message":"request refused"}}`,
			status:  http.StatusTooManyRequests, hint: execution.FailureHintRateLimited,
			effect: health.EffectCooldownModel, retry: health.RetryNextCandidate,
		},
		{
			name: "SSE error with outer type", httpStatus: http.StatusOK,
			payload: `{"type":"error","error":{"type":"server_error","message":"internal error"}}`,
			effect:  health.EffectNone, retry: health.RetryNone,
		},
		{
			name: "SSE error without outer type", httpStatus: http.StatusOK,
			payload: `{"error":{"type":"server_error","message":"internal error"}}`,
			effect:  health.EffectNone, retry: health.RetryNone,
		},
		{
			name: "SSE request rejection without outer type", httpStatus: http.StatusOK,
			payload: `{"error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"input is too large"}}`,
			hint:    execution.FailureHintRequestRejected, effect: health.EffectNone, retry: health.RetryNone,
		},
		{
			name: "HTTP authentication rejection", httpStatus: http.StatusUnauthorized,
			payload: `{"type":"error","error":{"message":"request refused"}}`,
			status:  http.StatusUnauthorized, hint: execution.FailureHintInvalidCredential,
			effect: health.EffectRecordCredentialFailure, retry: health.RetryNextCandidate,
		},
		{
			name: "HTTP status wins over body status", httpStatus: http.StatusTooManyRequests,
			payload: `{"type":"error","status_code":502,"error":{"message":"request refused"}}`,
			status:  http.StatusTooManyRequests, hint: execution.FailureHintRateLimited,
			effect: health.EffectCooldownModel, retry: health.RetryNextCandidate,
		},
	} {
		for _, afterData := range []bool{false, true} {
			if afterData && test.httpStatus != http.StatusOK {
				continue
			}
			phase := "/before data"
			if afterData {
				phase = "/after data"
			}
			t.Run(test.name+phase, func(t *testing.T) {
				var calls atomic.Int64
				dataDelivered := make(chan struct{})
				var dataOnce sync.Once
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					calls.Add(1)
					_, _ = io.Copy(io.Discard, request.Body)
					if test.httpStatus != http.StatusOK {
						writer.Header().Set("Content-Type", "application/json")
						writer.WriteHeader(test.httpStatus)
						_, _ = io.WriteString(writer, test.payload)
						return
					}
					writer.Header().Set("Content-Type", "text/event-stream")
					if afterData {
						_, _ = io.WriteString(writer, "data: {\"id\":\"chat\",\"model\":\"deepseek-flash\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"}}]}\n\n")
						writer.(http.Flusher).Flush()
						select {
						case <-dataDelivered:
						case <-request.Context().Done():
							return
						}
					}
					_, _ = io.WriteString(writer, "data: "+test.payload+"\n\n")
				}))
				defer server.Close()
				runtime := newProtocolTestRuntime(t, testRuntimeOptions{allowPrivateNetwork: true})
				spec := compatibleTestSpec(t, server.URL, `{"model":"client-model","input":"hi"}`, true)
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				events := 0
				result := runtime.ExecuteStream(ctx, spec, func(event execution.StreamEvent) error {
					events++
					if event.Kind == execution.StreamEventData {
						dataOnce.Do(func() { close(dataDelivered) })
					}
					return nil
				})
				if err := result.Validate(); err != nil || result.Error == nil {
					t.Fatalf("result=%+v validation=%v", result, err)
				}
				wantStatus, wantRetry := test.status, test.retry
				if afterData {
					wantStatus, wantRetry = http.StatusOK, health.RetryNone
				}
				if result.ResponseStarted != (afterData || test.status != 0) || result.StatusCode != wantStatus || result.Error.Hint != test.hint {
					t.Fatalf("started=%v status=%d evidence=%+v", result.ResponseStarted, result.StatusCode, result.Error)
				}
				if test.status == 0 && (result.Error.Kind != execution.ErrorKindProvider || result.Error.StatusCode != 0) {
					t.Fatalf("inferred HTTP status reached evidence: %+v", result.Error)
				}
				decision := health.JudgeExecution(health.ExecutionAttempt{
					DispatchState: result.DispatchState, StatusCode: result.StatusCode,
					Header: result.Header, Evidence: result.Error, DownstreamCommitted: afterData,
				}, health.DecisionContext{Method: spec.Method, Operation: spec.Operation})
				if decision.Effect != test.effect || decision.Retry != wantRetry {
					t.Fatalf("health decision=%+v, want effect=%s retry=%s", decision, test.effect, wantRetry)
				}
				if calls.Load() != 1 || (!afterData && events != 0) || (afterData && events < 2) {
					t.Fatalf("calls=%d events=%d afterData=%v", calls.Load(), events, afterData)
				}
			})
		}
	}
}
