package pricing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// decodeStrictObjectFields 将 JSON 对象解析为字段→原始值映射，拒绝重复与未知字段。
func decodeStrictObjectFields(data []byte, canonical map[string]struct{}, subject string) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("pricing: %s must be a JSON object", subject)
	}
	raw := make(map[string]json.RawMessage)
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key := tok.(string)
		if _, exists := raw[key]; exists {
			return nil, fmt.Errorf("pricing: duplicate %s field %q", subject, key)
		}
		if _, ok := canonical[key]; !ok {
			return nil, fmt.Errorf("pricing: unknown %s field %q", subject, key)
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		raw[key] = value
	}
	return raw, nil
}

func isJSONNull(value json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

func missingOrNull(raw map[string]json.RawMessage, key string) bool {
	value, ok := raw[key]
	return !ok || len(value) == 0 || isJSONNull(value)
}

// requireFromVersion 要求字段自指定 schema 版本起必须存在且非 null，之前版本必须完全缺席。
func requireFromVersion(raw map[string]json.RawMessage, schemaVersion, from int, field, absentMsg, missingMsg string) error {
	if schemaVersion < from {
		if _, ok := raw[field]; ok {
			return errors.New(absentMsg)
		}
		return nil
	}
	if missingOrNull(raw, field) {
		return errors.New(missingMsg)
	}
	return nil
}

// UnmarshalJSON 区分缺省字段与显式 null，并保持历史版本的字段边界。
func (receipt *Receipt) UnmarshalJSON(data []byte) error {
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

	rawValues, err := decodeStrictObjectFields(data, canonicalKeys, "receipt")
	if err != nil {
		return err
	}

	if missingOrNull(rawValues, "schema_version") {
		return fmt.Errorf("pricing: receipt missing or null schema_version")
	}
	var schemaVersion int
	if err := json.Unmarshal(rawValues["schema_version"], &schemaVersion); err != nil {
		return fmt.Errorf("pricing: invalid schema_version: %w", err)
	}
	if schemaVersion < 1 || schemaVersion > 7 {
		return fmt.Errorf("unsupported pricing receipt contract")
	}

	if schemaVersion >= 7 {
		for _, req := range []string{"method", "method_version", "currency", "rule", "line_items", "total_nano_usd"} {
			if missingOrNull(rawValues, req) {
				return fmt.Errorf("pricing: receipt missing or null required field %q", req)
			}
		}
	} else {
		for _, req := range []string{"method", "method_version", "currency", "rule", "total_nano_usd"} {
			if raw, ok := rawValues[req]; ok && isJSONNull(raw) {
				return fmt.Errorf("pricing: receipt field %q must not be null", req)
			}
		}
	}

	versioned := []struct {
		from       int
		field      string
		absentMsg  string
		missingMsg string
	}{
		{4, "pricing_mode", "historical pricing receipt must not contain a pricing mode", "pricing: receipt missing or null pricing_mode"},
		{5, "price_multipliers", "historical pricing receipt must not contain price multipliers", "price multipliers require a non-null receipt field from v5 onward"},
		{6, "base_total_nano_usd", "historical pricing receipt must not contain a base total", "base total requires a non-null receipt field from v6 onward"},
		{7, "policy_factors", "historical pricing receipt must not contain policy factors", "policy factors require a non-null receipt field from v7 onward"},
	}
	for _, req := range versioned {
		if err := requireFromVersion(rawValues, schemaVersion, req.from, req.field, req.absentMsg, req.missingMsg); err != nil {
			return err
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
	if receipt.SchemaVersion < 1 || receipt.SchemaVersion > 7 ||
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
