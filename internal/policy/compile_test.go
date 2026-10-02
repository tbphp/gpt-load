package policy

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestCompileEmptyConfig(t *testing.T) {
	explicitEmpty := []byte(`{"schema_version": 1, "rules": []}`)
	cfg, err := Compile(explicitEmpty)
	if err != nil {
		t.Fatalf("Compile explicit empty config failed: %v", err)
	}
	if len(cfg.Rules()) != 0 {
		t.Fatalf("expected 0 rules, got %d", len(cfg.Rules()))
	}

	empty := Empty()
	if empty == nil || len(empty.Rules()) != 0 {
		t.Fatalf("Empty() should be valid empty config")
	}
}

func TestCompileRejectsInvalidSchemaVersionAndMissingFields(t *testing.T) {
	cases := []struct {
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
		{"unknown root field", `{"schema_version": 1, "rules": [], "extra": 123}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile([]byte(tc.json))
			if err == nil {
				t.Fatalf("expected error for case %q, got nil", tc.name)
			}
		})
	}
}

func TestCompileRejectsDuplicateJSONFields(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{
			name: "duplicate schema_version",
			json: `{"schema_version": 1, "schema_version": 1, "rules": []}`,
		},
		{
			name: "duplicate rules",
			json: `{"schema_version": 1, "rules": [], "rules": []}`,
		},
		{
			name: "duplicate rule id",
			json: `{"schema_version": 1, "rules": [{"id": "r1", "id": "r1", "name": "N", "domain": "scheduling", "enabled": false, "when": {"fact": "request.model", "op": "eq", "value": "m"}, "then": {"type": "exclude_candidate"}}]}`,
		},
		{
			name: "duplicate fact in condition",
			json: `{"schema_version": 1, "rules": [{"id": "r1", "name": "N", "domain": "scheduling", "enabled": false, "when": {"fact": "request.model", "fact": "upstream.model", "op": "eq", "value": "m"}, "then": {"type": "exclude_candidate"}}]}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile([]byte(tc.json))
			if err == nil {
				t.Fatalf("expected duplicate field error for %q, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), "duplicate field") {
				t.Fatalf("expected error message to mention duplicate field, got: %v", err)
			}
		})
	}
}

func TestCompileRejectsTrailingData(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"trailing object", `{"schema_version": 1, "rules": []} {"extra": 1}`},
		{"trailing number", `{"schema_version": 1, "rules": []} 123`},
		{"trailing string", `{"schema_version": 1, "rules": []} "hello"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile([]byte(tc.json))
			if err == nil {
				t.Fatalf("expected trailing data error for %q, got nil", tc.name)
			}
		})
	}
}

func TestCompileRejectsDuplicateRuleIDs(t *testing.T) {
	jsonStr := `{
		"schema_version": 1,
		"rules": [
			{
				"id": "rule-dup",
				"name": "First Rule",
				"domain": "scheduling",
				"enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "Astra"},
				"then": {"type": "exclude_candidate"}
			},
			{
				"id": "rule-dup",
				"name": "Second Rule",
				"domain": "scheduling",
				"enabled": false,
				"when": {"fact": "request.model", "op": "eq", "value": "Luna"},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`

	_, err := Compile([]byte(jsonStr))
	if err == nil {
		t.Fatal("expected error for duplicate rule ID, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate rule id") {
		t.Fatalf("expected error mentioning duplicate rule id, got %v", err)
	}
}

func TestCompileRejectsInvalidRuleMetadata(t *testing.T) {
	validRuleBase := func(id, name, domain string, enabled string) string {
		return fmt.Sprintf(`{
			"schema_version": 1,
			"rules": [{
				"id": %s,
				"name": %s,
				"domain": %s,
				"enabled": %s,
				"when": {"fact": "request.model", "op": "eq", "value": "Astra"},
				"then": {"type": "exclude_candidate"}
			}]
		}`, id, name, domain, enabled)
	}

	cases := []struct {
		name string
		json string
	}{
		{"empty ID", validRuleBase(`""`, `"valid"`, `"scheduling"`, `true`)},
		{"ID with spaces", validRuleBase(`"id with space"`, `"valid"`, `"scheduling"`, `true`)},
		{"ID with special chars", validRuleBase(`"id@invalid"`, `"valid"`, `"scheduling"`, `true`)},
		{"ID too long (>64)", validRuleBase(fmt.Sprintf(`"%s"`, strings.Repeat("a", 65)), `"valid"`, `"scheduling"`, `true`)},
		{"empty Name", validRuleBase(`"r1"`, `""`, `"scheduling"`, `true`)},
		{"Name with surrounding spaces", validRuleBase(`"r1"`, `" name "`, `"scheduling"`, `true`)},
		{"Name too long (>128 Unicode)", validRuleBase(`"r1"`, fmt.Sprintf(`"%s"`, strings.Repeat("测", 129)), `"scheduling"`, `true`)},
		{"invalid domain", validRuleBase(`"r1"`, `"valid"`, `"invalid_domain"`, `true`)},
		{"enabled not bool", validRuleBase(`"r1"`, `"valid"`, `"scheduling"`, `"true"`)},
		{"model value with surrounding whitespace", `{
			"schema_version": 1,
			"rules": [{
				"id": "r1", "name": "N", "domain": "scheduling", "enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": " Astra "},
				"then": {"type": "exclude_candidate"}
			}]
		}`},
		{"model value too long (>255)", fmt.Sprintf(`{
			"schema_version": 1,
			"rules": [{
				"id": "r1", "name": "N", "domain": "scheduling", "enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "%s"},
				"then": {"type": "exclude_candidate"}
			}]
		}`, strings.Repeat("M", 256))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile([]byte(tc.json))
			if err == nil {
				t.Fatalf("expected error for case %q, got nil", tc.name)
			}
		})
	}
}

func TestCompileActionDomainValidation(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{
			name: "scheduling with multiply_price",
			json: `{
				"schema_version": 1,
				"rules": [{
					"id": "r1", "name": "N", "domain": "scheduling", "enabled": true,
					"when": {"fact": "request.model", "op": "eq", "value": "Astra"},
					"then": {"type": "multiply_price", "factor": "2"}
				}]
			}`,
		},
		{
			name: "pricing with exclude_candidate",
			json: `{
				"schema_version": 1,
				"rules": [{
					"id": "r1", "name": "N", "domain": "pricing", "enabled": true,
					"when": {"fact": "request.model", "op": "eq", "value": "Astra"},
					"then": {"type": "exclude_candidate"}
				}]
			}`,
		},
		{
			name: "pricing with factor > 1000",
			json: `{
				"schema_version": 1,
				"rules": [{
					"id": "r1", "name": "N", "domain": "pricing", "enabled": true,
					"when": {"fact": "request.model", "op": "eq", "value": "Astra"},
					"then": {"type": "multiply_price", "factor": "1001"}
				}]
			}`,
		},
		{
			name: "pricing with factor > 6 decimals",
			json: `{
				"schema_version": 1,
				"rules": [{
					"id": "r1", "name": "N", "domain": "pricing", "enabled": true,
					"when": {"fact": "request.model", "op": "eq", "value": "Astra"},
					"then": {"type": "multiply_price", "factor": "1.1234567"}
				}]
			}`,
		},
		{
			name: "pricing with negative factor",
			json: `{
				"schema_version": 1,
				"rules": [{
					"id": "r1", "name": "N", "domain": "pricing", "enabled": true,
					"when": {"fact": "request.model", "op": "eq", "value": "Astra"},
					"then": {"type": "multiply_price", "factor": "-1"}
				}]
			}`,
		},
		{
			name: "pricing with extra action fields",
			json: `{
				"schema_version": 1,
				"rules": [{
					"id": "r1", "name": "N", "domain": "pricing", "enabled": true,
					"when": {"fact": "request.model", "op": "eq", "value": "Astra"},
					"then": {"type": "multiply_price", "factor": "2", "extra": "invalid"}
				}]
			}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile([]byte(tc.json))
			if err == nil {
				t.Fatalf("expected error for case %q, got nil", tc.name)
			}
		})
	}
}

func TestCompileRejectsInvalidRulesEvenWhenDisabled(t *testing.T) {
	// 合同第8节与第9节：禁用规则也严格校验，不把非法配置藏在 enabled=false 中
	jsonStr := `{
		"schema_version": 1,
		"rules": [{
			"id": "r-disabled-invalid",
			"name": "Disabled Rule",
			"domain": "scheduling",
			"enabled": false,
			"when": {"unknown_field": "invalid"},
			"then": {"type": "exclude_candidate"}
		}]
	}`

	_, err := Compile([]byte(jsonStr))
	if err == nil {
		t.Fatal("expected error for invalid rule even when enabled=false, got nil")
	}
}

func TestCompileBudgetLimits(t *testing.T) {
	t.Run("too many rules (>100)", func(t *testing.T) {
		var rules []string
		for i := 0; i < 101; i++ {
			rules = append(rules, fmt.Sprintf(`{
				"id": "r%d", "name": "R%d", "domain": "scheduling", "enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "Astra"},
				"then": {"type": "exclude_candidate"}
			}`, i, i))
		}
		jsonStr := fmt.Sprintf(`{"schema_version": 1, "rules": [%s]}`, strings.Join(rules, ","))
		_, err := Compile([]byte(jsonStr))
		if err == nil {
			t.Fatal("expected error exceeding 100 rules, got nil")
		}
	})

	t.Run("depth exceeds 16", func(t *testing.T) {
		// 构造深度为 17 的条件树
		deepTree := `{"fact": "request.model", "op": "eq", "value": "Astra"}`
		for i := 0; i < 16; i++ {
			deepTree = fmt.Sprintf(`{"not": %s}`, deepTree)
		}
		jsonStr := fmt.Sprintf(`{
			"schema_version": 1,
			"rules": [{
				"id": "r-deep", "name": "Deep", "domain": "scheduling", "enabled": true,
				"when": %s,
				"then": {"type": "exclude_candidate"}
			}]
		}`, deepTree)
		_, err := Compile([]byte(jsonStr))
		if err == nil {
			t.Fatal("expected error exceeding depth 16, got nil")
		}
	})

	t.Run("rule nodes exceed 256", func(t *testing.T) {
		var children []string
		for i := 0; i < 100; i++ {
			children = append(children, `{"fact": "request.model", "op": "eq", "value": "Astra"}`)
		}
		// 一个 all 包含 100 个节点，再套一层
		jsonStr := fmt.Sprintf(`{
			"schema_version": 1,
			"rules": [{
				"id": "r-nodes", "name": "Nodes", "domain": "scheduling", "enabled": true,
				"when": {
					"all": [
						{"all": [%s]},
						{"all": [%s]},
						{"all": [%s]}
					]
				},
				"then": {"type": "exclude_candidate"}
			}]
		}`, strings.Join(children, ","), strings.Join(children, ","), strings.Join(children, ","))
		_, err := Compile([]byte(jsonStr))
		if err == nil {
			t.Fatal("expected error exceeding 256 nodes per rule, got nil")
		}
	})

	t.Run("config bytes exceed 256KiB", func(t *testing.T) {
		bigBuf := bytes.Repeat([]byte(" "), MaxConfigBytes+1)
		_, err := Compile(bigBuf)
		if err == nil {
			t.Fatal("expected error exceeding MaxConfigBytes, got nil")
		}
	})
}

func TestCompileCloneAndImmutability(t *testing.T) {
	jsonStr := []byte(`{
		"schema_version": 1,
		"rules": [{
			"id": "r1", "name": "Rule 1", "domain": "pricing", "enabled": true,
			"when": {"fact": "request.model", "op": "in", "value": ["Astra", "Luna"]},
			"then": {"type": "multiply_price", "factor": "2.5"}
		}]
	}`)

	cfg, err := Compile(jsonStr)
	if err != nil {
		t.Fatal(err)
	}

	// 原地修改输入 byte slice，确保 CompiledConfig 保持不变
	jsonStr[0] = 'X'
	if cfg.Rules()[0].ID != "r1" {
		t.Fatal("CompiledConfig modified after mutating input bytes")
	}

	// Clone 深度拷贝测试
	cloned := cfg.Clone()
	if len(cloned.Rules()) != 1 {
		t.Fatalf("cloned rules count %d, want 1", len(cloned.Rules()))
	}
	if cloned.Rules()[0].ID != "r1" {
		t.Fatalf("cloned rule ID mismatch")
	}

	// 验证修改 cloned 内部树不影响 cfg
	cloned.Rules()[0].When.InValues[0] = "MUTATED"
	if cfg.Rules()[0].When.InValues[0] == "MUTATED" {
		t.Fatal("modifying cloned AST mutated original CompiledConfig")
	}
}
