package policy

import (
	"testing"

)

// TestModelExclusionDuality 验证「when 过滤」与「exclude_models 动作」两种形式在
// 额度与模型事实组合下给出完全一致的排除结论。
func TestModelExclusionDuality(t *testing.T) {
	lowQuota := map[string]any{"any": []any{quotaLt(18000, 0.4), quotaLt(604800, 0.08)}}
	targetModel := map[string]any{"fact": "request.model", "op": "in", "value": []any{"gpt-6.1-sol"}}

	cfgA, err := Compile(configJSON(t, schedRule("rule-a", map[string]any{"all": []any{lowQuota, targetModel}})))
	if err != nil {
		t.Fatalf("failed to compile config A: %v", err)
	}
	excludeModels := schedRule("rule-b", lowQuota)
	excludeModels["actions"] = []any{map[string]any{"type": string(ActionExcludeModels), "models": []any{"gpt-6.1-sol"}}}
	cfgB, err := Compile(configJSON(t, excludeModels))
	if err != nil {
		t.Fatalf("failed to compile config B: %v", err)
	}

	for _, tc := range []struct {
		name        string
		reqModel    string
		reqState    FactState
		r5h, r7d    float64
		wantExclude bool
	}{
		{"5h low, target model -> exclude", "gpt-6.1-sol", FactStateMeasured, 0.35, 0.50, true},
		{"7d low, target model -> exclude", "gpt-6.1-sol", FactStateMeasured, 0.80, 0.05, true},
		{"both low, target model -> exclude", "gpt-6.1-sol", FactStateMeasured, 0.20, 0.02, true},
		{"5h boundary exact 0.4 (lt is false), target model -> pass", "gpt-6.1-sol", FactStateMeasured, 0.40, 0.50, false},
		{"both healthy, target model -> pass", "gpt-6.1-sol", FactStateMeasured, 0.50, 0.10, false},
		{"5h low, non-target model -> pass", "gpt-4o-mini", FactStateMeasured, 0.35, 0.50, false},
		{"5h low, model fact unknown -> pass (effective check)", "gpt-6.1-sol", FactStateUnknown, 0.35, 0.50, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &EvalContext{
				RequestModel: StringFact{Value: tc.reqModel, State: tc.reqState},
				QuotaWindows: []QuotaWindowFact{
					{Scope: "account", WindowSeconds: 18000, Ratio: tc.r5h, State: FactStateMeasured},
					{Scope: "account", WindowSeconds: 604800, Ratio: tc.r7d, State: FactStateMeasured},
				},
			}
			resA, resB := cfgA.EvalScheduling(ctx), cfgB.EvalScheduling(ctx)
			if resA.Excluded != tc.wantExclude {
				t.Errorf("Config A Excluded = %v, want %v", resA.Excluded, tc.wantExclude)
			}
			if resB.Excluded != tc.wantExclude {
				t.Errorf("Config B Excluded = %v, want %v", resB.Excluded, tc.wantExclude)
			}
			if resA.Excluded != resB.Excluded {
				t.Fatalf("Duality mismatch: resA=%v, resB=%v", resA.Excluded, resB.Excluded)
			}
			if resB.Excluded {
				if resB.Reason.ActionType != ActionExcludeModels {
					t.Errorf("expected ActionType %s, got %s", ActionExcludeModels, resB.Reason.ActionType)
				}
				if resB.Reason.ExcludedModel != tc.reqModel {
					t.Errorf("expected ExcludedModel %s, got %s", tc.reqModel, resB.Reason.ExcludedModel)
				}
			}
		})
	}
}
