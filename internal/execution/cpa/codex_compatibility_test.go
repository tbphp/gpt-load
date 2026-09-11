package cpa

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/execution"
	"gpt-load/internal/health"
)

func TestCodexUpgradePreservesQuotaAndRejectionPolicy(t *testing.T) {
	for _, test := range []struct {
		name       string
		stream     bool
		status     int
		errorJSON  string
		wantStatus int
		wantHint   execution.FailureHint
		wantScope  execution.ErrorScope
		wantReplay bool
		wantEffect health.Effect
	}{
		{
			name: "unary quota remains credential model scoped", status: http.StatusTooManyRequests,
			errorJSON:  `{"type":"usage_limit_reached","resets_in_seconds":60}`,
			wantStatus: http.StatusTooManyRequests, wantHint: execution.FailureHintRateLimited,
			wantScope: execution.ErrorScopeModel, wantReplay: true, wantEffect: health.EffectCooldownModel,
		},
		{
			name: "stream quota remains credential model scoped", stream: true, status: http.StatusTooManyRequests,
			errorJSON:  `{"type":"usage_limit_reached","resets_in_seconds":60}`,
			wantStatus: http.StatusTooManyRequests, wantHint: execution.FailureHintRateLimited,
			wantScope: execution.ErrorScopeModel, wantReplay: true, wantEffect: health.EffectCooldownModel,
		},
		{
			name: "model capacity code is not quota exhaustion", stream: true, status: http.StatusOK,
			errorJSON:  `{"type":"server_error","code":"model_at_capacity","message":"model_at_capacity"}`,
			wantStatus: http.StatusTooManyRequests, wantHint: execution.FailureHintCandidateUnavailable,
			wantScope: execution.ErrorScopeModel, wantReplay: true, wantEffect: health.EffectNone,
		},
		{
			name: "explicit retryable bootstrap error", stream: true, status: http.StatusOK,
			errorJSON:  `{"type":"server_error","code":"server_error","message":"An error occurred. You can retry your request."}`,
			wantStatus: http.StatusServiceUnavailable, wantHint: execution.FailureHintHostError,
			wantScope: execution.ErrorScopeGroup, wantReplay: true, wantEffect: health.EffectSkipGroup,
		},
		{
			name: "ordinary server error does not gain replay proof", stream: true, status: http.StatusOK,
			errorJSON:  `{"type":"server_error","code":"server_error","message":"Unknown server failure"}`,
			wantStatus: http.StatusBadGateway, wantHint: execution.FailureHintHostError,
			wantScope: execution.ErrorScopeGroup, wantEffect: health.EffectSkipGroup,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter, _, _, service, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			body := `{"error":` + test.errorJSON + `}`
			contentType := "application/json"
			if test.status == http.StatusOK {
				body = "data: " + `{"type":"response.failed","response":{"error":` + test.errorJSON + `}}` + "\n\n"
				contentType = "text/event-stream"
			}
			transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: test.status, Header: http.Header{"Content-Type": {contentType}},
					Body: io.NopCloser(strings.NewReader(body)), Request: request,
				}, nil
			})
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
			spec := validSpec(t, row, service)
			var evidence *execution.ErrorEvidence
			var status int
			var dispatch execution.DispatchState
			if test.stream {
				events := 0
				result := adapter.ExecuteStream(ctx, spec, func(execution.StreamEvent) error { events++; return nil })
				evidence, status, dispatch = result.Error, result.StatusCode, result.DispatchState
				if test.wantReplay && events != 0 {
					t.Fatalf("rejection emitted %d downstream events", events)
				}
			} else {
				result := adapter.Execute(ctx, spec)
				evidence, status, dispatch = result.Error, result.StatusCode, result.DispatchState
			}
			if evidence == nil || status != test.wantStatus || evidence.Hint != test.wantHint || evidence.ScopeHint != test.wantScope {
				t.Fatalf("status=%d evidence=%+v", status, evidence)
			}
			if got := evidence.ReplaySafety == execution.ReplaySafetyRejectedBeforeProcessing; got != test.wantReplay {
				t.Fatalf("replay proof=%t, want %t; evidence=%+v", got, test.wantReplay, evidence)
			}
			decision := health.JudgeExecution(health.ExecutionAttempt{
				DispatchState: dispatch, StatusCode: status, Evidence: evidence, Now: time.Now(),
			}, health.DecisionContext{Method: http.MethodPost, Operation: spec.Operation})
			if decision.Effect != test.wantEffect {
				t.Fatalf("health decision=%+v, want effect %s", decision, test.wantEffect)
			}
			if got := decision.Retry == health.RetryNextCandidate; got != test.wantReplay {
				t.Fatalf("health retry=%s, want retryable=%t", decision.Retry, test.wantReplay)
			}
		})
	}
}

func TestCodexRetryableErrorAfterOutputDoesNotGainReplayProof(t *testing.T) {
	adapter, _, _, service, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	body := "data: " + `{"type":"response.created","response":{"id":"resp_partial","status":"in_progress"}}` + "\n\n" +
		"data: " + `{"type":"response.output_text.delta","response_id":"resp_partial","output_index":0,"content_index":0,"delta":"partial"}` + "\n\n" +
		"data: " + `{"type":"response.failed","response":{"id":"resp_partial","error":{"type":"server_error","code":"server_error","message":"You can retry your request."}}}` + "\n\n"
	transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
	events := 0
	result := adapter.ExecuteStream(ctx, validSpec(t, row, service), func(execution.StreamEvent) error { events++; return nil })
	if events == 0 || result.Error == nil || result.Error.ReplaySafety == execution.ReplaySafetyRejectedBeforeProcessing {
		t.Fatalf("events=%d evidence=%+v", events, result.Error)
	}
}
