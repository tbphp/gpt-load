package pricing

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// UnmarshalJSON 区分缺省字段与显式 null，并保持历史版本的字段边界。
func (receipt *Receipt) UnmarshalJSON(data []byte) error {
	var header struct {
		SchemaVersion    int             `json:"schema_version"`
		PriceMultipliers json.RawMessage `json:"price_multipliers"`
		BaseTotalNanoUSD json.RawMessage `json:"base_total_nano_usd"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return err
	}
	if len(header.PriceMultipliers) > 0 && (header.SchemaVersion < 5 || bytes.Equal(bytes.TrimSpace(header.PriceMultipliers), []byte("null"))) {
		return fmt.Errorf("price multipliers require a non-null receipt field from v5 onward")
	}
	if len(header.BaseTotalNanoUSD) > 0 && (header.SchemaVersion < 6 || bytes.Equal(bytes.TrimSpace(header.BaseTotalNanoUSD), []byte("null"))) {
		return fmt.Errorf("base total requires a non-null receipt field from v6 onward")
	}
	type receiptJSON Receipt
	var decoded receiptJSON
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*receipt = Receipt(decoded)
	return nil
}

// ValidateReceipt verifies a persisted request-time receipt without consulting
// the mutable current pricing table.
func ValidateReceipt(receipt Receipt) error {
	if (receipt.SchemaVersion != 1 && receipt.SchemaVersion != 2 &&
		receipt.SchemaVersion != 3 && receipt.SchemaVersion != 4 && receipt.SchemaVersion != 5 && receipt.SchemaVersion != 6) ||
		receipt.Method != ReceiptMethodUnitRateSum || receipt.MethodVersion != 1 || receipt.Currency != "USD" {
		return fmt.Errorf("unsupported pricing receipt contract")
	}
	priceMultipliers := PriceMultipliers{Group: DefaultPriceMultiplier, AccessKey: DefaultPriceMultiplier}
	if receipt.SchemaVersion < 5 {
		if receipt.PriceMultipliers != nil {
			return fmt.Errorf("historical pricing receipt must not contain price multipliers")
		}
	} else {
		if receipt.PriceMultipliers == nil || !receipt.PriceMultipliers.Group.Valid() || !receipt.PriceMultipliers.AccessKey.Valid() {
			return fmt.Errorf("invalid pricing receipt price multipliers")
		}
		priceMultipliers = *receipt.PriceMultipliers
	}
	if receipt.SchemaVersion < 6 {
		if receipt.BaseTotalNanoUSD != nil {
			return fmt.Errorf("historical pricing receipt must not contain a base total")
		}
	} else if receipt.BaseTotalNanoUSD == nil || *receipt.BaseTotalNanoUSD < 0 {
		return fmt.Errorf("invalid pricing receipt base total")
	}
	if receipt.SchemaVersion < 4 {
		if receipt.PricingMode != "" {
			return fmt.Errorf("historical pricing receipt must not contain a pricing mode")
		}
	} else if !receipt.PricingMode.Valid() {
		return fmt.Errorf("invalid pricing receipt mode")
	}
	if err := validateReceiptRule(receipt.Rule, receipt.SchemaVersion); err != nil {
		return fmt.Errorf("invalid pricing receipt rule: %w", err)
	}
	if receipt.ContextThresholdTokens != nil && *receipt.ContextThresholdTokens < 0 {
		return fmt.Errorf("invalid pricing receipt context threshold")
	}
	if receipt.TotalNanoUSD < 0 {
		return fmt.Errorf("invalid pricing receipt total")
	}

	allowed := map[string]struct{}{
		"input": {}, "cache_read": {}, "cache_write_5m": {},
		"cache_write_1h": {}, "cache_write": {}, "output": {},
	}
	seen := make(map[string]struct{}, len(receipt.LineItems))
	total := NanoUSD(0)
	for _, line := range receipt.LineItems {
		if _, ok := allowed[line.Code]; !ok {
			return fmt.Errorf("invalid pricing receipt line code %q", line.Code)
		}
		if _, exists := seen[line.Code]; exists {
			return fmt.Errorf("duplicate pricing receipt line code %q", line.Code)
		}
		seen[line.Code] = struct{}{}
		if line.Quantity <= 0 || line.Multiplier.Numerator <= 0 ||
			line.Multiplier.Denominator <= 0 {
			return fmt.Errorf("invalid pricing receipt line quantity or multiplier")
		}
		switch line.State {
		case ReceiptLinePriced:
			if line.RateNanoUSDPerMillion == nil || line.AmountNanoUSD == nil ||
				*line.RateNanoUSDPerMillion < 0 || *line.AmountNanoUSD < 0 {
				return fmt.Errorf("invalid priced receipt line")
			}
			amount, ok := QuoteComponent(
				line.Quantity,
				NanoUSD(*line.RateNanoUSDPerMillion),
				line.Multiplier,
			)
			if receipt.SchemaVersion == 5 {
				amount, ok = quoteComponentWithPriceMultipliers(line.Quantity, NanoUSD(*line.RateNanoUSDPerMillion), line.Multiplier, priceMultipliers)
			}
			if !ok || int64(amount) != *line.AmountNanoUSD {
				return fmt.Errorf("pricing receipt line amount mismatch")
			}
			total, ok = CheckedAddNanoUSD(total, amount)
			if !ok {
				return fmt.Errorf("pricing receipt total overflows")
			}
		case ReceiptLineUnpriced:
			if line.RateNanoUSDPerMillion != nil || line.AmountNanoUSD != nil {
				return fmt.Errorf("invalid unpriced receipt line")
			}
		default:
			return fmt.Errorf("invalid pricing receipt line state")
		}
	}
	if receipt.SchemaVersion == 6 {
		if int64(total) != *receipt.BaseTotalNanoUSD {
			return fmt.Errorf("pricing receipt base total mismatch")
		}
		adjusted, ok := applyPriceMultipliers(total, priceMultipliers)
		if !ok || int64(adjusted) != receipt.TotalNanoUSD {
			return fmt.Errorf("pricing receipt adjusted total mismatch")
		}
	} else if int64(total) != receipt.TotalNanoUSD {
		return fmt.Errorf("pricing receipt total mismatch")
	}
	return nil
}
