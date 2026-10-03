package policy

import (
	"testing"
	"time"
)

func TestExtensibleParametersWithoutChangingCoreTree(t *testing.T) {
	reg := NewRegistry()

	// 注册第二个普通 string 参数：client.region
	err := reg.RegisterParam(ParamDescriptor{
		Key:           "client.region",
		Type:          ParamTypeString,
		Label:         "fact.client.region.label",
		Description:   "fact.client.region.desc",
		Operators:     []string{"eq", "in"},
		Domains:       []Domain{DomainScheduling, DomainPricing},
		BindingScopes: []string{"group", "credential"},
	})
	if err != nil {
		t.Fatalf("register client.region failed: %v", err)
	}

	// 注册第二个普通 number 参数：credential.priority
	err = reg.RegisterParam(ParamDescriptor{
		Key:           "credential.priority",
		Type:          ParamTypeNumber,
		Label:         "fact.credential.priority.label",
		Description:   "fact.credential.priority.desc",
		Operators:     []string{"eq", "lt", "lte", "gt", "gte"},
		Domains:       []Domain{DomainScheduling, DomainPricing},
		BindingScopes: []string{"credential"},
	})
	if err != nil {
		t.Fatalf("register credential.priority failed: %v", err)
	}

	// 编写包含这两个新参数的规则正文
	jsonStr := `{
		"schema_version": 1,
		"rules": [
			{
				"id": "region-rule",
				"name": "Region In Rule",
				"domain": "pricing",
				"enabled": true,
				"when": {
					"fact": "client.region",
					"op": "in",
					"value": ["APAC", "EMEA"]
				},
				"then": {"type": "multiply_price", "factor": "1.25"}
			},
			{
				"id": "priority-rule",
				"name": "High Priority Exclude",
				"domain": "scheduling",
				"enabled": true,
				"when": {
					"fact": "credential.priority",
					"op": "gte",
					"value": 10
				},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`

	cfg, err := CompileWithRegistry([]byte(jsonStr), reg)
	if err != nil {
		t.Fatalf("CompileWithRegistry with extended params failed: %v", err)
	}

	// Case 1: 事实满足条件，求值成功命中
	ctxHit := &EvalContext{
		Now: time.Now(),
		CustomStringFacts: map[string]StringFact{
			"client.region": {Value: "APAC", State: FactStateMeasured},
		},
		CustomNumberFacts: map[string]NumberFact{
			"credential.priority": {Value: 15, State: FactStateMeasured},
		},
	}

	pRes := cfg.EvalPricing(ctxHit)
	if len(pRes.Matches) != 1 || pRes.Matches[0].RuleID != "region-rule" || pRes.Matches[0].Factor != "1.25" {
		t.Fatalf("expected region-rule match, got %v", pRes.Matches)
	}

	sRes := cfg.EvalScheduling(ctxHit)
	if !sRes.Excluded || sRes.Reason.RuleID != "priority-rule" {
		t.Fatalf("expected priority-rule match, got %v", sRes)
	}

	// Case 2: 事实不满足
	ctxMiss := &EvalContext{
		Now: time.Now(),
		CustomStringFacts: map[string]StringFact{
			"client.region": {Value: "US", State: FactStateMeasured},
		},
		CustomNumberFacts: map[string]NumberFact{
			"credential.priority": {Value: 5, State: FactStateMeasured},
		},
	}
	if len(cfg.EvalPricing(ctxMiss).Matches) != 0 {
		t.Fatal("expected pricing miss for US")
	}
	if cfg.EvalScheduling(ctxMiss).Excluded {
		t.Fatal("expected scheduling not excluded for priority 5")
	}

	// Case 3: 事实未知 (Unknown)
	ctxUnknown := &EvalContext{
		Now: time.Now(),
		CustomStringFacts: map[string]StringFact{
			"client.region": {Value: "", State: FactStateUnknown},
		},
	}
	diag := cfg.Inspect(ctxUnknown)
	if diag.Rules[0].Status != RuleStatusSkippedUnknown {
		t.Fatalf("expected skipped_unknown for region when state is unknown, got %v", diag.Rules[0].Status)
	}
}
