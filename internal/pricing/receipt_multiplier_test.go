package pricing

import (
	"encoding/json"
	"strings"
	"testing"

	"gpt-load/internal/usage"
)

func TestValidateReceiptPriceMultipliersPreserveHistoricalVersionBoundary(t *testing.T) {
	for version := 1; version <= 4; version++ {
		rule := ReceiptRule{ModelID: "model"}
		if version == 1 {
			rule.ScopeKey = "provider:openai"
		} else if version >= 3 {
			rule.ChannelID = "openai"
		}
		receipt := validReceipt(version, rule)
		if err := ValidateReceipt(receipt); err != nil {
			t.Fatalf("historical v%d receipt rejected: %v", version, err)
		}
		receipt.PriceMultipliers = &PriceMultipliers{Group: DefaultPriceMultiplier, AccessKey: DefaultPriceMultiplier}
		if err := ValidateReceipt(receipt); err == nil {
			t.Fatalf("historical v%d receipt accepted price multipliers", version)
		}
	}
}

func TestValidateReceiptPriceMultipliersRejectsMissingAndTamperedInputs(t *testing.T) {
	identity := Identity{ChannelID: "openai", ModelID: "model"}
	table := mustTable(t, Rule{Identity: identity, Prices: Prices{Input: fixedPrice(100)}})
	for _, test := range []struct {
		name   string
		mutate func(*Receipt)
	}{
		{"missing multipliers", func(r *Receipt) { r.PriceMultipliers = nil }},
		{"negative group", func(r *Receipt) { r.PriceMultipliers.Group = -1 }},
		{"access key over limit", func(r *Receipt) { r.PriceMultipliers.AccessKey = 1_000_000_001 }},
		{"changed group", func(r *Receipt) { r.PriceMultipliers.Group = DefaultPriceMultiplier }},
		{"changed access key", func(r *Receipt) { r.PriceMultipliers.AccessKey = DefaultPriceMultiplier }},
		{"changed protocol multiplier", func(r *Receipt) { r.LineItems[0].Multiplier.Numerator = 2 }},
		{"changed base rate", func(r *Receipt) { *r.LineItems[0].RateNanoUSDPerMillion = 200 }},
		{"changed line amount", func(r *Receipt) { *r.LineItems[0].AmountNanoUSD = 100 }},
		{"changed total", func(r *Receipt) { r.TotalNanoUSD = 100 }},
		{"downgraded schema", func(r *Receipt) { r.SchemaVersion = 4 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, receipt := table.QuoteForModeWithMultipliers(identity, usage.Result{
				State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1_000_000},
			}, ModeStandard, PriceMultipliers{Group: 800_000, AccessKey: 1_500_000})
			if receipt == nil {
				t.Fatal("expected receipt")
			}
			test.mutate(receipt)
			if err := ValidateReceipt(*receipt); err == nil {
				t.Fatalf("ValidateReceipt accepted %s", test.name)
			}
		})
	}
}

func TestReceiptJSONRejectsNullMultipliersAndHistoricalInjectedFields(t *testing.T) {
	for _, test := range []struct {
		name string
		json string
	}{
		{"v5 null factors", `{"schema_version":5,"price_multipliers":null}`},
		{"v4 null factors", `{"schema_version":4,"price_multipliers":null}`},
		{"v4 explicit factors", `{"schema_version":4,"price_multipliers":{"group":"1","access_key":"1"}}`},
		{"v5 missing group", `{"schema_version":5,"price_multipliers":{"access_key":"1"}}`},
		{"v5 unknown factor", `{"schema_version":5,"price_multipliers":{"group":"1","access_key":"1","other":"1"}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var receipt Receipt
			if err := json.Unmarshal([]byte(test.json), &receipt); err == nil {
				t.Fatalf("Unmarshal accepted %s", test.json)
			}
		})
	}

	// 自定义解码仍须保留请求日志现有的未知字段拒绝行为。
	var receipt Receipt
	decoder := json.NewDecoder(strings.NewReader(`{"schema_version":4,"unknown":1}`))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err == nil {
		t.Fatal("receipt decoder accepted an unknown field")
	}
}
