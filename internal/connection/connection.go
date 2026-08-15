// Package connection defines the product vocabulary for upstream credential
// connection types shared by persistence, routing and channel metadata.
package connection

import "strings"

// Stable product connection type values.
const (
	APIKey       = "api_key"
	Subscription = "subscription"
)

// Normalize trims an explicitly configured connection type.
func Normalize(value string) string {
	return strings.TrimSpace(value)
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
