package embedded

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
)

type environmentProxyRoundTripper struct {
	resolveProxy    func(*http.Request) (*url.URL, error)
	transportForURL func(string) http.RoundTripper
}

func executionRoundTripper(
	ctx context.Context,
	cfg *internalconfig.Config,
	auth *cliproxyauth.Auth,
	proxyFromEnvironment bool,
) http.RoundTripper {
	if proxyFromEnvironment {
		return newEnvironmentProxyRoundTripper(cfg)
	}
	client := helps.NewUtlsHTTPClient(ctx, cfg, auth, 0)
	if client.Transport != nil {
		return client.Transport
	}
	return http.DefaultTransport
}

func newEnvironmentProxyRoundTripper(cfg *internalconfig.Config) *environmentProxyRoundTripper {
	return &environmentProxyRoundTripper{
		resolveProxy: http.ProxyFromEnvironment,
		transportForURL: func(proxyURL string) http.RoundTripper {
			client := helps.NewUtlsHTTPClient(
				nil,
				cfg,
				&cliproxyauth.Auth{ProxyURL: proxyURL},
				0,
			)
			return client.Transport
		},
	}
}

func (transport *environmentProxyRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if transport == nil || request == nil || transport.resolveProxy == nil || transport.transportForURL == nil {
		return nil, errors.New("resolve environment proxy")
	}
	proxyURL, err := transport.resolveProxy(request)
	if err != nil {
		return nil, errors.New("resolve environment proxy")
	}
	setting := "direct"
	if proxyURL != nil {
		setting = proxyURL.String()
		parsed, parseErr := proxyutil.Parse(setting)
		if parseErr != nil || parsed.Mode != proxyutil.ModeProxy {
			return nil, errors.New("resolve environment proxy")
		}
	}
	selected := transport.transportForURL(setting)
	if selected == nil {
		return nil, errors.New("initialize environment proxy transport")
	}
	return selected.RoundTrip(request)
}
