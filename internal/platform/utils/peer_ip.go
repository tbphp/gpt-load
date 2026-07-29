package utils

import (
	"fmt"
	"net/netip"
	"strings"
)

func NormalizePeerIP(remoteAddr string) (string, error) {
	endpoint, err := netip.ParseAddrPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		return "", fmt.Errorf("parse remote peer endpoint: %w", err)
	}
	return endpoint.Addr().Unmap().WithZone("").String(), nil
}
