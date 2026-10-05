package policy

import (
	"fmt"
	"testing"
	"time"
)

// 本文件是策略引擎纯求值的微基准，目标见 RFC_POLICY_ENGINE_TECHNICAL.md 第 9 节。
// 约束：
//   - 只测纯内存求值，不覆盖实时延迟、发布锁、亲和压力、数据库或外部 I/O。
//   - 编译与夹具构造一律在定时器之外完成；基准只测量 Evaluation。
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
		rules = append(rules, schedRule(fmt.Sprintf("s%d", i), modelEq(expected)))
	}
	return rules
}

func benchUnknownLeaves(factPrefix string, count int) []any {
	leaves := make([]any, 0, count)
	for i := 0; i < count; i++ {
		leaves = append(leaves, modelEq(fmt.Sprintf("%s%d", factPrefix, i)))
	}
	return leaves
}

func benchDisabledRules() []map[string]any {
	rules := make([]map[string]any, 0, MaxRulesPerConfig)
	for i := 0; i < MaxRulesPerConfig; i++ {
		rule := schedRule(fmt.Sprintf("d%d", i), modelEq(benchMatchModel))
		rule["enabled"] = false
		rules = append(rules, rule)
	}
	return rules
}

func benchPricingRules(n int) []map[string]any {
	rules := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		rules = append(rules, priceRule(fmt.Sprintf("p%d", i), "1.5", modelEq(benchMatchModel)))
	}
	return rules
}

func benchNestedUnknown() any {
	var nested any = modelEq("z")
	for depth := 1; depth < MaxConditionDepth; depth++ {
		nested = map[string]any{"all": []any{nested}}
	}
	return nested
}

// BenchmarkEvalSchedulingEmptyDisabled 测量无规则与全禁用规则的最快路径。
func BenchmarkEvalSchedulingEmptyDisabled(b *testing.B) {
	ctx := benchMeasuredCtx()
	cases := []struct {
		name string
		cfg  *CompiledConfig
	}{
		{"empty", compileRules(b)},
		{"disabled_100", compileRules(b, benchDisabledRules()...)},
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
			cfg := compileRules(b, benchSchedRules(n, mode == "first_exclude")...)
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
		cfg := compileRules(b, benchPricingRules(n)...)
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
	view, err := CompileRuntimeView([]BindingConfig{
		{Scope: "group", GroupID: 1, Config: configJSON(b, benchSchedRules(1, false)...)},
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

// BenchmarkQuotaWindowScan 测量额度窗口 min 扫描随窗口数的线性成本及 unknown/未命中路径。
func BenchmarkQuotaWindowScan(b *testing.B) {
	cfg := compileRules(b, schedRule("q", quotaLt(18000, 0.1)))
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

	b.Run("no_match", func(b *testing.B) {
		ctx := &EvalContext{Now: benchNow(), QuotaWindows: nil}
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			if cfg.EvalScheduling(ctx).Excluded {
				b.Fatal("unexpected exclusion")
			}
		}
	})

	b.Run("unknown_state", func(b *testing.B) {
		ctx := &EvalContext{
			Now: benchNow(),
			QuotaWindows: []QuotaWindowFact{{
				Scope: "account", WindowSeconds: 18000, Ratio: 0.5, State: FactStateUnknown,
			}},
		}
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			if cfg.EvalScheduling(ctx).Excluded {
				b.Fatal("unexpected exclusion")
			}
		}
	})

	b.Run("expired_reset", func(b *testing.B) {
		ctx := &EvalContext{
			Now: benchNow(),
			QuotaWindows: []QuotaWindowFact{{
				Scope: "account", WindowSeconds: 18000, Ratio: 0.05, State: FactStateMeasured,
				ResetAt: benchNow().Add(-time.Hour), ObservedAt: benchNow().Add(-2 * time.Hour),
			}},
		}
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			if cfg.EvalScheduling(ctx).Excluded {
				b.Fatal("unexpected exclusion")
			}
		}
	})
}

// BenchmarkEvalUnknownWorstCase 测量有界 AND/OR 全 unknown 的最坏遍历（无短路）。
func BenchmarkEvalUnknownWorstCase(b *testing.B) {
	unknownCtx := &EvalContext{Now: benchNow(), RequestModel: StringFact{Value: benchMatchModel, State: ""}}
	allCfg := compileRules(b, schedRule("all", map[string]any{"all": benchUnknownLeaves("x", MaxListItems)}))
	anyCfg := compileRules(b, schedRule("any", map[string]any{"any": benchUnknownLeaves("y", MaxListItems)}))
	nestedCfg := compileRules(b, schedRule("nested", benchNestedUnknown()))

	for _, tc := range []struct {
		name string
		cfg  *CompiledConfig
	}{
		{fmt.Sprintf("all_unknown_%d", MaxListItems), allCfg},
		{fmt.Sprintf("any_unknown_%d", MaxListItems), anyCfg},
		{fmt.Sprintf("nested_unknown_depth_%d", MaxConditionDepth), nestedCfg},
	} {
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

// BenchmarkConcurrentSchedulingReads 测量不可变配置在并发只读下的扩展性。
// EvalScheduling 不修改配置或上下文，因此可安全并行读取。
func BenchmarkConcurrentSchedulingReads(b *testing.B) {
	cfg := compileRules(b, benchSchedRules(10, false)...)
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
