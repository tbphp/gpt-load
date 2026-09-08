package health

import (
	"context"
	"net/http"
	"testing"
	"time"

	"gpt-load/internal/execution"
)

func TestInferenceRateLimitsCooldownOnlyTheirModel(t *testing.T) {
	now := time.Now()
	for _, scope := range []execution.ErrorScope{"", execution.ErrorScopeModel, execution.ErrorScopeCredential, execution.ErrorScopeRequest} {
		for _, committed := range []bool{false, true} {
			result := JudgeExecution(ExecutionAttempt{DispatchState: execution.DispatchMaybeSent, StatusCode: 429,
				DownstreamCommitted: committed, Now: now, Evidence: &execution.ErrorEvidence{
					Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintRateLimited, StatusCode: 429,
					ScopeHint: scope, RetryAfter: 7 * time.Hour, ReplaySafety: execution.ReplaySafetyRejectedBeforeProcessing,
				}}, DecisionContext{Operation: execution.OperationChatCompletion})
			want := Effect("cooldown_model")
			if scope == execution.ErrorScopeCredential {
				want = EffectCooldownCredential
			}
			if scope == execution.ErrorScopeRequest {
				want = EffectNone
			}
			if result.Effect != want {
				t.Fatalf("scope=%s committed=%t decision=%#v", scope, committed, result)
			}
			if want != EffectNone && !result.CooldownUntil.Equal(now.Add(7*time.Hour)) {
				t.Fatalf("deadline = %v", result.CooldownUntil)
			}
			if committed && result.Retry != RetryNone {
				t.Fatal("committed response retried")
			}
			if err := result.Validate(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestModelCooldownHonorsLongExplicitRetryAfter(t *testing.T) {
	now := time.Now()
	result := JudgeExecution(ExecutionAttempt{DispatchState: execution.DispatchMaybeSent, StatusCode: 429, Now: now,
		Header: http.Header{"Retry-After": {"172800"}}, Evidence: &execution.ErrorEvidence{
			Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintRateLimited, StatusCode: 429,
		}}, DecisionContext{Operation: execution.OperationChatCompletion})
	if !result.CooldownUntil.Equal(now.Add(48 * time.Hour)) {
		t.Fatalf("deadline = %v", result.CooldownUntil)
	}
}

func TestNonInferenceUnknownLimitsDoNotCooldownInference(t *testing.T) {
	for _, operation := range []execution.Operation{execution.OperationCountTokens, execution.OperationResponsesInputTokens, execution.OperationListModels, execution.OperationResponsesRetrieve, execution.OperationProbe} {
		result := JudgeExecution(ExecutionAttempt{DispatchState: execution.DispatchMaybeSent, StatusCode: 429, Now: time.Now(), Evidence: &execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintRateLimited, StatusCode: 429}}, DecisionContext{Operation: operation})
		if result.Effect != EffectNone {
			t.Errorf("%s writes inference cooldown: %#v", operation, result)
		}
	}
}

func TestCanceledTransientCapacityRejectionDoesNotCreateModelCooldown(t *testing.T) {
	result := JudgeExecution(ExecutionAttempt{
		DispatchState: execution.DispatchMaybeSent, StatusCode: http.StatusOK,
		Now: time.Now(), DownstreamErr: context.Canceled,
		Evidence: &execution.ErrorEvidence{Kind: execution.ErrorKindProvider, Hint: execution.FailureHintRateLimited,
			OriginHint: execution.ErrorOriginUpstream, Code: "rate_limit_exceeded",
			ReplaySafety: execution.ReplaySafetyRejectedBeforeProcessing},
	}, DecisionContext{Operation: execution.OperationResponsesCreate})
	if result.Effect != EffectNone || !result.CooldownUntil.IsZero() || result.Retry != RetryNone {
		t.Fatalf("cancellation changed a transient rejection into a cooldown: %#v", result)
	}
}

func TestModelCooldownDoesNotUseUnrelatedWindowResetHeaders(t *testing.T) {
	now := time.Now()
	for _, duration := range []time.Duration{time.Minute, 10 * time.Minute} {
		result := JudgeExecution(ExecutionAttempt{
			DispatchState: execution.DispatchMaybeSent, StatusCode: http.StatusTooManyRequests, Now: now,
			Header:   http.Header{"Anthropic-Ratelimit-Tokens-Reset": {now.Add(30 * time.Minute).Format(time.RFC3339)}},
			Evidence: &execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintRateLimited},
		}, DecisionContext{Operation: execution.OperationChatCompletion, DefaultRateLimitCooldown: duration})
		if result.Effect != EffectCooldownModel || !result.CooldownUntil.Equal(now.Add(duration)) {
			t.Fatalf("default %v replaced by an unrelated window: %#v", duration, result)
		}
	}
}
