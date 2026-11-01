package codex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

const syncUsageBody = `{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":25,"limit_window_seconds":18000}}}`

func syncTestCredential(t *testing.T) subscriptionruntime.Credential {
	t.Helper()
	credential, err := newCodexDriver().Parse([]byte(`{"type":"codex","access_token":"test-access","refresh_token":"test-refresh","account_id":"account+one"}`))
	if err != nil {
		t.Fatal(err)
	}
	return credential
}

func syncTestResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}
}

func TestAccountSyncStartsIndependentQueriesTogether(t *testing.T) {
	started := make(chan string, 3)
	release := make(chan struct{})
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	http.DefaultTransport = targetRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "relay.example" || request.Header.Get("Authorization") != "Bearer test-access" ||
			request.Header.Get("Chatgpt-Account-Id") != "account+one" {
			t.Error("account sync lost its target or identity")
		}
		started <- request.URL.Path
		select {
		case <-release:
		case <-request.Context().Done():
			return nil, request.Context().Err()
		}
		body := "{}"
		switch request.URL.Path {
		case "/tenant/backend-api/wham/usage":
			body = syncUsageBody
		case "/tenant/backend-api/wham/rate-limit-reset-credits":
			body = `{"available_count":2,"credits":[]}`
		case "/tenant/backend-api/subscriptions":
			if request.URL.Query().Get("account_id") != "account+one" {
				t.Error("subscription query lost escaped account ID")
			}
			deadline, ok := request.Context().Deadline()
			if !ok || time.Until(deadline) > 10*time.Second {
				t.Error("subscription query has no bounded timeout")
			}
			body = `{"active_until":"2026-12-01T08:00:00+08:00","will_renew":false}`
		default:
			t.Errorf("unexpected path: %s", request.URL.Path)
		}
		return syncTestResponse(request, http.StatusOK, body), nil
	})
	credential := syncTestCredential(t)
	done := make(chan error, 1)
	go func() {
		result, err := newCodexDriver().Observe(t.Context(), credential, subscriptionruntime.NewTarget([]byte(`{"base_url":"https://relay.example/tenant"}`)))
		if err == nil {
			var payload struct {
				Subscription struct {
					State string `json:"state"`
					End   int64  `json:"period_end_ms"`
					Renew *bool  `json:"will_renew"`
				} `json:"subscription"`
			}
			err = json.Unmarshal(result.Payload, &payload)
			if err == nil && (payload.Subscription.State != "confirmed" ||
				payload.Subscription.End != 1796083200000 || payload.Subscription.Renew == nil || *payload.Subscription.Renew) {
				err = errors.New("subscription fields were not normalized")
			}
			if strings.Contains(string(result.Payload), "observed_at_ms") {
				err = errors.New("subscription contains a separate observation timestamp")
			}
		}
		done <- err
	}()
	paths := make(map[string]bool)
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for len(paths) < 3 {
		select {
		case path := <-started:
			paths[path] = true
		case <-timer.C:
			close(release)
			<-done
			t.Fatalf("queries were serialized: only %d started before a response was released", len(paths))
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAccountSyncKeepsQuotaWhenSubscriptionIsUnavailable(t *testing.T) {
	for _, test := range []struct {
		name, body, state string
		status            int
	}{
		{"renewing", `{"active_until":"2026-12-01T00:00:00Z","will_renew":true}`, "confirmed", 200},
		{"unknown renewal", `{"active_until":"2026-12-01T00:00:00Z"}`, "confirmed", 200},
		{"no record", "{}", "unavailable", 404},
		{"forbidden", "<html>challenge</html>", "error", 403},
		{"unauthorized", "{}", "error", 401},
		{"rate limited", "{}", "error", 429},
		{"invalid date", `{"active_until":"invalid"}`, "error", 200},
		{"invalid renewal", `{"active_until":"2026-12-01T00:00:00Z","will_renew":"false"}`, "error", 200},
		{"timeout", "", "error", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = previous })
			http.DefaultTransport = targetRoundTripper(func(request *http.Request) (*http.Response, error) {
				switch request.URL.Path {
				case "/backend-api/wham/usage":
					return syncTestResponse(request, 200, syncUsageBody), nil
				case "/backend-api/wham/rate-limit-reset-credits":
					return syncTestResponse(request, 500, "{}"), nil
				case "/backend-api/subscriptions":
					if test.status == 0 {
						return nil, context.DeadlineExceeded
					}
					return syncTestResponse(request, test.status, test.body), nil
				default:
					t.Errorf("unexpected path %s", request.URL.Path)
					return syncTestResponse(request, 404, "{}"), nil
				}
			})
			result, err := newCodexDriver().Observe(t.Context(), syncTestCredential(t), subscriptionruntime.NewTarget(nil))
			if err != nil {
				t.Fatal(err)
			}
			var snapshot struct {
				Subscription struct {
					State string `json:"state"`
					Renew *bool  `json:"will_renew"`
				} `json:"subscription"`
				Windows []struct{ Used float64 } `json:"quota_windows"`
			}
			if json.Unmarshal(result.Payload, &snapshot) != nil || !result.QuotaObserved ||
				len(snapshot.Windows) != 1 || snapshot.Windows[0].Used != 25 || snapshot.Subscription.State != test.state {
				t.Fatalf("unexpected observation: %s", result.Payload)
			}
			if test.name == "unknown renewal" && snapshot.Subscription.Renew != nil {
				t.Fatal("missing renewal was treated as disabled")
			}
			if test.name == "renewing" && (snapshot.Subscription.Renew == nil || !*snapshot.Subscription.Renew) {
				t.Fatal("renewal flag was lost")
			}
		})
	}
}

func TestAccountSyncCancelsOptionalQueriesWhenQuotaFails(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, body: "{}"},
		{name: "null", status: http.StatusOK, body: "null"},
		{name: "array", status: http.StatusOK, body: "[]"},
	} {
		t.Run(test.name, func(t *testing.T) {
			started := make(chan struct{}, 2)
			cancelled := make(chan struct{}, 2)
			previous := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = previous })
			http.DefaultTransport = targetRoundTripper(func(request *http.Request) (*http.Response, error) {
				if request.URL.Path == "/backend-api/wham/usage" {
					for range 2 {
						select {
						case <-started:
						case <-request.Context().Done():
							return nil, request.Context().Err()
						}
					}
					return syncTestResponse(request, test.status, test.body), nil
				}
				started <- struct{}{}
				<-request.Context().Done()
				cancelled <- struct{}{}
				return nil, request.Context().Err()
			})
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			_, err := newCodexDriver().Observe(ctx, syncTestCredential(t), subscriptionruntime.NewTarget(nil))
			if test.status == http.StatusOK {
				if !errors.Is(err, subscriptionruntime.ErrObservationPayloadInvalid) {
					t.Fatalf("invalid payload was not rejected: %v", err)
				}
			} else {
				var upstream *subscriptionruntime.UpstreamHTTPError
				if !errors.As(err, &upstream) || upstream.StatusCode != test.status {
					t.Fatalf("quota failure lost: %v", err)
				}
			}
			if len(cancelled) != 2 || ctx.Err() != nil {
				t.Fatalf("quota failure lost or optional queries not cancelled: %v, cancelled=%d", err, len(cancelled))
			}
		})
	}
}
