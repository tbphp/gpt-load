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

func TestJSONToolEncryptionMatchesDecodedText(t *testing.T) {
	cipher := syntheticRedactionCipher(t)
	patterns := []string{`\b1[3-9][0-9]{9}\b`, `[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`, `\b(?:sk-(?:proj-|ant-)?|gh[pousr]_)[A-Za-z0-9_-]{16,}\b`, `prefix\\`, `敏感|😀`}
	samples := []string{"13812345678\n13912345678", "13812345678\t13912345678", "alice@a.com\nbob@b.com", "alice@a.com\"bob@b.com", "prefix\nsuffix", "prefix\\suffix", "hello\nsk-proj-AAAAAAAAAAAAAAAAAAAA", "敏感\n😀", "literal\\n13912345678", "before\r13912345678", "before\b13912345678", "before\f13912345678", "before\x0013912345678"}
	for _, pattern := range patterns {
		rules, err := Compile([]Rule{{Pattern: pattern, Mode: ModeEncrypt}})
		if err != nil {
			t.Fatal(err)
		}
		for _, plain := range samples {
			encoded, err := json.Marshal(plain)
			if err != nil {
				t.Fatal(err)
			}
			encodings := []string{string(encoded), strings.ReplaceAll(strings.ReplaceAll(string(encoded), "敏感", `\u654f\u611f`), "😀", `\ud83d\ude00`), strings.ReplaceAll(string(encoded), "13912345678", `\u00313912345678`)}
			for _, encoded := range encodings {
				inner := `{"value":` + encoded + `,"count":42,"ok":true}`
				body, err := json.Marshal(map[string]any{"input": []any{map[string]any{"type": "function_call_output", "output": inner}}})
				if err != nil {
					t.Fatal(err)
				}
				got, err := rules.ApplyWithCipher(body, cipher)
				if err != nil {
					t.Errorf("pattern=%q sample=%q: %v", pattern, plain, err)
					continue
				}
				output := gjson.GetBytes(got, "input.0.output").Str
				want, err := rules.TextWithCipher(plain, cipher)
				if err != nil {
					t.Fatal(err)
				}
				value := gjson.Get(output, "value").Str
				if !json.Valid([]byte(output)) || value != want {
					t.Errorf("decoded matching differs: pattern=%q sample=%q", pattern, plain)
					continue
				}
				restored, err := cipher.RestoreText(value)
				if err != nil || restored != plain {
					t.Error("restoration changed original value")
				}
				again, err := rules.ApplyWithCipher(got, cipher)
				if err != nil || string(again) != string(got) {
					t.Error("tool result was encrypted twice")
				}
			}
		}
	}
}

func TestJSONToolKeepsRawContextPriorityAndAuthenticatedTokens(t *testing.T) {
	cipher := syntheticRedactionCipher(t)
	for _, rules := range [][]Rule{
		{{Pattern: `"password"\s*:\s*"[^"]*"`, Replacement: "[MASK]"}, {Pattern: `synthetic-secret`, Mode: ModeEncrypt}},
		{{Pattern: `synthetic-secret`, Mode: ModeEncrypt}, {Pattern: `"password"\s*:\s*"[^"]*"`, Replacement: "[MASK]"}},
	} {
		c, err := Compile(rules)
		if err != nil {
			t.Fatal(err)
		}
		inner := `{ "password" : "synthetic-secret", "untouched" : "a\nb", "n":9007199254740993 }`
		got, err := c.textWithCipher(inner, cipher, true)
		if err != nil {
			t.Fatal(err)
		}
		want, err := c.TextWithCipher(inner, cipher)
		if err != nil || got != want {
			t.Fatal("context rule overlap changed priority or unrelated bytes")
		}
	}
	token, err := cipher.EncryptToken("already-protected")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Compile([]Rule{{Pattern: `[A-Za-z0-9_]+`, Mode: ModeEncrypt}})
	if err != nil {
		t.Fatal(err)
	}
	// 转义后的合法密文同样必须保持完整，不能再次加密。
	inner := `"` + strings.Replace(token, "g", `\u0067`, 1) + `"`
	got, err := c.textWithCipher(inner, cipher, true)
	if err != nil || got != inner {
		t.Fatal("escaped authenticated token changed")
	}
}
