package control

import (
	"encoding/json"
	"fmt"
	"testing"

	"gpt-load/internal/automodel"
	"gpt-load/internal/jev"
	"gpt-load/internal/pricing"
	"gpt-load/internal/requestaudit"
	"gpt-load/internal/requestlog"
)

func TestMapRequestLogReceiptIncludesFrozenPriceMultipliers(t *testing.T) {
	for _, test := range []struct {
		name       string
		schema     int
		baseField  string
		wantBase   string
		lineAmount int64
	}{
		{"v6 separates original and final totals", 6, `,"base_total_nano_usd":2000000`, "2000000", 2_000_000},
		{"v5 preserves historical adjusted lines", 5, "", "", 2_400_000},
	} {
		t.Run(test.name, func(t *testing.T) {
			var receipt pricing.Receipt
			if err := json.Unmarshal(fmt.Appendf(nil, `{
		"schema_version":%d,"method":"unit_rate_sum","method_version":1,
		"currency":"USD","pricing_mode":"standard",
		"rule":{"channel_id":"openai","model_id":"gpt-4o"},
		"price_multipliers":{"group":"0.8","access_key":"1.5"},
		"line_items":[{"code":"input","quantity":1000,"rate_nano_usd_per_million":2000000000,
		"multiplier":{"numerator":1,"denominator":1},"state":"priced","amount_nano_usd":%d}],
		"total_nano_usd":2400000%s
	}`, test.schema, test.lineAmount, test.baseField), &receipt); err != nil {
				t.Fatal(err)
			}
			response, err := mapRequestLogPricingReceipt(&receipt)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			var actual struct {
				Rule struct {
					ChannelID string `json:"channel_id"`
				} `json:"rule"`
				PriceMultipliers map[string]string `json:"price_multipliers"`
				TotalNanoUSD     string            `json:"total_nano_usd"`
				BaseTotalNanoUSD *string           `json:"base_total_nano_usd"`
				LineItems        []struct {
					AmountNanoUSD string `json:"amount_nano_usd"`
				} `json:"line_items"`
			}
			if err := json.Unmarshal(encoded, &actual); err != nil {
				t.Fatal(err)
			}
			if actual.Rule.ChannelID != "openai" || actual.PriceMultipliers["group"] != "0.8" || actual.PriceMultipliers["access_key"] != "1.5" || actual.TotalNanoUSD != "2400000" {
				t.Fatalf("receipt response = %s", encoded)
			}
			if test.wantBase == "" {
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(encoded, &fields); err != nil {
					t.Fatal(err)
				}
				if _, present := fields["base_total_nano_usd"]; present {
					t.Fatalf("historical receipt acquired a base total: %s", encoded)
				}
			} else if actual.BaseTotalNanoUSD == nil || *actual.BaseTotalNanoUSD != test.wantBase {
				t.Fatalf("receipt lost original total: %s", encoded)
			}
			if len(actual.LineItems) != 1 || actual.LineItems[0].AmountNanoUSD != fmt.Sprint(test.lineAmount) {
				t.Fatalf("receipt reinterpreted original lines: %s", encoded)
			}
		})
	}
}

func TestMapRequestLogReceiptIncludesSchema7PolicyFactors(t *testing.T) {
	rawJSON := `{
		"schema_version":7,"method":"unit_rate_sum","method_version":1,
		"currency":"USD","pricing_mode":"standard",
		"rule":{"channel_id":"openai","model_id":"gpt-4o"},
		"price_multipliers":{"group":"1","access_key":"1"},
		"base_total_nano_usd":100,"total_nano_usd":200,
		"line_items":[{"code":"input","quantity":1000000,"rate_nano_usd_per_million":100,
		"multiplier":{"numerator":1,"denominator":1},"state":"priced","amount_nano_usd":100}],
		"policy_factors":[{"rule_id":"p","name_snapshot":"Admin billing rule","binding_scope":"group","revision":42,"factor":"2","multiplier":"2"}]
	}`
	var receipt pricing.Receipt
	if err := json.Unmarshal([]byte(rawJSON), &receipt); err != nil {
		t.Fatal(err)
	}
	response, err := mapRequestLogPricingReceipt(&receipt)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var actual struct {
		SchemaVersion int                              `json:"schema_version"`
		PolicyFactors []requestLogPolicyFactorResponse `json:"policy_factors"`
	}
	if err := json.Unmarshal(encoded, &actual); err != nil {
		t.Fatal(err)
	}
	if actual.SchemaVersion != 7 || len(actual.PolicyFactors) != 1 {
		t.Fatalf("unexpected mapped receipt: %s", encoded)
	}
	factor := actual.PolicyFactors[0]
	if factor.RuleID != "p" || factor.NameSnapshot != "Admin billing rule" ||
		factor.BindingScope != "group" || factor.Revision != "42" ||
		factor.Factor != "2" || factor.Multiplier != "2" {
		t.Fatalf("unexpected mapped policy factor: %+v", factor)
	}
}

func TestMapRequestLogReceiptFullUint64RevisionAndNonCanonicalFactor(t *testing.T) {
	rawJSON := `{
		"schema_version":7,"method":"unit_rate_sum","method_version":1,
		"currency":"USD","pricing_mode":"standard",
		"rule":{"channel_id":"openai","model_id":"gpt-4o"},
		"price_multipliers":{"group":"1","access_key":"1"},
		"base_total_nano_usd":100,"total_nano_usd":200,
		"line_items":[{"code":"input","quantity":1000000,"rate_nano_usd_per_million":100,
		"multiplier":{"numerator":1,"denominator":1},"state":"priced","amount_nano_usd":100}],
		"policy_factors":[{"rule_id":"p","name_snapshot":"Admin billing rule","binding_scope":"group","revision":18446744073709551615,"factor":"02.000000","multiplier":"2"}]
	}`
	var receipt pricing.Receipt
	if err := json.Unmarshal([]byte(rawJSON), &receipt); err != nil {
		t.Fatal(err)
	}
	var rejected pricing.Receipt
	if err := json.Unmarshal([]byte(`{"schema_version":7,"method":"unit_rate_sum","method_version":1,"currency":"USD","pricing_mode":"standard","rule":{"channel_id":"openai","model_id":"gpt-4o"},"price_multipliers":{"group":"1","access_key":"1"},"base_total_nano_usd":100,"total_nano_usd":200,"line_items":[],"policy_factors":[{"rule_id":"p","name_snapshot":"p","binding_scope":"group","revision":"1","factor":"2","multiplier":"2"}]}`), &rejected); err == nil {
		t.Fatal("string policy factor revision must be rejected")
	}
	if err := pricing.ValidateReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	response, err := mapRequestLogPricingReceipt(&receipt)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.PolicyFactors) != 1 {
		t.Fatalf("unexpected policy factors: %+v", response.PolicyFactors)
	}
	f := response.PolicyFactors[0]
	if f.Revision != "18446744073709551615" {
		t.Fatalf("revision corrupted: %s", f.Revision)
	}
	if f.Factor != "02.000000" {
		t.Fatalf("original noncanonical factor syntax changed: %s", f.Factor)
	}
	if f.Multiplier != "2" {
		t.Fatalf("multiplier mismatch: %s", f.Multiplier)
	}
}

func TestSanitizeAccessKeyRequestLogStripsAllPolicyReceipts(t *testing.T) {
	rawReceipt := []byte(`{"schema_version":7,"method":"unit_rate_sum","method_version":1,"currency":"USD","pricing_mode":"standard","rule":{"channel_id":"openai","model_id":"gpt-4o"},"price_multipliers":{"group":"1","access_key":"1"},"base_total_nano_usd":100,"total_nano_usd":200,"line_items":[],"policy_factors":[{"rule_id":"p","name_snapshot":"p","binding_scope":"group","revision":1,"factor":"2","multiplier":"2"}]}`)
	var r pricing.Receipt
	if err := json.Unmarshal(rawReceipt, &r); err != nil {
		t.Fatal(err)
	}
	record := requestlog.Record{
		Attempts:     []requestlog.Attempt{{Sequence: 1, PricingReceipt: &r}},
		AutoDecision: &automodel.Decision{Receipt: rawReceipt},
		RequestAudit: &requestaudit.Result{Calls: []jev.Observation{{Receipt: rawReceipt}}},
	}
	got := sanitizeAccessKeyRequestLog(record)
	if len(got.Attempts) != 0 || len(got.AutoDecision.Receipt) != 0 || len(got.RequestAudit.Calls[0].Receipt) != 0 {
		t.Fatalf("nonadmin access key request log leaked receipt: %+v", got)
	}
}
