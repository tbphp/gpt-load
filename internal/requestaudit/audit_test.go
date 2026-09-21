package requestaudit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestInspectFindsSecretsWithoutRetainingTheirValues(t *testing.T) {
	for _, body := range []string{
		`{"messages":[{"role":"tool","content":"-----BEGIN PRIVATE KEY-----\\nabc"}]}`,
		`{"input":[{"type":"function_call_output","output":"postgres://user:sensitive-password@db/internal"}]}`,
		`{"contents":[{"parts":[{"functionResponse":{"response":{"password":"live-secret-value"}}}]}]}`,
	} {
		_, findings, reason := Inspect([]byte(body))
		if len(findings) == 0 || reason != "" {
			t.Fatalf("want local finding, got %v / %s", findings, reason)
		}
		encoded, _ := json.Marshal(findings)
		if bytes.Contains(encoded, []byte("live-secret-value")) || bytes.Contains(encoded, []byte("sensitive-password")) {
			t.Fatal("finding retained secret")
		}
	}
}

func TestInspectReportsIncompleteCoverage(t *testing.T) {
	for name, body := range map[string]string{
		"attachment": `{"input":[{"type":"input_image","image_url":"https://example.com/a.png"}]}`,
		"history":    `{"previous_response_id":"resp_test","input":"continue"}`,
		"oversized":  `{"input":"` + strings.Repeat("x", MaxStateBytes) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, _, reason := Inspect([]byte(body))
			if reason == "" {
				t.Fatal("incomplete content passed")
			}
		})
	}
}

func TestInterpretRequiresEveryRuleAndReportsUncertainty(t *testing.T) {
	rules := DefaultConfig().Rules
	for name, body := range map[string]string{
		"missing":   `{"answers":{"personal_data":{"type":"noul","noul":0.01}}}`,
		"uncertain": `{"answers":{"personal_data":{"type":"noul","noul":0.5},"prompt_injection":{"type":"noul","noul":0.01}}}`,
		"invalid":   `{"answers":{"personal_data":{"type":"noul","noul":2},"prompt_injection":{"type":"noul","noul":0.01}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, reason := Interpret([]byte(body), rules)
			if reason == "" {
				t.Fatal("invalid or uncertain judgment passed")
			}
		})
	}
}

func TestConfigRejectsAmbiguousPolicy(t *testing.T) {
	for _, raw := range []string{`{"mode":"silent"}`, `{"enabled":true,"local_secrets":false,"semantic_enabled":false}`, `{"semantic_enabled":true,"rules":[]}`, `{"rules":[{"id":"x","name":"x","instructions":"x","threshold":0.4}]}`} {
		if _, err := Decode([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid config %s", raw)
		}
	}
}
