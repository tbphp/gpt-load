package embedded

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestCodexLiveHTTPSProxyUsesCancelableDialerForExplicitAndEnvironmentSettings(t *testing.T) {
	proxyURL := &url.URL{Scheme: "https", Host: "proxy.example.test:443"}
	target := &url.URL{Scheme: "wss", Host: "api.openai.com", Path: "/v1/live/rtc_contract"}
	for _, test := range []struct {
		name             string
		configuredProxy  string
		wantResolveCalls int
	}{
		{name: "explicit", configuredProxy: proxyURL.String()},
		{name: "environment", wantResolveCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolveCalls := 0
			dialer, err := liveCodexDialer(test.configuredProxy, target, nil, func(request *http.Request) (*url.URL, error) {
				resolveCalls++
				if request.URL.Scheme != "https" || request.URL.Host != target.Host {
					t.Fatalf("environment proxy request URL = %s", request.URL)
				}
				return proxyURL, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if dialer.Proxy != nil || dialer.NetDialContext == nil {
				t.Fatal("HTTPS proxy must use a cancellable connection dialer")
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			_, _, err = dialer.DialContext(ctx, target.String(), nil)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("HTTPS proxy sideband dial = %v, want canceled dial", err)
			}
			if resolveCalls != test.wantResolveCalls {
				t.Fatalf("environment proxy resolutions = %d, want %d", resolveCalls, test.wantResolveCalls)
			}
		})
	}
}

func TestCodexLiveUsesSelectedCredentialAndPreservesCallID(t *testing.T) {
	t.Parallel()
	var calls int
	request := CodexLiveRequest{
		CredentialID: "credential-1",
		Credential:   CodexCredential{Type: ProviderCodex, AccessToken: "live-test-access", RefreshToken: "live-test-refresh", AccountID: "live-test-account"},
		ProxyURL:     "direct", Body: []byte(`{"sdp":"v=0","session":{"model":"gpt-live-1-codex"}}`),
	}
	request.doHTTP = func(_ context.Context, outgoing *http.Request) (*http.Response, error) {
		calls++
		if outgoing.Header.Get("Authorization") != "Bearer live-test-access" ||
			outgoing.Header.Get("Chatgpt-Account-Id") != "live-test-account" {
			t.Fatalf("selected Codex identity was not applied")
		}
		if strings.Contains(outgoing.URL.String(), "live-test-") {
			t.Fatal("credential leaked into upstream URL")
		}
		if calls == 1 {
			if outgoing.Method != http.MethodPost || outgoing.URL.Path != "/backend-api/codex/realtime/calls" ||
				outgoing.URL.Query().Get("intent") != "quicksilver" {
				t.Fatalf("call target = %s %s", outgoing.Method, outgoing.URL.Path)
			}
			return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader("v=0\r\n")),
				Header: http.Header{"Location": []string{"/v1/realtime/calls/rtc_contract"}}}, nil
		}
		if outgoing.URL.Path != "/v1/realtime/calls/rtc_contract/hangup" {
			t.Fatalf("hangup path = %s", outgoing.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	}
	call, err := StartCodexLive(t.Context(), request)
	if err != nil || call.CallID != "rtc_contract" || call.SDP != "v=0\r\n" || call.Session == nil {
		t.Fatalf("call = %#v, error = %v", call, err)
	}
	if err := call.Session.Hangup(t.Context()); err != nil || calls != 2 {
		t.Fatalf("hangup error = %v, calls = %d", err, calls)
	}
}

func TestCodexLiveRejectsMissingCallIDAfterUpstreamSuccess(t *testing.T) {
	t.Parallel()
	request := CodexLiveRequest{
		CredentialID: "credential-1",
		Credential:   CodexCredential{Type: ProviderCodex, AccessToken: "access", RefreshToken: "refresh", AccountID: "account"},
		ProxyURL:     "direct", Body: []byte(`{"sdp":"v=0","session":{"model":"gpt-live-1-codex"}}`),
		doHTTP: func(context.Context, *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("v=0\r\n")), Header: make(http.Header)}, nil
		},
	}
	if _, err := StartCodexLive(t.Context(), request); err == nil {
		t.Fatal("call without Location was accepted")
	}
}

func TestCodexLiveAcceptsJSONAnswerAndCallIDShapes(t *testing.T) {
	t.Parallel()
	for _, location := range []string{"rtc_contract", "/v1/realtime?call_id=rtc_contract", "https://api.openai.com/v1/live/rtc_contract"} {
		t.Run(location, func(t *testing.T) {
			request := CodexLiveRequest{
				CredentialID: "credential-1",
				Credential:   CodexCredential{Type: ProviderCodex, AccessToken: "access", RefreshToken: "refresh", AccountID: "account"},
				ProxyURL:     "direct", Body: []byte(`{"sdp":"v=0","session":{"model":"gpt-live-1-codex"}}`),
				doHTTP: func(context.Context, *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(`{"sdp":"v=0\r\n"}`)),
						Header: http.Header{"Location": {location}, "Content-Type": {"application/json"}}}, nil
				},
			}
			call, err := StartCodexLive(t.Context(), request)
			if err != nil || call.CallID != "rtc_contract" || call.SDP != "v=0\r\n" {
				t.Fatalf("call = %#v, error = %v", call, err)
			}
		})
	}
}
