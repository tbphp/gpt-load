package redact

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// ExtractErrorMessage returns the human-readable message from an upstream
// error body. It deliberately returns no raw JSON or markup when a
// known message field is unavailable, so callers can fall back to a safe
// generic message instead.
func ExtractErrorMessage(body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || !utf8.Valid(body) || containsUnsafeTextControl(body) {
		return ""
	}
	if message, isJSON := extractJSONErrorMessage(body); isJSON {
		return safeErrorMessageText(message)
	}
	text := strings.TrimSpace(string(body))
	if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") || looksLikeMarkup(text) {
		return ""
	}
	return safeErrorMessageText(text)
}

func extractJSONErrorMessage(body []byte) (string, bool) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return "", false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", false
	}
	if nested, ok := payload["error"].(map[string]any); ok {
		if message := firstErrorMessage(nested); message != "" {
			return message, true
		}
	}
	if message := firstErrorMessage(payload); message != "" {
		return message, true
	}
	if message, ok := payload["error"].(string); ok {
		return strings.TrimSpace(message), true
	}
	return "", true
}

func firstErrorMessage(payload map[string]any) string {
	for _, field := range []string{"message", "detail", "title"} {
		if value, ok := payload[field].(string); ok {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func safeErrorMessageText(value string) string {
	for _, runeValue := range value {
		if runeValue == 0 || (runeValue < 0x20 && runeValue != '\t' && runeValue != '\n' && runeValue != '\r') ||
			(runeValue >= 0x7f && runeValue <= 0x9f) {
			return ""
		}
	}
	return strings.TrimSpace(value)
}

func containsUnsafeTextControl(body []byte) bool {
	for _, value := range body {
		if value == 0 || (value < 0x20 && value != '\t' && value != '\n' && value != '\r') {
			return true
		}
	}
	return false
}

func looksLikeMarkup(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), "<")
}
