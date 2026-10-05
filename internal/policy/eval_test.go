package policy

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"gpt-load/internal/pricing"
)

func TestStringEqualityPatterns(t *testing.T) {
	for _, tc := range []struct {
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
		t.Run(tc.actual+"="+tc.pattern, func(t *testing.T) {
			if got := compareString(tc.actual, "eq", tc.pattern, nil); got != truthOf(tc.match) {
				t.Fatalf("match = %v, want %v", got, tc.match)
			}
		})
	}
	if compareString("gpt-5", "in", "", []string{"gpt-*"}) != TruthFalse {
		t.Fatal("JSON in retains exact set membership")
	}
}

func TestTruthValueLogic(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  TruthValue
		want TruthValue
	}{
		{"NOT True", TruthTrue.Not(), TruthFalse},
		{"NOT False", TruthFalse.Not(), TruthTrue},
		{"NOT Unknown", TruthUnknown.Not(), TruthUnknown},
		{"False AND Unknown", TruthFalse.And(TruthUnknown), TruthFalse},
		{"Unknown AND False", TruthUnknown.And(TruthFalse), TruthFalse},
		{"True AND Unknown", TruthTrue.And(TruthUnknown), TruthUnknown},
		{"True OR Unknown", TruthTrue.Or(TruthUnknown), TruthTrue},
		{"Unknown OR True", TruthUnknown.Or(TruthTrue), TruthTrue},
		{"False OR Unknown", TruthFalse.Or(TruthUnknown), TruthUnknown},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %s, want %s", tc.name, tc.got, tc.want)
		}
	}
}

// TestIndependentModelFacts 验证 request.model 与 upstream.model 是两个互不影响的事实。
func TestIndependentModelFacts(t *testing.T) {
	cfg := compileRules(t,
		schedRule("match-request-smart", modelEq("smart")),
		schedRule("match-upstream-astra", upstreamEq("Astra")),
	)
	ctx := &EvalContext{Now: time.Now(), RequestModel: measuredString("smart"), UpstreamModel: measuredString("Astra")}

	requestOnly := &CompiledConfig{rules: []Rule{cfg.Rules()[0]}}
	if res := requestOnly.EvalScheduling(ctx); !res.Excluded || res.Reason.RuleID != "match-request-smart" {
		t.Fatalf("expected request.model match, got %+v", res)
	}
	mismatch := &EvalContext{Now: time.Now(), RequestModel: measuredString("other"), UpstreamModel: measuredString("Astra")}
	if requestOnly.EvalScheduling(mismatch).Excluded {
		t.Fatal("request.model should not match other")
	}
	upstreamOnly := &CompiledConfig{rules: []Rule{cfg.Rules()[1]}}
	if res := upstreamOnly.EvalScheduling(ctx); !res.Excluded || res.Reason.RuleID != "match-upstream-astra" {
		t.Fatalf("expected upstream.model match, got %+v", res)
	}
}

// TestMissing5hQuotaDoesNotBlock7dORMatch 验证缺 5h 不拦截 7d 已使 OR 为真的规则。
func TestMissing5hQuotaDoesNotBlock7dORMatch(t *testing.T) {
	cfg := compileRules(t, schedRule("protect-quota-or", map[string]any{
		"any": []any{quotaLt(18000, 0.1), quotaLt(604800, 0.1)},
	}))
	ctx := &EvalContext{Now: time.Now(), QuotaWindows: []QuotaWindowFact{
		{Scope: "account", WindowSeconds: 18000, State: FactStateUnknown},
		{Scope: "account", WindowSeconds: 604800, Ratio: 0.05, State: FactStateMeasured},
	}}
	res := cfg.EvalScheduling(ctx)
	if !res.Excluded || res.Reason.RuleID != "protect-quota-or" {
		t.Fatalf("expected 7d match to satisfy OR and exclude candidate, got %+v", res)
	}
}

// TestPricingRulesOrderedMultipliersPreserveAllFactors 验证 ×2、×3、×0.5、×0、×1 全部保留且有序。
func TestPricingRulesOrderedMultipliersPreserveAllFactors(t *testing.T) {
	cfg := compileRules(t,
		priceRule("p1", "2", modelEq("Astra")),
		priceRule("p2", "3", modelEq("Astra")),
		priceRule("p3", "0.5", modelEq("Astra")),
		priceRule("p4", "0", modelEq("Astra")),
		priceRule("p5", "1", modelEq("Astra")),
	)
	want := []struct {
		id, factor string
		mult       pricing.PriceMultiplier
	}{
		{"p1", "2", 2_000_000},
		{"p2", "3", 3_000_000},
		{"p3", "0.5", 500_000},
		{"p4", "0", 0},
		{"p5", "1", 1_000_000},
	}
	res := cfg.EvalPricing(modelCtx("Astra"))
	if len(res.Matches) != len(want) {
		t.Fatalf("expected %d matches, got %d", len(want), len(res.Matches))
	}
	for i, w := range want {
		if m := res.Matches[i]; m.RuleID != w.id || m.Factor != w.factor || m.Multiplier != w.mult {
			t.Fatalf("match %d = %+v, want {id:%q factor:%q mult:%d}", i, m, w.id, w.factor, w.mult)
		}
	}
}

// TestUnknownPricingRuleDoesNotBlockSubsequentMatches 验证未决价格规则不阻断其他命中。
func TestUnknownPricingRuleDoesNotBlockSubsequentMatches(t *testing.T) {
	cfg := compileRules(t,
		priceRule("p-unknown", "2", quotaLt(18000, 0.1)),
		priceRule("p-hit", "1.5", modelEq("Astra")),
	)
	res := cfg.EvalPricing(modelCtx("Astra")) // 无窗口 -> 额度事实 unknown
	if len(res.Matches) != 1 || res.Matches[0].RuleID != "p-hit" || res.Matches[0].Factor != "1.5" {
		t.Fatalf("expected only p-hit to match, got %+v", res.Matches)
	}
}

// TestDisabledRuleDoesNotExclude 验证禁用规则不参与调度排除。
func TestDisabledRuleDoesNotExclude(t *testing.T) {
	disabled := schedRule("r-disabled", modelEq("Astra"))
	disabled["enabled"] = false
	cfg := compileRules(t, disabled)
	if cfg.EvalScheduling(modelCtx("Astra")).Excluded {
		t.Fatal("disabled rule must not exclude candidates")
	}
}

func TestConcurrentReadOnlyEvaluationSafe(t *testing.T) {
	cfg := compileRules(t,
		schedRule("r-sched", modelEq("Astra")),
		priceRule("r-price", "1.5", upstreamEq("Luna")),
	)
	ctx := &EvalContext{Now: time.Now(), RequestModel: measuredString("Astra"), UpstreamModel: measuredString("Luna")}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if !cfg.EvalScheduling(ctx).Excluded {
				t.Errorf("routine %d expected excluded", idx)
			}
			if len(cfg.EvalPricing(ctx).Matches) != 1 {
				t.Errorf("routine %d expected 1 match", idx)
			}
		}(i)
	}
	wg.Wait()
}

func TestEvalConditionEdgeCases(t *testing.T) {
	// min reducer：两个有效窗口 0.3/0.15，应取 0.15 命中 lte 0.2。
	minCfg := compileRules(t, schedRule("r-min", map[string]any{
		"fact":   "credential.quota.remaining_ratio",
		"select": map[string]any{"scope": "account", "window_seconds": 18000},
		"reduce": "min", "op": "lte", "value": 0.2,
	}))
	minCtx := &EvalContext{Now: time.Now(), QuotaWindows: []QuotaWindowFact{
		{Scope: "account", WindowSeconds: 18000, Ratio: 0.3, State: FactStateMeasured},
		{Scope: "account", WindowSeconds: 18000, Ratio: 0.15, State: FactStateMeasured},
	}}
	if !minCfg.EvalScheduling(minCtx).Excluded {
		t.Fatal("expected min reducer to pick 0.15 and exclude candidate")
	}

	// all 全 true 命中；any 全 false 不命中。
	boolCfg := compileRules(t,
		schedRule("r-all-true", map[string]any{"all": []any{modelEq("A"), upstreamEq("B")}}),
		schedRule("r-any-false", map[string]any{"any": []any{modelEq("X"), upstreamEq("Y")}}),
	)
	res := boolCfg.EvalScheduling(&EvalContext{Now: time.Now(), RequestModel: measuredString("A"), UpstreamModel: measuredString("B")})
	if !res.Excluded || res.Reason.RuleID != "r-all-true" {
		t.Fatalf("expected r-all-true to exclude, got %+v", res)
	}
}

// TestQuotaNowSeparation 验证 QuotaNow 与 Now 隔离：
// 模拟时间前进不能复活过期窗口，模拟时间倒退也不改变按真实时间判定的额度有效性。
func TestQuotaNowSeparation(t *testing.T) {
	cfg := compileRules(t, schedRule("quota-rule", map[string]any{"all": []any{
		map[string]any{
			"fact":   "credential.quota.remaining_ratio",
			"select": map[string]any{"scope": "account", "window_seconds": 3600},
			"reduce": "min", "op": "lt", "value": 0.2,
		},
		map[string]any{"predicate": "time_window", "weekdays": []any{1}, "ranges": []any{[]any{"10:00", "12:00"}}},
	}}))

	// 2026-10-05 是周一：真实时间 09:00，重置时间 11:00，观测时间 08:55。
	loc := time.UTC
	quotaWindows := []QuotaWindowFact{{
		Scope: "account", WindowSeconds: 3600, Ratio: 0.05, State: FactStateMeasured,
		ObservedAt: time.Date(2026, 10, 5, 8, 55, 0, 0, loc),
		ResetAt:    time.Date(2026, 10, 5, 11, 0, 0, 0, loc),
	}}

	// Case 1：模拟时间 11:30 落在时间段内，QuotaNow 09:00 时额度仍有效 -> 命中排除。
	ctxFuture := &EvalContext{
		Now:          time.Date(2026, 10, 5, 11, 30, 0, 0, loc),
		QuotaNow:     time.Date(2026, 10, 5, 9, 0, 0, 0, loc),
		QuotaWindows: quotaWindows,
	}
	if !cfg.EvalScheduling(ctxFuture).Excluded {
		t.Fatal("expected excluded when QuotaNow=09:00 (valid quota) and Now=11:30 (in window)")
	}

	// Case 2：真实时间 12:00 已过重置时间 -> 额度 unknown，模拟回 10:30 也不能复活。
	ctxPast := &EvalContext{
		Now:          time.Date(2026, 10, 5, 10, 30, 0, 0, loc),
		QuotaNow:     time.Date(2026, 10, 5, 12, 0, 0, 0, loc),
		QuotaWindows: quotaWindows,
	}
	if cfg.EvalScheduling(ctxPast).Excluded {
		t.Fatal("expected NOT excluded when real quota is expired, simulated past time must not resurrect expired window")
	}
}

// TestQuotaConflictCases 验证同源同窗冲突成员退化为 unknown，不同来源/采样/周期按 min 取真值。
func TestQuotaConflictCases(t *testing.T) {
	cfg := compileRules(t, schedRule("quota-conflict", quotaLt(18000, 0.1)))

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
			if got := evalConditionFast(cfg.rules[0].When, ctx); got != tc.want {
				t.Fatalf("condition truth = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestNumberComparisonsAndQuotaStates(t *testing.T) {
	for _, tc := range []struct {
		op       string
		actual   float64
		expected float64
		want     TruthValue
	}{
		{"eq", 0.5, 0.5, TruthTrue},
		{"eq", 0.5, 0.6, TruthFalse},
		{"lt", 0.4, 0.5, TruthTrue},
		{"lt", 0.5, 0.5, TruthFalse},
		{"lte", 0.5, 0.5, TruthTrue},
		{"lte", 0.6, 0.5, TruthFalse},
		{"gt", 0.6, 0.5, TruthTrue},
		{"gt", 0.5, 0.5, TruthFalse},
		{"gte", 0.5, 0.5, TruthTrue},
		{"gte", 0.4, 0.5, TruthFalse},
		{"unknown_op", 0.5, 0.5, TruthUnknown},
	} {
		if got := compareNumber(tc.actual, tc.op, tc.expected); got != tc.want {
			t.Fatalf("compareNumber(%v, %q, %v) = %v, want %v", tc.actual, tc.op, tc.expected, got, tc.want)
		}
	}

	cfg := compileRules(t, schedRule("r-quota", quotaLt(18000, 0.2)))

	ctx1 := &EvalContext{Now: time.Now(), QuotaWindows: []QuotaWindowFact{
		{Scope: "account", WindowSeconds: 18000, Ratio: 0.15, State: FactStateRetainedInPeriod},
	}}
	if !cfg.EvalScheduling(ctx1).Excluded {
		t.Fatal("expected retained_in_period 0.15 < 0.2 to exclude")
	}

	ctx2 := &EvalContext{Now: time.Now(), QuotaWindows: []QuotaWindowFact{
		{Scope: "account", WindowSeconds: 18000, Ratio: 1.0, State: FactStateInferredReset},
	}}
	if cfg.EvalScheduling(ctx2).Excluded {
		t.Fatal("expected inferred_reset 1.0 < 0.2 not to exclude")
	}
}

func TestQuotaRatioFloatThresholds(t *testing.T) {
	ratioCtx := func(ratio float64) *EvalContext {
		return &EvalContext{Now: time.Now(), QuotaWindows: []QuotaWindowFact{
			{Scope: "account", WindowSeconds: 18000, Ratio: ratio, State: FactStateMeasured},
		}}
	}
	for _, raw := range []string{"0.1", "1e-1", "0.10"} {
		cfg := compileRules(t, schedRule("r-eq", map[string]any{
			"fact":   "credential.quota.remaining_ratio",
			"select": map[string]any{"scope": "account", "window_seconds": 18000},
			"reduce": "min", "op": "eq", "value": json.RawMessage(raw),
		}))
		if !cfg.EvalScheduling(ratioCtx(0.1)).Excluded {
			t.Fatalf("value=%s should equal ratio 0.1", raw)
		}
	}
}
