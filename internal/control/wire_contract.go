package control

import (
	"fmt"
	"strconv"
	"time"

	"gpt-load/internal/platform/epochms"
)

const maxSafeInteger = int64(9_007_199_254_740_991)

func safeEpochMilliseconds(value time.Time) (int64, error) {
	milliseconds, err := epochms.FromTime(value)
	if err != nil {
		return 0, fmt.Errorf("map management timestamp: %w", err)
	}
	if milliseconds > maxSafeInteger {
		return 0, fmt.Errorf("map management timestamp: unsafe integer")
	}
	return milliseconds, nil
}

func optionalSafeEpochMilliseconds(value time.Time) (*int64, error) {
	if value.IsZero() {
		return nil, nil
	}
	milliseconds, err := safeEpochMilliseconds(value)
	if err != nil {
		return nil, err
	}
	return &milliseconds, nil
}

func validateSafeMilliseconds(value int64) error {
	if value < 0 || value > maxSafeInteger {
		return fmt.Errorf("management timestamp is outside the JSON safe integer range")
	}
	return nil
}

func parseCanonicalSafeMilliseconds(value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || strconv.FormatInt(parsed, 10) != value {
		return 0, fmt.Errorf("management timestamp must be canonical base-10")
	}
	if err := validateSafeMilliseconds(parsed); err != nil {
		return 0, err
	}
	return parsed, nil
}
