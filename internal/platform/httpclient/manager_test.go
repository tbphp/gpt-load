package httpclient

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSameOriginUsesEffectivePorts(t *testing.T) {
	tests := []struct {
		name  string
		left  string
		right string
		want  bool
	}{
		{name: "HTTPS default port", left: "https://api.example.com/v1", right: "https://api.example.com:443/v2", want: true},
		{name: "HTTP default port", left: "http://api.example.com/v1", right: "http://api.example.com:80/v2", want: true},
		{name: "different explicit port", left: "https://api.example.com:443/v1", right: "https://api.example.com:444/v2", want: false},
		{name: "different scheme", left: "http://api.example.com:80/v1", right: "https://api.example.com:443/v2", want: false},
		{name: "hostname case", left: "https://API.example.com/v1", right: "https://api.EXAMPLE.com:443/v2", want: true},
		{name: "IPv6 default port", left: "https://[2001:db8::1]/v1", right: "https://[2001:db8::1]:443/v2", want: true},
		{name: "unknown scheme fails closed", left: "custom://api.example.com/v1", right: "custom://api.example.com/v2", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			leftURL, err := url.Parse(tt.left)
			if err != nil {
				t.Fatalf("parse left URL: %v", err)
			}
			rightURL, err := url.Parse(tt.right)
			if err != nil {
				t.Fatalf("parse right URL: %v", err)
			}

			got := sameOrigin(&http.Request{URL: leftURL}, &http.Request{URL: rightURL})
			if got != tt.want {
				t.Fatalf("sameOrigin(%q, %q) = %t, want %t", tt.left, tt.right, got, tt.want)
			}
		})
	}
}

func TestCrossOriginRedirectIsRejectedBeforeReplayingRequest(t *testing.T) {
	var targetCalls int
	var gotCredential string

	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls++
		gotCredential = r.Header.Get("X-Custom-Credential")
		w.WriteHeader(http.StatusOK)
	}))
	defer attacker.Close()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://attacker.local/v1/messages", http.StatusTemporaryRedirect)
	}))
	defer upstream.Close()

	attackerAddr := strings.TrimPrefix(attacker.URL, "http://")
	upstreamAddr := strings.TrimPrefix(upstream.URL, "http://")

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			switch addr {
			case "victim.local:80":
				addr = upstreamAddr
			case "attacker.local:80":
				addr = attackerAddr
			}
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}

	client := &http.Client{
		Transport:     transport,
		CheckRedirect: rejectCrossOriginRedirect,
	}

	req, _ := http.NewRequest(
		http.MethodPost,
		"http://victim.local/v1/messages",
		bytes.NewBufferString(`{"prompt":"secret request body"}`),
	)
	req.Header.Set("X-Custom-Credential", "custom-secret")

	resp, err := client.Do(req)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil {
		t.Fatal("cross-origin redirect was followed, want rejection")
	}
	if !errors.Is(err, errCrossOriginRedirect) {
		t.Fatalf("request error = %v, want %v", err, errCrossOriginRedirect)
	}
	if targetCalls != 0 || gotCredential != "" {
		t.Fatalf("redirect target received request: calls=%d credential=%q", targetCalls, gotCredential)
	}
}

func TestSameOriginRedirectPreservesRequestHeaders(t *testing.T) {
	var gotAPIKey string
	var hops int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			hops++
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		gotAPIKey = r.Header.Get("x-api-key")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{CheckRedirect: rejectCrossOriginRedirect}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/redirect", nil)
	req.Header.Set("x-api-key", "sk-secret-upstream-key")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if hops == 0 {
		t.Fatal("expected a same-host redirect hop")
	}
	if gotAPIKey != "sk-secret-upstream-key" {
		t.Errorf("x-api-key was incorrectly stripped on same-host redirect: %q", gotAPIKey)
	}
}

func TestRedirectToSameHostnameDifferentPortIsRejected(t *testing.T) {
	var targetCalls int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/final", http.StatusFound)
	}))
	defer source.Close()

	client := &http.Client{CheckRedirect: rejectCrossOriginRedirect}
	req, err := http.NewRequest(http.MethodGet, source.URL+"/redirect", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("x-api-key", "sk-secret-upstream-key")

	resp, err := client.Do(req)
	if resp != nil {
		resp.Body.Close()
	}
	if !errors.Is(err, errCrossOriginRedirect) {
		t.Fatalf("request error = %v, want %v", err, errCrossOriginRedirect)
	}
	if targetCalls != 0 {
		t.Fatalf("cross-port redirect target received %d requests", targetCalls)
	}
}

func TestHTTPSDowngradeRedirectIsRejected(t *testing.T) {
	previous, err := http.NewRequest(http.MethodGet, "https://upstream.example/v1", nil)
	if err != nil {
		t.Fatalf("create previous request: %v", err)
	}
	redirected, err := http.NewRequest(http.MethodGet, "http://upstream.example/v2", nil)
	if err != nil {
		t.Fatalf("create redirected request: %v", err)
	}

	if err := rejectCrossOriginRedirect(redirected, []*http.Request{previous}); !errors.Is(err, errCrossOriginRedirect) {
		t.Fatalf("redirect policy error = %v, want %v", err, errCrossOriginRedirect)
	}
}
