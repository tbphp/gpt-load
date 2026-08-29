package utils

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeAllowedCIDRsCanonicalizesAndCompiles(t *testing.T) {
	canonical, prefixes, err := NormalizeAllowedCIDRs([]string{
		" 192.0.2.1 ",
		"192.0.2.99/24",
		"2001:db8::1",
		"::ffff:192.0.2.1",
		"192.0.2.1/32",
	})
	if err != nil {
		t.Fatalf("NormalizeAllowedCIDRs() error = %v", err)
	}
	wantCanonical := []string{
		"192.0.2.0/24",
		"192.0.2.1/32",
		"2001:db8::1/128",
	}
	if !reflect.DeepEqual(canonical, wantCanonical) {
		t.Fatalf("canonical = %#v, want %#v", canonical, wantCanonical)
	}
	wantPrefixes := make([]netip.Prefix, 0, len(wantCanonical))
	for _, value := range wantCanonical {
		wantPrefixes = append(wantPrefixes, netip.MustParsePrefix(value))
	}
	if !reflect.DeepEqual(prefixes, wantPrefixes) {
		t.Fatalf("prefixes = %#v, want %#v", prefixes, wantPrefixes)
	}

	canonical[0] = "mutated"
	if prefixes[0] != netip.MustParsePrefix("192.0.2.0/24") {
		t.Fatalf("prefixes share mutable canonical storage: %#v", prefixes)
	}
}

func TestNormalizeAllowedCIDRsRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name   string
		values []string
	}{
		{name: "blank", values: []string{" "}},
		{name: "hostname", values: []string{"example.com"}},
		{name: "range", values: []string{"192.0.2.1-192.0.2.10"}},
		{name: "zone", values: []string{"fe80::1%en0"}},
		{name: "mapped prefix", values: []string{"::ffff:192.0.2.0/120"}},
		{name: "too many", values: repeatedCIDRValues(65)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := NormalizeAllowedCIDRs(test.values); err == nil {
				t.Fatalf("NormalizeAllowedCIDRs(%#v) error = nil", test.values)
			}
		})
	}
}

func TestNormalizeAllowedCIDRsReturnsEmptyArrays(t *testing.T) {
	canonical, prefixes, err := NormalizeAllowedCIDRs(nil)
	if err != nil {
		t.Fatalf("NormalizeAllowedCIDRs(nil) error = %v", err)
	}
	if canonical == nil || len(canonical) != 0 || prefixes == nil || len(prefixes) != 0 {
		t.Fatalf("NormalizeAllowedCIDRs(nil) = %#v, %#v, want non-nil empty slices", canonical, prefixes)
	}
}

func TestAllowedCIDRsContain(t *testing.T) {
	prefixes := []netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
	for _, peer := range []string{"192.0.2.10", "::ffff:192.0.2.10", "2001:db8::7"} {
		if !AllowedCIDRsContain(prefixes, peer) {
			t.Fatalf("AllowedCIDRsContain(%q) = false", peer)
		}
	}
	for _, peer := range []string{"198.51.100.1", "2001:db9::1", "invalid"} {
		if AllowedCIDRsContain(prefixes, peer) {
			t.Fatalf("AllowedCIDRsContain(%q) = true", peer)
		}
	}
}

func repeatedCIDRValues(count int) []string {
	values := make([]string, count)
	for index := range values {
		values[index] = "192.0.2.1/32"
	}
	return values
}

func TestNormalizeAllowedCIDRsErrorDoesNotExposeRawValue(t *testing.T) {
	const raw = "secret.invalid.example"
	_, _, err := NormalizeAllowedCIDRs([]string{raw})
	if err == nil {
		t.Fatal("NormalizeAllowedCIDRs() error = nil")
	}
	if strings.Contains(err.Error(), raw) {
		t.Fatalf("error exposes raw value: %v", err)
	}
}
