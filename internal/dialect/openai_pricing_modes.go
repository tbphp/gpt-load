package dialect

import (
	"encoding/json"
	"strings"

	"gpt-load/internal/usage"
)

func openAIRequestPricingDiagnostics(body []byte) usage.Diagnostics {
	diagnostics := usage.Diagnostics{}
	root, err := decodeJSONObject(body)
	if err != nil {
		return diagnostics
	}
	if openAIUnsupportedServiceTier(root) ||
		jsonStringEquals(root, "speed", "fast") ||
		openAIUnsupportedReasoningMode(root) {
		diagnostics.Add(usage.DiagnosticUnsupportedBillableDetail)
	}
	return diagnostics
}

func openAIUnsupportedServiceTier(root map[string]json.RawMessage) bool {
	value, ok := jsonString(root, "service_tier")
	if !ok {
		return false
	}
	switch strings.ToLower(value) {
	case "", "auto", "default":
		return false
	default:
		return true
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
