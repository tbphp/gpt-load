package policy_test

import (
	"encoding/json"
	"math"
	"testing"

	"gpt-load/internal/policy"
)

func reviewConfig(t testing.TB, condition any) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"rules": []any{map[string]any{
			"id": "review", "name": "Review", "domain": "scheduling", "enabled": true,
			"when": condition, "then": map[string]any{"type": "exclude_candidate"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func reviewConfigJSON(condition string) []byte {
	return []byte(`{"schema_version":1,"rules":[{"id":"review","name":"Review","domain":"scheduling","enabled":true,"when":` + condition + `,"then":{"type":"exclude_candidate"}}]}`)
}

func TestReviewRulesDoNotExposeCompiledConditions(t *testing.T) {
	cfg, err := policy.Compile(reviewConfig(t, map[string]any{"fact": "request.model", "op": "eq", "value": "Astra"}))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Rules()[0].When.StrValue = "Luna"
	ctx := &policy.EvalContext{RequestModel: policy.StringFact{Value: "Astra", State: policy.FactStateMeasured}}
	if !cfg.EvalScheduling(ctx).Excluded {
		t.Fatal("mutating a returned rule changed the compiled configuration")
	}
}

func TestReviewConditionDepthBoundary(t *testing.T) {
	var condition any = map[string]any{"fact": "request.model", "op": "eq", "value": "Astra"}
	for depth := 1; depth < policy.MaxConditionDepth; depth++ {
		condition = map[string]any{"all": []any{condition}}
	}
	if _, err := policy.Compile(reviewConfig(t, condition)); err != nil {
		t.Fatalf("a valid tree at the advertised depth limit was rejected: %v", err)
	}
	condition = map[string]any{"all": []any{condition}}
	if _, err := policy.Compile(reviewConfig(t, condition)); err == nil {
		t.Fatal("tree beyond the depth limit was accepted")
	}
}

func TestReviewTimeRequiresDecimalDigits(t *testing.T) {
	for _, start := range []string{"+1:00", "01:+2"} {
		t.Run(start, func(t *testing.T) {
			condition := map[string]any{"predicate": "time_window", "weekdays": []int{1}, "ranges": [][]string{{start, "03:00"}}}
			if _, err := policy.Compile(reviewConfig(t, condition)); err == nil {
				t.Fatal("time with a sign was accepted as HH:MM")
			}
		})
	}
}

func TestReviewInvalidQuotaMembersStayUnknown(t *testing.T) {
	condition := map[string]any{
		"fact": "credential.quota.remaining_ratio", "op": "lt", "value": 0.1,
		"select": map[string]any{"scope": "account", "window_seconds": 18000}, "reduce": "min",
	}
	cfg, err := policy.Compile(reviewConfig(t, condition))
	if err != nil {
		t.Fatal(err)
	}
	for _, ratio := range []float64{math.NaN(), math.Inf(1), -0.1, 1.1} {
		ctx := &policy.EvalContext{QuotaWindows: []policy.QuotaWindowFact{
			{Scope: "account", WindowSeconds: 18000, Ratio: 0.05, State: policy.FactStateMeasured},
			{Scope: "account", WindowSeconds: 18000, Ratio: ratio, State: policy.FactStateMeasured},
		}}
		if got := cfg.Inspect(ctx).Rules[0].Status; got != policy.RuleStatusSkippedUnknown {
			t.Errorf("invalid member %v produced %s instead of unknown", ratio, got)
		}
	}
}

func TestReviewQuotaSelectionDoesNotHideUnknownMembers(t *testing.T) {
	condition := map[string]any{
		"fact": "credential.quota.remaining_ratio", "op": "lt", "value": 0.1,
		"select": map[string]any{"scope": "account", "window_seconds": 18000}, "reduce": "min",
	}
	cfg, err := policy.Compile(reviewConfig(t, condition))
	if err != nil {
		t.Fatal(err)
	}
	ctx := &policy.EvalContext{QuotaWindows: []policy.QuotaWindowFact{
		{Scope: "account", WindowSeconds: 18000, Ratio: 0.05, State: policy.FactStateMeasured},
		{Scope: "account", WindowSeconds: 18000, Ratio: 0.01, State: policy.FactStateUnknown},
	}}
	if got := cfg.Inspect(ctx).Rules[0].Status; got != policy.RuleStatusSkippedUnknown {
		t.Fatalf("mixed effective and unknown quota members produced %s", got)
	}
}

func TestReviewNilCloneIsSafe(t *testing.T) {
	var cfg *policy.CompiledConfig
	if cfg.Clone() == nil {
		t.Fatal("nil Clone should return an empty configuration")
	}
}

func TestReviewNilRegistryIsRejected(t *testing.T) {
	if _, err := policy.CompileWithRegistry([]byte(`{"schema_version":1,"rules":[]}`), nil); err == nil {
		t.Fatal("nil registry should be rejected instead of panicking")
	}
}

func TestReviewQuotaThresholdUsesJSONNumberRange(t *testing.T) {
	for _, raw := range []string{"1.0000000000000001", "-0.0000000000000001"} {
		t.Run(raw, func(t *testing.T) {
			condition := `{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},"reduce":"min","op":"lt","value":` + raw + `}`
			if _, err := policy.Compile(reviewConfigJSON(condition)); err == nil {
				t.Fatal("out-of-range JSON number was accepted after float64 rounding")
			}
		})
	}
}

func TestReviewQuotaThresholdDoesNotRoundIntoOne(t *testing.T) {
	condition := `{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},"reduce":"min","op":"eq","value":0.99999999999999999}`
	cfg, err := policy.Compile(reviewConfigJSON(condition))
	if err != nil {
		t.Fatal(err)
	}
	ctx := &policy.EvalContext{QuotaWindows: []policy.QuotaWindowFact{
		{Scope: "account", WindowSeconds: 18000, Ratio: 1, State: policy.FactStateMeasured},
	}}
	if cfg.EvalScheduling(ctx).Excluded {
		t.Fatal("threshold below one was rounded to one and matched a ratio of one")
	}
}

func TestReviewQuotaThresholdEquivalentDecimalSyntax(t *testing.T) {
	condition := `{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},"reduce":"min","op":"eq","value":0.10}`
	cfg, err := policy.Compile(reviewConfigJSON(condition))
	if err != nil {
		t.Fatal(err)
	}
	ctx := &policy.EvalContext{QuotaWindows: []policy.QuotaWindowFact{
		{Scope: "account", WindowSeconds: 18000, Ratio: 0.1, State: policy.FactStateMeasured},
	}}
	if !cfg.EvalScheduling(ctx).Excluded {
		t.Fatal("equivalent decimal syntax changed the quota threshold")
	}
}

func BenchmarkReviewScheduling(b *testing.B) {
	condition := map[string]any{"all": []any{
		map[string]any{"fact": "request.model", "op": "eq", "value": "smart"},
		map[string]any{"fact": "upstream.model", "op": "eq", "value": "Astra"},
	}}
	cfg, err := policy.Compile(reviewConfig(b, condition))
	if err != nil {
		b.Fatal(err)
	}
	ctx := &policy.EvalContext{
		RequestModel:  policy.StringFact{Value: "smart", State: policy.FactStateMeasured},
		UpstreamModel: policy.StringFact{Value: "Luna", State: policy.FactStateMeasured},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if cfg.EvalScheduling(ctx).Excluded {
			b.Fatal("unexpected exclusion")
		}
	}
}
