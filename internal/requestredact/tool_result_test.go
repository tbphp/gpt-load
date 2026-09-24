package requestredact

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestToolResultPreservesOriginalRuleCoverage(t *testing.T) {
	cipher := syntheticRedactionCipher(t)
	for _, tc := range []struct {
		name, text, secret string
		rules              []Rule
	}{
		{"numeric value", `{"phone":13812345678}`, "13812345678", []Rule{{Pattern: `1[3-9][0-9]{9}`, Mode: ModeEncrypt}}},
		{"object key", `{"13812345678":{}}`, "13812345678", []Rule{{Pattern: `1[3-9][0-9]{9}`, Mode: ModeEncrypt}}},
		{"numeric text", `13812345678`, "13812345678", []Rule{{Pattern: `1[3-9][0-9]{9}`, Mode: ModeEncrypt}}},
		{"unrelated encrypt", `{"password":"synthetic-secret"}`, "synthetic-secret", []Rule{{Pattern: `"password"\s*:\s*"[^"]*"`, Replacement: "[MASK]"}, {Pattern: `[a-z]+@example.invalid`, Mode: ModeEncrypt}}},
		{"overlapping rules", `{"password":"synthetic-secret"}`, "synthetic-secret", []Rule{{Pattern: `"password"\s*:\s*"[^"]*"`, Replacement: "[MASK]"}, {Pattern: `synthetic-secret`, Mode: ModeEncrypt}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rules, err := Compile(tc.rules)
			if err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(map[string]any{"input": []any{map[string]any{"type": "function_call_output", "output": tc.text}}})
			if err != nil {
				t.Fatal(err)
			}
			got, err := rules.ApplyWithCipher(body, cipher)
			if err != nil {
				t.Fatal(err)
			}
			text := gjson.GetBytes(got, "input.0.output").Str
			want, err := rules.TextWithCipher(tc.text, cipher)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(text, tc.secret) || text != want {
				t.Fatal("tool result changed original rule coverage or priority")
			}
		})
	}
}
