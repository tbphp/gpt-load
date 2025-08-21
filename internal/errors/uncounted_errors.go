package errors

import "strings"

// unCountedSubstrings contains a list of substrings that indicate an error
var unCountedSubstrings = []string{
	"resource has been exhausted",
	"please reduce the length of the messages",
}

// IsUnCounted checks if the given error message contains substrings
func IsUnCounted(errorMsg string) bool {
	if errorMsg == "" {
		return false
	}

	errorLower := strings.ToLower(errorMsg)

	for _, pattern := range unCountedSubstrings {
		if strings.Contains(errorLower, pattern) {
			return true
		}
	}

	return false
}
