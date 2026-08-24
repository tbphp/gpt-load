package embedded

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type environmentRoundTripperFunc func(*http.Request) (*http.Response, error)

func (fn environmentRoundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestEnvironmentProxyRoundTripperResolvesProxyAndNoProxyPerRequest(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		proxyURL *url.URL
		want     string
	}{
		{name: "proxy", proxyURL: &url.URL{Scheme: "http", Host: "proxy.example.com:8080"}, want: "http://proxy.example.com:8080"},
		{name: "no proxy", want: "direct"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var selected string
			transport := &environmentProxyRoundTripper{
				resolveProxy: func(*http.Request) (*url.URL, error) {
					return test.proxyURL, nil
				},
				transportForURL: func(proxyURL string) http.RoundTripper {
					selected = proxyURL
					return environmentRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
						return &http.Response{
							StatusCode: http.StatusNoContent,
							Header:     make(http.Header),
							Body:       http.NoBody,
							Request:    request,
						}, nil
					})
				},
			}
			request, err := http.NewRequest(http.MethodGet, "https://chatgpt.com/backend-api/codex", nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := transport.RoundTrip(request)
			if err != nil || response.StatusCode != http.StatusNoContent || selected != test.want {
				t.Fatalf("RoundTrip() response/error/setting = %#v/%v/%q, want %q", response, err, selected, test.want)
			}
		})
	}
}

func TestEnvironmentProxyRoundTripperRejectsInvalidProxyWithoutFallback(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		resolve func(*http.Request) (*url.URL, error)
	}{
		{
			name: "resolver failure",
			resolve: func(*http.Request) (*url.URL, error) {
				return nil, errors.New("password-secret")
			},
		},
		{
			name: "unsupported scheme",
			resolve: func(*http.Request) (*url.URL, error) {
				return &url.URL{Scheme: "ftp", Host: "proxy.example.com"}, nil
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			transportCalls := 0
			transport := &environmentProxyRoundTripper{
				resolveProxy: test.resolve,
				transportForURL: func(string) http.RoundTripper {
					transportCalls++
					return http.DefaultTransport
				},
			}
			request, err := http.NewRequest(http.MethodGet, "https://api.anthropic.com/v1/messages", nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = transport.RoundTrip(request)
			if err == nil || transportCalls != 0 || strings.Contains(err.Error(), "password-secret") {
				t.Fatalf("RoundTrip() error/calls = %v/%d", err, transportCalls)
			}
		})
	}
}

func TestCPAProtectedExecutorsInstallEnvironmentProxyRoundTripper(t *testing.T) {
	t.Parallel()

	codexContext := NewCodexHTTPExecutor().executionContext(t.Context(), nil, nil, true)
	assertEnvironmentProxyContext(t, codexContext)
	claudeExecutor, ok := NewClaudeHTTPExecutor().(*claudeHTTPExecutor)
	if !ok {
		t.Fatal("Claude executor has an unexpected implementation")
	}
	claudeContext := claudeExecutor.executionContext(t.Context(), nil, nil, true)
	assertEnvironmentProxyContext(t, claudeContext)
}

func assertEnvironmentProxyContext(t *testing.T, ctx interface{ Value(any) any }) {
	t.Helper()
	wrapped, ok := ctx.Value("cliproxy.roundtripper").(noRedirectRoundTripper)
	if !ok {
		t.Fatal("execution context has no guarded round tripper")
	}
	if _, ok := wrapped.base.(*environmentProxyRoundTripper); !ok {
		t.Fatalf("execution transport = %T, want environment proxy transport", wrapped.base)
	}
}
