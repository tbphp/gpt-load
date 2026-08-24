package health

import (
	"net/http"
	"testing"
	"time"

	"gpt-load/internal/execution"
)

func TestJudgeExecutionProducesIndependentRetryAndEffect(t *testing.T) {
	now := time.Date(2026, time.August, 24, 8, 0, 0, 0, time.UTC)
	context := DecisionContext{
		DefaultRateLimitCooldown: 10 * time.Minute,
		CredentialRefreshable:    true,
		Method:                   http.MethodPost,
		Operation:                execution.OperationChatCompletion,
	}
	evidence := &execution.ErrorEvidence{
		Kind:       execution.ErrorKindHTTP,
		Hint:       execution.FailureHintRateLimited,
		ScopeHint:  execution.ErrorScopeCredential,
		StatusCode: http.StatusTooManyRequests,
		Summary:    "credential rate limited",
	}

	decision := JudgeExecution(ExecutionAttempt{
		DispatchState: execution.DispatchMaybeSent,
		StatusCode:    http.StatusTooManyRequests,
		Evidence:      evidence,
		Now:           now,
	}, context)

	want := Decision{
		Category:      FailureCategoryRateLimited,
		Origin:        execution.ErrorOriginUpstream,
		Scope:         execution.ErrorScopeCredential,
		Retry:         RetryNextCandidate,
		Effect:        EffectCooldownCredential,
		CooldownUntil: now.Add(10 * time.Minute),
		RuleID:        RuleID("rate_limit.credential.default_cooldown"),
	}
	if decision != want {
		t.Fatalf("JudgeExecution() = %#v, want %#v", decision, want)
	}
}

func TestJudgeExecutionCommittedKeepsOnlyTrustedCredentialEffect(t *testing.T) {
	now := time.Date(2026, time.August, 24, 8, 0, 0, 0, time.UTC)
	context := DecisionContext{DefaultRateLimitCooldown: time.Minute}

	trusted := JudgeExecution(ExecutionAttempt{
		DispatchState:       execution.DispatchMaybeSent,
		StatusCode:          http.StatusTooManyRequests,
		DownstreamCommitted: true,
		Evidence: &execution.ErrorEvidence{
			Kind:       execution.ErrorKindHTTP,
			Hint:       execution.FailureHintRateLimited,
			ScopeHint:  execution.ErrorScopeCredential,
			StatusCode: http.StatusTooManyRequests,
			Summary:    "credential rate limited",
		},
		Now: now,
	}, context)
	if trusted.Retry != RetryNone || trusted.Effect != EffectCooldownCredential ||
		trusted.Scope != execution.ErrorScopeCredential {
		t.Fatalf("trusted committed decision = %#v", trusted)
	}

	unscoped := JudgeExecution(ExecutionAttempt{
		DispatchState:       execution.DispatchMaybeSent,
		StatusCode:          http.StatusTooManyRequests,
		DownstreamCommitted: true,
		Evidence: &execution.ErrorEvidence{
			Kind:       execution.ErrorKindHTTP,
			Hint:       execution.FailureHintRateLimited,
			StatusCode: http.StatusTooManyRequests,
			Summary:    "overloaded",
		},
		Now: now,
	}, context)
	if unscoped.Retry != RetryNone || unscoped.Effect != EffectNone || unscoped.Scope != "" {
		t.Fatalf("unscoped committed decision = %#v", unscoped)
	}
}

func TestJudgeExecutionDoesNotRotateCredentialsForScopedRateLimit(t *testing.T) {
	for _, scope := range []execution.ErrorScope{
		execution.ErrorScopeRequest,
		execution.ErrorScopeModel,
	} {
		t.Run(string(scope), func(t *testing.T) {
			decision := JudgeExecution(ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusTooManyRequests,
				Evidence: &execution.ErrorEvidence{
					Kind:       execution.ErrorKindHTTP,
					Hint:       execution.FailureHintRateLimited,
					ScopeHint:  scope,
					StatusCode: http.StatusTooManyRequests,
					Summary:    "scoped rate limit",
				},
			}, DecisionContext{})
			if decision.Scope != scope || decision.Retry != RetryNone || decision.Effect != EffectNone {
				t.Fatalf("JudgeExecution() = %#v", decision)
			}
			if err := decision.Validate(); err != nil {
				t.Fatalf("Decision.Validate() error = %v", err)
			}
		})
	}
}

func TestJudgeExecutionHandlesCandidatePreparationFacts(t *testing.T) {
	tests := []struct {
		code   string
		scope  execution.ErrorScope
		effect Effect
	}{
		{code: "credential_decrypt_failed", scope: execution.ErrorScopeCredential, effect: EffectNone},
		{code: "credential_normalization_failed", scope: execution.ErrorScopeCredential, effect: EffectNone},
		{code: "credential_proxy_prepare_failed", scope: execution.ErrorScopeCredential, effect: EffectNone},
		{code: "group_proxy_prepare_failed", scope: execution.ErrorScopeGroup, effect: EffectSkipGroup},
	}
	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			decision := JudgeExecution(ExecutionAttempt{
				DispatchState: execution.DispatchNotSent,
				Evidence: &execution.ErrorEvidence{
					Kind: execution.ErrorKindInternal, OriginHint: execution.ErrorOriginInternal,
					ScopeHint: test.scope, Code: test.code, Summary: "candidate preparation failed",
				},
			}, DecisionContext{})
			if decision.Category != FailureCategoryAmbiguous ||
				decision.Origin != execution.ErrorOriginInternal || decision.Scope != test.scope ||
				decision.Retry != RetryNextCandidate || decision.Effect != test.effect ||
				decision.RuleID != RuleID("candidate."+test.code) {
				t.Fatalf("JudgeExecution() = %#v", decision)
			}
			if err := decision.Validate(); err != nil {
				t.Fatalf("Decision.Validate() error = %v", err)
			}
		})
	}
}

func TestJudgeExecutionRefreshRequiresRefreshableCredential(t *testing.T) {
	attempt := ExecutionAttempt{
		DispatchState: execution.DispatchMaybeSent,
		StatusCode:    http.StatusUnauthorized,
		Evidence: &execution.ErrorEvidence{
			Kind:         execution.ErrorKindHTTP,
			Hint:         execution.FailureHintRefreshRequired,
			ScopeHint:    execution.ErrorScopeCredential,
			StatusCode:   http.StatusUnauthorized,
			ReplaySafety: execution.ReplaySafetyRejectedBeforeProcessing,
			Summary:      "access token expired",
		},
	}

	refresh := JudgeExecution(attempt, DecisionContext{
		CredentialRefreshable: true,
		Method:                http.MethodPost,
		Operation:             execution.OperationResponsesCreate,
	})
	if refresh.Retry != RetryRefreshCredential || refresh.RuleID != RuleID("auth.refresh_required") {
		t.Fatalf("refreshable decision = %#v", refresh)
	}

	next := JudgeExecution(attempt, DecisionContext{
		Method:    http.MethodPost,
		Operation: execution.OperationResponsesCreate,
	})
	if next.Retry != RetryNextCandidate || next.RuleID != RuleID("auth.refresh_unavailable") {
		t.Fatalf("non-refreshable decision = %#v", next)
	}
}

func TestJudgeExecutionPreservesReplayCompatibilityRules(t *testing.T) {
	tests := []struct {
		name      string
		attempt   ExecutionAttempt
		context   DecisionContext
		wantRetry RetryDirective
		wantRule  RuleID
	}{
		{
			name: "generic payment required terminates",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusPaymentRequired,
				Evidence: &execution.ErrorEvidence{
					Kind: execution.ErrorKindHTTP, StatusCode: http.StatusPaymentRequired,
					Summary: "billing disabled",
				},
			},
			wantRetry: RetryNone,
			wantRule:  RuleID("fallback.http_client_error"),
		},
		{
			name: "candidate payment required retries",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusPaymentRequired,
				Evidence: &execution.ErrorEvidence{
					Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintCandidateUnavailable,
					StatusCode: http.StatusPaymentRequired, ReplaySafety: execution.ReplaySafetyRejectedBeforeProcessing,
					Summary: "candidate unavailable",
				},
			},
			wantRetry: RetryNextCandidate,
			wantRule:  RuleID("candidate.unavailable"),
		},
		{
			name: "explicit unknown blocks unauthorized retry",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusUnauthorized,
				Evidence: &execution.ErrorEvidence{
					Kind: execution.ErrorKindHTTP, StatusCode: http.StatusUnauthorized,
					ReplaySafety: execution.ReplaySafetyUnknown, Summary: "authorization failed",
				},
			},
			wantRetry: RetryNone,
			wantRule:  RuleID("safety.replay_unknown"),
		},
		{
			name: "read only host error retries",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusServiceUnavailable,
				Evidence: &execution.ErrorEvidence{
					Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintHostError,
					StatusCode: http.StatusServiceUnavailable, Summary: "unavailable",
				},
			},
			context:   DecisionContext{Method: http.MethodGet, Operation: execution.OperationResponsesRetrieve},
			wantRetry: RetryNextCandidate,
			wantRule:  RuleID("upstream.host_error.read_only"),
		},
		{
			name: "mutating host error terminates",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusServiceUnavailable,
				Evidence: &execution.ErrorEvidence{
					Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintHostError,
					StatusCode: http.StatusServiceUnavailable, Summary: "unavailable",
				},
			},
			context:   DecisionContext{Method: http.MethodPost, Operation: execution.OperationChatCompletion},
			wantRetry: RetryNone,
			wantRule:  RuleID("upstream.host_error.replay_unsafe"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := JudgeExecution(test.attempt, test.context)
			if decision.Retry != test.wantRetry || decision.RuleID != test.wantRule {
				t.Fatalf("JudgeExecution() = %#v, want retry=%q rule=%q", decision, test.wantRetry, test.wantRule)
			}
		})
	}
}

func TestJudgeExecutionRejectsFinalInformationalStatusRange(t *testing.T) {
	for _, status := range []int{http.StatusSwitchingProtocols, 199} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			decision := JudgeExecution(ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    status,
				Evidence: &execution.ErrorEvidence{
					Kind: execution.ErrorKindHTTP, StatusCode: status,
					Summary: "unexpected informational response",
				},
			}, DecisionContext{})
			if decision.Retry != RetryNone || decision.Effect != EffectNone ||
				decision.RuleID != "safety.final_informational_response" {
				t.Fatalf("JudgeExecution(%d) = %#v", status, decision)
			}
			if err := decision.Validate(); err != nil {
				t.Fatalf("Decision.Validate() error = %v", err)
			}
		})
	}
}

func TestJudgeExecutionCompatibilityRuleMatrix(t *testing.T) {
	now := time.Date(2026, time.August, 24, 14, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		status     int
		evidence   execution.ErrorEvidence
		context    DecisionContext
		category   FailureCategory
		scope      execution.ErrorScope
		retry      RetryDirective
		effect     Effect
		ruleID     RuleID
		cooldownAt time.Time
	}{
		{
			name: "legacy 401 invalid credential", status: http.StatusUnauthorized,
			evidence: execution.ErrorEvidence{
				Kind: execution.ErrorKindHTTP, StatusCode: http.StatusUnauthorized,
				Summary: "credential rejected",
			},
			category: FailureCategoryInvalidKey, scope: execution.ErrorScopeCredential,
			retry: RetryNextCandidate, effect: EffectRecordCredentialFailure,
			ruleID: "auth.invalid_credential",
		},
		{
			name: "legacy unscoped 429", status: http.StatusTooManyRequests,
			evidence: execution.ErrorEvidence{
				Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintRateLimited,
				StatusCode: http.StatusTooManyRequests, Summary: "rate limited",
			},
			context:  DecisionContext{DefaultRateLimitCooldown: 10 * time.Minute},
			category: FailureCategoryRateLimited, retry: RetryNextCandidate,
			effect: EffectCooldownCredential, ruleID: "legacy.http_429_credential_cooldown",
			cooldownAt: now.Add(10 * time.Minute),
		},
		{
			name: "generic 403", status: http.StatusForbidden,
			evidence: execution.ErrorEvidence{
				Kind: execution.ErrorKindHTTP, StatusCode: http.StatusForbidden,
				Summary: "permission denied",
			},
			category: FailureCategoryClientError, scope: execution.ErrorScopeRequest,
			retry: RetryNone, effect: EffectNone, ruleID: "fallback.http_client_error",
		},
		{
			name: "candidate scoped 403", status: http.StatusForbidden,
			evidence: execution.ErrorEvidence{
				Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintCandidateUnavailable,
				ScopeHint: execution.ErrorScopeModel, StatusCode: http.StatusForbidden,
				ReplaySafety: execution.ReplaySafetyRejectedBeforeProcessing,
				Summary:      "candidate unavailable",
			},
			category: FailureCategoryModelUnavailable, scope: execution.ErrorScopeModel,
			retry: RetryNextCandidate, effect: EffectNone, ruleID: "candidate.unavailable",
		},
		{
			name: "request scoped rejection", status: http.StatusTooManyRequests,
			evidence: execution.ErrorEvidence{
				Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintRequestRejected,
				ScopeHint: execution.ErrorScopeRequest, StatusCode: http.StatusTooManyRequests,
				Summary: "request credits unavailable",
			},
			category: FailureCategoryClientError, scope: execution.ErrorScopeRequest,
			retry: RetryNone, effect: EffectNone, ruleID: "fallback.http_client_error",
		},
		{
			name: "model unavailable retains credential cooldown", status: http.StatusNotFound,
			evidence: execution.ErrorEvidence{
				Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintModelUnavailable,
				ScopeHint: execution.ErrorScopeModel, StatusCode: http.StatusNotFound,
				Summary: "model unavailable",
			},
			category: FailureCategoryModelUnavailable, scope: execution.ErrorScopeModel,
			retry: RetryNextCandidate, effect: EffectCooldownCredential,
			ruleID: "model.unavailable", cooldownAt: now.Add(time.Hour),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := JudgeExecution(ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    test.status,
				Evidence:      &test.evidence,
				Now:           now,
			}, test.context)
			if decision.Category != test.category || decision.Scope != test.scope ||
				decision.Retry != test.retry || decision.Effect != test.effect ||
				decision.RuleID != test.ruleID || !decision.CooldownUntil.Equal(test.cooldownAt) {
				t.Fatalf("JudgeExecution() = %#v", decision)
			}
			if err := decision.Validate(); err != nil {
				t.Fatalf("Decision.Validate() error = %v", err)
			}
		})
	}
}
