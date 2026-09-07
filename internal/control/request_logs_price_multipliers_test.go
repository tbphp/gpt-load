package control

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/pricing"
)

func TestMapRequestLogReceiptIncludesFrozenPriceMultipliers(t *testing.T) {
	var receipt pricing.Receipt
	if err := json.Unmarshal([]byte(`{
		"schema_version":5,"method":"unit_rate_sum","method_version":1,
		"currency":"USD","pricing_mode":"standard",
		"rule":{"channel_id":"openai","model_id":"gpt-4o"},
		"price_multipliers":{"group":"0.8","access_key":"1.5"},
		"line_items":[{"code":"input","quantity":1000,"rate_nano_usd_per_million":2000000000,
		"multiplier":{"numerator":1,"denominator":1},"state":"priced","amount_nano_usd":2400000}],
		"total_nano_usd":2400000
	}`), &receipt); err != nil {
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
	}
	if err := json.Unmarshal(encoded, &actual); err != nil {
		t.Fatal(err)
	}
	if actual.Rule.ChannelID != "openai" || actual.PriceMultipliers["group"] != "0.8" || actual.PriceMultipliers["access_key"] != "1.5" || actual.TotalNanoUSD != "2400000" {
		t.Fatalf("receipt response = %s", encoded)
	}
}
