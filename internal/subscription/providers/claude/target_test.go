package claude

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"gpt-load/internal/outboundproxy"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestBusinessTargetPreservesNativeEndpoints(t *testing.T) {
	driver := newClaudeDriver()
	credential, err := driver.Parse([]byte(`{"type":"claude","access_token":"test-access","refresh_token":"test-refresh","account_uuid":"account-one","organization_uuid":"org-one","email":"owner@example.com","expired":"2030-01-01T00:00:00Z"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"", "https://relay.example", "https://relay.example/team-a"} {
		t.Run(root, func(t *testing.T) {
			target := subscriptionruntime.NewTarget([]byte(`{"base_url":"` + root + `"}`))
			for _, op := range []struct {
				name string
				call func(context.Context, subscriptionruntime.Credential, subscriptionruntime.Target) error
				urls []string
			}{
				{"models", func(ctx context.Context, c subscriptionruntime.Credential, target subscriptionruntime.Target) error {
					_, err := driver.DiscoverModels(ctx, c, target)
					return err
				}, []string{"https://api.anthropic.com/api/claude_cli/bootstrap"}},
				{"observation", func(ctx context.Context, c subscriptionruntime.Credential, target subscriptionruntime.Target) error {
					_, err := driver.Observe(ctx, c, target)
					return err
				}, []string{"https://api.anthropic.com/api/oauth/profile", "https://api.anthropic.com/api/oauth/claude_cli/roles", "https://api.anthropic.com/api/claude_cli/bootstrap", "https://api.anthropic.com/api/oauth/usage"}},
				{"refresh official", func(ctx context.Context, c subscriptionruntime.Credential, _ subscriptionruntime.Target) error {
					_, err := driver.Refresh(ctx, c)
					return err
				}, []string{"https://platform.claude.com/v1/oauth/token"}},
			} {
				t.Run(op.name, func(t *testing.T) {
					var mu sync.Mutex
					var got []string
					previous := http.DefaultTransport
					t.Cleanup(func() { http.DefaultTransport = previous })
					server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						targetURL := *r.URL
						targetURL.Scheme, targetURL.Host = "https", r.Host
						// 只忽略随版本变化的客户端版本参数，业务查询参数必须保留。
						query := targetURL.Query()
						query.Del("client_version")
						query.Del("entrypoint")
						targetURL.RawQuery = query.Encode()
						mu.Lock()
						got = append(got, r.Method+" "+targetURL.String())
						mu.Unlock()
						if op.name != "refresh official" && r.Header.Get("Authorization") != "Bearer test-access" {
							t.Error("business request lost its OAuth access token")
						}
						w.WriteHeader(http.StatusTeapot)
					}))
					t.Cleanup(server.Close)
					transport := server.Client().Transport.(*http.Transport).Clone()
					transport.DialTLSContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
						return (&tls.Dialer{Config: transport.TLSClientConfig}).DialContext(ctx, network, server.Listener.Addr().String())
					}
					http.DefaultTransport = transport
					ctx := subscriptionruntime.WithNetworkContext(t.Context(), subscriptionruntime.NetworkContext{Proxy: outboundproxy.Effective{Config: outboundproxy.Config{Mode: outboundproxy.ModeDirect}}})
					if err := op.call(ctx, credential, target); err == nil {
						t.Fatal("unexpected success from failed upstream")
					}
					var want []string
					for _, raw := range op.urls {
						u, err := url.Parse(raw)
						if err != nil {
							t.Fatal(err)
						}
						method := "GET"
						if strings.Contains(u.Path, "v1internal:") || strings.HasSuffix(u.Path, "/token") || strings.HasSuffix(u.Path, "/consume") {
							method = "POST"
						}
						if root != "" && op.name != "refresh official" {
							raw = root + u.RequestURI()
						}
						want = append(want, method+" "+raw)
					}
					slices.Sort(got)
					slices.Sort(want)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("requests = %v; want %v", got, want)
					}
				})
			}
		})
	}
}
