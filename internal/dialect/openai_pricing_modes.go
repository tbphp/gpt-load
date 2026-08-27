package dialect

import (
	"encoding/json"
	"fmt"
	"strings"

	"gpt-load/internal/pricing"
	"gpt-load/internal/usage"
)

func openAIRequestPricing(body []byte) (pricing.Mode, usage.Diagnostics, error) {
	diagnostics := usage.Diagnostics{}
	root, err := decodeJSONObject(body)
	if err != nil {
		return "", diagnostics, nil
	}
	for field := range root {
		if strings.EqualFold(field, "service_tier") && field != "service_tier" {
			return "", diagnostics, fmt.Errorf("service_tier field must use lowercase service_tier")
		}
	}
	if value, ok := jsonString(root, "service_tier"); ok &&
		strings.EqualFold(strings.TrimSpace(value), "ultrafast") {
		return "", diagnostics, fmt.Errorf("service_tier ultrafast is not supported")
	}
	mode, supported := openAIRequestedPricingMode(root)
	if !supported || jsonStringEquals(root, "speed", "fast") ||
		openAIUnsupportedReasoningMode(root) {
		diagnostics.Add(usage.DiagnosticUnsupportedBillableDetail)
	}
	return mode, diagnostics, nil
}

func openAIRequestedPricingMode(root map[string]json.RawMessage) (pricing.Mode, bool) {
	value, ok := jsonString(root, "service_tier")
	if !ok {
		return "", true
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return "", true
	case "default":
		return pricing.ModeStandard, true
	case "fast", "priority":
		return pricing.ModeFast, true
	default:
		return "", false
	}
}

func openAIUnsupportedReasoningMode(root map[string]json.RawMessage) bool {
	raw, exists := root["reasoning"]
	if !exists {
		return false
	}
	object, err := decodeJSONObject(raw)
	return err == nil && jsonStringEquals(object, "mode", "pro")
}

func jsonStringEquals(object map[string]json.RawMessage, field, want string) bool {
	value, ok := jsonString(object, field)
	return ok && strings.EqualFold(value, want)
}

func jsonString(object map[string]json.RawMessage, field string) (string, bool) {
	raw, exists := object[field]
	if !exists {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}
