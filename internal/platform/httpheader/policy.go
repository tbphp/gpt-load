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

func normalizeName(name string) string {
	if name == "" {
		return ""
	}
	return strings.ToLower(textproto.CanonicalMIMEHeaderKey(name))
}
