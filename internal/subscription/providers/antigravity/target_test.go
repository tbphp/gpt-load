package antigravity

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
	driver := newAntigravityDriver()
	credential, err := driver.Parse([]byte(`{"type":"antigravity","access_token":"test-access","refresh_token":"test-refresh","account_id":"account-one","email":"owner@example.com","project_id":"project-one","expired":"2030-01-01T00:00:00Z"}`))
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
				}, []string{"https://daily-cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels"}},
				{"observation", func(ctx context.Context, c subscriptionruntime.Credential, target subscriptionruntime.Target) error {
					_, err := driver.Observe(ctx, c, target)
					return err
				}, []string{"https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist", "https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary"}},
				{"refresh official", func(ctx context.Context, c subscriptionruntime.Credential, _ subscriptionruntime.Target) error {
					_, err := driver.Refresh(ctx, c)
					return err
				}, []string{"https://oauth2.googleapis.com/token"}},
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

func TestCustomBusinessTargetKeepsImportedIdentityAndProjectPreparationOfficial(t *testing.T) {
	driver := newAntigravityDriver()
	raw := []byte(`{"type":"antigravity","access_token":"test-access","refresh_token":"test-refresh","account_id":"account-one","email":"owner@example.com","project_id":"project-one","expired":"2030-01-01T00:00:00Z"}`)
	credential, err := driver.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	var got []string
	http.DefaultTransport = targetRoundTripper(func(r *http.Request) (*http.Response, error) {
		got = append(got, r.URL.String())
		status, payload := http.StatusOK, `{}`
		switch r.URL.String() {
		case "https://relay.example/team-a/v1internal:fetchAvailableModels":
			status = http.StatusTeapot
		case "https://www.googleapis.com/oauth2/v2/userinfo?alt=json":
			payload = `{"id":"account-one","email":"owner@example.com","verified_email":true}`
		case "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist":
			payload = `{"allowedTiers":[{"id":"free-tier","isDefault":true}]}`
		case "https://daily-cloudcode-pa.googleapis.com/v1internal:onboardUser":
			payload = `{"done":true,"response":{"projectId":"project-one"}}`
		default:
			t.Errorf("unexpected upstream URL %s", r.URL)
			status = http.StatusTeapot
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: r}, nil
	})
	target := subscriptionruntime.NewTarget([]byte(`{"base_url":"https://relay.example/team-a"}`))
	if _, err := driver.DiscoverModels(t.Context(), credential, target); err == nil {
		t.Fatal("unexpected successful model discovery")
	}
	imported, err := driver.ImportCredential(t.Context(), raw)
	if err != nil || imported.Identity() != "account-one" {
		t.Fatalf("imported identity = %q, error = %v", imported.Identity(), err)
	}
	want := []string{
		"https://relay.example/team-a/v1internal:fetchAvailableModels",
		"https://www.googleapis.com/oauth2/v2/userinfo?alt=json",
		"https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist",
		"https://daily-cloudcode-pa.googleapis.com/v1internal:onboardUser",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("request URLs = %v; want %v", got, want)
	}
}
