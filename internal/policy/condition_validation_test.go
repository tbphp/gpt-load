package policy

import (
	"fmt"
	"testing"
	"time"
)

func TestCompileConditionFormValidation(t *testing.T) {
	ruleWrap := func(when string) string {
		return fmt.Sprintf(`{
			"schema_version": 1,
			"rules": [{
				"id": "r1", "name": "N", "domain": "scheduling", "enabled": true,
				"when": %s,
				"then": {"type": "exclude_candidate"}
			}]
		}`, when)
	}

	cases := []struct {
		name string
		when string
	}{
		{"empty node object", `{}`},
		{"mixed all and any", `{"all": [{"fact": "request.model", "op": "eq", "value": "A"}], "any": [{"fact": "request.model", "op": "eq", "value": "B"}]}`},
		{"mixed fact and not", `{"fact": "request.model", "op": "eq", "value": "A", "not": {"fact": "request.model", "op": "eq", "value": "B"}}`},
		{"all empty array", `{"all": []}`},
		{"any empty array", `{"any": []}`},
		{"not not an object", `{"not": []}`},
		{"all contains unknown field", `{"all": [{"fact": "request.model", "op": "eq", "value": "A"}], "extra": 1}`},
		{"any contains unknown field", `{"any": [{"fact": "request.model", "op": "eq", "value": "A"}], "extra": 1}`},
		{"not contains unknown field", `{"not": {"fact": "request.model", "op": "eq", "value": "A"}, "extra": 1}`},
		{"unknown param key", `{"fact": "non_existent_key", "op": "eq", "value": "A"}`},
		{"unsupported operator", `{"fact": "request.model", "op": "gt", "value": "A"}`},
		{"fact missing op", `{"fact": "request.model", "value": "A"}`},
		{"fact missing value", `{"fact": "request.model", "op": "eq"}`},
		{"fact string in empty array", `{"fact": "request.model", "op": "in", "value": []}`},
		{"fact string in duplicate values", `{"fact": "request.model", "op": "in", "value": ["A", "A"]}`},
		{"fact string in whitespace value", `{"fact": "request.model", "op": "in", "value": [" A "]}`},
		{"fact disallows select", `{"fact": "request.model", "op": "eq", "value": "A", "select": {"scope": "account"}}`},
		{"fact disallows reduce", `{"fact": "request.model", "op": "eq", "value": "A", "reduce": "min"}`},
		{"fact contains unknown field", `{"fact": "request.model", "op": "eq", "value": "A", "extra": 1}`},
		{"quota missing select", `{"fact": "credential.quota.remaining_ratio", "reduce": "min", "op": "lt", "value": 0.1}`},
		{"quota missing reduce", `{"fact": "credential.quota.remaining_ratio", "select": {"scope": "account", "window_seconds": 18000}, "op": "lt", "value": 0.1}`},
		{"quota wrong scope", `{"fact": "credential.quota.remaining_ratio", "select": {"scope": "user", "window_seconds": 18000}, "reduce": "min", "op": "lt", "value": 0.1}`},
		{"quota invalid window_seconds", `{"fact": "credential.quota.remaining_ratio", "select": {"scope": "account", "window_seconds": 0}, "reduce": "min", "op": "lt", "value": 0.1}`},
		{"quota unsupported reducer", `{"fact": "credential.quota.remaining_ratio", "select": {"scope": "account", "window_seconds": 18000}, "reduce": "max", "op": "lt", "value": 0.1}`},
		{"quota select unknown field", `{"fact": "credential.quota.remaining_ratio", "select": {"scope": "account", "window_seconds": 18000, "extra": 1}, "reduce": "min", "op": "lt", "value": 0.1}`},
		{"quota ratio > 1", `{"fact": "credential.quota.remaining_ratio", "select": {"scope": "account", "window_seconds": 18000}, "reduce": "min", "op": "lt", "value": 1.1}`},
		{"quota ratio < 0", `{"fact": "credential.quota.remaining_ratio", "select": {"scope": "account", "window_seconds": 18000}, "reduce": "min", "op": "lt", "value": -0.1}`},
		{"quota value not number", `{"fact": "credential.quota.remaining_ratio", "select": {"scope": "account", "window_seconds": 18000}, "reduce": "min", "op": "lt", "value": "0.1"}`},
		{"time_window empty weekdays", `{"predicate": "time_window", "weekdays": [], "ranges": [["09:00", "12:00"]]}`},
		{"time_window duplicate weekday", `{"predicate": "time_window", "weekdays": [1, 1], "ranges": [["09:00", "12:00"]]}`},
		{"time_window invalid weekday", `{"predicate": "time_window", "weekdays": [7], "ranges": [["09:00", "12:00"]]}`},
		{"time_window empty ranges", `{"predicate": "time_window", "weekdays": [1], "ranges": []}`},
		{"time_window invalid range pair", `{"predicate": "time_window", "weekdays": [1], "ranges": [["09:00"]]}`},
		{"time_window invalid hour", `{"predicate": "time_window", "weekdays": [1], "ranges": [["24:00", "01:00"]]}`},
		{"time_window invalid minute", `{"predicate": "time_window", "weekdays": [1], "ranges": [["09:60", "12:00"]]}`},
		{"time_window invalid format", `{"predicate": "time_window", "weekdays": [1], "ranges": [["9:00", "12:00"]]}`},
		{"time_window unknown field", `{"predicate": "time_window", "weekdays": [1], "ranges": [["09:00", "12:00"]], "extra": 1}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			jsonStr := ruleWrap(tc.when)
			_, err := Compile([]byte(jsonStr))
			if err == nil {
				t.Fatalf("expected error for case %q, got nil", tc.name)
			}
		})
	}
}

func TestNumberComparisonsAndQuotaStates(t *testing.T) {
	// 验证所有数值比较操作符：eq, lt, lte, gt, gte
	ops := []struct {
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
	}

	for _, tc := range ops {
		got := compareNumber(tc.actual, tc.op, tc.expected)
		if got != tc.want {
			t.Fatalf("compareNumber(%v, %q, %v) = %v, want %v", tc.actual, tc.op, tc.expected, got, tc.want)
		}
	}

	// 验证额度窗口状态：retained_in_period 和 inferred_reset 均有效
	jsonStr := `{
		"schema_version": 1,
		"rules": [{
			"id": "r-quota", "name": "Quota", "domain": "scheduling", "enabled": true,
			"when": {
				"fact": "credential.quota.remaining_ratio",
				"select": {"scope": "account", "window_seconds": 18000},
				"reduce": "min",
				"op": "lt",
				"value": 0.2
			},
			"then": {"type": "exclude_candidate"}
		}]
	}`
	cfg, err := Compile([]byte(jsonStr))
	if err != nil {
		t.Fatal(err)
	}

	// Case 1: retained_in_period
	ctx1 := &EvalContext{
		Now: time.Now(),
		QuotaWindows: []QuotaWindowFact{
			{Scope: "account", WindowSeconds: 18000, Ratio: 0.15, State: FactStateRetainedInPeriod},
		},
	}
	if !cfg.EvalScheduling(ctx1).Excluded {
		t.Fatal("expected retained_in_period 0.15 < 0.2 to exclude")
	}

	// Case 2: inferred_reset
	ctx2 := &EvalContext{
		Now: time.Now(),
		QuotaWindows: []QuotaWindowFact{
			{Scope: "account", WindowSeconds: 18000, Ratio: 1.0, State: FactStateInferredReset},
		},
	}
	if cfg.EvalScheduling(ctx2).Excluded {
		t.Fatal("expected inferred_reset 1.0 < 0.2 not to exclude")
	}
}

func TestDeepConditionClone(t *testing.T) {
	// 测试包含 all, any, not, time_window, quota 的深拷贝
	jsonStr := `{
		"schema_version": 1,
		"rules": [{
			"id": "r-complex", "name": "Complex", "domain": "scheduling", "enabled": true,
			"when": {
				"all": [
					{
						"any": [
							{"predicate": "time_window", "weekdays": [1, 2], "ranges": [["09:00", "12:00"]]},
							{"not": {"fact": "request.model", "op": "eq", "value": "Astra"}}
						]
					},
					{
						"fact": "credential.quota.remaining_ratio",
						"select": {"scope": "account", "window_seconds": 18000},
						"reduce": "min",
						"op": "lt",
						"value": 0.5
					}
				]
			},
			"then": {"type": "exclude_candidate"}
		}]
	}`

	cfg, err := Compile([]byte(jsonStr))
	if err != nil {
		t.Fatal(err)
	}

	cloned := cfg.Clone()
	if cloned == nil || len(cloned.Rules()) != 1 {
		t.Fatal("clone failed")
	}

	// 确认子节点深拷贝
	origWhen := cfg.Rules()[0].When
	cloneWhen := cloned.Rules()[0].When
	if origWhen == cloneWhen {
		t.Fatal("When pointers should not be identical")
	}
	if origWhen.Children[0] == cloneWhen.Children[0] {
		t.Fatal("Children pointers should not be identical")
	}
	if origWhen.Children[1].Selector == cloneWhen.Children[1].Selector {
		t.Fatal("Selector pointers should not be identical")
	}
}
