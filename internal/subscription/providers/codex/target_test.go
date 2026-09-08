package codex

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

type targetRoundTripper func(*http.Request) (*http.Response, error)

func (f targetRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBusinessTargetPreservesNativeEndpoints(t *testing.T) {
	driver := newCodexDriver()
	credential, err := driver.Parse([]byte(`{"type":"codex","access_token":"test-access","refresh_token":"test-refresh","account_id":"account-one"}`))
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
				}, []string{"https://chatgpt.com/backend-api/codex/models"}},
				{"observation", func(ctx context.Context, c subscriptionruntime.Credential, target subscriptionruntime.Target) error {
					_, err := driver.Observe(ctx, c, target)
					return err
				}, []string{"https://chatgpt.com/backend-api/wham/usage"}},
				{"refresh official", func(ctx context.Context, c subscriptionruntime.Credential, _ subscriptionruntime.Target) error {
					_, err := driver.Refresh(ctx, c)
					return err
				}, []string{"https://auth.openai.com/oauth/token"}},
				{"reset details", func(ctx context.Context, c subscriptionruntime.Credential, target subscriptionruntime.Target) error {
					credential, err := ParseCredentialJSON(c.Canonical())
					if err != nil {
						return err
					}
					root, err := target.BaseURL()
					if err != nil {
						return err
					}
					_, err = ObserveResetCredits(ctx, credential, root)
					return err
				}, []string{"https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"}},
				{"reset consume", func(ctx context.Context, c subscriptionruntime.Credential, target subscriptionruntime.Target) error {
					_, err := driver.Consume(ctx, c, target, "test-request-id")
					return err
				}, []string{"https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume"}},
			} {
				t.Run(op.name, func(t *testing.T) {
					var mu sync.Mutex
					var got []string
					previous := http.DefaultTransport
					t.Cleanup(func() { http.DefaultTransport = previous })
					http.DefaultTransport = targetRoundTripper(func(r *http.Request) (*http.Response, error) {
						targetURL := *r.URL
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
						return &http.Response{StatusCode: http.StatusTeapot, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
					})
					if err := op.call(t.Context(), credential, target); err == nil {
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
