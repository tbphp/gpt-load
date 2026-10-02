package policy

import (
	"sync"
	"testing"
	"time"

	"gpt-load/internal/pricing"
)

func TestTruthValueLogic(t *testing.T) {
	// Not
	if TruthTrue.Not() != TruthFalse {
		t.Fatal("NOT True must be False")
	}
	if TruthFalse.Not() != TruthTrue {
		t.Fatal("NOT False must be True")
	}
	if TruthUnknown.Not() != TruthUnknown {
		t.Fatal("NOT Unknown must be Unknown")
	}

	// And: false AND unknown = false
	if TruthFalse.And(TruthUnknown) != TruthFalse {
		t.Fatal("False AND Unknown must be False")
	}
	if TruthUnknown.And(TruthFalse) != TruthFalse {
		t.Fatal("Unknown AND False must be False")
	}
	if TruthTrue.And(TruthUnknown) != TruthUnknown {
		t.Fatal("True AND Unknown must be Unknown")
	}

	// Or: true OR unknown = true
	if TruthTrue.Or(TruthUnknown) != TruthTrue {
		t.Fatal("True OR Unknown must be True")
	}
	if TruthUnknown.Or(TruthTrue) != TruthTrue {
		t.Fatal("Unknown OR True must be True")
	}
	if TruthFalse.Or(TruthUnknown) != TruthUnknown {
		t.Fatal("False OR Unknown must be Unknown")
	}
}

func TestIndependentModelFacts(t *testing.T) {
	// request.model 和 upstream.model 是独立事实
	jsonStr := `{
		"schema_version": 1,
		"rules": [
			{
				"id": "match-request-smart",
				"name": "Match Request Smart",
				"domain": "scheduling",
				"enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "smart"},
				"then": {"type": "exclude_candidate"}
			},
			{
				"id": "match-upstream-astra",
				"name": "Match Upstream Astra",
				"domain": "scheduling",
				"enabled": true,
				"when": {"fact": "upstream.model", "op": "eq", "value": "Astra"},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`

	cfg, err := Compile([]byte(jsonStr))
	if err != nil {
		t.Fatal(err)
	}

	// 事实：客户端请求是 "smart"，实际上游是 "Astra"
	ctx := &EvalContext{
		Now:           time.Now(),
		RequestModel:  StringFact{Value: "smart", State: FactStateMeasured},
		UpstreamModel: StringFact{Value: "Astra", State: FactStateMeasured},
	}

	// 单独只跑第一条
	singleReqOnly := &CompiledConfig{rules: []Rule{cfg.Rules()[0]}}
	res1 := singleReqOnly.EvalScheduling(ctx)
	if !res1.Excluded || res1.Reason.RuleID != "match-request-smart" {
		t.Fatalf("expected request.model match, got %v", res1)
	}

	// 若 request.model 为 Astra（与事实 smart 不符），则不命中
	ctxMismatched := &EvalContext{
		Now:           time.Now(),
		RequestModel:  StringFact{Value: "other", State: FactStateMeasured},
		UpstreamModel: StringFact{Value: "Astra", State: FactStateMeasured},
	}
	resMismatched := singleReqOnly.EvalScheduling(ctxMismatched)
	if resMismatched.Excluded {
		t.Fatal("request.model should not match other")
	}

	// 上游模型事实独立
	singleUpstreamOnly := &CompiledConfig{rules: []Rule{cfg.Rules()[1]}}
	res2 := singleUpstreamOnly.EvalScheduling(ctx)
	if !res2.Excluded || res2.Reason.RuleID != "match-upstream-astra" {
		t.Fatalf("expected upstream.model match, got %v", res2)
	}
}

func TestMissing5hQuotaDoesNotBlock7dORMatch(t *testing.T) {
	// 合同核心验证点3：“缺5h不拦截7d已使OR为真仍命中”
	jsonStr := `{
		"schema_version": 1,
		"rules": [
			{
				"id": "protect-quota-or",
				"name": "5h or 7d low quota protection",
				"domain": "scheduling",
				"enabled": true,
				"when": {
					"any": [
						{
							"fact": "credential.quota.remaining_ratio",
							"select": {"scope": "account", "window_seconds": 18000},
							"reduce": "min",
							"op": "lt",
							"value": 0.1
						},
						{
							"fact": "credential.quota.remaining_ratio",
							"select": {"scope": "account", "window_seconds": 604800},
							"reduce": "min",
							"op": "lt",
							"value": 0.1
						}
					]
				},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`

	cfg, err := Compile([]byte(jsonStr))
	if err != nil {
		t.Fatal(err)
	}

	// 5h 窗口状态为 unknown（无5h窗口），而 7d 窗口有测量值 0.05 (< 0.1)
	ctx := &EvalContext{
		Now: time.Now(),
		QuotaWindows: []QuotaWindowFact{
			{
				Scope:         "account",
				WindowSeconds: 18000, // 5h
				Ratio:         0,
				State:         FactStateUnknown, // 5h 缺失或未知
			},
			{
				Scope:         "account",
				WindowSeconds: 604800, // 7d
				Ratio:         0.05,   // < 0.1
				State:         FactStateMeasured,
			},
		},
	}

	res := cfg.EvalScheduling(ctx)
	if !res.Excluded {
		t.Fatalf("expected 7d match to satisfy OR and exclude candidate, got Excluded=false")
	}
	if res.Reason.RuleID != "protect-quota-or" {
		t.Fatalf("expected reason protect-quota-or, got %v", res.Reason)
	}
}

func TestPricingRulesOrderedMultipliersPreserveAllFactors(t *testing.T) {
	// 合同第6节与第10节验证点5：“×2、×3、×0.5保留全部因子；×0、×1不当默认；复用倍率解析”
	jsonStr := `{
		"schema_version": 1,
		"rules": [
			{
				"id": "p1", "name": "Rule 1", "domain": "pricing", "enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "Astra"},
				"then": {"type": "multiply_price", "factor": "2"}
			},
			{
				"id": "p2", "name": "Rule 2", "domain": "pricing", "enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "Astra"},
				"then": {"type": "multiply_price", "factor": "3"}
			},
			{
				"id": "p3", "name": "Rule 3", "domain": "pricing", "enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "Astra"},
				"then": {"type": "multiply_price", "factor": "0.5"}
			},
			{
				"id": "p4", "name": "Rule 4", "domain": "pricing", "enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "Astra"},
				"then": {"type": "multiply_price", "factor": "0"}
			},
			{
				"id": "p5", "name": "Rule 5", "domain": "pricing", "enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "Astra"},
				"then": {"type": "multiply_price", "factor": "1"}
			}
		]
	}`

	cfg, err := Compile([]byte(jsonStr))
	if err != nil {
		t.Fatal(err)
	}

	ctx := &EvalContext{
		Now:          time.Now(),
		RequestModel: StringFact{Value: "Astra", State: FactStateMeasured},
	}

	res := cfg.EvalPricing(ctx)
	if len(res.Matches) != 5 {
		t.Fatalf("expected 5 matches, got %d", len(res.Matches))
	}

	expected := []struct {
		id     string
		factor string
		mult   pricing.PriceMultiplier
	}{
		{"p1", "2", 2_000_000},
		{"p2", "3", 3_000_000},
		{"p3", "0.5", 500_000},
		{"p4", "0", 0},
		{"p5", "1", 1_000_000},
	}

	for i, exp := range expected {
		m := res.Matches[i]
		if m.RuleID != exp.id || m.Factor != exp.factor || m.Multiplier != exp.mult {
			t.Fatalf("match %d mismatch: got {id:%q, factor:%q, mult:%d}, want {id:%q, factor:%q, mult:%d}",
				i, m.RuleID, m.Factor, m.Multiplier, exp.id, exp.factor, exp.mult)
		}
	}
}

func TestUnknownPricingRuleDoesNotBlockSubsequentMatches(t *testing.T) {
	// 合同验证点2：“未决价格规则不阻断其他命中”
	jsonStr := `{
		"schema_version": 1,
		"rules": [
			{
				"id": "p-unknown", "name": "Unknown Rule", "domain": "pricing", "enabled": true,
				"when": {
					"fact": "credential.quota.remaining_ratio",
					"select": {"scope": "account", "window_seconds": 18000},
					"reduce": "min",
					"op": "lt",
					"value": 0.1
				},
				"then": {"type": "multiply_price", "factor": "2"}
			},
			{
				"id": "p-hit", "name": "Hit Rule", "domain": "pricing", "enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "Astra"},
				"then": {"type": "multiply_price", "factor": "1.5"}
			}
		]
	}`

	cfg, err := Compile([]byte(jsonStr))
	if err != nil {
		t.Fatal(err)
	}

	// 5h 窗口事实缺失（返回 unknown），而 request.model 命中
	ctx := &EvalContext{
		Now:          time.Now(),
		RequestModel: StringFact{Value: "Astra", State: FactStateMeasured},
		QuotaWindows: nil, // 无窗口
	}

	res := cfg.EvalPricing(ctx)
	if len(res.Matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(res.Matches))
	}
	if res.Matches[0].RuleID != "p-hit" || res.Matches[0].Factor != "1.5" {
		t.Fatalf("expected p-hit to match, got %v", res.Matches[0])
	}
}

func TestInspectDiagnosisModePureCalculation(t *testing.T) {
	jsonStr := `{
		"schema_version": 1,
		"rules": [
			{
				"id": "r-disabled", "name": "Disabled Rule", "domain": "scheduling", "enabled": false,
				"when": {"fact": "request.model", "op": "eq", "value": "Astra"},
				"then": {"type": "exclude_candidate"}
			},
			{
				"id": "r-hit", "name": "Hit Rule", "domain": "scheduling", "enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "Astra"},
				"then": {"type": "exclude_candidate"}
			},
			{
				"id": "r-miss", "name": "Miss Rule", "domain": "scheduling", "enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "Luna"},
				"then": {"type": "exclude_candidate"}
			},
			{
				"id": "r-unknown", "name": "Unknown Rule", "domain": "scheduling", "enabled": true,
				"when": {
					"fact": "credential.quota.remaining_ratio",
					"select": {"scope": "account", "window_seconds": 18000},
					"reduce": "min",
					"op": "lt",
					"value": 0.1
				},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`

	cfg, err := Compile([]byte(jsonStr))
	if err != nil {
		t.Fatal(err)
	}

	ctx := &EvalContext{
		Now:          time.Now(),
		RequestModel: StringFact{Value: "Astra", State: FactStateMeasured},
	}

	diag := cfg.Inspect(ctx)
	if len(diag.Rules) != 4 {
		t.Fatalf("expected 4 inspect rules, got %d", len(diag.Rules))
	}

	if diag.Rules[0].Status != RuleStatusDisabled {
		t.Fatalf("expected rule 0 status disabled, got %v", diag.Rules[0].Status)
	}
	if diag.Rules[1].Status != RuleStatusHit {
		t.Fatalf("expected rule 1 status hit, got %v", diag.Rules[1].Status)
	}
	if diag.Rules[2].Status != RuleStatusMiss {
		t.Fatalf("expected rule 2 status miss, got %v", diag.Rules[2].Status)
	}
	if diag.Rules[3].Status != RuleStatusSkippedUnknown {
		t.Fatalf("expected rule 3 status skipped_unknown, got %v", diag.Rules[3].Status)
	}
	if diag.Rules[3].Condition.UnknownReason == "" {
		t.Fatal("expected non-empty unknown reason for rule 3")
	}
}

func TestConcurrentReadOnlyEvaluationSafe(t *testing.T) {
	jsonStr := `{
		"schema_version": 1,
		"rules": [
			{
				"id": "r-sched", "name": "Sched", "domain": "scheduling", "enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "Astra"},
				"then": {"type": "exclude_candidate"}
			},
			{
				"id": "r-price", "name": "Price", "domain": "pricing", "enabled": true,
				"when": {"fact": "upstream.model", "op": "eq", "value": "Luna"},
				"then": {"type": "multiply_price", "factor": "1.5"}
			}
		]
	}`

	cfg, err := Compile([]byte(jsonStr))
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ctx := &EvalContext{
				Now:           time.Now(),
				RequestModel:  StringFact{Value: "Astra", State: FactStateMeasured},
				UpstreamModel: StringFact{Value: "Luna", State: FactStateMeasured},
			}
			sRes := cfg.EvalScheduling(ctx)
			if !sRes.Excluded {
				t.Errorf("routine %d expected excluded", idx)
			}
			pRes := cfg.EvalPricing(ctx)
			if len(pRes.Matches) != 1 {
				t.Errorf("routine %d expected 1 match", idx)
			}
			diag := cfg.Inspect(ctx)
			if len(diag.Rules) != 2 {
				t.Errorf("routine %d expected 2 rules", idx)
			}
		}(i)
	}
	wg.Wait()
}

func TestEvalConditionEdgeCases(t *testing.T) {
	// 测试 multiple quota windows min reducer
	jsonStr := `{
		"schema_version": 1,
		"rules": [{
			"id": "r-min", "name": "Min Reducer", "domain": "scheduling", "enabled": true,
			"when": {
				"fact": "credential.quota.remaining_ratio",
				"select": {"scope": "account", "window_seconds": 18000},
				"reduce": "min",
				"op": "lte",
				"value": 0.2
			},
			"then": {"type": "exclude_candidate"}
		}]
	}`
	cfg, err := Compile([]byte(jsonStr))
	if err != nil {
		t.Fatal(err)
	}

	// 存在两个匹配的有效窗口：0.3 和 0.15，min 应该取 0.15 (<= 0.2 命中)
	ctx := &EvalContext{
		Now: time.Now(),
		QuotaWindows: []QuotaWindowFact{
			{Scope: "account", WindowSeconds: 18000, Ratio: 0.3, State: FactStateMeasured},
			{Scope: "account", WindowSeconds: 18000, Ratio: 0.15, State: FactStateMeasured},
		},
	}
	if !cfg.EvalScheduling(ctx).Excluded {
		t.Fatal("expected min reducer to pick 0.15 and exclude candidate")
	}

	// 测试全部为 true 的 all 和全部为 false 的 any
	jsonBool := `{
		"schema_version": 1,
		"rules": [
			{
				"id": "r-all-true", "name": "All True", "domain": "scheduling", "enabled": true,
				"when": {
					"all": [
						{"fact": "request.model", "op": "eq", "value": "A"},
						{"fact": "upstream.model", "op": "eq", "value": "B"}
					]
				},
				"then": {"type": "exclude_candidate"}
			},
			{
				"id": "r-any-false", "name": "Any False", "domain": "scheduling", "enabled": true,
				"when": {
					"any": [
						{"fact": "request.model", "op": "eq", "value": "X"},
						{"fact": "upstream.model", "op": "eq", "value": "Y"}
					]
				},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`
	cfgBool, err := Compile([]byte(jsonBool))
	if err != nil {
		t.Fatal(err)
	}
	ctxBool := &EvalContext{
		Now:           time.Now(),
		RequestModel:  StringFact{Value: "A", State: FactStateMeasured},
		UpstreamModel: StringFact{Value: "B", State: FactStateMeasured},
	}
	sRes := cfgBool.EvalScheduling(ctxBool)
	if !sRes.Excluded || sRes.Reason.RuleID != "r-all-true" {
		t.Fatal("expected r-all-true to exclude")
	}
}
