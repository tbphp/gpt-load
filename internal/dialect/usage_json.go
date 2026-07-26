package dialect

import (
	"bytes"
	"encoding/json"
	"strconv"

	"gpt-load/internal/usage"
)

func usageInteger(object map[string]json.RawMessage, field string, required bool) (*int64, usage.Diagnostics) {
	raw, exists := object[field]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if required {
			return nil, usageDiagnostic(usage.DiagnosticMissingRequiredField)
		}
		return nil, usage.Diagnostics{}
	}

	value, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		return nil, usageDiagnostic(usage.DiagnosticInvalidNumber)
	}
	if value < 0 {
		zero := int64(0)
		return &zero, usageDiagnostic(usage.DiagnosticNegativeValue)
	}
	return &value, usage.Diagnostics{}
}

func usageOptionalObject(object map[string]json.RawMessage, field string) (map[string]json.RawMessage, usage.Diagnostics) {
	raw, exists := object[field]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, usage.Diagnostics{}
	}
	nested, err := decodeJSONObject(raw)
	if err != nil {
		return nil, usageDiagnostic(usage.DiagnosticInvalidNumber)
	}
	return nested, usage.Diagnostics{}
}

func usageFieldPresent(object map[string]json.RawMessage, field string) bool {
	raw, exists := object[field]
	return exists && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func usageNormalizedTotal(reported *int64, tokens usage.Tokens, diagnostics *usage.Diagnostics) {
	if reported == nil {
		return
	}
	normalized, ok := usage.CheckedTotal(tokens)
	if !ok {
		diagnostics.Add(usage.DiagnosticInvalidNumber)
		return
	}
	if *reported != normalized {
		diagnostics.SetTotalDelta(*reported - normalized)
	}
}

func usageDiagnostic(code usage.DiagnosticCode) usage.Diagnostics {
	var diagnostics usage.Diagnostics
	diagnostics.Add(code)
	return diagnostics
}
