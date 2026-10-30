package codex

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestSubscriptionObservationIsIsolatedFromQuota(t *testing.T) {
	for _, tc := range []struct {
		name, body, state string
		status            int
	}{
		{"renewing", `{"active_until":"2026-12-01T00:00:00Z","will_renew":true}`, "confirmed", 200},
		{"cancelled renewal", `{"active_until":"2026-12-01T00:00:00Z","will_renew":false}`, "confirmed", 200},
		{"unknown renewal", `{"active_until":"2026-12-01T00:00:00Z"}`, "confirmed", 200},
		{"no subscription", `{}`, "unavailable", 404},
		{"unauthorized", `{}`, "error", 401},
		{"challenge", `<html>challenge</html>`, "error", 403},
		{"invalid date", `{"active_until":"invalid"}`, "error", 200},
		{"missing date", `{}`, "error", 200},
		{"invalid renewal", `{"active_until":"2026-12-01T00:00:00Z","will_renew":"false"}`, "error", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = previous })
			calls := 0
			http.DefaultTransport = targetRoundTripper(func(r *http.Request) (*http.Response, error) {
				status, body := 200, `{}`
				switch r.URL.Path {
				case "/relay/backend-api/wham/usage":
					body = `{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":25,"limit_window_seconds":18000}}}`
				case "/relay/backend-api/wham/rate-limit-reset-credits":
					body = `{"credits":[]}`
				case "/relay/backend-api/subscriptions":
					calls++
					if r.URL.Host != "upstream.example" || r.URL.Query().Get("account_id") != "account+one" || r.Header.Get("Authorization") != "Bearer test-access" || r.Header.Get("Chatgpt-Account-Id") != "account+one" {
						t.Fatalf("incorrect subscription target or identity: %s", r.URL)
					}
					deadline, ok := r.Context().Deadline()
					if !ok || time.Until(deadline) > 10*time.Second {
						t.Fatal("missing bounded timeout")
					}
					status, body = tc.status, tc.body
				default:
					t.Fatalf("unexpected request %s", r.URL)
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			driver := newCodexDriver()
			credential, err := driver.Parse([]byte(`{"type":"codex","access_token":"test-access","refresh_token":"test-refresh","account_id":"account+one"}`))
			if err != nil {
				t.Fatal(err)
			}
			got, err := driver.Observe(t.Context(), credential, subscriptionruntime.NewTarget([]byte(`{"base_url":"https://upstream.example/relay"}`)))
			if err != nil {
				t.Fatal(err)
			}
			var snapshot quotaSnapshot
			if err := json.Unmarshal(got.Payload, &snapshot); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || !got.QuotaObserved || snapshot.Subscription == nil || snapshot.Subscription.State != tc.state || len(snapshot.QuotaWindows) != 1 || *snapshot.QuotaWindows[0].Used != 25 {
				t.Fatalf("observation: %s", got.Payload)
			}
			if tc.state == "confirmed" {
				expected, _ := time.Parse(time.RFC3339, "2026-12-01T00:00:00Z")
				if snapshot.Subscription.PeriodEndMS == nil || *snapshot.Subscription.PeriodEndMS != expected.UnixMilli() || snapshot.Subscription.ObservedAtMS == nil {
					t.Fatalf("billing period: %s", got.Payload)
				}
				switch tc.name {
				case "renewing":
					if snapshot.Subscription.WillRenew == nil || !*snapshot.Subscription.WillRenew {
						t.Fatal("lost true")
					}
				case "cancelled renewal":
					if snapshot.Subscription.WillRenew == nil || *snapshot.Subscription.WillRenew {
						t.Fatal("lost false")
					}
				case "unknown renewal":
					if snapshot.Subscription.WillRenew != nil {
						t.Fatal("invented renewal status")
					}
				}
			}
		})
	}
}
