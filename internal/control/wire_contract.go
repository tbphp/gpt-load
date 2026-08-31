package control

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"gpt-load/internal/platform/epochms"
)

const maxSafeInteger = int64(9_007_199_254_740_991)

var errUnsafeCanonicalUint = errors.New(
	"management unsigned integer is outside the JSON safe integer range",
)

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

func parseCanonicalSafeUint(value string) (uint64, error) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || strconv.FormatUint(parsed, 10) != value {
		return 0, fmt.Errorf("management unsigned integer must be canonical base-10")
	}
	if parsed > uint64(maxSafeInteger) {
		return 0, errUnsafeCanonicalUint
	}
	return parsed, nil
}

func parseCanonicalSafePlatformUint(value string) (uint, error) {
	parsed, err := parseCanonicalSafeUint(value)
	if err != nil {
		return 0, err
	}
	if parsed > math.MaxUint {
		return 0, errUnsafeCanonicalUint
	}
	return uint(parsed), nil
}
