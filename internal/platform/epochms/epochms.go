// Package epochms provides checked Unix epoch millisecond primitives.
package epochms

import (
	"errors"
	"math"
	"time"
)

const (
	MillisecondsPerHour int64 = 3_600_000
	MillisecondsPerDay  int64 = 86_400_000
)

var errInvalidEpochMilliseconds = errors.New("epoch milliseconds must be non-negative")

// FromTime converts value to a non-negative Unix epoch millisecond timestamp.
func FromTime(value time.Time) (int64, error) {
	milliseconds := value.UTC().UnixMilli()
	if milliseconds < 0 {
		return 0, errInvalidEpochMilliseconds
	}
	return milliseconds, nil
}

// ToTime converts a non-negative Unix epoch millisecond timestamp to UTC.
func ToTime(value int64) (time.Time, error) {
	if value < 0 {
		return time.Time{}, errInvalidEpochMilliseconds
	}
	return time.Unix(value/1_000, (value%1_000)*int64(time.Millisecond)).UTC(), nil
}

// AlignDown returns the Unix epoch boundary at or before value.
func AlignDown(value, width int64) (int64, error) {
	if value < 0 {
		return 0, errInvalidEpochMilliseconds
	}
	if width <= 0 {
		return 0, errors.New("epoch millisecond width must be positive")
	}
	return value - value%width, nil
}

// WindowEndingAt returns count full buckets ending after observedAt's bucket.
func WindowEndingAt(observedAt, width int64, count int) (from, to int64, err error) {
	if count <= 0 {
		return 0, 0, errors.New("epoch millisecond window count must be positive")
	}

	start, err := AlignDown(observedAt, width)
	if err != nil {
		return 0, 0, err
	}
	if start > math.MaxInt64-width {
		return 0, 0, errors.New("epoch millisecond bucket end overflows int64")
	}
	to = start + width

	windowWidth := int64(count)
	if width > math.MaxInt64/windowWidth {
		return 0, 0, errors.New("epoch millisecond window width overflows int64")
	}
	windowWidth *= width
	if windowWidth > to {
		return 0, 0, errors.New("epoch millisecond window begins before the Unix epoch")
	}
	return to - windowWidth, to, nil
}
