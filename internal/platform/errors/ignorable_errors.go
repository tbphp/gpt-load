package errors

import (
	"strings"
)

// ignorableErrorSubstrings contains a list of substrings that indicate an error
// can be safely ignored. These typically occur when a client disconnects prematurely.
var ignorableErrorSubstrings = []string{
	"context canceled",
	"connection reset by peer",
	"broken pipe",
	"use of closed network connection",
	"request canceled",
}

// IsIgnorableError checks if the given error is a common, non-critical error
// that can occur when a client disconnects. This is used to prevent logging
// unnecessary errors and to avoid marking keys as failed for client-side issues.
func IsIgnorableError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	for _, sub := range ignorableErrorSubstrings {
		if strings.Contains(errStr, sub) {
			return true
		}
	}
	return false
}
