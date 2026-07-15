package utils

import (
	"fmt"
	"strings"
)

// MaskAPIKey masks an API key for safe logging.
func MaskAPIKey(key string) string {
	length := len(key)
	if length <= 8 {
		return key
	}
	return fmt.Sprintf("%s****%s", key[:4], key[length-4:])
}

// RedactSecret replaces every occurrence of secret in text with its masked form.
func RedactSecret(text, secret string) string {
	if secret == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, MaskAPIKey(secret))
}

// TruncateString shortens a string to a maximum length.
func TruncateString(s string, maxLength int) string {
	if len(s) > maxLength {
		return s[:maxLength]
	}
	return s
}

// SplitAndTrim splits a string by a separator
func SplitAndTrim(s string, sep string) []string {
	if s == "" {
		return []string{}
	}

	parts := strings.Split(s, sep)
	result := make([]string, 0, len(parts))

	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}

	return result
}

// StringToSet converts a separator-delimited string into a set
func StringToSet(s string, sep string) map[string]struct{} {
	parts := SplitAndTrim(s, sep)
	if len(parts) == 0 {
		return nil
	}

	set := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		set[part] = struct{}{}
	}
	return set
}
