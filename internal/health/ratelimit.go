package health

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxRateLimitResetDelay = time.Hour

type resetHeaderFamily struct {
	values     []string
	retryAfter bool
}

func ParseRateLimitReset(header http.Header, now time.Time) (time.Time, bool) {
	families := []resetHeaderFamily{
		{values: matchingHeaderValues(header, func(name string) bool {
			return name == "retry-after"
		}), retryAfter: true},
		{values: matchingHeaderValues(header, func(name string) bool {
			return strings.HasPrefix(name, "anthropic-ratelimit-") &&
				strings.HasSuffix(name, "-reset")
		})},
		{values: matchingHeaderValues(header, func(name string) bool {
			return name == "x-ratelimit-reset" ||
				strings.HasPrefix(name, "x-ratelimit-reset-")
		})},
	}
	for _, family := range families {
		if reset, ok := latestValidReset(family.values, now, family.retryAfter); ok {
			return reset, true
		}
	}
	return time.Time{}, false
}

func matchingHeaderValues(
	header http.Header,
	matches func(string) bool,
) []string {
	var values []string
	for name, entries := range header {
		if matches(strings.ToLower(name)) {
			values = append(values, entries...)
		}
	}
	return values
}

func latestValidReset(
	values []string,
	now time.Time,
	retryAfter bool,
) (time.Time, bool) {
	limit := now.Add(maxRateLimitResetDelay)
	var latest time.Time
	for _, value := range values {
		var reset time.Time
		var ok bool
		if retryAfter {
			reset, ok = parseRetryAfter(value, now)
		} else {
			reset, ok = parseResetValue(value, now)
		}
		if !ok || !reset.After(now) || reset.After(limit) {
			continue
		}
		if latest.IsZero() || reset.After(latest) {
			latest = reset
		}
	}
	return latest, !latest.IsZero()
}

func parseRetryAfter(value string, now time.Time) (time.Time, bool) {
	trimmed := strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		if seconds <= 0 || seconds > int64(maxRateLimitResetDelay/time.Second) {
			return time.Time{}, false
		}
		return now.Add(time.Duration(seconds) * time.Second), true
	}
	reset, err := http.ParseTime(trimmed)
	return reset, err == nil
}

func parseResetValue(value string, now time.Time) (time.Time, bool) {
	trimmed := strings.TrimSpace(value)
	if reset, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return reset, true
	}
	if duration, err := time.ParseDuration(trimmed); err == nil {
		if duration <= 0 || duration > maxRateLimitResetDelay {
			return time.Time{}, false
		}
		return now.Add(duration), true
	}
	numeric, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	switch {
	case numeric >= 1_000_000_000_000:
		return time.UnixMilli(numeric), true
	case numeric >= 1_000_000_000:
		return time.Unix(numeric, 0), true
	case numeric > 0 && numeric <= int64(maxRateLimitResetDelay/time.Second):
		return now.Add(time.Duration(numeric) * time.Second), true
	default:
		return time.Time{}, false
	}
}
