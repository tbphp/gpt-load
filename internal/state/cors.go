package state

import (
	"fmt"
	"net/textproto"
	"net/url"
	"strings"
)

var defaultCORSMethods = []string{
	"GET",
	"POST",
	"PUT",
	"PATCH",
	"DELETE",
	"HEAD",
	"OPTIONS",
}

// CORSConfig controls browser access to the data-plane namespaces. An empty or
// disabled policy never bypasses normal data-plane authentication.
type CORSConfig struct {
	Enabled          bool
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	ExposedHeaders   []string
	AllowCredentials bool
	MaxAgeSeconds    int
}

func defaultCORSConfig() CORSConfig {
	return CORSConfig{
		AllowedMethods: append([]string(nil), defaultCORSMethods...),
		AllowedHeaders: []string{"*"},
		MaxAgeSeconds:  600,
	}
}

func parseCORSConfig(value any) (CORSConfig, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return CORSConfig{}, fmt.Errorf("cors must be an object")
	}
	for key := range object {
		switch key {
		case "enabled",
			"allowed_origins",
			"allowed_methods",
			"allowed_headers",
			"exposed_headers",
			"allow_credentials",
			"max_age":
		default:
			return CORSConfig{}, fmt.Errorf("unknown cors field %q", key)
		}
	}

	config := defaultCORSConfig()
	var err error
	if raw, exists := object["enabled"]; exists {
		config.Enabled, err = strictBoolean("cors.enabled", raw)
		if err != nil {
			return CORSConfig{}, err
		}
	}
	if raw, exists := object["allowed_origins"]; exists {
		config.AllowedOrigins, err = parseCORSOrigins(raw)
		if err != nil {
			return CORSConfig{}, err
		}
	}
	if raw, exists := object["allowed_methods"]; exists {
		config.AllowedMethods, err = parseCORSMethods(raw)
		if err != nil {
			return CORSConfig{}, err
		}
	}
	if raw, exists := object["allowed_headers"]; exists {
		config.AllowedHeaders, err = parseCORSHeaderNames("cors.allowed_headers", raw)
		if err != nil {
			return CORSConfig{}, err
		}
	}
	if raw, exists := object["exposed_headers"]; exists {
		config.ExposedHeaders, err = parseCORSHeaderNames("cors.exposed_headers", raw)
		if err != nil {
			return CORSConfig{}, err
		}
	}
	if raw, exists := object["allow_credentials"]; exists {
		config.AllowCredentials, err = strictBoolean("cors.allow_credentials", raw)
		if err != nil {
			return CORSConfig{}, err
		}
	}
	if raw, exists := object["max_age"]; exists {
		config.MaxAgeSeconds, err = nonNegativeWholeNumber("cors.max_age", raw)
		if err != nil {
			return CORSConfig{}, err
		}
	}

	if config.Enabled && len(config.AllowedOrigins) == 0 {
		return CORSConfig{}, fmt.Errorf("cors.allowed_origins must not be empty when cors is enabled")
	}
	if config.Enabled && len(config.AllowedMethods) == 0 {
		return CORSConfig{}, fmt.Errorf("cors.allowed_methods must not be empty when cors is enabled")
	}
	if config.Enabled && len(config.AllowedHeaders) == 0 {
		return CORSConfig{}, fmt.Errorf("cors.allowed_headers must not be empty when cors is enabled")
	}
	if config.AllowCredentials && containsExact(config.AllowedOrigins, "*") {
		return CORSConfig{}, fmt.Errorf("cors.allow_credentials cannot be used with wildcard origin")
	}
	if config.AllowCredentials && containsExact(config.ExposedHeaders, "*") {
		return CORSConfig{}, fmt.Errorf("cors.allow_credentials cannot be used with wildcard exposed headers")
	}
	return config, nil
}

func parseCORSOrigins(value any) ([]string, error) {
	values, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("cors.allowed_origins must be an array")
	}
	parsed := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, raw := range values {
		origin, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("cors.allowed_origins[%d] must be a string", index)
		}
		origin = strings.TrimSpace(origin)
		if !validCORSOrigin(origin) {
			return nil, fmt.Errorf("cors.allowed_origins[%d] is invalid", index)
		}
		if _, duplicate := seen[origin]; duplicate {
			return nil, fmt.Errorf("cors.allowed_origins contains duplicate origin %q", origin)
		}
		seen[origin] = struct{}{}
		parsed = append(parsed, origin)
	}
	if len(parsed) > 1 && containsExact(parsed, "*") {
		return nil, fmt.Errorf("cors.allowed_origins wildcard must be the only origin")
	}
	return parsed, nil
}

func parseCORSMethods(value any) ([]string, error) {
	values, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("cors.allowed_methods must be an array")
	}
	parsed := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, raw := range values {
		method, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("cors.allowed_methods[%d] must be a string", index)
		}
		method = strings.ToUpper(strings.TrimSpace(method))
		if method == "*" || !validHTTPHeaderName(method) {
			return nil, fmt.Errorf("cors.allowed_methods[%d] is invalid", index)
		}
		if _, duplicate := seen[method]; duplicate {
			return nil, fmt.Errorf("cors.allowed_methods contains duplicate method %q", method)
		}
		seen[method] = struct{}{}
		parsed = append(parsed, method)
	}
	return parsed, nil
}

func validCORSOrigin(origin string) bool {
	if origin == "*" || origin == "null" {
		return true
	}
	if origin == "" || strings.ContainsAny(origin, " \t\r\n,") || !validHTTPHeaderValue(origin) {
		return false
	}
	parsed, err := url.Parse(origin)
	return err == nil &&
		parsed.Scheme != "" &&
		parsed.Host != "" &&
		parsed.User == nil &&
		parsed.Path == "" &&
		parsed.RawQuery == "" &&
		parsed.Fragment == "" &&
		parsed.Opaque == ""
}

func parseCORSHeaderNames(path string, value any) ([]string, error) {
	values, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array", path)
	}
	parsed := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, raw := range values {
		name, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("%s[%d] must be a string", path, index)
		}
		name = strings.TrimSpace(name)
		if name != "*" && !validHTTPHeaderName(name) {
			return nil, fmt.Errorf("%s[%d] contains invalid header name %q", path, index, name)
		}
		identity := strings.ToLower(name)
		if _, duplicate := seen[identity]; duplicate {
			return nil, fmt.Errorf("%s contains duplicate header %q", path, name)
		}
		seen[identity] = struct{}{}
		if name != "*" {
			name = textproto.CanonicalMIMEHeaderKey(name)
		}
		parsed = append(parsed, name)
	}
	if len(parsed) > 1 && containsExact(parsed, "*") {
		return nil, fmt.Errorf("%s wildcard must be the only header", path)
	}
	return parsed, nil
}

func containsExact(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
