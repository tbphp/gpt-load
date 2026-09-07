package httpheader

import (
	"net/textproto"
	"strings"
)

var credentialNames = map[string]struct{}{
	"authorization":       {},
	"proxy-authorization": {},
	"api-key":             {},
	"x-api-key":           {},
	"x-goog-api-key":      {},
}

var forbiddenRequestRuleNames = map[string]struct{}{
	"connection":        {},
	"proxy-connection":  {},
	"keep-alive":        {},
	"te":                {},
	"trailer":           {},
	"transfer-encoding": {},
	"upgrade":           {},
	"cookie":            {},
	"cookie2":           {},
	"accept-encoding":   {},
	"content-encoding":  {},
}

var forbiddenResponseRuleNames = map[string]struct{}{
	"connection":                       {},
	"proxy-connection":                 {},
	"keep-alive":                       {},
	"te":                               {},
	"trailer":                          {},
	"transfer-encoding":                {},
	"upgrade":                          {},
	"content-encoding":                 {},
	"content-length":                   {},
	"content-range":                    {},
	"content-type":                     {},
	"date":                             {},
	"server":                           {},
	"set-cookie":                       {},
	"set-cookie2":                      {},
	"vary":                             {},
	"access-control-allow-origin":      {},
	"access-control-allow-methods":     {},
	"access-control-allow-headers":     {},
	"access-control-allow-credentials": {},
	"access-control-expose-headers":    {},
	"access-control-max-age":           {},
}

func IsCredentialName(name string) bool {
	normalized := normalizeName(name)
	if normalized == "" {
		return false
	}
	_, exists := credentialNames[normalized]
	return exists
}

func IsForbiddenRequestRuleName(name string) bool {
	normalized := normalizeName(name)
	if normalized == "" {
		return false
	}
	if strings.HasPrefix(normalized, "proxy-") {
		return true
	}
	_, exists := forbiddenRequestRuleNames[normalized]
	return exists
}

func IsForbiddenRequestRuleSetName(name string) bool {
	return IsForbiddenRequestRuleName(name)
}

// IsForbiddenResponseRuleName reports whether a user-managed downstream rule
// could interfere with HTTP framing, gateway-owned metadata, credentials, or
// the dedicated CORS policy.
func IsForbiddenResponseRuleName(name string) bool {
	normalized := normalizeName(name)
	if normalized == "" {
		return false
	}
	if strings.HasPrefix(normalized, "proxy-") ||
		strings.HasPrefix(normalized, "x-gptload-") ||
		IsCredentialName(normalized) {
		return true
	}
	_, exists := forbiddenResponseRuleNames[normalized]
	return exists
}

func normalizeName(name string) string {
	if name == "" {
		return ""
	}
	return strings.ToLower(textproto.CanonicalMIMEHeaderKey(name))
}
