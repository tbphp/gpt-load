package health

import (
	"context"
	"net/http"
	"testing"
	"time"

	"gpt-load/internal/execution"
)

func TestJudgeExecutionUsesNeutralEvidenceAndReplayBoundary(t *testing.T) {
	type Result struct {
		Category      FailureCategory
		Action        Action
		CooldownUntil time.Time
	}
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
				DispatchState:       execution.DispatchMaybeSent,
				DownstreamCommitted: true,
				Evidence:            evidence(execution.ErrorKindHTTP, http.StatusTooManyRequests, "rate limited"),
			},
			want: Result{Category: FailureCategoryRateLimited, Action: ActionTerminate},
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
			name: "not sent subscription reauthorization retries another credential",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchNotSent,
				Evidence: &execution.ErrorEvidence{
					Kind: execution.ErrorKindProvider,
					Hint: execution.FailureHintReauthorizationRequired,
				},
			},
			want: Result{Category: FailureCategoryAuthenticationRequired, Action: ActionRetry},
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
			name: "request-scoped 429 terminates without credential penalty",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusTooManyRequests,
				Now:           now,
				Evidence: &execution.ErrorEvidence{
					Kind:       execution.ErrorKindHTTP,
					Hint:       execution.FailureHintRequestRejected,
					StatusCode: http.StatusTooManyRequests,
					Type:       "rate_limit_error",
					Summary:    "usage credits are required for fast mode",
				},
			},
			want: Result{Category: FailureCategoryClientError, Action: ActionTerminate},
		},
		{
			name: "candidate rejection retries without credential penalty",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusForbidden,
				Now:           now,
				Evidence: &execution.ErrorEvidence{
					Kind:         execution.ErrorKindHTTP,
					Hint:         execution.FailureHintCandidateUnavailable,
					StatusCode:   http.StatusForbidden,
					ReplaySafety: execution.ReplaySafetyRejectedBeforeProcessing,
					Summary:      "candidate is unavailable",
				},
			},
			want: Result{Category: FailureCategoryModelUnavailable, Action: ActionRetry},
		},
		{
			name: "unsupported upstream operation terminates without credential penalty",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusNotImplemented,
				Now:           now,
				Evidence: &execution.ErrorEvidence{
					Kind:       execution.ErrorKindHTTP,
					Hint:       execution.FailureHintRequestRejected,
					StatusCode: http.StatusNotImplemented,
					Code:       "unsupported_operation",
					Summary:    "count tokens is not supported",
				},
			},
			want: Result{Category: FailureCategoryClientError, Action: ActionTerminate},
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
			name: "unknown subscription authorization failure stays ambiguous",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusUnauthorized,
				Evidence: &execution.ErrorEvidence{
					Kind:         execution.ErrorKindHTTP,
					StatusCode:   http.StatusUnauthorized,
					Type:         "authentication_error",
					Code:         "auth_unavailable",
					Summary:      "authorization failed",
					ReplaySafety: execution.ReplaySafetyUnknown,
				},
			},
			want: Result{Category: FailureCategoryAmbiguous, Action: ActionTerminate},
		},
		{
			name: "payment required is a client error",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusPaymentRequired,
				Evidence:      evidence(execution.ErrorKindHTTP, http.StatusPaymentRequired, "billing disabled"),
			},
			want: Result{Category: FailureCategoryClientError, Action: ActionTerminate},
		},
		{
			name: "generic forbidden is a client error",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusForbidden,
				Evidence:      evidence(execution.ErrorKindHTTP, http.StatusForbidden, "permission denied"),
			},
			want: Result{Category: FailureCategoryClientError, Action: ActionTerminate},
		},
		{
			name: "forbidden model marker cools credential",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusForbidden,
				Now:           now,
				Evidence: &execution.ErrorEvidence{
					Kind:       execution.ErrorKindHTTP,
					StatusCode: http.StatusForbidden,
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
			name: "forbidden unsupported model marker cools credential",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusForbidden,
				Now:           now,
				Evidence: &execution.ErrorEvidence{
					Kind:       execution.ErrorKindHTTP,
					Hint:       execution.FailureHintModelUnavailable,
					StatusCode: http.StatusForbidden,
					Code:       "unsupported_model",
				},
			},
			want: Result{
				Category:      FailureCategoryModelUnavailable,
				Action:        ActionCooldownCredential,
				CooldownUntil: now.Add(time.Hour),
			},
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
			name: "subscription refresh hint retries without blacklisting the credential",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusUnauthorized,
				Evidence: &execution.ErrorEvidence{
					Kind:       execution.ErrorKindHTTP,
					Hint:       execution.FailureHintRefreshRequired,
					StatusCode: http.StatusUnauthorized,
					Summary:    "authorization expired",
				},
			},
			want: Result{Category: FailureCategoryAuthenticationRequired, Action: ActionTerminate},
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
			want: Result{
				Category: FailureCategoryRateLimited, Action: ActionCooldownCredential,
				CooldownUntil: now.Add(time.Minute),
			},
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
			name: "local conversion failure skips group without credential health mutation",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchNotSent,
				Evidence: &execution.ErrorEvidence{
					Kind:    execution.ErrorKind("conversion_unsupported"),
					Code:    "target_conversion_not_supported",
					Summary: "target conversion is not supported",
				},
			},
			want: Result{Category: FailureCategoryConversionUnsupported, Action: ActionSkipGroup},
		},
		{
			name: "structured unsupported model 400 is a client error",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusBadRequest,
				Evidence: &execution.ErrorEvidence{
					Kind:       execution.ErrorKindHTTP,
					Hint:       execution.FailureHintModelUnavailable,
					StatusCode: http.StatusBadRequest,
					Code:       "unsupported_model",
					Summary:    "request capability is unavailable",
				},
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
			got := JudgeExecution(test.attempt, DecisionContext{
				DefaultRateLimitCooldown: time.Minute,
				CredentialRefreshable:    true,
				Method:                   http.MethodPost,
				Operation:                execution.OperationChatCompletion,
			})
			if err := got.Validate(); err != nil {
				t.Fatalf("JudgeExecution() returned invalid decision %#v: %v", got, err)
			}
			wantCooldown := test.want.CooldownUntil
			if got.Category != test.want.Category ||
				got.LegacyAction() != test.want.Action ||
				!got.CooldownUntil.Equal(wantCooldown) {
				t.Fatalf(
					"JudgeExecution() = %#v action=%d, want category=%q action=%d cooldown=%s",
					got,
					got.LegacyAction(),
					test.want.Category.String(),
					test.want.Action,
					wantCooldown,
				)
			}
		})
	}
}

func evidence(kind execution.ErrorKind, statusCode int, summary string) *execution.ErrorEvidence {
	return &execution.ErrorEvidence{Kind: kind, StatusCode: statusCode, Summary: summary}
}
