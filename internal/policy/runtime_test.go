package policy

import (
	"reflect"
	"testing"
	"time"

)

func TestRuntimeView_CompileAndEvalCandidate(t *testing.T) {
	view, err := CompileRuntimeView([]BindingConfig{
		{Scope: "group", GroupID: 1, Config: configJSON(t, schedRule("rule-grp-block-gpt4", modelEq("gpt-4")))},
		{Scope: "credential", GroupID: 2, CredentialID: 10, Config: bindingConfig(t, "override", schedRule("rule-cred-block-opus", upstreamEq("claude-3-opus")))},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	ctxGpt4 := &EvalContext{Now: time.Now(), RequestModel: modelFact("gpt-4"), UpstreamModel: modelFact("gpt-4-target")}
	// 分组 1 命中 request.model 规则。
	if excluded, match := view.EvalCandidate(1, 999, ctxGpt4); !excluded || match == nil || match.RuleID != "rule-grp-block-gpt4" {
		t.Fatalf("expected Group 1 to exclude gpt-4, got excluded=%v, match=%v", excluded, match)
	}
	// 分组 2 无分组策略，不排除。
	if excluded, _ := view.EvalCandidate(2, 999, ctxGpt4); excluded {
		t.Fatal("expected Group 2 not to exclude gpt-4, got excluded=true")
	}

	ctxOpus := &EvalContext{Now: time.Now(), RequestModel: modelFact("claude-3-opus"), UpstreamModel: modelFact("claude-3-opus")}
	// 凭据 10 命中 upstream.model 规则。
	if excluded, match := view.EvalCandidate(2, 10, ctxOpus); !excluded || match == nil || match.RuleID != "rule-cred-block-opus" {
		t.Fatalf("expected Credential 10 to exclude opus, got excluded=%v, match=%v", excluded, match)
	}
	// 同组其他凭据不受影响。
	if excluded, _ := view.EvalCandidate(2, 20, ctxOpus); excluded {
		t.Fatal("expected Credential 20 not to be affected by Credential 10 rule")
	}
	// 同凭据的替代目标保留。
	ctxSonnet := &EvalContext{Now: time.Now(), RequestModel: modelFact("claude-3-sonnet"), UpstreamModel: modelFact("claude-3-sonnet")}
	if excluded, _ := view.EvalCandidate(2, 10, ctxSonnet); excluded {
		t.Fatal("expected Credential 10 alternate target sonnet to be preserved")
	}
}

func TestRuntimeView_DisabledRuleDoesNotExclude(t *testing.T) {
	disabled := schedRule("rule-disabled", upstreamEq("gpt-4"))
	disabled["enabled"] = false
	view, err := CompileRuntimeView([]BindingConfig{
		{Scope: "group", GroupID: 1, Config: configJSON(t, disabled)},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}
	ctx := &EvalContext{Now: time.Now(), UpstreamModel: modelFact("gpt-4")}
	if excluded, _ := view.EvalCandidate(1, 10, ctx); excluded {
		t.Fatal("disabled rule must not exclude candidate")
	}
}

func TestRuntimeView_UnavailableQuotaFactEvaluatesToUnknown(t *testing.T) {
	view, err := CompileRuntimeView([]BindingConfig{
		{Scope: "credential", GroupID: 1, CredentialID: 10, Config: bindingConfig(t, "override", schedRule("rule-quota", quotaLt(18000, 0.2)))},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}
	// 无 QuotaWindows 视为不可用/unknown，不得排除候选。
	if excluded, _ := view.EvalCandidate(1, 10, &EvalContext{Now: time.Now()}); excluded {
		t.Fatal("unknown quota fact must evaluate to unknown and not exclude candidate")
	}
}

func TestRuntimeView_CompileAndEvalPricing(t *testing.T) {
	disabledPricing := priceRule("grp-rule-disabled", "10", modelEq("gpt-4o"))
	disabledPricing["enabled"] = false
	group := configJSON(t,
		priceRule("grp-rule-1", "2", modelEq("gpt-4o")),
		disabledPricing,
		priceRule("grp-rule-2", "1.5", upstreamEq("gpt-4o-2024-08-06")),
	)
	cred := bindingConfig(t, "override",
		priceRule("cred-rule-unknown", "5", quotaLt(18000, 0.2)),
		priceRule("cred-rule-1", "0.5", modelEq("gpt-4o")),
	)
	view, err := CompileRuntimeView([]BindingConfig{
		{Scope: "group", GroupID: 10, Config: group},
		{Scope: "credential", GroupID: 10, CredentialID: 20, Config: cred},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error: %v", err)
	}

	ctx := &EvalContext{Now: time.Now(), RequestModel: modelFact("gpt-4o"), UpstreamModel: modelFact("gpt-4o-2024-08-06")}
	matches := view.EvalPricing(10, 20, ctx)
	// override：仅凭据规则生效，分组规则被抑制，cred-rule-unknown 因 unknown 被跳过。
	if len(matches) != 1 {
		t.Fatalf("expected 1 match (cred-rule-1) under override, got %d: %+v", len(matches), matches)
	}
	if m := matches[0]; m.RuleID != "cred-rule-1" || m.BindingScope != "credential" || m.Factor != "0.5" {
		t.Errorf("match 0 = %+v, want cred-rule-1 factor 0.5", m)
	}
}

func TestRuntimeView_GroupPolicyModePricing(t *testing.T) {
	group := configJSON(t, priceRule("g", "2", modelEq("m")))
	credRule := priceRule("c", "3", modelEq("m"))
	ctx := measuredCtx("m")

	for _, tc := range []struct {
		name       string
		cred       []byte
		want       []string
		wantScopes []string
	}{
		{"inherit appends credential factors", bindingConfig(t, "", credRule), []string{"2", "3"}, []string{"group", "credential"}},
		{"override uses credential factor", bindingConfig(t, "override", credRule), []string{"3"}, []string{"credential"}},
		{"override with empty rules yields none", bindingConfig(t, "override"), []string{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view, err := CompileRuntimeView([]BindingConfig{
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
	deny := func(id string) map[string]any { return schedRule(id, modelEq("m")) }
	ctx := measuredCtx("m")

	for _, tc := range []struct {
		name      string
		group     []byte
		cred      []byte
		credGroup uint
		excluded  bool
	}{
		{"group deny, local allow, inherit", bindingConfig(t, "", deny("g")), bindingConfig(t, "inherit"), 1, true},
		{"group deny, local allow, override", bindingConfig(t, "", deny("g")), bindingConfig(t, "override"), 1, false},
		{"group allow, account deny, inherit applies account", bindingConfig(t, ""), bindingConfig(t, "inherit", deny("c")), 1, true},
		{"group allow, account deny, override denies", bindingConfig(t, ""), bindingConfig(t, "override", deny("c")), 1, true},
		{"credential override from another group does not suppress group", bindingConfig(t, "", deny("g")), bindingConfig(t, "override"), 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view, err := CompileRuntimeView([]BindingConfig{
				{Scope: "group", GroupID: 1, Config: tc.group},
				{Scope: "credential", GroupID: tc.credGroup, CredentialID: 7, Config: tc.cred},
			})
			if err != nil {
				t.Fatalf("CompileRuntimeView error: %v", err)
			}
			if excluded, _ := view.EvalCandidate(1, 7, ctx); excluded != tc.excluded {
				t.Fatalf("excluded = %v, want %v", excluded, tc.excluded)
			}
		})
	}
}

func TestCompileRuntimeView_RejectsGroupScopeOverride(t *testing.T) {
	override := bindingConfig(t, "override")
	if _, err := CompileRuntimeView([]BindingConfig{{Scope: "group", GroupID: 1, Config: override}}); err == nil {
		t.Fatal("expected group scope group_policy override rejection")
	}
	if _, err := CompileRuntimeView([]BindingConfig{{Scope: "credential", GroupID: 1, CredentialID: 2, Config: override}}); err != nil {
		t.Fatalf("credential scope override should compile: %v", err)
	}
}

func TestCompileRuntimeView_DuplicateBindingOrderMatrix(t *testing.T) {
	valid := configJSON(t)
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
				a := BindingConfig{Scope: scope, GroupID: 1, Config: tc.first}
				if scope == "credential" {
					a.CredentialID = 7
				}
				b := a
				b.Config = tc.second
				if _, err := CompileRuntimeView([]BindingConfig{a, b}); err == nil {
					t.Fatalf("expected duplicate error for %s/%s, got nil", scope, tc.name)
				}
			})
		}
	}
}

func TestRuntimeView_HasApplicablePricing(t *testing.T) {
	pricingRule := func(enabled bool) map[string]any {
		rule := priceRule("p", "1.5", modelEq("gpt-4"))
		rule["enabled"] = enabled
		return rule
	}
	schedulingRule := schedRule("s", modelEq("gpt-4"))

	var nilView *RuntimeView
	if nilView.HasApplicablePricing(1, 10) {
		t.Fatal("nil RuntimeView must return false for HasApplicablePricing")
	}
	if NewEmptyRuntimeView().HasApplicablePricing(1, 10) {
		t.Fatal("empty RuntimeView must return false for HasApplicablePricing")
	}

	for _, tc := range []struct {
		name  string
		group []byte
		cred  []byte
		want  bool
	}{
		{"inherit: group pricing enabled, cred none", bindingConfig(t, "", pricingRule(true)), bindingConfig(t, "inherit"), true},
		{"inherit: group none, cred pricing enabled", bindingConfig(t, ""), bindingConfig(t, "inherit", pricingRule(true)), true},
		{"inherit: group disabled pricing, cred disabled pricing", bindingConfig(t, "", pricingRule(false)), bindingConfig(t, "inherit", pricingRule(false)), false},
		{"inherit: only scheduling rules", bindingConfig(t, "", schedulingRule), bindingConfig(t, "inherit", schedulingRule), false},
		{"override: group pricing enabled, cred override has no pricing rules", bindingConfig(t, "", pricingRule(true)), bindingConfig(t, "override"), false},
		{"override: group pricing enabled, cred override has disabled pricing", bindingConfig(t, "", pricingRule(true)), bindingConfig(t, "override", pricingRule(false)), false},
		{"override: group pricing enabled, cred override has enabled pricing", bindingConfig(t, "", pricingRule(true)), bindingConfig(t, "override", pricingRule(true)), true},
		{"override: group none, cred override has enabled pricing", bindingConfig(t, ""), bindingConfig(t, "override", pricingRule(true)), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := CompileRuntimeView([]BindingConfig{
				{Scope: "group", GroupID: 1, Config: tc.group},
				{Scope: "credential", GroupID: 1, CredentialID: 10, Config: tc.cred},
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := v.HasApplicablePricing(1, 10); got != tc.want {
				t.Fatalf("HasApplicablePricing(1,10) = %v, want %v", got, tc.want)
			}
		})
	}

	// 凭据属于分组 2，却按分组 1 求值 -> 凭据规则被忽略，分组 1 无计价规则。
	v, err := CompileRuntimeView([]BindingConfig{
		{Scope: "group", GroupID: 1, Config: bindingConfig(t, "")},
		{Scope: "credential", GroupID: 2, CredentialID: 10, Config: bindingConfig(t, "override", pricingRule(true))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.HasApplicablePricing(1, 10) {
		t.Fatal("expected false when credential groupID does not match requested groupID")
	}
	if !v.HasApplicablePricing(2, 10) {
		t.Fatal("expected true when credential groupID matches requested groupID")
	}
}
