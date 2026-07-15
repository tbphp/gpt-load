package errors

import (
	"encoding/json"
	"strings"
)

const (
	// maxErrorBodyLength defines the maximum length of an error message to be stored or returned.
	maxErrorBodyLength = 2048
)

// standardErrorResponse matches formats like: {"error": {"message": "..."}}
type standardErrorResponse struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// vendorErrorResponse matches formats like: {"error_msg": "..."}
type vendorErrorResponse struct {
	ErrorMsg string `json:"error_msg"`
}

// simpleErrorResponse matches formats like: {"error": "..."}
type simpleErrorResponse struct {
	Error string `json:"error"`
}

// rootMessageErrorResponse matches formats like: {"message": "..."}
type rootMessageErrorResponse struct {
	Message string `json:"message"`
}

// ParseUpstreamError attempts to parse a structured error message from an upstream response body
func ParseUpstreamError(body []byte) string {
	// 1. Attempt to parse the standard OpenAI/Gemini format.
	var stdErr standardErrorResponse
	if err := json.Unmarshal(body, &stdErr); err == nil {
		if msg := strings.TrimSpace(stdErr.Error.Message); msg != "" {
			return truncateString(msg, maxErrorBodyLength)
		}
	}

	// 2. Attempt to parse vendor-specific format (e.g., Baidu).
	var vendorErr vendorErrorResponse
	if err := json.Unmarshal(body, &vendorErr); err == nil {
		if msg := strings.TrimSpace(vendorErr.ErrorMsg); msg != "" {
			return truncateString(msg, maxErrorBodyLength)
		}
	}

	// 3. Attempt to parse simple error format.
	var simpleErr simpleErrorResponse
	if err := json.Unmarshal(body, &simpleErr); err == nil {
		if msg := strings.TrimSpace(simpleErr.Error); msg != "" {
			return truncateString(msg, maxErrorBodyLength)
		}
	}

	// 4. Attempt to parse root-level message format.
	var rootMsgErr rootMessageErrorResponse
	if err := json.Unmarshal(body, &rootMsgErr); err == nil {
		if msg := strings.TrimSpace(rootMsgErr.Message); msg != "" {
			return truncateString(msg, maxErrorBodyLength)
		}
	}

	// 5. Graceful Degradation: If all parsing fails, return the raw (but safe) body.
	return truncateString(string(body), maxErrorBodyLength)
}

// truncateString ensures a string does not exceed a maximum length.
func truncateString(s string, maxLength int) string {
	if len(s) > maxLength {
		return s[:maxLength]
	}
	return s
}
