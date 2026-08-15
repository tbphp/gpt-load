package spec

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// NormalizeNonEmpty trims and validates a required text or secret field.
func NormalizeNonEmpty(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("must not be empty")
	}
	return value, nil
}

// NormalizeBaseURL canonicalizes a configured HTTP(S) endpoint.
func NormalizeBaseURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("must not be empty")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed == nil || parsed.Opaque != "" || parsed.User != nil || parsed.Host == "" || parsed.Fragment != "" {
		return "", fmt.Errorf("must be an absolute HTTP(S) URL without credentials or fragment")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("must be an absolute HTTP(S) URL without credentials or fragment")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return "", fmt.Errorf("must not contain query parameters")
	}
	parsed.Host = strings.ToLower(parsed.Host)
	if (parsed.Scheme == "https" && parsed.Port() == "443") ||
		(parsed.Scheme == "http" && parsed.Port() == "80") {
		hostname := parsed.Hostname()
		if strings.Contains(hostname, ":") {
			hostname = "[" + hostname + "]"
		}
		parsed.Host = hostname
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = strings.TrimRight(parsed.RawPath, "/")
	parsed.ForceQuery = false
	return parsed.String(), nil
}

// NormalizeCloudIdentifier rejects whitespace and control characters in a
// provider-owned cloud configuration value.
func NormalizeCloudIdentifier(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 255 {
		return "", fmt.Errorf("must be between 1 and 255 bytes")
	}
	for _, character := range value {
		if character <= ' ' || character == 0x7f {
			return "", fmt.Errorf("must not contain whitespace or control characters")
		}
	}
	return value, nil
}

// NormalizeServiceAccountJSON validates and canonicalizes a Google service
// account credential without retaining fields outside the credential object.
func NormalizeServiceAccountJSON(value string) (string, error) {
	var object map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(value)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&object); err != nil || object == nil {
		return "", fmt.Errorf("must be a valid JSON object")
	}
	for _, key := range []string{"type", "project_id", "client_email", "private_key"} {
		raw, exists := object[key]
		if !exists {
			return "", fmt.Errorf("must contain %s", key)
		}
		var field string
		if json.Unmarshal(raw, &field) != nil || strings.TrimSpace(field) == "" {
			return "", fmt.Errorf("must contain non-empty %s", key)
		}
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return "", fmt.Errorf("must be a valid JSON object")
	}
	return string(encoded), nil
}
