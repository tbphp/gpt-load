package errors

import (
	"strings"
)

// ignorableCountSubstrings contains a list of substrings that indicate an error
var ignorableCountSubstrings = []string{
	"resource has been exhausted",
	"please reduce the length of the messages",
}

// IsIgnorableCount checks if the given error message contains substrings
func IsIgnorableCount(errorMsg string) bool {
	if errorMsg == "" {
		return false
	}

	errorLower := strings.ToLower(errorMsg)

	for _, pattern := range ignorableCountSubstrings {
		if strings.Contains(errorLower, pattern) {
			return true
		}
	}

	return false
}
