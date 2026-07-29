package utils

import "testing"

func TestNormalizePeerIP(t *testing.T) {
	for remote, want := range map[string]string{
		"192.0.2.1:1234":          "192.0.2.1",
		"[::ffff:192.0.2.1]:1234": "192.0.2.1",
		"[2001:db8::1]:1234":      "2001:db8::1",
		"[fe80::1%en0]:1234":      "fe80::1",
	} {
		t.Run(remote, func(t *testing.T) {
			got, err := NormalizePeerIP(remote)
			if err != nil {
				t.Fatalf("NormalizePeerIP(%q) error = %v", remote, err)
			}
			if got != want {
				t.Fatalf("NormalizePeerIP(%q) = %q, want %q", remote, got, want)
			}
		})
	}

	for _, remote := range []string{
		"",
		"192.0.2.1",
		"192.0.2.1:",
		"192.0.2.1:not-a-port",
		"192.0.2.1:65536",
		"hostname:1234",
		"[2001:db8::1",
	} {
		t.Run("invalid "+remote, func(t *testing.T) {
			if got, err := NormalizePeerIP(remote); err == nil {
				t.Fatalf("NormalizePeerIP(%q) = %q, want error", remote, got)
			}
		})
	}
}
