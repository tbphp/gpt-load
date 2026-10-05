package policy

import (
	"math"
	"testing"
)

func TestReviewConditionDepthBoundary(t *testing.T) {
	var condition any = modelEq("Astra")
	for depth := 1; depth < MaxConditionDepth; depth++ {
		condition = map[string]any{"all": []any{condition}}
	}
	if _, err := Compile(configJSON(t, schedRule("review", condition))); err != nil {
		t.Fatalf("a valid tree at the advertised depth limit was rejected: %v", err)
	}
	condition = map[string]any{"all": []any{condition}}
	if _, err := Compile(configJSON(t, schedRule("review", condition))); err == nil {
		t.Fatal("tree beyond the depth limit was accepted")
	}
}

func TestReviewQuotaMembersStayUnknown(t *testing.T) {
	cfg, err := Compile(configJSON(t, schedRule("review", quotaLt(18000, 0.1))))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		ratio float64
		state FactState
	}{
		{"NaN", math.NaN(), FactStateMeasured},
		{"+Inf", math.Inf(1), FactStateMeasured},
		{"negative", -0.1, FactStateMeasured},
		{">1", 1.1, FactStateMeasured},
		{"unknown state", 0.01, FactStateUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &EvalContext{QuotaWindows: []QuotaWindowFact{
				{Scope: "account", WindowSeconds: 18000, Ratio: 0.05, State: FactStateMeasured},
				{Scope: "account", WindowSeconds: 18000, Ratio: tc.ratio, State: tc.state},
			}}
			if got := evalConditionFast(cfg.rules[0].When, ctx); got != TruthUnknown {
				t.Errorf("%s member produced %s instead of unknown", tc.name, got)
			}
		})
	}
}
