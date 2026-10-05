package policy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestCompileEmptyConfig(t *testing.T) {
	cfg, err := Compile([]byte(`{"schema_version": 1, "rules": []}`))
	if err != nil {
		t.Fatalf("Compile explicit empty config failed: %v", err)
	}
	if len(cfg.Rules()) != 0 {
		t.Fatalf("expected 0 rules, got %d", len(cfg.Rules()))
	}
	if empty := Empty(); empty == nil || len(empty.Rules()) != 0 {
		t.Fatalf("Empty() should be valid empty config")
	}
}

func TestCompileIgnoresUnknownExtensionFields(t *testing.T) {
	// 未知字段是前向兼容扩展：忽略而非中断编译。
	rule := schedRule("r1", map[string]any{
		"fact": "request.model", "op": "eq", "value": "Astra", "future_leaf": 1,
	})
	rule["future_rule"] = "x"
	rule["actions"].([]any)[0].(map[string]any)["future_action"] = "y"
	doc := map[string]any{"schema_version": 1, "future_root": true, "rules": []any{rule}}
	cfg, err := Compile(mustJSON(t, doc))
	if err != nil {
		t.Fatalf("unknown extension fields must be ignored, got %v", err)
	}
	if len(cfg.Rules()) != 1 || cfg.Rules()[0].ID != "r1" {
		t.Fatalf("rules = %+v", cfg.Rules())
	}
}

func TestCompileRejectsInvalidSchemaVersionAndMissingFields(t *testing.T) {
	for _, tc := range []struct {
		name string
		json string
	}{
		{"null input", `null`},
		{"empty input", ``},
		{"whitespace input", "   \n\t  "},
		{"missing schema_version", `{"rules": []}`},
		{"missing rules", `{"schema_version": 1}`},
		{"schema_version string", `{"schema_version": "1", "rules": []}`},
		{"schema_version float", `{"schema_version": 1.5, "rules": []}`},
		{"schema_version 2", `{"schema_version": 2, "rules": []}`},
		{"rules is null", `{"schema_version": 1, "rules": null}`},
		{"rules is object", `{"schema_version": 1, "rules": {}}`},
		{"trailing object", `{"schema_version": 1, "rules": []} {"extra": 1}`},
		{"trailing number", `{"schema_version": 1, "rules": []} 123`},
		{"trailing string", `{"schema_version": 1, "rules": []} "hello"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Compile([]byte(tc.json)); err == nil {
				t.Fatalf("expected error for case %q, got nil", tc.name)
			}
		})
	}
}

func TestCompileRejectsDuplicateRuleIDs(t *testing.T) {
	_, err := Compile(configJSON(t,
		schedRule("rule-dup", modelEq("Astra")),
		schedRule("rule-dup", modelEq("Luna")),
	))
	if err == nil {
		t.Fatal("expected error for duplicate rule ID, got nil")
	}
}

func TestCompileRejectsInvalidRuleMetadata(t *testing.T) {
	// metaRule 在合法调度规则上覆盖给定字段。
	metaRule := func(fields map[string]any) []byte {
		rule := schedRule("r1", modelEq("Astra"))
		for k, v := range fields {
			rule[k] = v
		}
		return configJSON(t, rule)
	}
	disabledInvalid := schedRule("r-disabled-invalid", map[string]any{"unknown_field": "invalid"})
	disabledInvalid["enabled"] = false

	for _, tc := range []struct {
		name   string
		config []byte
	}{
		{"empty ID", metaRule(map[string]any{"id": ""})},
		{"ID with spaces", metaRule(map[string]any{"id": "id with space"})},
		{"ID with special chars", metaRule(map[string]any{"id": "id@invalid"})},
		{"ID too long (>64)", metaRule(map[string]any{"id": strings.Repeat("a", 65)})},
		{"empty Name", metaRule(map[string]any{"name": ""})},
		{"Name with surrounding spaces", metaRule(map[string]any{"name": " name "})},
		{"Name too long (>128 Unicode)", metaRule(map[string]any{"name": strings.Repeat("测", 129)})},
		{"invalid domain", metaRule(map[string]any{"domain": "invalid_domain"})},
		{"enabled not bool", metaRule(map[string]any{"enabled": "true"})},
		{"model value with surrounding whitespace", configJSON(t, schedRule("r1", modelEq(" Astra ")))},
		{"model value too long (>255)", configJSON(t, schedRule("r1", modelEq(strings.Repeat("M", 256))))},
		{"pricing factor > 1000", configJSON(t, priceRule("r1", "1001", modelEq("Astra")))},
		{"pricing factor > 6 decimals", configJSON(t, priceRule("r1", "1.1234567", modelEq("Astra")))},
		{"pricing negative factor", configJSON(t, priceRule("r1", "-1", modelEq("Astra")))},
		{"invalid rule hidden behind enabled=false", configJSON(t, disabledInvalid)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Compile(tc.config); err == nil {
				t.Fatalf("expected error for case %q, got nil", tc.name)
			}
		})
	}
}

func TestCompileBudgetLimits(t *testing.T) {
	t.Run("too many rules (>100)", func(t *testing.T) {
		rules := make([]map[string]any, 0, MaxRulesPerConfig+1)
		for i := 0; i <= MaxRulesPerConfig; i++ {
			rules = append(rules, schedRule(fmt.Sprintf("r%d", i), modelEq("Astra")))
		}
		if _, err := Compile(configJSON(t, rules...)); err == nil {
			t.Fatal("expected error exceeding 100 rules, got nil")
		}
	})

	t.Run("depth exceeds 16", func(t *testing.T) {
		var deep any = modelEq("Astra")
		for i := 0; i < MaxConditionDepth; i++ {
			deep = map[string]any{"not": deep}
		}
		if _, err := Compile(configJSON(t, schedRule("r-deep", deep))); err == nil {
			t.Fatal("expected error exceeding depth 16, got nil")
		}
	})

	t.Run("rule nodes exceed 256", func(t *testing.T) {
		children := make([]any, 0, 100)
		for i := 0; i < 100; i++ {
			children = append(children, modelEq("Astra"))
		}
		when := map[string]any{"all": []any{
			map[string]any{"all": children},
			map[string]any{"all": children},
			map[string]any{"all": children},
		}}
		if _, err := Compile(configJSON(t, schedRule("r-nodes", when))); err == nil {
			t.Fatal("expected error exceeding 256 nodes per rule, got nil")
		}
	})

	t.Run("config bytes exceed 256KiB", func(t *testing.T) {
		if _, err := Compile(bytes.Repeat([]byte(" "), MaxConfigBytes+1)); err == nil {
			t.Fatal("expected error exceeding MaxConfigBytes, got nil")
		}
	})
}

func TestCompileInputImmutability(t *testing.T) {
	raw := configJSON(t, priceRule("r1", "2.5", map[string]any{
		"fact": "request.model", "op": "in", "value": []any{"Astra", "Luna"},
	}))
	cfg, err := Compile(raw)
	if err != nil {
		t.Fatal(err)
	}

	// 原地修改输入 byte slice，确保 CompiledConfig 保持不变。
	raw[0] = 'X'
	if cfg.Rules()[0].ID != "r1" {
		t.Fatal("CompiledConfig modified after mutating input bytes")
	}

}

func TestCompileGroupPolicyField(t *testing.T) {
	for _, tc := range []struct {
		name string
		json string
		mode GroupPolicyMode
	}{
		{"default inherit", `{"schema_version":1,"rules":[]}`, GroupPolicyInherit},
		{"explicit inherit", `{"schema_version":1,"group_policy":"inherit","rules":[]}`, GroupPolicyInherit},
		{"explicit override", `{"schema_version":1,"group_policy":"override","rules":[]}`, GroupPolicyOverride},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Compile([]byte(tc.json))
			if err != nil {
				t.Fatalf("Compile error: %v", err)
			}
			if cfg.GroupPolicyMode() != tc.mode {
				t.Fatalf("mode = %q, want %q", cfg.GroupPolicyMode(), tc.mode)
			}
		})
	}

	// null/未知值/非字符串类型均严格拒绝。
	for _, invalid := range []string{
		`{"schema_version":1,"group_policy":"standard","rules":[]}`,
		`{"schema_version":1,"group_policy":null,"rules":[]}`,
		`{"schema_version":1,"group_policy":1,"rules":[]}`,
		`{"schema_version":1,"group_policy":true,"rules":[]}`,
	} {
		if _, err := Compile([]byte(invalid)); err == nil {
			t.Fatalf("expected error for %s", invalid)
		}
	}
}

func TestCompileConditionFormValidation(t *testing.T) {
	for _, tc := range []struct{ name, when string }{
		{"empty node object", `{}`},
		{"mixed all and any", `{"all": [{"fact": "request.model", "op": "eq", "value": "A"}], "any": [{"fact": "request.model", "op": "eq", "value": "B"}]}`},
		{"mixed fact and not", `{"fact": "request.model", "op": "eq", "value": "A", "not": {"fact": "request.model", "op": "eq", "value": "B"}}`},
		{"all empty array", `{"all": []}`},
		{"any empty array", `{"any": []}`},
		{"not not an object", `{"not": []}`},
		{"unknown param key", `{"fact": "non_existent_key", "op": "eq", "value": "A"}`},
		{"unsupported operator", `{"fact": "request.model", "op": "gt", "value": "A"}`},
		{"fact missing op", `{"fact": "request.model", "value": "A"}`},
		{"fact missing value", `{"fact": "request.model", "op": "eq"}`},
		{"fact string in empty array", `{"fact": "request.model", "op": "in", "value": []}`},
		{"fact string in duplicate values", `{"fact": "request.model", "op": "in", "value": ["A", "A"]}`},
		{"fact string in whitespace value", `{"fact": "request.model", "op": "in", "value": [" A "]}`},
		{"quota missing select", `{"fact": "credential.quota.remaining_ratio", "reduce": "min", "op": "lt", "value": 0.1}`},
		{"quota invalid window_seconds", `{"fact": "credential.quota.remaining_ratio", "select": {"scope": "account", "window_seconds": 0}, "reduce": "min", "op": "lt", "value": 0.1}`},
		{"quota ratio > 1", `{"fact": "credential.quota.remaining_ratio", "select": {"scope": "account", "window_seconds": 18000}, "reduce": "min", "op": "lt", "value": 1.1}`},
		{"time_window invalid weekday", `{"predicate": "time_window", "weekdays": [7], "ranges": [["09:00", "12:00"]]}`},
		{"time_window invalid format", `{"predicate": "time_window", "weekdays": [1], "ranges": [["9:00", "12:00"]]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Compile(configJSON(t, schedRule("r1", json.RawMessage(tc.when)))); err == nil {
				t.Fatalf("expected error for case %q, got nil", tc.name)
			}
		})
	}
}

func TestStructuredValidationErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		json string
		path string
	}{
		{
			name: "nested any value",
			json: string(configJSON(t, schedRule("r1", map[string]any{
				"any": []any{modelEq("Astra"), modelEq(" InvalidWhitespace ")},
			}))),
			path: "rules[0].when.any[1].value",
		},
		{
			name: "factor out of range",
			json: string(configJSON(t, priceRule("r1", "1005", modelEq("Astra")))),
			path: "rules[0].actions[0].factor",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile([]byte(tc.json))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.path) {
				t.Fatalf("expected error to locate %q, got %q", tc.path, err.Error())
			}
		})
	}
}
