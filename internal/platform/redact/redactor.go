// Package redact removes credentials from text before it crosses an
// observability or downstream-response boundary.
package redact

import (
	"bytes"
	"regexp"
	"strings"
)

const Placeholder = "[REDACTED]"

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
			pattern: regexp.MustCompile(`(?i)(^|[\s,{?&])(authorization\s*[:=]\s*(?:bearer\s+)?)[^\s,\"&}]+`),
			value:   `${1}${2}` + Placeholder,
		},
		{
			pattern: regexp.MustCompile(`(?i)(^|[\s,{?&])([\"']?(?:api[_-]?key|x-api-key|x-goog-api-key|access[_-]?key|key|token)[\"']?\s*[:=]\s*[\"']?)[^\"',\s&}]+`),
			value:   `${1}${2}` + Placeholder,
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
