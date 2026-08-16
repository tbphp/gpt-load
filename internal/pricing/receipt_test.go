package pricing

import (
	"strings"
	"testing"
)

func TestValidateReceiptPreservesVersionedRuleIdentitySemantics(t *testing.T) {
	tests := []struct {
		name    string
		receipt Receipt
	}{
		{
			name: "v1 requires and accepts legacy scope",
			receipt: validReceipt(1, ReceiptRule{
				ScopeKey: "provider:openai",
				ModelID:  "gpt-4.1",
			}),
		},
		{
			name:    "v2 remains global",
			receipt: validReceipt(2, ReceiptRule{ModelID: "gpt-4.1"}),
		},
		{
			name: "v3 freezes channel and model",
			receipt: validReceipt(3, ReceiptRule{
				ChannelID: "openai",
				ModelID:   "gpt-4.1",
			}),
		},
		{
			name: "v4 freezes channel model and pricing mode",
			receipt: validReceipt(4, ReceiptRule{
				ChannelID: "openai",
				ModelID:   "gpt-4.1",
			}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateReceipt(test.receipt); err != nil {
				t.Fatalf("ValidateReceipt() error = %v", err)
			}
		})
	}
}

func TestValidateReceiptRejectsCrossVersionIdentityReinterpretation(t *testing.T) {
	tests := []struct {
		name    string
		receipt Receipt
	}{
		{name: "v1 missing scope", receipt: validReceipt(1, ReceiptRule{ModelID: "gpt-4.1"})},
		{name: "v1 with channel", receipt: validReceipt(1, ReceiptRule{ScopeKey: "provider:openai", ChannelID: "openai", ModelID: "gpt-4.1"})},
		{name: "v2 with legacy scope", receipt: validReceipt(2, ReceiptRule{ScopeKey: "provider:openai", ModelID: "gpt-4.1"})},
		{name: "v2 with channel", receipt: validReceipt(2, ReceiptRule{ChannelID: "openai", ModelID: "gpt-4.1"})},
		{name: "v3 missing channel", receipt: validReceipt(3, ReceiptRule{ModelID: "gpt-4.1"})},
		{name: "v3 with legacy scope", receipt: validReceipt(3, ReceiptRule{ScopeKey: "provider:openai", ChannelID: "openai", ModelID: "gpt-4.1"})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateReceipt(test.receipt); err == nil {
				t.Fatalf("ValidateReceipt() accepted incompatible rule %#v", test.receipt.Rule)
			}
		})
	}
}

func TestValidateReceiptPreservesPricingModeVersionBoundary(t *testing.T) {
	historical := validReceipt(3, ReceiptRule{ChannelID: "openai", ModelID: "gpt-4.1"})
	historical.PricingMode = ModeFast
	if err := ValidateReceipt(historical); err == nil {
		t.Fatal("ValidateReceipt() accepted a pricing mode in historical v3")
	}

	current := validReceipt(4, ReceiptRule{ChannelID: "openai", ModelID: "gpt-4.1"})
	current.PricingMode = Mode("Invalid Mode")
	if err := ValidateReceipt(current); err == nil {
		t.Fatal("ValidateReceipt() accepted an invalid v4 pricing mode")
	}
}

func TestValidateReceiptV3UsesStrictChannelIDValidation(t *testing.T) {
	for _, channelID := range []string{
		"",
		" openai",
		"openai ",
		"openai\n",
		strings.Repeat("c", 65),
	} {
		t.Run(channelID, func(t *testing.T) {
			receipt := validReceipt(3, ReceiptRule{ChannelID: channelID, ModelID: "gpt-4.1"})
			if err := ValidateReceipt(receipt); err == nil {
				t.Fatalf("ValidateReceipt() accepted channel ID %q", channelID)
			}
		})
	}
}

func validReceipt(schemaVersion int, rule ReceiptRule) Receipt {
	receipt := Receipt{
		SchemaVersion: schemaVersion,
		Method:        ReceiptMethodUnitRateSum,
		MethodVersion: 1,
		Currency:      "USD",
		Rule:          rule,
		LineItems:     []ReceiptLine{},
	}
	if schemaVersion == 4 {
		receipt.PricingMode = ModeStandard
	}
	return receipt
}
