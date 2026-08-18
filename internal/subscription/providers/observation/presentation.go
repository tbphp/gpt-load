package observation

import (
	"strconv"
	"strings"
	"unicode"
)

// DisplayName turns provider identifiers into a stable human-readable fallback.
// Known product aliases remain provider-owned; this helper only formats unknown IDs.
func DisplayName(value string) string {
	fields := strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(strings.TrimSpace(value)))
	for index, field := range fields {
		normalized := strings.ToLower(field)
		switch normalized {
		case "ai", "api", "gpt", "oauth":
			fields[index] = strings.ToUpper(normalized)
		default:
			runes := []rune(normalized)
			if len(runes) > 0 {
				runes[0] = unicode.ToUpper(runes[0])
			}
			fields[index] = string(runes)
		}
	}
	return strings.Join(fields, " ")
}

func SafeID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var result strings.Builder
	lastSeparator := false
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			result.WriteRune(character)
			lastSeparator = false
		} else if result.Len() > 0 && !lastSeparator {
			result.WriteByte('-')
			lastSeparator = true
		}
	}
	return strings.Trim(result.String(), "-")
}

func PeriodLabel(seconds int64) string {
	if seconds <= 0 {
		return ""
	}
	const (
		minute = int64(60)
		hour   = 60 * minute
		day    = 24 * hour
	)
	switch {
	case seconds%day == 0:
		return strconv.FormatInt(seconds/day, 10) + "d"
	case seconds%hour == 0:
		return strconv.FormatInt(seconds/hour, 10) + "h"
	case seconds%minute == 0:
		return strconv.FormatInt(seconds/minute, 10) + "min"
	default:
		return strconv.FormatInt(seconds, 10) + "s"
	}
}

func WindowLabel(subject string, seconds int64) string {
	subject = strings.TrimSpace(subject)
	period := PeriodLabel(seconds)
	if subject == "" {
		return period
	}
	if period == "" {
		return subject
	}
	return subject + " · " + period
}
