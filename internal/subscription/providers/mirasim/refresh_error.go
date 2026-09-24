package mirasim

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RefreshError classifies a private token-refresh failure without retaining or
// reflecting the upstream body, which may contain sensitive material.
type RefreshError struct {
	status     int
	code       string
	retryAfter *time.Duration
	retryable  bool
	cause      error
}

func newRefreshHTTPError(status int, headers http.Header, body []byte) *RefreshError {
	return &RefreshError{
		status:     status,
		code:       refreshErrorCode(body),
		retryAfter: parseRetryAfter(headers, time.Now()),
		retryable:  status == http.StatusTooManyRequests || status >= http.StatusInternalServerError,
	}
}

func newRefreshTransportError(cause error) *RefreshError {
	return &RefreshError{retryable: true, cause: cause}
}

func newRefreshProtocolError(code string, cause error) *RefreshError {
	return &RefreshError{status: http.StatusBadGateway, code: safeErrorCode(code), retryable: true, cause: cause}
}

func (e *RefreshError) Error() string {
	if e == nil {
		return "Mirasim token refresh failed"
	}
	message := "Mirasim token refresh failed"
	if e.status > 0 {
		message = fmt.Sprintf("Mirasim token refresh returned HTTP %d", e.status)
	}
	if e.code != "" {
		message += " (" + e.code + ")"
	}
	if e.status == 0 && e.cause != nil {
		message += ": " + e.cause.Error()
	}
	return message
}

func (e *RefreshError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *RefreshError) StatusCode() int {
	if e == nil {
		return 0
	}
	return e.status
}

func (e *RefreshError) RetryAfter() *time.Duration {
	if e == nil || e.retryAfter == nil {
		return nil
	}
	value := *e.retryAfter
	return &value
}

func (e *RefreshError) Retryable() bool {
	return e != nil && e.retryable
}

func refreshErrorCode(body []byte) string {
	var payload map[string]any
	if errDecode := json.Unmarshal(body, &payload); errDecode != nil {
		return ""
	}
	for _, key := range []string{"code", "type"} {
		if code := safeErrorCode(stringValue(payload[key])); code != "" {
			return code
		}
	}
	if nested, ok := payload["error"].(map[string]any); ok {
		for _, key := range []string{"code", "type"} {
			if code := safeErrorCode(stringValue(nested[key])); code != "" {
				return code
			}
		}
	}
	if text, ok := payload["error"].(string); ok {
		return safeErrorCode(text)
	}
	return ""
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func safeErrorCode(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return ""
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return ""
	}
	return value
}

func parseRetryAfter(headers http.Header, now time.Time) *time.Duration {
	if headers == nil {
		return nil
	}
	value := strings.TrimSpace(headers.Get("Retry-After"))
	if value == "" {
		return nil
	}
	if seconds, errSeconds := strconv.ParseInt(value, 10, 64); errSeconds == nil && seconds >= 0 {
		duration := time.Duration(seconds) * time.Second
		return &duration
	}
	if retryAt, errDate := http.ParseTime(value); errDate == nil {
		duration := retryAt.Sub(now)
		if duration < 0 {
			duration = 0
		}
		return &duration
	}
	return nil
}
