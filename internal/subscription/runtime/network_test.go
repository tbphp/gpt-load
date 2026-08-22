package subscriptionruntime

import (
	"net/http"
	"testing"

	"gpt-load/internal/outboundproxy"
)

func TestHTTPClientUsesFrozenProxyMode(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		effective outboundproxy.Effective
		wantProxy bool
	}{
		{name: "direct", effective: outboundproxy.Effective{Config: outboundproxy.Config{Mode: outboundproxy.ModeDirect}, Source: outboundproxy.SourceCredential}},
		{name: "http", effective: outboundproxy.Effective{Config: outboundproxy.Config{Mode: outboundproxy.ModeCustom, URL: "http://user:password@proxy.example.com:8080"}, Source: outboundproxy.SourceGroup}, wantProxy: true},
		{name: "https", effective: outboundproxy.Effective{Config: outboundproxy.Config{Mode: outboundproxy.ModeCustom, URL: "https://proxy.example.com:8443"}, Source: outboundproxy.SourceGlobal}, wantProxy: true},
		{name: "socks5", effective: outboundproxy.Effective{Config: outboundproxy.Config{Mode: outboundproxy.ModeCustom, URL: "socks5://user:password@proxy.example.com:1080"}, Source: outboundproxy.SourceGlobal}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := WithNetworkContext(t.Context(), NetworkContext{Proxy: test.effective, Fingerprint: "fingerprint"})
			client, err := HTTPClient(ctx)
			if err != nil {
				t.Fatalf("HTTPClient() error = %v", err)
			}
			transport, ok := client.Transport.(*http.Transport)
			if !ok {
				t.Fatalf("Transport = %T", client.Transport)
			}
			if (transport.Proxy != nil) != test.wantProxy {
				t.Fatalf("Transport proxy = %t, want %t", transport.Proxy != nil, test.wantProxy)
			}
		})
	}
}
