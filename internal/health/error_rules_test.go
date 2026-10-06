package health

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"gpt-load/internal/execution"
)

func TestCustomErrorRulesMatchFieldsAndRespectFirstMatch(t *testing.T) {
	rules := mustCompileErrorRules(t, []ErrorRule{
		{StatusCodes: []int{400, 403}, Keywords: []string{"余额不足", "BILLING"}, Retry: RetryNextCandidate, Effect: EffectRecordCredentialFailure},
		{StatusCodes: []int{403}, Retry: RetryNone, Effect: EffectNone},
		{StatusCodes: []int{403}, Retry: RetryNextCandidate, Effect: EffectCooldownCredential, CooldownSeconds: 60},
	}, "group")
	for _, test := range []struct {
		name, code, typeValue, summary string
		status                         int
		retry                          RetryDirective
		effect                         Effect
		ruleID                         RuleID
	}{
		{name: "message", status: 403, summary: "账户余额不足", retry: RetryNextCandidate, effect: EffectRecordCredentialFailure, ruleID: "custom.group.1"},
		{name: "code", status: 403, code: "billing_failed", retry: RetryNextCandidate, effect: EffectRecordCredentialFailure, ruleID: "custom.group.1"},
		{name: "type", status: 400, typeValue: "Billing_Error", retry: RetryNextCandidate, effect: EffectRecordCredentialFailure, ruleID: "custom.group.1"},
		{name: "status and keyword", status: 403, summary: "permission denied", retry: RetryNone, effect: EffectNone, ruleID: "custom.group.2"},
		{name: "status mismatch", status: 402, summary: "余额不足", retry: RetryNextCandidate, effect: EffectNone, ruleID: "fallback.upstream_response"},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempt := customErrorAttempt(test.status)
			attempt.Evidence.Code, attempt.Evidence.Type, attempt.Evidence.Summary = test.code, test.typeValue, test.summary
			got := JudgeExecution(attempt, DecisionContext{ErrorRules: rules, Method: http.MethodPost, Operation: execution.OperationChatCompletion})
			if got.Retry != test.retry || got.Effect != test.effect || got.RuleID != test.ruleID {
				t.Fatalf("decision = %+v", got)
			}
			if got.Category != FailureCategoryAmbiguous {
				t.Fatal("custom actions must preserve the original error category")
			}
		})
	}
}

func TestCustomErrorRulesKeywordsDoNotCrossFields(t *testing.T) {
	rules := mustCompileErrorRules(t, []ErrorRule{{Keywords: []string{"billing failed"}, Retry: RetryNone, Effect: EffectRecordCredentialFailure}}, "global")
	attempt := customErrorAttempt(403)
	attempt.Evidence.Type, attempt.Evidence.Code = "billing", "failed"
	base := JudgeExecution(attempt, DecisionContext{Operation: execution.OperationChatCompletion})
	got := JudgeExecution(attempt, DecisionContext{ErrorRules: rules, Operation: execution.OperationChatCompletion})
	if got != base {
		t.Fatalf("keyword crossed field boundaries: %+v", got)
	}
}

func TestCustomErrorRulesApplyToExisting200ErrorsAndUnsentRejections(t *testing.T) {
	rules := mustCompileErrorRules(t, []ErrorRule{{Keywords: []string{"balance"}, Retry: RetryNextCandidate, Effect: EffectCooldownCredential, CooldownSeconds: 90}}, "global")
	for _, status := range []int{200, 403} {
		attempt := customErrorAttempt(status)
		attempt.Evidence.Summary = "insufficient BALANCE"
		if status == 403 {
			attempt.DispatchState = execution.DispatchNotSent
			attempt.Evidence.ReplaySafety = execution.ReplaySafetyUnknown
		}
		original := attempt.Evidence.Clone()
		got := JudgeExecution(attempt, DecisionContext{ErrorRules: rules, Method: http.MethodPost, Operation: execution.OperationChatCompletion})
		if got.Effect != EffectCooldownCredential || got.Retry != RetryNextCandidate || got.Scope != execution.ErrorScopeCredential ||
			got.RuleID != "custom.global.1" || !got.CooldownUntil.Equal(attempt.Now.Add(90*time.Second)) {
			t.Fatalf("status=%d decision=%+v", status, got)
		}
		if !reflect.DeepEqual(*attempt.Evidence, original) {
			t.Fatal("custom rule changed original execution evidence")
		}
	}
	attempt := customErrorAttempt(200)
	attempt.Evidence = nil
	got := JudgeExecution(attempt, DecisionContext{ErrorRules: rules, Operation: execution.OperationChatCompletion})
	if got.Category != FailureCategoryOK || got.Effect != EffectNone {
		t.Fatalf("ordinary success was reclassified: %+v", got)
	}
}

func TestCustomErrorRulesPreserveReplayAndHealthBoundaries(t *testing.T) {
	rules := mustCompileErrorRules(t, []ErrorRule{{StatusCodes: []int{400, 403, 429, 503}, Retry: RetryNextCandidate, Effect: EffectRecordCredentialFailure}}, "global")
	for _, operation := range []execution.Operation{execution.OperationResponsesDelete, execution.OperationResponsesCancel, execution.OperationWebSearch, execution.OperationImagesGenerate} {
		attempt := customErrorAttempt(403)
		got := JudgeExecution(attempt, DecisionContext{ErrorRules: rules, Method: http.MethodPost, Operation: operation})
		if got.Retry != RetryNone || got.Effect != EffectRecordCredentialFailure {
			t.Fatalf("operation=%s decision=%+v", operation, got)
		}
		attempt.Evidence.ReplaySafety = execution.ReplaySafetyRejectedBeforeProcessing
		got = JudgeExecution(attempt, DecisionContext{ErrorRules: rules, Method: http.MethodPost, Operation: operation})
		if got.Retry != RetryNextCandidate {
			t.Fatalf("operation=%s rejected-before-processing decision=%+v", operation, got)
		}
	}
	for _, test := range []struct {
		name   string
		adjust func(*ExecutionAttempt)
		op     execution.Operation
	}{
		{name: "client error", adjust: func(a *ExecutionAttempt) { a.Evidence.Code = "invalid_parameter" }},
		{name: "authentication", adjust: func(a *ExecutionAttempt) { a.Evidence.Hint = execution.FailureHintRefreshRequired }},
		{name: "internal", adjust: func(a *ExecutionAttempt) {
			a.Evidence.Kind = execution.ErrorKindInternal
			a.Evidence.OriginHint = execution.ErrorOriginInternal
		}},
		{name: "transport", adjust: func(a *ExecutionAttempt) { a.Evidence.Kind = execution.ErrorKindTransport }},
		{name: "canceled", adjust: func(a *ExecutionAttempt) { a.DownstreamErr = context.Canceled }},
		{name: "model resource", op: execution.OperationResponsesRetrieve, adjust: func(a *ExecutionAttempt) { a.Evidence.Hint = execution.FailureHintModelUnavailable }},
		{name: "request scoped limit", adjust: func(a *ExecutionAttempt) {
			a.StatusCode = 429
			a.Evidence.Hint = execution.FailureHintRateLimited
			a.Evidence.ScopeHint = execution.ErrorScopeRequest
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempt := customErrorAttempt(403)
			test.adjust(&attempt)
			op := test.op
			if op == "" {
				op = execution.OperationChatCompletion
			}
			ctx := DecisionContext{Method: http.MethodPost, Operation: op}
			base := JudgeExecution(attempt, ctx)
			ctx.ErrorRules = rules
			if got := JudgeExecution(attempt, ctx); got != base {
				t.Fatalf("protected decision changed: %+v, baseline=%+v", got, base)
			}
		})
	}
	attempt := customErrorAttempt(403)
	attempt.DownstreamCommitted = true
	got := JudgeExecution(attempt, DecisionContext{ErrorRules: rules, Operation: execution.OperationChatCompletion})
	if got.Retry != RetryNone || got.Effect != EffectNone {
		t.Fatalf("committed response accepted untrusted custom effects: %+v", got)
	}
}

func TestCompileErrorRulesRejectsInvalidConfiguration(t *testing.T) {
	for _, value := range []any{
		nil, map[string]any{}, []any{map[string]any{"retry": "none", "effect": "none"}},
		[]ErrorRule{{StatusCodes: []int{199}, Retry: RetryNone, Effect: EffectNone}},
		[]ErrorRule{{StatusCodes: []int{600}, Retry: RetryNone, Effect: EffectNone}},
		[]ErrorRule{{StatusCodes: []int{403}, Retry: RetryRefreshCredential, Effect: EffectNone}},
		[]ErrorRule{{StatusCodes: []int{403}, Retry: RetryNone, Effect: "disable"}},
		[]ErrorRule{{StatusCodes: []int{403}, Retry: RetryNone, Effect: EffectCooldownModel}},
		[]ErrorRule{{StatusCodes: []int{403}, Retry: RetryNone, Effect: EffectCooldownCredential, CooldownSeconds: 10_000_000_000}},
		[]ErrorRule{{StatusCodes: []int{403}, Retry: RetryNone, Effect: EffectNone, CooldownSeconds: 60}},
		[]any{map[string]any{"status_codes": []int{403}, "retry": "none", "effect": "none", "id": "unused"}},
	} {
		if _, err := CompileErrorRules(value, "global"); err == nil {
			t.Errorf("invalid configuration accepted: %#v", value)
		}
	}
	rules := mustCompileErrorRules(t, []ErrorRule{{StatusCodes: []int{403, 403}, Keywords: []string{" Balance ", "BALANCE", ""}, Retry: RetryNone, Effect: EffectNone}}, "global")
	config := rules.Rules()
	if !reflect.DeepEqual(config[0].StatusCodes, []int{403}) || !reflect.DeepEqual(config[0].Keywords, []string{"Balance"}) {
		t.Fatalf("normalized rules = %+v", config)
	}
	config[0].StatusCodes[0], config[0].Keywords[0] = 500, "changed"
	if reflect.DeepEqual(config, rules.Rules()) {
		t.Fatal("management response mutated compiled rules")
	}
}

func mustCompileErrorRules(t *testing.T, config []ErrorRule, source string) ErrorRules {
	t.Helper()
	rules, err := CompileErrorRules(config, source)
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

func customErrorAttempt(status int) ExecutionAttempt {
	return ExecutionAttempt{DispatchState: execution.DispatchMaybeSent, StatusCode: status, Now: time.Unix(100, 0), Evidence: &execution.ErrorEvidence{
		Kind: execution.ErrorKindHTTP, OriginHint: execution.ErrorOriginUpstream, StatusCode: status,
	}}
}
