package policy_test

import (
	"fmt"
	"reflect"
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
		"group_policy": "override",
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
		"group_policy": "override",
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
		"group_policy": "override",
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
	if len(matches) != 1 {
		t.Fatalf("expected 1 match (cred-rule-1) under override, got %d: %+v", len(matches), matches)
	}

	// override：仅凭据规则生效；分组规则被抑制，cred-rule-unknown 仍因 unknown 被跳过
	if matches[0].RuleID != "cred-rule-1" || matches[0].BindingScope != "credential" || matches[0].Factor != "0.5" {
		t.Errorf("match 0 = %+v, want cred-rule-1 factor 0.5", matches[0])
	}
}

func TestRuntimeView_GroupPolicyModePricing(t *testing.T) {
	group := []byte(`{"schema_version":1,"rules":[{"id":"g","name":"G","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"m"},"then":{"type":"multiply_price","factor":"2"}}]}`)
	credRule := `{"id":"c","name":"C","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"m"},"then":{"type":"multiply_price","factor":"3"}}`
	cred := func(mode, rules string) []byte {
		if mode == "" {
			return []byte(fmt.Sprintf(`{"schema_version":1,"rules":[%s]}`, rules))
		}
		return []byte(fmt.Sprintf(`{"schema_version":1,"group_policy":%q,"rules":[%s]}`, mode, rules))
	}
	ctx := &policy.EvalContext{Now: time.Now(), RequestModel: policy.StringFact{Value: "m", State: policy.FactStateMeasured}}

	for _, tc := range []struct {
		name       string
		cred       []byte
		want       []string
		wantScopes []string
	}{
		{"inherit appends credential factors", cred("", credRule), []string{"2", "3"}, []string{"group", "credential"}},
		{"override uses credential factor", cred("override", credRule), []string{"3"}, []string{"credential"}},
		{"override with empty rules yields none", cred("override", ""), []string{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view, err := policy.CompileRuntimeView([]policy.BindingConfig{
				{Scope: "group", GroupID: 1, Config: group},
				{Scope: "credential", GroupID: 1, CredentialID: 7, Config: tc.cred},
			})
			if err != nil {
				t.Fatalf("CompileRuntimeView error: %v", err)
			}
			factors := []string{}
			for i, m := range view.EvalPricing(1, 7, ctx) {
				factors = append(factors, m.Factor)
				if i >= len(tc.wantScopes) || m.BindingScope != tc.wantScopes[i] {
					t.Errorf("binding scopes do not match %v: %+v", tc.wantScopes, m)
				}
			}
			if !reflect.DeepEqual(factors, tc.want) {
				t.Fatalf("factors = %v, want %v", factors, tc.want)
			}
		})
	}
}

func TestRuntimeView_GroupPolicyModeScheduling(t *testing.T) {
	deny := func(id string) string {
		return fmt.Sprintf(`{"id":%q,"name":%q,"domain":"scheduling","enabled":true,"when":{"fact":"request.model","op":"eq","value":"m"},"then":{"type":"exclude_candidate"}}`, id, id)
	}
	cfg := func(mode, rules string) []byte {
		if mode == "" {
			return []byte(fmt.Sprintf(`{"schema_version":1,"rules":[%s]}`, rules))
		}
		return []byte(fmt.Sprintf(`{"schema_version":1,"group_policy":%q,"rules":[%s]}`, mode, rules))
	}
	ctx := &policy.EvalContext{Now: time.Now(), RequestModel: policy.StringFact{Value: "m", State: policy.FactStateMeasured}}

	for _, tc := range []struct {
		name     string
		bindings []policy.BindingConfig
		excluded bool
	}{
		{"group deny, local allow, inherit", []policy.BindingConfig{
			{Scope: "group", GroupID: 1, Config: cfg("", deny("g"))},
			{Scope: "credential", GroupID: 1, CredentialID: 7, Config: cfg("inherit", "")},
		}, true},
		{"group deny, local allow, override", []policy.BindingConfig{
			{Scope: "group", GroupID: 1, Config: cfg("", deny("g"))},
			{Scope: "credential", GroupID: 1, CredentialID: 7, Config: cfg("override", "")},
		}, false},
		{"group allow, account deny, inherit applies account", []policy.BindingConfig{
			{Scope: "group", GroupID: 1, Config: cfg("", "")},
			{Scope: "credential", GroupID: 1, CredentialID: 7, Config: cfg("inherit", deny("c"))},
		}, true},
		{"group allow, account deny, override denies", []policy.BindingConfig{
			{Scope: "group", GroupID: 1, Config: cfg("", "")},
			{Scope: "credential", GroupID: 1, CredentialID: 7, Config: cfg("override", deny("c"))},
		}, true},
		{"credential override from another group does not suppress group", []policy.BindingConfig{
			{Scope: "group", GroupID: 1, Config: cfg("", deny("g"))},
			{Scope: "credential", GroupID: 2, CredentialID: 7, Config: cfg("override", "")},
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view, err := policy.CompileRuntimeView(tc.bindings)
			if err != nil {
				t.Fatalf("CompileRuntimeView error: %v", err)
			}
			excluded, _ := view.EvalCandidate(1, 7, ctx)
			if excluded != tc.excluded {
				t.Fatalf("excluded = %v, want %v", excluded, tc.excluded)
			}
		})
	}
}

func TestCompileRuntimeView_RejectsGroupScopeOverride(t *testing.T) {
	override := []byte(`{"schema_version":1,"group_policy":"override","rules":[]}`)
	if _, err := policy.CompileRuntimeView([]policy.BindingConfig{{Scope: "group", GroupID: 1, Config: override}}); err == nil {
		t.Fatal("expected group scope group_policy override rejection")
	}
	if _, err := policy.CompileRuntimeView([]policy.BindingConfig{{Scope: "credential", GroupID: 1, CredentialID: 2, Config: override}}); err != nil {
		t.Fatalf("credential scope override should compile: %v", err)
	}
}

func TestCompileRuntimeView_DuplicateBindingOrderMatrix(t *testing.T) {
	valid := []byte(`{"schema_version":1,"rules":[]}`)
	for _, scope := range []string{"group", "credential"} {
		for _, tc := range []struct {
			name   string
			first  []byte
			second []byte
		}{
			{"empty-empty", nil, nil},
			{"empty-valid", nil, valid},
			{"valid-empty", valid, nil},
			{"valid-valid", valid, valid},
		} {
			t.Run(scope+"/"+tc.name, func(t *testing.T) {
				a := policy.BindingConfig{Scope: scope, GroupID: 1, Config: tc.first}
				if scope == "credential" {
					a.CredentialID = 7
				}
				b := a
				b.Config = tc.second
				_, err := policy.CompileRuntimeView([]policy.BindingConfig{a, b})
				if err == nil {
					t.Fatalf("expected duplicate error for %s/%s, got nil", scope, tc.name)
				}
			})
		}
	}
}

func TestRuntimeView_HasApplicablePricing(t *testing.T) {
	pricingRule := func(enabled bool, id string) string {
		enStr := "false"
		if enabled {
			enStr = "true"
		}
		return fmt.Sprintf(`{
			"id": %q,
			"name": "Pricing Rule",
			"domain": "pricing",
			"enabled": %s,
			"when": {"fact": "request.model", "op": "eq", "value": "gpt-4"},
			"then": {"type": "multiply_price", "factor": "1.5"}
		}`, id, enStr)
	}
	schedulingRule := func(id string) string {
		return fmt.Sprintf(`{
			"id": %q,
			"name": "Scheduling Rule",
			"domain": "scheduling",
			"enabled": true,
			"when": {"fact": "request.model", "op": "eq", "value": "gpt-4"},
			"then": {"type": "exclude_candidate"}
		}`, id)
	}
	cfgJSON := func(mode string, rules ...string) []byte {
		modeField := ""
		if mode != "" {
			modeField = fmt.Sprintf(`"group_policy": %q,`, mode)
		}
		var joinedRules string
		for i, r := range rules {
			if i > 0 {
				joinedRules += ","
			}
			joinedRules += r
		}
		return []byte(fmt.Sprintf(`{%s"schema_version":1,"rules":[%s]}`, modeField, joinedRules))
	}

	// 1. Nil RuntimeView
	var nilView *policy.RuntimeView
	if nilView.HasApplicablePricing(1, 10) {
		t.Fatal("nil RuntimeView must return false for HasApplicablePricing")
	}

	// 2. Empty view
	emptyView := policy.NewEmptyRuntimeView()
	if emptyView.HasApplicablePricing(1, 10) {
		t.Fatal("empty RuntimeView must return false for HasApplicablePricing")
	}

	// 3. Inherit mode tests
	t.Run("inherit: group pricing enabled, cred none", func(t *testing.T) {
		v, err := policy.CompileRuntimeView([]policy.BindingConfig{
			{Scope: "group", GroupID: 1, Config: cfgJSON("", pricingRule(true, "gp1"))},
			{Scope: "credential", GroupID: 1, CredentialID: 10, Config: cfgJSON("inherit")},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !v.HasApplicablePricing(1, 10) {
			t.Fatal("expected true when group has enabled pricing in inherit mode")
		}
	})

	t.Run("inherit: group none, cred pricing enabled", func(t *testing.T) {
		v, err := policy.CompileRuntimeView([]policy.BindingConfig{
			{Scope: "group", GroupID: 1, Config: cfgJSON("")},
			{Scope: "credential", GroupID: 1, CredentialID: 10, Config: cfgJSON("inherit", pricingRule(true, "cp1"))},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !v.HasApplicablePricing(1, 10) {
			t.Fatal("expected true when credential has enabled pricing in inherit mode")
		}
	})

	t.Run("inherit: group disabled pricing, cred disabled pricing", func(t *testing.T) {
		v, err := policy.CompileRuntimeView([]policy.BindingConfig{
			{Scope: "group", GroupID: 1, Config: cfgJSON("", pricingRule(false, "gp1"))},
			{Scope: "credential", GroupID: 1, CredentialID: 10, Config: cfgJSON("inherit", pricingRule(false, "cp1"))},
		})
		if err != nil {
			t.Fatal(err)
		}
		if v.HasApplicablePricing(1, 10) {
			t.Fatal("expected false when all pricing rules are disabled")
		}
	})

	t.Run("inherit: only scheduling rules", func(t *testing.T) {
		v, err := policy.CompileRuntimeView([]policy.BindingConfig{
			{Scope: "group", GroupID: 1, Config: cfgJSON("", schedulingRule("gs1"))},
			{Scope: "credential", GroupID: 1, CredentialID: 10, Config: cfgJSON("inherit", schedulingRule("cs1"))},
		})
		if err != nil {
			t.Fatal(err)
		}
		if v.HasApplicablePricing(1, 10) {
			t.Fatal("expected false when there are only scheduling rules")
		}
	})

	// 4. Override mode tests
	t.Run("override: group pricing enabled, cred override has no pricing rules", func(t *testing.T) {
		v, err := policy.CompileRuntimeView([]policy.BindingConfig{
			{Scope: "group", GroupID: 1, Config: cfgJSON("", pricingRule(true, "gp1"))},
			{Scope: "credential", GroupID: 1, CredentialID: 10, Config: cfgJSON("override")},
		})
		if err != nil {
			t.Fatal(err)
		}
		// override 模式下必须忽略分组规则，即使分组有已启用计价规则，也不继承
		if v.HasApplicablePricing(1, 10) {
			t.Fatal("expected false when credential overrides and has no pricing rules")
		}
	})

	t.Run("override: group pricing enabled, cred override has disabled pricing", func(t *testing.T) {
		v, err := policy.CompileRuntimeView([]policy.BindingConfig{
			{Scope: "group", GroupID: 1, Config: cfgJSON("", pricingRule(true, "gp1"))},
			{Scope: "credential", GroupID: 1, CredentialID: 10, Config: cfgJSON("override", pricingRule(false, "cp1"))},
		})
		if err != nil {
			t.Fatal(err)
		}
		if v.HasApplicablePricing(1, 10) {
			t.Fatal("expected false when credential overrides and pricing rule is disabled")
		}
	})

	t.Run("override: group pricing enabled, cred override has enabled pricing", func(t *testing.T) {
		v, err := policy.CompileRuntimeView([]policy.BindingConfig{
			{Scope: "group", GroupID: 1, Config: cfgJSON("", pricingRule(true, "gp1"))},
			{Scope: "credential", GroupID: 1, CredentialID: 10, Config: cfgJSON("override", pricingRule(true, "cp1"))},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !v.HasApplicablePricing(1, 10) {
			t.Fatal("expected true when credential override has enabled pricing")
		}
	})

	t.Run("override: group none, cred override has enabled pricing", func(t *testing.T) {
		v, err := policy.CompileRuntimeView([]policy.BindingConfig{
			{Scope: "group", GroupID: 1, Config: cfgJSON("")},
			{Scope: "credential", GroupID: 1, CredentialID: 10, Config: cfgJSON("override", pricingRule(true, "cp1"))},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !v.HasApplicablePricing(1, 10) {
			t.Fatal("expected true when credential override has enabled pricing")
		}
	})

	// 5. Credential group mismatch
	t.Run("group mismatch: credential belongs to group 2 evaluated for group 1", func(t *testing.T) {
		v, err := policy.CompileRuntimeView([]policy.BindingConfig{
			{Scope: "group", GroupID: 1, Config: cfgJSON("")},
			{Scope: "credential", GroupID: 2, CredentialID: 10, Config: cfgJSON("override", pricingRule(true, "cp1"))},
		})
		if err != nil {
			t.Fatal(err)
		}
		// Credential 10 belongs to group 2, evaluated with group 1 -> credential ignored, group 1 has no pricing
		if v.HasApplicablePricing(1, 10) {
			t.Fatal("expected false when credential groupID does not match requested groupID")
		}
		// But evaluated with group 2 -> credential applies
		if !v.HasApplicablePricing(2, 10) {
			t.Fatal("expected true when credential groupID matches requested groupID")
		}
	})
}
