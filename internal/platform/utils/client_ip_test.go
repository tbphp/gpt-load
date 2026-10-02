package utils

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIPResolver(t *testing.T) {
	for _, test := range []struct {
		name, header, remote string
		trusted, values      []string
		want                 string
	}{
		{name: "default ignores headers", remote: "192.0.2.10:80", values: []string{"203.0.113.9"}, want: "192.0.2.10"},
		{name: "trusted list alone keeps direct IP", remote: "192.0.2.10:80", trusted: []string{"192.0.2.0/24"}, values: []string{"203.0.113.9"}, want: "192.0.2.10"},
		{name: "explicit header without allowlist", header: "CF-Connecting-IP", remote: "192.0.2.10:80", values: []string{"203.0.113.9"}, want: "203.0.113.9"},
		{name: "header matched proxy", header: "X-Real-IP", remote: "192.0.2.10:80", trusted: []string{"192.0.2.10"}, values: []string{"2001:0DB8:0:0::1"}, want: "2001:db8::1"},
		{name: "header unmatched proxy", header: "X-Real-IP", remote: "192.0.2.10:80", trusted: []string{"192.0.2.11"}, values: []string{"203.0.113.9"}, want: "192.0.2.10"},
		{name: "IPv6 proxy and mapped visitor", header: "X-Real-IP", remote: "[2001:db8::2]:80", trusted: []string{"2001:db8::/32"}, values: []string{" ::ffff:203.0.113.9 "}, want: "203.0.113.9"},
		{name: "mapped proxy", header: "X-Real-IP", remote: "[::ffff:192.0.2.10]:80", trusted: []string{"192.0.2.0/24"}, values: []string{"203.0.113.9"}, want: "203.0.113.9"},
		{name: "forwarded explicit trust", header: "x-forwarded-for", remote: "192.0.2.10:80", values: []string{"203.0.113.9, 198.51.100.7"}, want: "203.0.113.9"},
		{name: "forwarded drops forged prefix", header: "X-Forwarded-For", remote: "192.0.2.10:80", trusted: []string{"192.0.2.0/24"}, values: []string{"203.0.113.9, 198.51.100.7"}, want: "198.51.100.7"},
		{name: "forwarded multiple header lines", header: "X-Forwarded-For", remote: "192.0.2.10:80", trusted: []string{"192.0.2.0/24", "2001:db8::/32"}, values: []string{"forged, 198.51.100.7", "2001:db8::2"}, want: "198.51.100.7"},
		{name: "forwarded all trusted", header: "X-Forwarded-For", remote: "192.0.2.10:80", trusted: []string{"192.0.2.0/24"}, values: []string{"192.0.2.2, 192.0.2.3"}, want: "192.0.2.2"},
		{name: "invalid leftmost does not skip", header: "X-Forwarded-For", remote: "192.0.2.10:80", values: []string{"invalid, 203.0.113.9"}, want: "192.0.2.10"},
		{name: "invalid chain does not skip", header: "X-Forwarded-For", remote: "192.0.2.10:80", trusted: []string{"192.0.2.0/24"}, values: []string{"203.0.113.9, invalid"}, want: "192.0.2.10"},
		{name: "custom header is single IP", header: "X-Client-IP", remote: "192.0.2.10:80", values: []string{"203.0.113.9, 198.51.100.7"}, want: "192.0.2.10"},
		{name: "duplicate custom header rejected", header: "X-Client-IP", remote: "192.0.2.10:80", values: []string{"203.0.113.9", "198.51.100.7"}, want: "192.0.2.10"},
		{name: "zone in header rejected", header: "X-Client-IP", remote: "192.0.2.10:80", values: []string{"fe80::1%eth0"}, want: "192.0.2.10"},
		{name: "missing header falls back", header: "CF-Connecting-IP", remote: "[fe80::1%eth0]:80", want: "fe80::1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolver, err := NewClientIPResolver(test.header, test.trusted)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.RemoteAddr = test.remote
			name := test.header
			if name == "" {
				name = "X-Forwarded-For"
			}
			for _, value := range test.values {
				request.Header.Add(name, value)
			}
			request = resolver.Apply(request)
			got, err := ClientIP(request)
			if err != nil || got != test.want {
				t.Fatalf("ClientIP() = %q, %v; want %q", got, err, test.want)
			}
			request.RemoteAddr = "198.51.100.99:80"
			request.Header.Set(name, "198.51.100.99")
			if frozen, err := ClientIP(request); err != nil || frozen != got {
				t.Fatalf("IP changed after request entry: %q, %v", frozen, err)
			}
		})
	}
}

func TestClientIPResolverRejectsInvalidConfiguration(t *testing.T) {
	for _, test := range []struct {
		header  string
		trusted []string
	}{
		{header: "bad header"}, {header: "X-IP\r\nX-Other"}, {header: "X-IP:"},
		{trusted: []string{"example.com"}}, {trusted: []string{"192.0.2.1/99"}}, {trusted: []string{""}},
	} {
		if _, err := NewClientIPResolver(test.header, test.trusted); err == nil {
			t.Fatalf("accepted invalid configuration: %#v", test)
		}
	}
}

func TestClientIPUnknownIsNotAnAddress(t *testing.T) {
	resolver, err := NewClientIPResolver("", nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "malformed"
	if value, err := ClientIP(resolver.Apply(request)); value != "" || err == nil {
		t.Fatalf("unknown peer = %q, %v", value, err)
	}
}
