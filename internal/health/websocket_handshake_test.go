package health

import (
	"net/http"
	"testing"
	"time"

	"gpt-load/internal/execution"
)

func TestUnsentHTTPRejectionUsesExistingHealthRules(t *testing.T) {
	now := time.Unix(100, 0)
	for _, test := range []struct {
		name     string
		status   int
		code     string
		hint     execution.FailureHint
		scope    execution.ErrorScope
		category FailureCategory
		retry    RetryDirective
		effect   Effect
	}{
		{"rate limit", 429, "rate_limit_exceeded", "", "", FailureCategoryRateLimited, RetryNextCandidate, EffectCooldownModel},
		{"credential quota", 429, "quota_exceeded", execution.FailureHintRateLimited, execution.ErrorScopeCredential, FailureCategoryRateLimited, RetryNextCandidate, EffectCooldownCredential},
		{"host failure", 503, "server_error", "", "", FailureCategoryUpstreamHostError, RetryNextCandidate, EffectSkipGroup},
		{"invalid credential", 401, "invalid_api_key", "", "", FailureCategoryInvalidKey, RetryNextCandidate, EffectRecordCredentialFailure},
		{"invalid parameter", 400, "invalid_parameter", "", "", FailureCategoryClientError, RetryNone, EffectNone},
		{"generic client response", 400, "invalid_request_error", "", "", FailureCategoryAmbiguous, RetryNextCandidate, EffectNone},
		{"model capacity", 429, "model_at_capacity", execution.FailureHintCandidateUnavailable, execution.ErrorScopeModel, FailureCategoryModelUnavailable, RetryNextCandidate, EffectNone},
	} {
		t.Run(test.name, func(t *testing.T) {
			evidence := &execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, OriginHint: execution.ErrorOriginUpstream,
				StatusCode: test.status, Code: test.code, Hint: test.hint, ScopeHint: test.scope, ReplaySafety: execution.ReplaySafetyUnknown}
			got := JudgeExecution(ExecutionAttempt{DispatchState: execution.DispatchNotSent, StatusCode: test.status,
				Evidence: evidence, Header: http.Header{"Retry-After": {"120"}}, Now: now},
				DecisionContext{Operation: execution.OperationResponsesCreate, Method: http.MethodPost})
			if got.Category != test.category || got.Retry != test.retry || got.Effect != test.effect {
				t.Fatalf("decision = %+v", got)
			}
			if (test.effect == EffectCooldownModel || test.effect == EffectCooldownCredential) && !got.CooldownUntil.Equal(now.Add(120*time.Second)) {
				t.Fatalf("cooldown = %v", got.CooldownUntil)
			}
			if evidence.ReplaySafety != execution.ReplaySafetyUnknown {
				t.Fatal("classification mutated original dispatch evidence")
			}
		})
	}
}
