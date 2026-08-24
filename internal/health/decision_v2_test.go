package health

import (
	"testing"
	"time"

	"gpt-load/internal/execution"
)

func TestDecisionSeparatesRetryFromEffect(t *testing.T) {
	decision := Decision{
		Category: FailureCategoryRateLimited,
		Origin:   execution.ErrorOriginUpstream,
		Scope:    execution.ErrorScopeCredential,
		Retry:    RetryNone,
		Effect:   EffectCooldownCredential,
		RuleID:   RuleID("rate_limit.credential.committed"),
	}

	if decision.ShouldRetry() {
		t.Fatal("Decision.ShouldRetry() = true, want false")
	}
	if got := decision.LegacyAction(); got != ActionCooldownCredential {
		t.Fatalf("Decision.LegacyAction() = %d, want %d", got, ActionCooldownCredential)
	}
}

func TestDecisionLegacyActionProjection(t *testing.T) {
	tests := []struct {
		name     string
		decision Decision
		want     Action
	}{
		{name: "none", decision: Decision{}, want: ActionTerminate},
		{name: "retry", decision: Decision{Retry: RetryNextCandidate}, want: ActionRetry},
		{name: "refresh", decision: Decision{Retry: RetryRefreshCredential}, want: ActionRetry},
		{name: "cooldown", decision: Decision{Effect: EffectCooldownCredential}, want: ActionCooldownCredential},
		{name: "failure", decision: Decision{Effect: EffectRecordCredentialFailure}, want: ActionFailCredential},
		{name: "skip group", decision: Decision{Effect: EffectSkipGroup}, want: ActionSkipGroup},
		{
			name: "effect wins",
			decision: Decision{
				Retry:  RetryNextCandidate,
				Effect: EffectCooldownCredential,
			},
			want: ActionCooldownCredential,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.decision.LegacyAction(); got != test.want {
				t.Fatalf("Decision.LegacyAction() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestDecisionContextCarriesExistingPolicyInputs(t *testing.T) {
	context := DecisionContext{
		DefaultRateLimitCooldown: 10 * time.Minute,
		CredentialRefreshable:    true,
		Method:                   "POST",
		Operation:                execution.OperationResponsesCreate,
	}
	if context.DefaultRateLimitCooldown != 10*time.Minute ||
		!context.CredentialRefreshable || context.Method != "POST" ||
		context.Operation != execution.OperationResponsesCreate {
		t.Fatalf("DecisionContext = %#v", context)
	}
}

func TestDecisionValidateEnforcesCoreInvariants(t *testing.T) {
	tests := []struct {
		name     string
		decision Decision
		wantErr  bool
	}{
		{
			name: "valid credential cooldown",
			decision: Decision{
				Category:      FailureCategoryRateLimited,
				Origin:        execution.ErrorOriginUpstream,
				Scope:         execution.ErrorScopeCredential,
				Retry:         RetryNone,
				Effect:        EffectCooldownCredential,
				CooldownUntil: time.Unix(100, 0),
				RuleID:        "rate_limit.credential.committed",
			},
		},
		{name: "missing retry", decision: Decision{Effect: EffectNone, RuleID: "rule"}, wantErr: true},
		{name: "missing effect", decision: Decision{Retry: RetryNone, RuleID: "rule"}, wantErr: true},
		{name: "missing rule", decision: Decision{Retry: RetryNone, Effect: EffectNone}, wantErr: true},
		{
			name: "scope and effect are independent",
			decision: Decision{
				Retry: RetryNextCandidate, Effect: EffectRecordCredentialFailure,
				Scope: execution.ErrorScopeRequest, RuleID: "rule",
			},
		},
		{
			name: "cooldown requires deadline",
			decision: Decision{
				Retry: RetryNextCandidate, Effect: EffectCooldownCredential,
				Scope: execution.ErrorScopeCredential, RuleID: "rule",
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.decision.Validate()
			if (err != nil) != test.wantErr {
				t.Fatalf("Decision.Validate() error = %v, wantErr=%t", err, test.wantErr)
			}
		})
	}
}
