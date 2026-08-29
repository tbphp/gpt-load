package utils

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

const maxAllowedCIDRs = 64

// NormalizeAllowedCIDRs canonicalizes the configured direct-peer allowlist and
// compiles it for request-time matching.
func NormalizeAllowedCIDRs(values []string) ([]string, []netip.Prefix, error) {
	canonical := make([]string, 0, len(values))
	prefixes := make([]netip.Prefix, 0, len(values))
	if len(values) > maxAllowedCIDRs {
		return nil, nil, fmt.Errorf("allowed CIDR count exceeds limit")
	}

	seen := make(map[string]netip.Prefix, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			return nil, nil, fmt.Errorf("allowed CIDR is empty")
		}

		prefix, err := normalizeAllowedCIDR(value)
		if err != nil {
			return nil, nil, fmt.Errorf("allowed CIDR is invalid")
		}
		seen[prefix.String()] = prefix
	}

	for value := range seen {
		canonical = append(canonical, value)
	}
	sort.Strings(canonical)
	for _, value := range canonical {
		prefixes = append(prefixes, seen[value])
	}
	return canonical, prefixes, nil
}

func normalizeAllowedCIDR(value string) (netip.Prefix, error) {
	if address, err := netip.ParseAddr(value); err == nil {
		if address.Zone() != "" {
			return netip.Prefix{}, fmt.Errorf("zoned address is unsupported")
		}
		address = address.Unmap()
		bits := 128
		if address.Is4() {
			bits = 32
		}
		return netip.PrefixFrom(address, bits), nil
	}

	prefix, err := netip.ParsePrefix(value)
	if err != nil || prefix.Addr().Zone() != "" || prefix.Addr().Is4In6() {
		return netip.Prefix{}, fmt.Errorf("unsupported prefix")
	}
	return prefix.Masked(), nil
}

// AllowedCIDRsContain reports whether a normalized peer address belongs to the
// compiled allowlist.
func AllowedCIDRsContain(prefixes []netip.Prefix, peer string) bool {
	address, err := netip.ParseAddr(strings.TrimSpace(peer))
	if err != nil {
		return false
	}
	address = address.Unmap().WithZone("")
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
