package pricing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// UnmarshalJSON 区分缺省字段与显式 null，并保持历史版本的字段边界。
func (receipt *Receipt) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("pricing: receipt must be a JSON object")
	}

	canonicalKeys := map[string]struct{}{
		"schema_version":           {},
		"method":                   {},
		"method_version":           {},
		"currency":                 {},
		"pricing_mode":             {},
		"price_multipliers":        {},
		"base_total_nano_usd":      {},
		"policy_factors":           {},
		"rule":                     {},
		"context_threshold_tokens": {},
		"line_items":               {},
		"total_nano_usd":           {},
	}

	seenExact := make(map[string]struct{})
	seenLower := make(map[string]struct{})
	rawValues := make(map[string]json.RawMessage)

	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("pricing: object key must be a string")
		}
		lowerKey := strings.ToLower(key)
		if _, exists := seenLower[lowerKey]; exists {
			return fmt.Errorf("pricing: duplicate or alias receipt field %q", key)
		}
		seenLower[lowerKey] = struct{}{}
		seenExact[key] = struct{}{}

		if _, ok := canonicalKeys[key]; !ok {
			return fmt.Errorf("pricing: unknown receipt field %q", key)
		}

		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
		rawValues[key] = raw
	}

	rawSchemaVer, ok := rawValues["schema_version"]
	if !ok || len(rawSchemaVer) == 0 || bytes.Equal(bytes.TrimSpace(rawSchemaVer), []byte("null")) {
		return fmt.Errorf("pricing: receipt missing or null schema_version")
	}
	var schemaVersion int
	if err := json.Unmarshal(rawSchemaVer, &schemaVersion); err != nil {
		return fmt.Errorf("pricing: invalid schema_version: %w", err)
	}
	if schemaVersion < 1 || schemaVersion > 7 {
		return fmt.Errorf("unsupported pricing receipt contract")
	}

	if schemaVersion >= 7 {
		for _, req := range []string{"method", "method_version", "currency", "rule", "line_items", "total_nano_usd"} {
			raw, ok := rawValues[req]
			if !ok || len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return fmt.Errorf("pricing: receipt missing or null required field %q", req)
			}
		}
	} else {
		for _, req := range []string{"method", "method_version", "currency", "rule", "total_nano_usd"} {
			if raw, ok := rawValues[req]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return fmt.Errorf("pricing: receipt field %q must not be null", req)
			}
		}
	}

	if schemaVersion < 4 {
		if _, ok := rawValues["pricing_mode"]; ok {
			return fmt.Errorf("historical pricing receipt must not contain a pricing mode")
		}
	} else {
		raw, ok := rawValues["pricing_mode"]
		if !ok || len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("pricing: receipt missing or null pricing_mode")
		}
	}

	if schemaVersion < 5 {
		if _, ok := rawValues["price_multipliers"]; ok {
			return fmt.Errorf("historical pricing receipt must not contain price multipliers")
		}
	} else {
		raw, ok := rawValues["price_multipliers"]
		if !ok || len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("price multipliers require a non-null receipt field from v5 onward")
		}
	}

	if schemaVersion < 6 {
		if _, ok := rawValues["base_total_nano_usd"]; ok {
			return fmt.Errorf("historical pricing receipt must not contain a base total")
		}
	} else {
		raw, ok := rawValues["base_total_nano_usd"]
		if !ok || len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("base total requires a non-null receipt field from v6 onward")
		}
	}

	if schemaVersion < 7 {
		if _, ok := rawValues["policy_factors"]; ok {
			return fmt.Errorf("historical pricing receipt must not contain policy factors")
		}
	} else {
		raw, ok := rawValues["policy_factors"]
		if !ok || len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("policy factors require a non-null receipt field from v7 onward")
		}
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

// ValidatePolicyFactors centralizes factor validation for Quote and ValidateReceipt.
func ValidatePolicyFactors(factors []PolicyFactor) error {
	if len(factors) == 0 {
		return fmt.Errorf("pricing: policy factors must not be empty")
	}
	if len(factors) > 200 {
		return fmt.Errorf("pricing: policy factors count %d exceeds maximum limit of 200", len(factors))
	}
	seenGroupIDs := make(map[string]struct{})
	seenCredIDs := make(map[string]struct{})
	var groupRevision *uint64
	var credRevision *uint64
	seenCredentialScope := false
	groupCount := 0
	credCount := 0

	for _, f := range factors {
		if f.RuleID == "" || f.NameSnapshot == "" || !f.Multiplier.Valid() || f.Revision == 0 {
			return fmt.Errorf("pricing: invalid policy factor required fields")
		}
		parsedM, err := ParsePriceMultiplier(f.Factor)
		if err != nil || parsedM != f.Multiplier {
			return fmt.Errorf("pricing: policy factor factor %q does not match multiplier %v", f.Factor, f.Multiplier)
		}
		switch f.BindingScope {
		case "group":
			if seenCredentialScope {
				return fmt.Errorf("pricing: policy factors out of order: group factors must precede credential factors")
			}
			groupCount++
			if groupCount > 100 {
				return fmt.Errorf("pricing: group policy factors count exceeds limit of 100")
			}
			if _, dup := seenGroupIDs[f.RuleID]; dup {
				return fmt.Errorf("pricing: duplicate rule ID %q within group scope", f.RuleID)
			}
			seenGroupIDs[f.RuleID] = struct{}{}
			if groupRevision == nil {
				rev := f.Revision
				groupRevision = &rev
			} else if *groupRevision != f.Revision {
				return fmt.Errorf("pricing: inconsistent revision within group scope: %d vs %d", *groupRevision, f.Revision)
			}
		case "credential":
			seenCredentialScope = true
			credCount++
			if credCount > 100 {
				return fmt.Errorf("pricing: credential policy factors count exceeds limit of 100")
			}
			if _, dup := seenCredIDs[f.RuleID]; dup {
				return fmt.Errorf("pricing: duplicate rule ID %q within credential scope", f.RuleID)
			}
			seenCredIDs[f.RuleID] = struct{}{}
			if credRevision == nil {
				rev := f.Revision
				credRevision = &rev
			} else if *credRevision != f.Revision {
				return fmt.Errorf("pricing: inconsistent revision within credential scope: %d vs %d", *credRevision, f.Revision)
			}
		default:
			return fmt.Errorf("pricing: invalid binding scope %q", f.BindingScope)
		}
	}
	return nil
}

// ValidateReceipt verifies a persisted request-time receipt without consulting
// the mutable current pricing table.
func ValidateReceipt(receipt Receipt) error {
	if (receipt.SchemaVersion != 1 && receipt.SchemaVersion != 2 &&
		receipt.SchemaVersion != 3 && receipt.SchemaVersion != 4 && receipt.SchemaVersion != 5 && receipt.SchemaVersion != 6 && receipt.SchemaVersion != 7) ||
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
	if receipt.SchemaVersion < 7 {
		if receipt.PolicyFactors != nil {
			return fmt.Errorf("historical pricing receipt must not contain policy factors")
		}
	} else {
		if err := ValidatePolicyFactors(receipt.PolicyFactors); err != nil {
			return err
		}
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
	if receipt.SchemaVersion == 6 || receipt.SchemaVersion == 7 {
		if int64(total) != *receipt.BaseTotalNanoUSD {
			return fmt.Errorf("pricing receipt base total mismatch")
		}
		adjusted, ok := applyPriceMultipliers(total, priceMultipliers, receipt.PolicyFactors...)
		if !ok || int64(adjusted) != receipt.TotalNanoUSD {
			return fmt.Errorf("pricing receipt adjusted total mismatch")
		}
	} else if int64(total) != receipt.TotalNanoUSD {
		return fmt.Errorf("pricing receipt total mismatch")
	}
	return nil
}
