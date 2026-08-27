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
	if unscoped.Retry != RetryNone || unscoped.Effect != EffectNone || unscoped.Scope != "" ||
		unscoped.RuleID != "safety.committed" {
		t.Fatalf("unscoped committed decision = %#v", unscoped)
	}

	invalidCredential := JudgeExecution(ExecutionAttempt{
		DispatchState:       execution.DispatchMaybeSent,
		StatusCode:          http.StatusUnauthorized,
		DownstreamCommitted: true,
		Evidence: &execution.ErrorEvidence{
			Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintInvalidCredential,
			ScopeHint: execution.ErrorScopeCredential, StatusCode: http.StatusUnauthorized,
			ReplaySafety: execution.ReplaySafetyRejectedBeforeProcessing,
			Summary:      "credential rejected",
		},
		Now: now,
	}, context)
	if invalidCredential.Retry != RetryNone ||
		invalidCredential.Effect != EffectRecordCredentialFailure ||
		invalidCredential.RuleID != "safety.committed" {
		t.Fatalf("invalid credential committed decision = %#v", invalidCredential)
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
		{
			name: "explicitly rejected mutating host error retries",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusServiceUnavailable,
				Evidence: &execution.ErrorEvidence{
					Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintHostError,
					StatusCode:   http.StatusServiceUnavailable,
					ReplaySafety: execution.ReplaySafetyRejectedBeforeProcessing,
					Summary:      "request rejected before processing",
				},
			},
			context:   DecisionContext{Method: http.MethodPost, Operation: execution.OperationChatCompletion},
			wantRetry: RetryNextCandidate,
			wantRule:  RuleID("upstream.host_error.rejected_before_processing"),
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

func TestJudgeExecutionRetriesSafeBootstrapCapacityRejectionsWithoutEffects(t *testing.T) {
	for _, test := range []struct {
		name      string
		typeValue string
		code      string
		category  FailureCategory
		scope     execution.ErrorScope
	}{
		{
			name: "server overload", typeValue: "service_unavailable_error",
			code: "server_is_overloaded", category: FailureCategoryUpstreamHostError,
			scope: execution.ErrorScopeGroup,
		},
		{
			name: "transient rate limit", typeValue: "rate_limit_error",
			code: "rate_limit_exceeded", category: FailureCategoryRateLimited,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			decision := JudgeExecution(ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusServiceUnavailable,
				Evidence: &execution.ErrorEvidence{
					Kind: execution.ErrorKindHTTP, OriginHint: execution.ErrorOriginUpstream,
					ScopeHint: test.scope, StatusCode: http.StatusServiceUnavailable,
					Type: test.typeValue, Code: test.code, Summary: "rejected before generation",
					ReplaySafety: execution.ReplaySafetyRejectedBeforeProcessing,
				},
			}, DecisionContext{Method: http.MethodPost, Operation: execution.OperationChatCompletion})
			if decision.Category != test.category || decision.Scope != test.scope ||
				decision.Retry != RetryNextCandidate || decision.Effect != EffectNone ||
				decision.RuleID != RuleID("candidate.transient_capacity") {
				t.Fatalf("JudgeExecution() = %#v", decision)
			}
		})
	}
}

func TestJudgeExecutionDoesNotBroadenBootstrapCapacityRetry(t *testing.T) {
	tests := []struct {
		name       string
		evidence   execution.ErrorEvidence
		committed  bool
		wantRetry  RetryDirective
		wantEffect Effect
		wantRule   RuleID
	}{
		{
			name: "unknown replay safety",
			evidence: execution.ErrorEvidence{
				Kind: execution.ErrorKindHTTP, OriginHint: execution.ErrorOriginUpstream,
				StatusCode: http.StatusServiceUnavailable, Code: "server_is_overloaded",
				Summary: "replay is unknown", ReplaySafety: execution.ReplaySafetyUnknown,
			},
			wantRetry: RetryNone, wantEffect: EffectSkipGroup,
			wantRule: "upstream.host_error.replay_unsafe",
		},
		{
			name: "similar code",
			evidence: execution.ErrorEvidence{
				Kind: execution.ErrorKindHTTP, OriginHint: execution.ErrorOriginUpstream,
				StatusCode: http.StatusServiceUnavailable, Code: "server_is_overloaded_later",
				Summary: "different failure", ReplaySafety: execution.ReplaySafetyRejectedBeforeProcessing,
			},
			wantRetry: RetryNextCandidate, wantEffect: EffectSkipGroup,
			wantRule: "upstream.host_error.rejected_before_processing",
		},
		{
			name: "downstream already committed",
			evidence: execution.ErrorEvidence{
				Kind: execution.ErrorKindProvider, OriginHint: execution.ErrorOriginUpstream,
				ScopeHint: execution.ErrorScopeCredential, Code: "server_is_overloaded",
				Summary: "too late to retry", ReplaySafety: execution.ReplaySafetyRejectedBeforeProcessing,
			},
			committed: true, wantRetry: RetryNone, wantEffect: EffectNone,
			wantRule: "safety.committed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := JudgeExecution(ExecutionAttempt{
				DispatchState:       execution.DispatchMaybeSent,
				StatusCode:          test.evidence.StatusCode,
				Evidence:            &test.evidence,
				DownstreamCommitted: test.committed,
			}, DecisionContext{Method: http.MethodPost, Operation: execution.OperationChatCompletion})
			if decision.Retry != test.wantRetry || decision.Effect != test.wantEffect || decision.RuleID != test.wantRule {
				t.Fatalf("JudgeExecution() = %#v", decision)
			}
		})
	}
}

func TestJudgeExecutionReplayUnknownKeepsUnaffectedRuleID(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		evidence execution.ErrorEvidence
		wantRule RuleID
	}{
		{
			name:   "request scoped rate limit",
			status: http.StatusTooManyRequests,
			evidence: execution.ErrorEvidence{
				Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintRateLimited,
				ScopeHint: execution.ErrorScopeRequest, StatusCode: http.StatusTooManyRequests,
				ReplaySafety: execution.ReplaySafetyUnknown, Summary: "request rate limited",
			},
			wantRule: "rate_limit.scoped",
		},
		{
			name:   "generic client error",
			status: http.StatusForbidden,
			evidence: execution.ErrorEvidence{
				Kind: execution.ErrorKindHTTP, StatusCode: http.StatusForbidden,
				ReplaySafety: execution.ReplaySafetyUnknown, Summary: "permission denied",
			},
			wantRule: "fallback.http_client_error",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := JudgeExecution(ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    test.status,
				Evidence:      &test.evidence,
			}, DecisionContext{})
			if decision.Retry != RetryNone || decision.RuleID != test.wantRule {
				t.Fatalf("JudgeExecution() = %#v, want rule %q", decision, test.wantRule)
			}
		})
	}
}

func TestJudgeExecutionStableFallbackRuleMatrix(t *testing.T) {
	tests := []struct {
		name    string
		attempt ExecutionAttempt
		want    RuleID
	}{
		{
			name:    "missing evidence",
			attempt: ExecutionAttempt{DispatchState: execution.DispatchNotSent},
			want:    "fallback.missing_evidence",
		},
		{
			name: "execution canceled",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				Evidence:      &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled, Summary: "canceled"},
			},
			want: "safety.execution_canceled",
		},
		{
			name: "unclassified not sent",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchNotSent,
				Evidence:      &execution.ErrorEvidence{Kind: execution.ErrorKindInternal, Summary: "internal failure"},
			},
			want: "fallback.not_sent",
		},
		{
			name: "invalid dispatch state",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchState("invalid"),
				Evidence:      &execution.ErrorEvidence{Kind: execution.ErrorKindInternal, Summary: "invalid state"},
			},
			want: "fallback.invalid_dispatch_state",
		},
		{
			name: "transport outcome unknown",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				Evidence:      &execution.ErrorEvidence{Kind: execution.ErrorKindTransport, Summary: "connection lost"},
			},
			want: "transport.outcome_unknown",
		},
		{
			name: "authentication replay unsafe",
			attempt: ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusUnauthorized,
				Evidence: &execution.ErrorEvidence{
					Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintRefreshRequired,
					StatusCode: http.StatusUnauthorized, ReplaySafety: execution.ReplaySafetyUnknown,
					Summary: "authentication failed",
				},
			},
			want: "auth.replay_unsafe",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := JudgeExecution(test.attempt, DecisionContext{CredentialRefreshable: true})
			if decision.RuleID != test.want {
				t.Fatalf("JudgeExecution() = %#v, want rule %q", decision, test.want)
			}
			if err := decision.Validate(); err != nil {
				t.Fatalf("Decision.Validate() error = %v", err)
			}
		})
	}
}

func TestJudgeExecutionImagesRequireExplicitReplaySafety(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	imageOperation := execution.Operation("images_generate")
	tests := []struct {
		name         string
		status       int
		evidence     execution.ErrorEvidence
		wantRetry    RetryDirective
		wantEffect   Effect
		wantCooldown bool
	}{
		{
			name:   "model unavailable without rejection proof",
			status: http.StatusNotFound,
			evidence: execution.ErrorEvidence{
				Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintModelUnavailable,
				ScopeHint: execution.ErrorScopeModel, StatusCode: http.StatusNotFound,
				Summary: "model unavailable",
			},
			wantRetry: RetryNone, wantEffect: EffectNone,
		},
		{
			name:   "candidate unavailable without rejection proof",
			status: http.StatusBadRequest,
			evidence: execution.ErrorEvidence{
				Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintCandidateUnavailable,
				ScopeHint: execution.ErrorScopeModel, StatusCode: http.StatusBadRequest,
				Summary: "operation unsupported",
			},
			wantRetry: RetryNone, wantEffect: EffectNone,
		},
		{
			name:   "rate limit without rejection proof",
			status: http.StatusTooManyRequests,
			evidence: execution.ErrorEvidence{
				Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintRateLimited,
				StatusCode: http.StatusTooManyRequests, Summary: "rate limited",
			},
			wantRetry: RetryNone, wantEffect: EffectCooldownCredential, wantCooldown: true,
		},
		{
			name:   "invalid credential without rejection proof",
			status: http.StatusUnauthorized,
			evidence: execution.ErrorEvidence{
				Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintInvalidCredential,
				ScopeHint: execution.ErrorScopeCredential, StatusCode: http.StatusUnauthorized,
				Summary: "credential rejected",
			},
			wantRetry: RetryNone, wantEffect: EffectRecordCredentialFailure,
		},
		{
			name:   "explicit pre-processing rejection may advance",
			status: http.StatusTooManyRequests,
			evidence: execution.ErrorEvidence{
				Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintRateLimited,
				StatusCode: http.StatusTooManyRequests, Summary: "request rejected",
				ReplaySafety: execution.ReplaySafetyRejectedBeforeProcessing,
			},
			wantRetry: RetryNextCandidate, wantEffect: EffectCooldownCredential, wantCooldown: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := JudgeExecution(ExecutionAttempt{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    test.status, Evidence: &test.evidence, Now: now,
			}, DecisionContext{
				Operation:                imageOperation,
				Method:                   http.MethodPost,
				DefaultRateLimitCooldown: time.Minute,
			})
			if result.Retry != test.wantRetry || result.Effect != test.wantEffect {
				t.Fatalf("JudgeExecution() = %#v, want retry=%q effect=%q", result, test.wantRetry, test.wantEffect)
			}
			if result.CooldownUntil.IsZero() == test.wantCooldown {
				t.Fatalf("cooldown = %v, want present=%t", result.CooldownUntil, test.wantCooldown)
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
