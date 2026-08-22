package subscriptionruntime

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"

	"golang.org/x/net/proxy"

	"gpt-load/internal/outboundproxy"
)

type NetworkContext struct {
	Proxy       outboundproxy.Effective `json:"proxy"`
	Fingerprint string                  `json:"fingerprint"`
}

type networkContextKey struct{}

func WithNetworkContext(ctx context.Context, network NetworkContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, networkContextKey{}, network)
}

func NetworkFromContext(ctx context.Context) (NetworkContext, bool) {
	if ctx == nil {
		return NetworkContext{}, false
	}
	network, ok := ctx.Value(networkContextKey{}).(NetworkContext)
	return network, ok
}

// HTTPClient builds one request-scoped client from the frozen network policy.
// A missing NetworkContext preserves the caller's existing transport behavior.
func HTTPClient(ctx context.Context) (*http.Client, error) {
	network, ok := NetworkFromContext(ctx)
	if !ok {
		return nil, nil
	}
	effective, err := outboundproxy.NormalizeEffective(network.Proxy)
	if err != nil {
		return nil, fmt.Errorf("invalid subscription proxy config")
	}
	transport := cloneDefaultHTTPTransport()
	switch effective.Config.Mode {
	case outboundproxy.ModeDirect:
		transport.Proxy = nil
	case outboundproxy.ModeEnvironment:
		transport.Proxy = http.ProxyFromEnvironment
	case outboundproxy.ModeCustom:
		endpoint, parseErr := url.Parse(effective.Config.URL)
		if parseErr != nil || endpoint.Host == "" {
			return nil, fmt.Errorf("invalid subscription proxy config")
		}
		switch endpoint.Scheme {
		case "http", "https":
			transport.Proxy = http.ProxyURL(endpoint)
		case "socks5":
			var auth *proxy.Auth
			if endpoint.User != nil {
				password, _ := endpoint.User.Password()
				auth = &proxy.Auth{User: endpoint.User.Username(), Password: password}
			}
			dialer, dialErr := proxy.SOCKS5("tcp", endpoint.Host, auth, proxy.Direct)
			if dialErr != nil {
				return nil, fmt.Errorf("initialize subscription proxy dialer")
			}
			transport.Proxy = nil
			transport.DialContext = func(dialContext context.Context, network, address string) (net.Conn, error) {
				if contextual, ok := dialer.(proxy.ContextDialer); ok {
					return contextual.DialContext(dialContext, network, address)
				}
				if err := dialContext.Err(); err != nil {
					return nil, err
				}
				return dialer.Dial(network, address)
			}
		default:
			return nil, fmt.Errorf("invalid subscription proxy config")
		}
	default:
		return nil, fmt.Errorf("invalid subscription proxy config")
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

func cloneDefaultHTTPTransport() *http.Transport {
	if transport, ok := http.DefaultTransport.(*http.Transport); ok && transport != nil {
		return transport.Clone()
	}
	return &http.Transport{}
}
