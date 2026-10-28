package requestredact

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// 直接读取两套前端共用的数据，使用真实 Go 引擎验证预设，避免维护另一份表达式。
func redactionPresetsForTest(t *testing.T) map[string]Rule {
	t.Helper()
	raw, err := os.ReadFile("../../web/src/shared/request-redaction-presets.json")
	if err != nil {
		t.Fatal(err)
	}
	var presets []struct {
		Key string `json:"key"`
		Rule
	}
	if err := json.Unmarshal(raw, &presets); err != nil {
		t.Fatal(err)
	}
	result := make(map[string]Rule)
	for _, preset := range presets {
		if _, duplicate := result[preset.Key]; duplicate || preset.Key == "" {
			t.Fatalf("duplicate or empty preset key: %q", preset.Key)
		}
		if _, err := Compile([]Rule{preset.Rule}); err != nil {
			t.Fatalf("invalid preset %s: %v", preset.Key, err)
		}
		result[preset.Key] = preset.Rule
	}
	return result
}

func TestRedactionExistingPresetsStayUnchanged(t *testing.T) {
	presets := redactionPresetsForTest(t)
	for key, want := range map[string]Rule{
		"email":      {Pattern: `[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`, Replacement: "[EMAIL]"},
		"phone":      {Pattern: `\b1[3-9][0-9]{9}\b`, Replacement: "[PHONE]"},
		"apiKey":     {Pattern: `\b(?:sk-(?:proj-|ant-)?|gh[pousr]_)[A-Za-z0-9_-]{16,}\b`, Replacement: "[API_KEY]"},
		"privateKey": {Pattern: `-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----[\s\S]*?-----END (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`, Replacement: "[PRIVATE_KEY]"},
	} {
		if got := presets[key]; got != want {
			t.Errorf("existing preset %s changed", key)
		}
	}
}

func TestRedactionNewPresets(t *testing.T) {
	presets := redactionPresetsForTest(t)
	for _, test := range []struct {
		key      string
		positive []string
		negative []string
	}{
		{
			key:      "jwt",
			positive: []string{"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJzeW50aGV0aWMifQ." + strings.Repeat("a", 32)},
			negative: []string{"version.2026.10", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJzeW50aGV0aWMifQ", "header.payload.signature"},
		},
		{
			key:      "awsAccessKey",
			positive: []string{"AKIA" + strings.Repeat("A1", 8), "ASIA" + strings.Repeat("B2", 8)},
			negative: []string{"AKIA" + strings.Repeat("A", 15), "AKIA" + strings.Repeat("A", 17), "AKIA" + strings.Repeat("a", 16)},
		},
		{
			key:      "googleApiKey",
			positive: []string{"AIza" + strings.Repeat("aB2_", 8) + "xY-"},
			negative: []string{"AIza" + strings.Repeat("a", 34), "AIza" + strings.Repeat("a", 12) + "!" + strings.Repeat("a", 22), "Google API key"},
		},
		{
			key: "databaseURL",
			positive: []string{
				"postgresql://demo:synthetic%40value@localhost:5432/app?sslmode=require",
				"mysql://demo:synthetic-value@localhost:3306/app",
				"rediss://:synthetic-value@localhost:6379/1",
				"mongodb+srv://demo:synthetic-value@database.invalid/app",
			},
			negative: []string{"postgresql://localhost:5432/app", "https://demo:synthetic-value@host.invalid", "redis://localhost:6379"},
		},
	} {
		t.Run(test.key, func(t *testing.T) {
			rule, found := presets[test.key]
			if !found {
				t.Fatalf("preset %s is unavailable", test.key)
			}
			rule.Mode = ModeEncrypt
			compiled, err := Compile([]Rule{rule})
			if err != nil {
				t.Fatal(err)
			}
			pattern := regexp.MustCompile(rule.Pattern)
			cipher := syntheticRedactionCipher(t)
			for _, value := range test.positive {
				// 引号和括号不属于原值，避免解密后把标点带入另一轮历史。
				for _, pair := range [][2]string{{"", ""}, {"before (", ") after"}, {"\"", "\""}} {
					input := pair[0] + value + pair[1]
					if got := pattern.FindString(input); got != value {
						t.Fatalf("preset %s did not match exactly the complete synthetic value", test.key)
					}
					masked, err := compiled.TextWithCipher(input, cipher)
					if err != nil || strings.Contains(masked, value) {
						t.Fatalf("preset %s failed to encrypt: %v", test.key, err)
					}
					reply := "The model quoted " + masked + " in another sentence."
					restored, err := cipher.RestoreText(reply)
					if err != nil || restored != "The model quoted "+input+" in another sentence." {
						t.Fatalf("preset %s failed to restore: %v", test.key, err)
					}
					history, err := json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "assistant", "content": restored}}})
					if err != nil {
						t.Fatal(err)
					}
					next, err := compiled.ApplyWithCipher(history, cipher)
					if err != nil || strings.Contains(string(next), value) {
						t.Fatalf("preset %s failed to protect the next turn: %v", test.key, err)
					}
				}
			}
			for _, value := range test.negative {
				if pattern.MatchString(value) {
					t.Fatalf("preset %s matched a non-secret fixture", test.key)
				}
			}
		})
	}
}
