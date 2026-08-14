// Package connection defines the product vocabulary for upstream credential
// connection types shared by persistence, routing and channel metadata.
package connection

import "strings"

// Stable product connection type values.
const (
	APIKey       = "api_key"
	Subscription = "subscription"
)

// Normalize keeps empty legacy values compatible with API-key groups.
func Normalize(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return APIKey
	}
	return value
}

// Valid reports whether value names a supported product connection type.
func Valid(value string) bool {
	switch Normalize(value) {
	case APIKey, Subscription:
		return true
	default:
		return false
	}
}
