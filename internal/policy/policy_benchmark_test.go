package policy

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// 本文件是策略引擎纯求值的微基准与夹具语义校验，目标见 RFC_POLICY_ENGINE_TECHNICAL.md 第 9 节。
// 约束：
//   - 只测纯内存求值，不覆盖实时延迟、发布锁、亲和压力、数据库或外部 I/O。
//   - 编译与夹具构造一律在定时器之外完成；基准只测量 Evaluation/Inspect。
//   - 各维度独立测量，不构造规则×候选×窗口的笛卡尔积。

const (
	benchMatchModel = "Astra"
	benchMissModel  = "Luna"
)

// benchNow 返回固定时刻，避免基准依赖真实时钟。
func benchNow() time.Time {
	return time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC) // 周一
}

// benchMeasuredCtx 返回 request.model 命中 benchMatchModel 的已测量上下文。
func benchMeasuredCtx() *EvalContext {
	return &EvalContext{
		Now:          benchNow(),
		RequestModel: StringFact{Value: benchMatchModel, State: FactStateMeasured},
	}
}

func benchMarshal(tb testing.TB, rules ...map[string]any) []byte {
	tb.Helper()
	data, err := json.Marshal(map[string]any{"schema_version": 1, "rules": rules})
	if err != nil {
		tb.Fatalf("marshal benchmark fixture: %v", err)
	}
	return data
}

func benchMustCompile(tb testing.TB, data []byte) *CompiledConfig {
	tb.Helper()
	cfg, err := Compile(data)
	if err != nil {
		tb.Fatalf("compile benchmark fixture: %v", err)
	}
	return cfg
}

func benchModelEq(value string) map[string]any {
	return map[string]any{"fact": "request.model", "op": "eq", "value": value}
}

func benchSchedRule(id string, condition any) map[string]any {
	return map[string]any{
		"id":      id,
		"name":    "bench-" + id,
		"domain":  string(DomainScheduling),
		"enabled": true,
		"when":    condition,
		"then":    map[string]any{"type": string(ActionExcludeCandidate)},
	}
}

func benchPriceRule(id, factor string, condition any) map[string]any {
	return map[string]any{
		"id":      id,
		"name":    "bench-" + id,
		"domain":  string(DomainPricing),
		"enabled": true,
		"when":    condition,
		"then":    map[string]any{"type": string(ActionMultiplyPrice), "factor": factor},
	}
}

// benchSchedRules 构造 n 条 request.model 调度规则。
// matchFirst=true 时仅第一条命中 benchMatchModel，用于测首条排除的最优短路；
// matchFirst=false 时全部比较 benchMissModel，用于测扫描全部规则的最坏情况。
func benchSchedRules(n int, matchFirst bool) []map[string]any {
	rules := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		expected := benchMissModel
		if matchFirst && i == 0 {
			expected = benchMatchModel
		}
		rules = append(rules, benchSchedRule(fmt.Sprintf("s%d", i), benchModelEq(expected)))
	}
	return rules
}

func benchUnknownLeaves(factPrefix string, count int) []any {
	leaves := make([]any, 0, count)
	for i := 0; i < count; i++ {
		leaves = append(leaves, benchModelEq(fmt.Sprintf("%s%d", factPrefix, i)))
	}
	return leaves
}

func benchDisabledRules() []map[string]any {
	rules := make([]map[string]any, 0, MaxRulesPerConfig)
	for i := 0; i < MaxRulesPerConfig; i++ {
		rule := benchSchedRule(fmt.Sprintf("d%d", i), benchModelEq(benchMatchModel))
		rule["enabled"] = false
		rules = append(rules, rule)
	}
	return rules
}

func benchPricingRules(n int) []map[string]any {
	rules := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		rules = append(rules, benchPriceRule(fmt.Sprintf("p%d", i), "1.5", benchModelEq(benchMatchModel)))
	}
	return rules
}

func benchInspectRules(n int) []map[string]any {
	rules := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		rules = append(rules, benchSchedRule(fmt.Sprintf("i%d", i), map[string]any{"all": []any{
			benchModelEq(benchMatchModel),
			benchModelEq(benchMissModel),
		}}))
	}
	return rules
}

func benchQuotaRule() map[string]any {
	return benchSchedRule("q", map[string]any{
		"fact":   "credential.quota.remaining_ratio",
		"select": map[string]any{"scope": "account", "window_seconds": 18000},
		"reduce": "min",
		"op":     "lt",
		"value":  0.1,
	})
}

func benchNestedUnknown() any {
	var nested any = benchModelEq("z")
	for depth := 1; depth < MaxConditionDepth; depth++ {
		nested = map[string]any{"all": []any{nested}}
	}
	return nested
}

// TestPolicyBenchmarkFixturesValid 校验基准夹具可编译且语义正确。
// 它保证基准测量的是合同约定的行为，而不是意外的 unknown/miss。
func TestPolicyBenchmarkFixturesValid(t *testing.T) {
	ctx := benchMeasuredCtx()

	empty := benchMustCompile(t, []byte(`{"schema_version":1,"rules":[]}`))
	if empty.NodeCount() != 0 {
		t.Fatalf("empty config NodeCount = %d, want 0", empty.NodeCount())
	}
	if empty.EvalScheduling(ctx).Excluded {
		t.Fatal("empty config must not exclude")
	}

	disabledCfg := benchMustCompile(t, benchMarshal(t, benchDisabledRules()...))
	if disabledCfg.EvalScheduling(ctx).Excluded {
		t.Fatal("disabled-only config must not exclude")
	}

	firstExclude := benchMustCompile(t, benchMarshal(t, benchSchedRules(100, true)...))
	res := firstExclude.EvalScheduling(ctx)
	if !res.Excluded || res.Reason == nil || res.Reason.RuleID != "s0" {
		t.Fatalf("first-exclude fixture: got %+v, want s0 exclusion", res)
	}

	scanAll := benchMustCompile(t, benchMarshal(t, benchSchedRules(100, false)...))
	if scanAll.EvalScheduling(ctx).Excluded {
		t.Fatal("scan-all fixture must not exclude")
	}

	pricingCfg := benchMustCompile(t, benchMarshal(t, benchPricingRules(100)...))
	if got := len(pricingCfg.EvalPricing(ctx).Matches); got != 100 {
		t.Fatalf("pricing fixture matches = %d, want 100", got)
	}

	unknownCtx := &EvalContext{Now: benchNow(), RequestModel: StringFact{Value: benchMatchModel, State: ""}}
	allUnknown := benchMustCompile(t, benchMarshal(t,
		benchSchedRule("all", map[string]any{"all": benchUnknownLeaves("x", MaxListItems)})))
	anyUnknown := benchMustCompile(t, benchMarshal(t,
		benchSchedRule("any", map[string]any{"any": benchUnknownLeaves("y", MaxListItems)})))

	nestedUnknownCfg := benchMustCompile(t, benchMarshal(t, benchSchedRule("nested", benchNestedUnknown())))

	unknownCases := []struct {
		name string
		cfg  *CompiledConfig
	}{
		{"all_unknown", allUnknown},
		{"any_unknown", anyUnknown},
		{"nested_unknown_depth_16", nestedUnknownCfg},
	}
	for _, tc := range unknownCases {
		if tc.cfg.EvalScheduling(unknownCtx).Excluded {
			t.Fatalf("%s fixture must stay unknown, not exclude", tc.name)
		}
		inspectRes := tc.cfg.Inspect(unknownCtx)
		if len(inspectRes.Rules) != 1 || inspectRes.Rules[0].Status != RuleStatusSkippedUnknown {
			t.Fatalf("%s fixture Inspect = %+v, want single rule with status %s", tc.name, inspectRes.Rules, RuleStatusSkippedUnknown)
		}
	}

	quotaCfg := benchMustCompile(t, benchMarshal(t, benchQuotaRule()))
	quotaCtx := &EvalContext{Now: benchNow(), QuotaWindows: []QuotaWindowFact{
		{Scope: "account", WindowSeconds: 18000, Ratio: 0.5, State: FactStateMeasured},
	}}
	if quotaCfg.EvalScheduling(quotaCtx).Excluded {
		t.Fatal("quota fixture with ratio 0.5 must not satisfy op lt 0.1")
	}

	inspectCfg := benchMustCompile(t, benchMarshal(t,
		benchSchedRule("ins0", map[string]any{"all": []any{benchModelEq(benchMatchModel), benchModelEq(benchMissModel)}}),
		benchSchedRule("ins1", benchModelEq(benchMissModel))))
	if got := len(inspectCfg.Inspect(ctx).Rules); got != 2 {
		t.Fatalf("inspect fixture rules = %d, want 2", got)
	}
	if got := inspectCfg.Inspect(ctx).Rules[0].Status; got != RuleStatusMiss {
		t.Fatalf("inspect fixture rule 0 status = %s, want miss", got)
	}
}

// BenchmarkEvalSchedulingEmptyDisabled 测量无规则与全禁用规则的最快路径。
func BenchmarkEvalSchedulingEmptyDisabled(b *testing.B) {
	ctx := benchMeasuredCtx()

	empty := benchMustCompile(b, []byte(`{"schema_version":1,"rules":[]}`))

	disabledCfg := benchMustCompile(b, benchMarshal(b, benchDisabledRules()...))

	cases := []struct {
		name string
		cfg  *CompiledConfig
	}{
		{"empty", empty},
		{"disabled_100", disabledCfg},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if tc.cfg.EvalScheduling(ctx).Excluded {
					b.Fatal("unexpected exclusion")
				}
			}
		})
	}
}

// BenchmarkEvalSchedulingRuleCount 对比首条排除短路与扫描全部规则。
func BenchmarkEvalSchedulingRuleCount(b *testing.B) {
	ctx := benchMeasuredCtx()
	for _, n := range []int{1, 10, 100} {
		for _, mode := range []string{"first_exclude", "scan_all"} {
			cfg := benchMustCompile(b, benchMarshal(b, benchSchedRules(n, mode == "first_exclude")...))
			expectExcluded := mode == "first_exclude"
			b.Run(fmt.Sprintf("rules=%d/%s", n, mode), func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if cfg.EvalScheduling(ctx).Excluded != expectExcluded {
						b.Fatal("unexpected scheduling result")
					}
				}
			})
		}
	}
}

// BenchmarkEvalPricingAllHits 测量定价域全部命中的有序因子链构建成本。
func BenchmarkEvalPricingAllHits(b *testing.B) {
	ctx := benchMeasuredCtx()
	for _, n := range []int{1, 10, 100} {
		cfg := benchMustCompile(b, benchMarshal(b, benchPricingRules(n)...))
		b.Run(fmt.Sprintf("rules=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if got := len(cfg.EvalPricing(ctx).Matches); got != n {
					b.Fatalf("matches = %d, want %d", got, n)
				}
			}
		})
	}
}

// BenchmarkEvalCandidateCount 测量 RuntimeView 逐候选调度准入的线性成本。
func BenchmarkEvalCandidateCount(b *testing.B) {
	groupRules := benchSchedRules(1, false)
	view, err := CompileRuntimeView([]BindingConfig{
		{Scope: "group", GroupID: 1, Config: benchMarshal(b, groupRules...)},
	})
	if err != nil {
		b.Fatalf("compile runtime view: %v", err)
	}
	ctx := benchMeasuredCtx()
	for _, n := range []int{1, 10, 50} {
		b.Run(fmt.Sprintf("candidates=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				for i := 0; i < n; i++ {
					if excluded, _ := view.EvalCandidate(1, uint(i+1), ctx); excluded {
						b.Fatal("unexpected exclusion")
					}
				}
			}
		})
	}
}

// BenchmarkQuotaWindowScan 测量额度窗口 min 扫描随窗口数的线性成本。
func BenchmarkQuotaWindowScan(b *testing.B) {
	cfg := benchMustCompile(b, benchMarshal(b, benchQuotaRule()))
	for _, n := range []int{1, 4, 16} {
		windows := make([]QuotaWindowFact, 0, n)
		for i := 0; i < n; i++ {
			windows = append(windows, QuotaWindowFact{
				Scope:         "account",
				WindowSeconds: 18000,
				Ratio:         0.5 - float64(i)*0.01,
				State:         FactStateMeasured,
			})
		}
		ctx := &EvalContext{Now: benchNow(), QuotaWindows: windows}
		b.Run(fmt.Sprintf("windows=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if cfg.EvalScheduling(ctx).Excluded {
					b.Fatal("unexpected exclusion")
				}
			}
		})
	}
}

// BenchmarkEvalUnknownWorstCase 测量有界 AND/OR 全 unknown 的最坏遍历（无短路）。
func BenchmarkEvalUnknownWorstCase(b *testing.B) {
	unknownCtx := &EvalContext{Now: benchNow(), RequestModel: StringFact{Value: benchMatchModel, State: ""}}

	allCfg := benchMustCompile(b, benchMarshal(b,
		benchSchedRule("all", map[string]any{"all": benchUnknownLeaves("x", MaxListItems)})))
	anyCfg := benchMustCompile(b, benchMarshal(b,
		benchSchedRule("any", map[string]any{"any": benchUnknownLeaves("y", MaxListItems)})))

	nestedCfg := benchMustCompile(b, benchMarshal(b, benchSchedRule("nested", benchNestedUnknown())))

	cases := []struct {
		name string
		cfg  *CompiledConfig
	}{
		{fmt.Sprintf("all_unknown_%d", MaxListItems), allCfg},
		{fmt.Sprintf("any_unknown_%d", MaxListItems), anyCfg},
		{fmt.Sprintf("nested_unknown_depth_%d", MaxConditionDepth), nestedCfg},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if tc.cfg.EvalScheduling(unknownCtx).Excluded {
					b.Fatal("unknown conditions must not exclude")
				}
			}
		})
	}
}

// BenchmarkInspectRuleCount 测量完整诊断树生成（含节点分配）的规模成本。
func BenchmarkInspectRuleCount(b *testing.B) {
	ctx := benchMeasuredCtx()
	for _, n := range []int{1, 10, 100} {
		cfg := benchMustCompile(b, benchMarshal(b, benchInspectRules(n)...))
		b.Run(fmt.Sprintf("rules=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if got := len(cfg.Inspect(ctx).Rules); got != n {
					b.Fatalf("inspect rules = %d, want %d", got, n)
				}
			}
		})
	}
}

// BenchmarkConcurrentSchedulingReads 测量不可变配置在并发只读下的扩展性。
// EvalScheduling 不修改配置或上下文，因此可安全并行读取。
func BenchmarkConcurrentSchedulingReads(b *testing.B) {
	cfg := benchMustCompile(b, benchMarshal(b, benchSchedRules(10, false)...))
	ctx := benchMeasuredCtx()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if cfg.EvalScheduling(ctx).Excluded {
				b.Error("unexpected exclusion")
				return
			}
		}
	})
}
