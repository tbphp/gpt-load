package pricing

import "fmt"

// ValidateReceipt verifies a persisted request-time receipt without consulting
// the mutable current pricing table.
func ValidateReceipt(receipt Receipt) error {
	if (receipt.SchemaVersion != 1 && receipt.SchemaVersion != 2) ||
		receipt.Method != ReceiptMethodUnitRateSum || receipt.MethodVersion != 1 || receipt.Currency != "USD" {
		return fmt.Errorf("unsupported pricing receipt contract")
	}
	if err := validateReceiptRule(receipt.Rule, receipt.SchemaVersion == 1); err != nil {
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
	if int64(total) != receipt.TotalNanoUSD {
		return fmt.Errorf("pricing receipt total mismatch")
	}
	return nil
}
