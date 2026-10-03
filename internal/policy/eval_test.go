package policy

import (
	"sync"
	"testing"
	"time"

	"gpt-load/internal/pricing"
)

func TestStringEqualityPatterns(t *testing.T) {
	for _, test := range []struct {
		actual, pattern string
		match           bool
	}{
		{"gpt-5", "gpt-5", true},
		{"gpt-5-mini", "gpt-5", false},
		{"gpt-5-mini", "gpt-*", true},
		{"provider/gpt-5", "*gpt-5", true},
		{"gpt-5-mini", "gpt-*-mini", true},
		{"gpt-5-mini", "gpt-*-pro", false},
		{"gpt-5", "gpt-**", true},
		{"", "*", true},
		{"模型/旗舰", "模型/*", true},
		{"gpt-5", "gpt-?", false},
		{"gpt-[5]", "gpt-[5]", true},
	} {
		t.Run(test.actual+"="+test.pattern, func(t *testing.T) {
			if got := compareString(test.actual, "eq", test.pattern, nil); got != truthOf(test.match) {
				t.Fatalf("match = %v, want %v", got, test.match)
			}
		})
	}
	if compareString("gpt-5", "in", "", []string{"gpt-*"}) != TruthFalse {
		t.Fatal("JSON in retains exact set membership")
	}
}

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

func TestQuotaNowSeparation(t *testing.T) {
	// 验证 QuotaNow 与 Now 隔离：
	// 1. 模拟时间 Now 前进到 ResetAt 之后，但 QuotaNow 仍在 ResetAt 之前：额度判断按真实 QuotaNow 保持有效
	// 2. 模拟时间 Now 倒退到 ResetAt 之前，但 QuotaNow 已超过 ResetAt：额度判断按真实 QuotaNow 判定过期(unknown)
	ruleJSON := `{
		"schema_version": 1,
		"rules": [
			{
				"id": "quota-rule",
				"name": "Quota Rule",
				"domain": "scheduling",
				"enabled": true,
				"when": {
					"all": [
						{
							"fact": "credential.quota.remaining_ratio",
							"select": {"scope": "account", "window_seconds": 3600},
							"reduce": "min",
							"op": "lt",
							"value": 0.2
						},
						{
							"predicate": "time_window",
							"weekdays": [1],
							"ranges": [["10:00", "12:00"]]
						}
					]
				},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`

	cfg, err := Compile([]byte(ruleJSON))
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}

	// 真实当前时间为周一 09:00:00 (尚未进入 10:00-12:00 时间段)
	// 重置时间为周一 11:00:00
	loc := time.UTC
	realNow := time.Date(2026, 10, 5, 9, 0, 0, 0, loc) // 2026-10-05 是周一
	resetAt := time.Date(2026, 10, 5, 11, 0, 0, 0, loc)
	observedAt := time.Date(2026, 10, 5, 8, 55, 0, 0, loc)

	quotaWindows := []QuotaWindowFact{
		{
			Scope:         "account",
			WindowSeconds: 3600,
			Ratio:         0.05,
			State:         FactStateMeasured,
			ObservedAt:    observedAt,
			ResetAt:       resetAt,
		},
	}

	// Case 1: 模拟时间前进至 11:30:00 (在时间段内，但已过 ResetAt 11:00)
	// 若 QuotaNow = realNow (09:00:00)，额度尚未过期且有效；时间段命中 11:30，整条命中排除！
	simFuture := time.Date(2026, 10, 5, 11, 30, 0, 0, loc)
	ctxFuture := &EvalContext{
		Now:          simFuture,
		QuotaNow:     realNow,
		QuotaWindows: quotaWindows,
	}
	resFuture := cfg.EvalScheduling(ctxFuture)
	if !resFuture.Excluded {
		t.Fatalf("expected excluded when QuotaNow=09:00 (valid quota) and Now=11:30 (in window)")
	}

	// Case 2: 真实时间已过重置时间 (12:00:00)，但用户试图模拟回 10:30:00
	// QuotaNow = 12:00:00 (已过 ResetAt 11:00:00 -> 额度为 unknown)
	// 无论模拟时间如何，额度条件均为 unknown，整条规则跳过，不能利用模拟时间复活过期窗口！
	expiredRealNow := time.Date(2026, 10, 5, 12, 0, 0, 0, loc)
	simPast := time.Date(2026, 10, 5, 10, 30, 0, 0, loc)
	ctxPast := &EvalContext{
		Now:          simPast,
		QuotaNow:     expiredRealNow,
		QuotaWindows: quotaWindows,
	}
	resPast := cfg.EvalScheduling(ctxPast)
	if resPast.Excluded {
		t.Fatalf("expected NOT excluded when real quota is expired, simulated past time must not resurrect expired window")
	}

	inspectPast := cfg.Inspect(ctxPast)
	if len(inspectPast.Rules) != 1 || inspectPast.Rules[0].Status != RuleStatusSkippedUnknown {
		t.Fatalf("expected rule to be skipped_unknown, got %+v", inspectPast.Rules)
	}
}

func TestQuotaConflictCases(t *testing.T) {
	cfg, err := Compile([]byte(`{"schema_version":1,"rules":[{
		"id":"quota-conflict","name":"Quota Conflict","domain":"scheduling","enabled":true,
		"when":{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},"reduce":"min","op":"lt","value":0.1},
		"then":{"type":"exclude_candidate"}}]}`))
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	base := QuotaWindowFact{Scope: "account", WindowSeconds: 18000, Ratio: 0.05,
		State: FactStateMeasured, ObservedAt: now.Add(-5 * time.Minute), ResetAt: now.Add(5 * time.Hour),
		SourceID: "codex", IdentityGeneration: 1}
	for _, tc := range []struct {
		name           string
		firstRatio     float64
		secondRatio    float64
		secondSource   string
		secondObserved time.Time
		secondWindow   int
		single         bool
		want           TruthValue
	}{
		{"same_sample_boundary", 0.099, 0.101, "codex", base.ObservedAt, 18000, false, TruthUnknown},
		{"different_sources", 0.20, 0.05, "probe", base.ObservedAt, 18000, false, TruthTrue},
		{"different_sample_timing", 0.30, 0.05, "codex", now.Add(-30 * time.Minute), 18000, false, TruthTrue},
		{"different_period", 0.05, 0.99, "codex", base.ObservedAt, 604800, false, TruthTrue},
		{"blank_source_single", 0.05, 0.05, "", base.ObservedAt, 18000, true, TruthTrue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, second := base, base
			first.Ratio = tc.firstRatio
			second.Ratio = tc.secondRatio
			second.SourceID = tc.secondSource
			second.ObservedAt = tc.secondObserved
			second.WindowSeconds = tc.secondWindow
			windows := []QuotaWindowFact{first, second}
			if tc.single {
				first.SourceID = ""
				windows = []QuotaWindowFact{first}
			}
			ctx := &EvalContext{Now: now, QuotaWindows: windows}
			if got := cfg.EvalScheduling(ctx).Excluded; got != (tc.want == TruthTrue) {
				t.Fatalf("scheduling excluded=%v, want truth=%s", got, tc.want)
			}
			wantStatus := RuleStatusHit
			if tc.want == TruthUnknown {
				wantStatus = RuleStatusSkippedUnknown
			}
			diag := cfg.Inspect(ctx).Rules[0]
			if diag.Condition.Truth != tc.want || diag.Status != wantStatus {
				t.Fatalf("inspect=%+v, want truth=%s status=%s", diag, tc.want, wantStatus)
			}
		})
	}
}
