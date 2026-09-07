package control

import (
	"encoding/json"
	"fmt"
	"testing"

	"gpt-load/internal/pricing"
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
