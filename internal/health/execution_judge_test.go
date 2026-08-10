package health

import (
	"context"
	"net/http"
	"testing"
	"time"

	"gpt-load/internal/execution"
)

func TestJudgeExecutionUsesNeutralEvidenceAndReplayBoundary(t *testing.T) {
	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		attempt ExecutionAttempt
		want    Result
	}{
		{
			name: "downstream cancellation always terminates",
			attempt: ExecutionAttempt{
				DownstreamErr: context.Canceled,
				Evidence:      evidence(execution.ErrorKindTransport, 0, "dial failed"),
			},
			want: Result{Category: FailureCategoryDownstreamCancel, Action: ActionTerminate},
		},
		{
			name: "committed response never retries",
			attempt: ExecutionAttempt{
				DownstreamCommitted: true,
				Evidence:            evidence(execution.ErrorKindHTTP, http.StatusTooManyRequests, "rate limited"),
			},
			want: Result{Category: FailureCategoryAmbiguous, Action: ActionTerminate},
		},
		{
			name: "clean committed response is successful",
			attempt: ExecutionAttempt{
				DownstreamCommitted: true,
				StatusCode:          http.StatusOK,
			},
			want: Result{Category: FailureCategoryOK, Action: ActionTerminate},
		},
		{
			name: "not sent transport error skips group",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchNotSent,
				Evidence:      evidence(execution.ErrorKindTransport, 0, "dial failed"),
			},
			want: Result{Category: FailureCategoryUpstreamHostError, Action: ActionSkipGroup},
		},
		{
			name: "not sent timeout skips group",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchNotSent,
				Evidence:      evidence(execution.ErrorKindTimeout, 0, "connect timeout"),
			},
			want: Result{Category: FailureCategoryUpstreamHostError, Action: ActionSkipGroup},
		},
		{
			name: "maybe sent transport error is ambiguous",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				Evidence:      evidence(execution.ErrorKindTransport, 0, "connection reset"),
			},
			want: Result{Category: FailureCategoryAmbiguous, Action: ActionTerminate},
		},
		{
			name: "maybe sent timeout is ambiguous",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				Evidence:      evidence(execution.ErrorKindTimeout, 0, "response timeout"),
			},
			want: Result{Category: FailureCategoryAmbiguous, Action: ActionTerminate},
		},
		{
			name: "rate limit cools credential using evidence duration",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusTooManyRequests,
				Now:           now,
				Evidence: &execution.ErrorEvidence{
					Kind:       execution.ErrorKindHTTP,
					StatusCode: http.StatusTooManyRequests,
					Summary:    "rate limited",
					RetryAfter: 12 * time.Second,
				},
			},
			want: Result{
				Category:      FailureCategoryRateLimited,
				Action:        ActionCooldownCredential,
				CooldownUntil: now.Add(12 * time.Second),
			},
		},
		{
			name: "rate limit falls back to safe response header",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusTooManyRequests,
				Header:        http.Header{"Retry-After": {"30"}},
				Now:           now,
				Evidence:      evidence(execution.ErrorKindHTTP, http.StatusTooManyRequests, "rate limited"),
			},
			want: Result{
				Category:      FailureCategoryRateLimited,
				Action:        ActionCooldownCredential,
				CooldownUntil: now.Add(30 * time.Second),
			},
		},
		{
			name: "unauthorized fails credential",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusUnauthorized,
				Evidence:      evidence(execution.ErrorKindHTTP, http.StatusUnauthorized, "invalid credential"),
			},
			want: Result{Category: FailureCategoryInvalidKey, Action: ActionFailCredential},
		},
		{
			name: "payment required fails credential",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusPaymentRequired,
				Evidence:      evidence(execution.ErrorKindHTTP, http.StatusPaymentRequired, "billing disabled"),
			},
			want: Result{Category: FailureCategoryInvalidKey, Action: ActionFailCredential},
		},
		{
			name: "provider hint identifies credential failure under HTTP 400",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusBadRequest,
				Evidence: &execution.ErrorEvidence{
					Kind:       execution.ErrorKindHTTP,
					Hint:       execution.FailureHintInvalidCredential,
					StatusCode: http.StatusBadRequest,
					Summary:    "upstream rejected request",
				},
			},
			want: Result{Category: FailureCategoryInvalidKey, Action: ActionFailCredential},
		},
		{
			name: "model not found cools credential",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusNotFound,
				Now:           now,
				Evidence: &execution.ErrorEvidence{
					Kind:       execution.ErrorKindProvider,
					StatusCode: http.StatusNotFound,
					Code:       "model_not_found",
					Summary:    "model unavailable",
				},
			},
			want: Result{
				Category:      FailureCategoryModelUnavailable,
				Action:        ActionCooldownCredential,
				CooldownUntil: now.Add(time.Hour),
			},
		},
		{
			name: "generic not found terminates",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusNotFound,
				Evidence:      evidence(execution.ErrorKindHTTP, http.StatusNotFound, "endpoint not found"),
			},
			want: Result{Category: FailureCategoryClientError, Action: ActionTerminate},
		},
		{
			name: "provider rate limit under success status still cools credential",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusOK,
				Now:           now,
				Evidence: &execution.ErrorEvidence{
					Kind:       execution.ErrorKindProvider,
					StatusCode: http.StatusOK,
					Type:       "rate_limit_error",
					Summary:    "rate limited",
				},
			},
			want: Result{Category: FailureCategoryRateLimited, Action: ActionCooldownCredential, UseFixed: true},
		},
		{
			name: "server error skips group",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusServiceUnavailable,
				Evidence:      evidence(execution.ErrorKindHTTP, http.StatusServiceUnavailable, "overloaded"),
			},
			want: Result{Category: FailureCategoryUpstreamHostError, Action: ActionSkipGroup},
		},
		{
			name: "invalid request terminates before dispatch",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchNotSent,
				Evidence:      evidence(execution.ErrorKindInvalidRequest, 0, "unsupported request"),
			},
			want: Result{Category: FailureCategoryClientError, Action: ActionTerminate},
		},
		{
			name: "clean success terminates",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusOK,
			},
			want: Result{Category: FailureCategoryOK, Action: ActionTerminate},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := JudgeExecution(test.attempt); got != test.want {
				t.Fatalf("JudgeExecution() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func evidence(kind execution.ErrorKind, statusCode int, summary string) *execution.ErrorEvidence {
	return &execution.ErrorEvidence{Kind: kind, StatusCode: statusCode, Summary: summary}
}
