// Package redact removes credentials from text before it crosses an
// observability or downstream-response boundary.
package redact

import (
	"bytes"
	"regexp"
	"strings"
)

const Placeholder = "[REDACTED]"

var controlledAccessKeyLocator = regexp.MustCompile(
	`^access-key:(?:[1-9][0-9]*|unknown)$`,
)

type replacement struct {
	pattern *regexp.Regexp
	value   string
}

type Redactor struct {
	replacements []replacement
}

func New() *Redactor {
	return &Redactor{replacements: []replacement{
		{
			pattern: regexp.MustCompile(`(?i)\b(?:sk|gl)-[a-z0-9][a-z0-9._-]{7,}\b`),
			value:   Placeholder,
		},
		{
			pattern: regexp.MustCompile(`(?i)(^|[\s,{?&])(["']?authorization["']?\s*[:=]\s*)("(?:\\.|[^"\\])*")`),
			value:   `${1}${2}"` + Placeholder + `"`,
		},
		{
			pattern: regexp.MustCompile(`(?i)(^|[\s,{?&])(["']?authorization["']?\s*[:=]\s*)('(?:\\.|[^'\\])*')`),
			value:   `${1}${2}'` + Placeholder + `'`,
		},
		{
			pattern: regexp.MustCompile(`(?im)(^|[\s,{?&])(["']?authorization["']?\s*[:=]\s*)([^"'\r\n&}][^\r\n&}]*)`),
			value:   `${1}${2}` + Placeholder,
		},
		{
			pattern: regexp.MustCompile(`(?i)(^|[\s,({?&])([\"']?(?:api[_-]?key|x-api-key|x-goog-api-key|access[_-]?key|client[_-]?secret|refresh[_-]?token|id[_-]?token|access[_-]?token|key|token)[\"']?\s*[:=]\s*[\"']?)[^\"',\s&})\[\]]+`),
			value:   `${1}${2}` + Placeholder,
		},
		{
			pattern: regexp.MustCompile(`(?i)(^|[\s,({?&])([\"']?(?:cookie|set-cookie|password|passcode|secret|credential|session|signature)[\"']?\s*[:=]\s*[\"']?)[^\"',\s&})\[\]]+`),
			value:   `${1}${2}` + Placeholder,
		},
		{
			pattern: regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+(?:[a-z0-9_-]*[-._~+/=][a-z0-9._~+/=-]*|[a-z0-9]{20,})`),
			value:   Placeholder,
		},
		{
			pattern: regexp.MustCompile(`(?i)(https?://[^\s?#]+)\?[^\s#]*`),
			value:   `${1}?` + Placeholder,
		},
	}}
}

func (r *Redactor) String(text string, knownSecrets ...string) string {
	if r == nil || text == "" {
		return text
	}
	result := text
	for _, secret := range knownSecrets {
		if secret != "" {
			result = strings.ReplaceAll(result, secret, Placeholder)
		}
	}
	if controlledAccessKeyLocator.MatchString(result) {
		return result
	}
	for _, item := range r.replacements {
		result = item.pattern.ReplaceAllString(result, item.value)
	}
	return result
}

func (r *Redactor) Bytes(body []byte, knownSecrets ...string) []byte {
	if body == nil {
		return nil
	}
	return bytes.Clone([]byte(r.String(string(body), knownSecrets...)))
}

func sensitiveFieldName(name string) bool {
	normalized := strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(name))
	switch normalized {
	case "authorization", "apikey", "xapikey", "xgoogapikey", "accesskey", "clientsecret", "refreshtoken", "idtoken", "accesstoken", "cookie", "setcookie", "password", "passcode", "secret", "session", "signature", "key", "token":
		return true
	default:
		return false
	}
}
