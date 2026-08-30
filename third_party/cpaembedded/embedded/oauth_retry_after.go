package embedded

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxOAuthRetryAfter = time.Hour

func boundedOAuthRetryAfter(header http.Header, now time.Time) time.Duration {
	var longest time.Duration
	for _, value := range header.Values("Retry-After") {
		if delay := boundedOAuthRetryAfterValue(value, now); delay > longest {
			longest = delay
		}
	}
	return longest
}

func boundedOAuthRetryAfterValue(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	deltaSeconds := true
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			deltaSeconds = false
			break
		}
	}
	if deltaSeconds {
		seconds, err := strconv.ParseUint(value, 10, 64)
		if err != nil || seconds > uint64(maxOAuthRetryAfter/time.Second) {
			return maxOAuthRetryAfter
		}
		if seconds == 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	retryAt, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	delay := retryAt.Sub(now)
	if delay <= 0 {
		return 0
	}
	if delay > maxOAuthRetryAfter {
		return maxOAuthRetryAfter
	}
	return delay
}
