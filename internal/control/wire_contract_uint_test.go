package control

import (
	"errors"
	"math"
	"strconv"
	"testing"
)

func TestParseCanonicalSafePlatformUint(t *testing.T) {
	t.Parallel()
	maxSupported := uint64(maxSafeInteger)
	if uint64(math.MaxUint) < maxSupported {
		maxSupported = uint64(math.MaxUint)
	}
	for _, value := range []string{"0", "1", strconv.FormatUint(maxSupported, 10)} {
		got, err := parseCanonicalSafePlatformUint(value)
		if err != nil || uint64(got) != mustParseUint(t, value) {
			t.Fatalf("parseCanonicalSafePlatformUint(%q) = %d, %v", value, got, err)
		}
	}

	for _, value := range []string{"", "00", "01", "+1", "-1", "18446744073709551616"} {
		if _, err := parseCanonicalSafePlatformUint(value); err == nil || errors.Is(err, errUnsafeCanonicalUint) {
			t.Fatalf("parseCanonicalSafePlatformUint(%q) error = %v, want canonical error", value, err)
		}
	}

	for _, value := range []string{"9007199254740992"} {
		if _, err := parseCanonicalSafePlatformUint(value); !errors.Is(err, errUnsafeCanonicalUint) {
			t.Fatalf("parseCanonicalSafePlatformUint(%q) error = %v, want unsafe integer", value, err)
		}
	}
	if strconv.IntSize == 32 {
		value := "4294967296"
		if _, err := parseCanonicalSafePlatformUint(value); !errors.Is(err, errUnsafeCanonicalUint) {
			t.Fatalf("parseCanonicalSafePlatformUint(%q) error = %v, want platform overflow", value, err)
		}
	}
}

func mustParseUint(t *testing.T, value string) uint64 {
	t.Helper()
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
