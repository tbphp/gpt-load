package policy_test

import (
	"testing"
	"time"

	"gpt-load/internal/policy"
)

func TestRuntimeView_CompileAndEvalCandidate(t *testing.T) {
	groupJSON := []byte(`{
		"schema_version": 1,
		"rules": [
			{
				"id": "rule-grp-block-gpt4",
				"name": "Block GPT-4 for Group 1",
				"domain": "scheduling",
				"enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "gpt-4"},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`)

	credJSON := []byte(`{
		"schema_version": 1,
		"rules": [
			{
				"id": "rule-cred-block-opus",
				"name": "Block Opus for Credential 10",
				"domain": "scheduling",
				"enabled": true,
				"when": {"fact": "upstream.model", "op": "eq", "value": "claude-3-opus"},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`)

	view, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "group", GroupID: 1, Config: groupJSON},
		{Scope: "credential", GroupID: 2, CredentialID: 10, Config: credJSON},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	now := time.Now()

	// 1. Group 1 with request.model = "gpt-4" -> excluded by group policy
	ctxGpt4 := &policy.EvalContext{
		Now:           now,
		RequestModel:  policy.StringFact{Value: "gpt-4", State: policy.FactStateMeasured},
		UpstreamModel: policy.StringFact{Value: "gpt-4-target", State: policy.FactStateMeasured},
	}
	excluded, match := view.EvalCandidate(1, 999, ctxGpt4)
	if !excluded || match == nil || match.RuleID != "rule-grp-block-gpt4" {
		t.Fatalf("expected Group 1 to exclude gpt-4, got excluded=%v, match=%v", excluded, match)
	}

	// 2. Group 2 (no group policy) with request.model = "gpt-4" -> not excluded
	excluded, _ = view.EvalCandidate(2, 999, ctxGpt4)
	if excluded {
		t.Fatalf("expected Group 2 not to exclude gpt-4, got excluded=true")
	}

	// 3. Credential 10 with upstream.model = "claude-3-opus" -> excluded by credential policy
	ctxOpus := &policy.EvalContext{
		Now:           now,
		RequestModel:  policy.StringFact{Value: "claude-3-opus", State: policy.FactStateMeasured},
		UpstreamModel: policy.StringFact{Value: "claude-3-opus", State: policy.FactStateMeasured},
	}
	excluded, match = view.EvalCandidate(2, 10, ctxOpus)
	if !excluded || match == nil || match.RuleID != "rule-cred-block-opus" {
		t.Fatalf("expected Credential 10 to exclude opus, got excluded=%v, match=%v", excluded, match)
	}

	// 4. Same group 2, Credential 20 (different credential) with upstream.model = "claude-3-opus" -> NOT excluded (account-wise semantics)
	excluded, _ = view.EvalCandidate(2, 20, ctxOpus)
	if excluded {
		t.Fatalf("expected Credential 20 not to be affected by Credential 10 rule")
	}

	// 5. Credential 10 with upstream.model = "claude-3-sonnet" -> NOT excluded (same-credential alternate target preserved)
	ctxSonnet := &policy.EvalContext{
		Now:           now,
		RequestModel:  policy.StringFact{Value: "claude-3-sonnet", State: policy.FactStateMeasured},
		UpstreamModel: policy.StringFact{Value: "claude-3-sonnet", State: policy.FactStateMeasured},
	}
	excluded, _ = view.EvalCandidate(2, 10, ctxSonnet)
	if excluded {
		t.Fatalf("expected Credential 10 alternate target sonnet to be preserved")
	}
}

func TestRuntimeView_DisabledRuleDoesNotExclude(t *testing.T) {
	disabledJSON := []byte(`{
		"schema_version": 1,
		"rules": [
			{
				"id": "rule-disabled",
				"name": "Disabled Rule",
				"domain": "scheduling",
				"enabled": false,
				"when": {"fact": "upstream.model", "op": "eq", "value": "gpt-4"},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`)

	view, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "group", GroupID: 1, Config: disabledJSON},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	ctx := &policy.EvalContext{
		Now:           time.Now(),
		UpstreamModel: policy.StringFact{Value: "gpt-4", State: policy.FactStateMeasured},
	}
	excluded, _ := view.EvalCandidate(1, 10, ctx)
	if excluded {
		t.Fatalf("disabled rule must not exclude candidate")
	}
}

func TestRuntimeView_UnavailableQuotaFactEvaluatesToUnknown(t *testing.T) {
	quotaRuleJSON := []byte(`{
		"schema_version": 1,
		"rules": [
			{
				"id": "rule-quota",
				"name": "Exclude on Low Quota",
				"domain": "scheduling",
				"enabled": true,
				"when": {"fact": "credential.quota.remaining_ratio", "select": {"scope": "account", "window_seconds": 18000}, "reduce": "min", "op": "lt", "value": 0.2},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`)

	view, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "credential", GroupID: 1, CredentialID: 10, Config: quotaRuleJSON},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	// Without QuotaWindows (treated as unavailable/unknown)
	ctx := &policy.EvalContext{
		Now: time.Now(),
	}
	excluded, _ := view.EvalCandidate(1, 10, ctx)
	if excluded {
		t.Fatalf("unknown quota fact must evaluate to unknown and not exclude candidate")
	}
}

func TestRuntime_PublishBindings_RetainsLastValidViewOnFailure(t *testing.T) {
	validJSON := []byte(`{
		"schema_version": 1,
		"rules": [
			{
				"id": "rule-valid",
				"name": "Valid Rule",
				"domain": "scheduling",
				"enabled": true,
				"when": {"fact": "upstream.model", "op": "eq", "value": "block-me"},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`)

	invalidJSON := []byte(`{
		"schema_version": 1,
		"rules": [
			{
				"id": "rule-invalid",
				"name": "Invalid operator",
				"domain": "scheduling",
				"enabled": true,
				"when": {"fact": "upstream.model", "op": "nonexistent_op", "value": "foo"},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`)

	r := policy.NewRuntime()

	// 1. Publish valid binding
	if err := r.PublishBindings([]policy.BindingConfig{
		{Scope: "group", GroupID: 5, Config: validJSON},
	}); err != nil {
		t.Fatalf("PublishBindings valid error = %v", err)
	}

	ctx := &policy.EvalContext{
		Now:           time.Now(),
		UpstreamModel: policy.StringFact{Value: "block-me", State: policy.FactStateMeasured},
	}
	excluded, _ := r.Load().EvalCandidate(5, 1, ctx)
	if !excluded {
		t.Fatalf("expected valid rule to exclude candidate")
	}

	// 2. Attempt to publish invalid binding
	err := r.PublishBindings([]policy.BindingConfig{
		{Scope: "group", GroupID: 5, Config: invalidJSON},
	})
	if err == nil {
		t.Fatalf("expected PublishBindings to fail on invalid config")
	}

	// 3. Verify that the previous valid view is retained!
	excluded, match := r.Load().EvalCandidate(5, 1, ctx)
	if !excluded || match == nil || match.RuleID != "rule-valid" {
		t.Fatalf("expected previous valid runtime view to be retained, got excluded=%v, match=%v", excluded, match)
	}
}

func TestCompileRuntimeView_RejectsDuplicateBindings(t *testing.T) {
	config := []byte(`{"schema_version":1,"rules":[]}`)
	for _, test := range []struct {
		name     string
		bindings []policy.BindingConfig
	}{
		{name: "duplicate group", bindings: []policy.BindingConfig{
			{Scope: "group", GroupID: 1, Config: config},
			{Scope: "group", GroupID: 1, Config: config},
		}},
		{name: "duplicate credential", bindings: []policy.BindingConfig{
			{Scope: "credential", GroupID: 1, CredentialID: 7, Config: config},
			{Scope: "credential", GroupID: 1, CredentialID: 7, Config: config},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := policy.CompileRuntimeView(test.bindings); err == nil {
				t.Fatal("CompileRuntimeView() error = nil")
			}
		})
	}
}

func TestRuntimeView_CompileAndEvalPricing(t *testing.T) {
	groupJSON := []byte(`{
		"schema_version": 1,
		"rules": [
			{
				"id": "grp-rule-1",
				"name": "Group Pricing 1 (x2)",
				"domain": "pricing",
				"enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "gpt-4o"},
				"then": {"type": "multiply_price", "factor": "2"}
			},
			{
				"id": "grp-rule-disabled",
				"name": "Group Pricing Disabled (x10)",
				"domain": "pricing",
				"enabled": false,
				"when": {"fact": "request.model", "op": "eq", "value": "gpt-4o"},
				"then": {"type": "multiply_price", "factor": "10"}
			},
			{
				"id": "grp-rule-2",
				"name": "Group Pricing 2 (x1.5)",
				"domain": "pricing",
				"enabled": true,
				"when": {"fact": "upstream.model", "op": "eq", "value": "gpt-4o-2024-08-06"},
				"then": {"type": "multiply_price", "factor": "1.5"}
			}
		]
	}`)

	credJSON := []byte(`{
		"schema_version": 1,
		"rules": [
			{
				"id": "cred-rule-unknown",
				"name": "Cred Unknown Fact",
				"domain": "pricing",
				"enabled": true,
				"when": {
					"fact": "credential.quota.remaining_ratio",
					"select": {"scope": "account", "window_seconds": 18000},
					"reduce": "min",
					"op": "lt",
					"value": 0.2
				},
				"then": {"type": "multiply_price", "factor": "5"}
			},
			{
				"id": "cred-rule-1",
				"name": "Cred Pricing Discount (x0.5)",
				"domain": "pricing",
				"enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "gpt-4o"},
				"then": {"type": "multiply_price", "factor": "0.5"}
			}
		]
	}`)

	view, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "group", GroupID: 10, Config: groupJSON},
		{Scope: "credential", GroupID: 10, CredentialID: 20, Config: credJSON},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error: %v", err)
	}

	ctx := &policy.EvalContext{
		Now:           time.Now(),
		RequestModel:  policy.StringFact{Value: "gpt-4o", State: policy.FactStateMeasured},
		UpstreamModel: policy.StringFact{Value: "gpt-4o-2024-08-06", State: policy.FactStateMeasured},
	}

	matches := view.EvalPricing(10, 20, ctx)
	if len(matches) != 3 {
		t.Fatalf("expected 3 matches (grp-rule-1, grp-rule-2, cred-rule-1), got %d: %+v", len(matches), matches)
	}

	// 1. Group rules come first in order
	if matches[0].RuleID != "grp-rule-1" || matches[0].BindingScope != "group" || matches[0].Factor != "2" {
		t.Errorf("match 0 = %+v, want grp-rule-1 factor 2", matches[0])
	}
	if matches[1].RuleID != "grp-rule-2" || matches[1].BindingScope != "group" || matches[1].Factor != "1.5" {
		t.Errorf("match 1 = %+v, want grp-rule-2 factor 1.5", matches[1])
	}

	// 2. Credential rule comes after group rules, cred-rule-unknown was skipped
	if matches[2].RuleID != "cred-rule-1" || matches[2].BindingScope != "credential" || matches[2].Factor != "0.5" {
		t.Errorf("match 2 = %+v, want cred-rule-1 factor 0.5", matches[2])
	}
}
