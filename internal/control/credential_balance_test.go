package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
)

func TestBalanceKindForChannel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		channel channel.ID
		want    balanceKind
		support bool
	}{
		{"deepseek", channel.DeepSeek, balanceKindDeepSeek, true},
		{"moonshot", channel.MoonshotAI, balanceKindMoonshot, true},
		{"openai", channel.OpenAI, "", false},
		{"zhipu (no public balance endpoint)", channel.ZhipuAI, "", false},
		{"unknown", channel.ID("nope"), "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			kind, ok := balanceKindForChannel(tc.channel)
			if ok != tc.support {
				t.Fatalf("supported = %v, want %v", ok, tc.support)
			}
			if kind != tc.want {
				t.Fatalf("kind = %q, want %q", kind, tc.want)
			}
		})
	}
}

func TestBalanceBaseURL(t *testing.T) {
	t.Parallel()

	t.Run("falls back to provider default when params empty", func(t *testing.T) {
		t.Parallel()
		got, err := balanceBaseURL(balanceKindDeepSeek, json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "https://api.deepseek.com" {
			t.Fatalf("base url = %q", got)
		}
	})

	t.Run("prefers configured base_url", func(t *testing.T) {
		t.Parallel()
		got, err := balanceBaseURL(
			balanceKindDeepSeek,
			json.RawMessage(`{"base_url":"http://127.0.0.1:8123/"}`),
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "http://127.0.0.1:8123" {
			t.Fatalf("base url = %q (trailing slash must be trimmed)", got)
		}
	})

	t.Run("rejects malformed base_url", func(t *testing.T) {
		t.Parallel()
		if _, err := balanceBaseURL(balanceKindDeepSeek, json.RawMessage(`{"base_url":":::"}`)); err == nil {
			t.Fatal("expected validation error for malformed base url")
		}
	})

	t.Run("moonshot default keeps v1 suffix", func(t *testing.T) {
		t.Parallel()
		got, err := balanceBaseURL(balanceKindMoonshot, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "https://api.moonshot.cn/v1" {
			t.Fatalf("base url = %q", got)
		}
	})
}

func TestParseDeepSeekBalance(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"is_available": true,
		"balance_infos": [
			{"currency":"CNY","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"},
			{"currency":"USD","total_balance":"5.50","granted_balance":"0.00","topped_up_balance":"5.50"}
		]
	}`)

	entries, err := parseDeepSeekBalance(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].Currency != "CNY" || entries[0].Total != "110.00" {
		t.Fatalf("first entry = %+v", entries[0])
	}
	if entries[0].Granted != "10.00" || entries[0].ToppedUp != "100.00" {
		t.Fatalf("first entry breakdown = %+v", entries[0])
	}
	if entries[1].Currency != "USD" || entries[1].Total != "5.50" {
		t.Fatalf("second entry = %+v", entries[1])
	}
}

func TestParseMoonshotBalance(t *testing.T) {
	t.Parallel()

	body := []byte(`{
		"code": 0,
		"data": {"available_balance": 49.58894, "voucher_balance": 0, "cash_balance": 49.58894},
		"scode": "0x0",
		"status": true
	}`)

	entries, err := parseMoonshotBalance(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Currency != "CNY" {
		t.Fatalf("currency = %q", entries[0].Currency)
	}
	if entries[0].Total != "49.58894" {
		t.Fatalf("total = %q", entries[0].Total)
	}
}

// TestFetchCredentialBalance exercises the request shape and parsing against a
// stub upstream, so the bearer header and endpoint path are covered too.
func TestFetchCredentialBalance(t *testing.T) {
	t.Parallel()

	var gotPath string
	var gotAuth string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"7.25","granted_balance":"0","topped_up_balance":"7.25"}]}`))
	}))
	defer upstream.Close()

	service := &Service{httpClient: upstream.Client()}
	entries, err := service.fetchCredentialBalance(
		context.Background(), balanceKindDeepSeek, upstream.URL, "sk-test-key",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/user/balance" {
		t.Fatalf("upstream path = %q, want /user/balance", gotPath)
	}
	if gotAuth != "Bearer sk-test-key" {
		t.Fatalf("authorization header = %q", gotAuth)
	}
	if len(entries) != 1 || entries[0].Total != "7.25" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestFetchCredentialBalanceRejectsUpstreamError(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()

	service := &Service{httpClient: upstream.Client()}
	if _, err := service.fetchCredentialBalance(
		context.Background(), balanceKindDeepSeek, upstream.URL, "sk-bad",
	); err == nil {
		t.Fatal("expected error for non-200 upstream response")
	}
}

// TestFetchCredentialBalanceMapsUpstreamStatus pins the distinction between an
// upstream that declined the credential (reported in-band, never as HTTP 401 —
// the console treats 401 on a management call as an expired session) and a
// genuine upstream fault.
func TestFetchCredentialBalanceMapsUpstreamStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		status     int
		rejection  bool
		wantReason string
	}{
		{"unauthorized", http.StatusUnauthorized, true, "rejected"},
		{"forbidden", http.StatusForbidden, true, "rejected"},
		{"rate limited", http.StatusTooManyRequests, true, "rate limited"},
		{"server fault", http.StatusInternalServerError, false, ""},
		{"bad gateway", http.StatusBadGateway, false, ""},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer upstream.Close()

			service := &Service{httpClient: upstream.Client()}
			_, err := service.fetchCredentialBalance(
				context.Background(), balanceKindDeepSeek, upstream.URL, "sk-x",
			)
			if err == nil {
				t.Fatalf("status %d: expected error", tc.status)
			}

			var rejection *balanceUpstreamRejection
			if got := errors.As(err, &rejection); got != tc.rejection {
				t.Fatalf("status %d: rejection = %v, want %v (err=%v)", tc.status, got, tc.rejection, err)
			}
			if tc.rejection {
				if !strings.Contains(rejection.Reason, tc.wantReason) {
					t.Fatalf("status %d: reason = %q", tc.status, rejection.Reason)
				}
				return
			}
			if !errors.Is(err, app_errors.ErrBadGateway) {
				t.Fatalf("status %d: error = %v, want ErrBadGateway", tc.status, err)
			}
		})
	}
}

func TestFetchCredentialBalanceRequiresClient(t *testing.T) {
	t.Parallel()

	service := &Service{}
	if _, err := service.fetchCredentialBalance(
		context.Background(), balanceKindDeepSeek, "https://api.deepseek.com", "sk-x",
	); err == nil {
		t.Fatal("expected error when no http client is wired")
	}
}

func TestBalanceEndpointPaths(t *testing.T) {
	t.Parallel()

	if got := balanceEndpointPaths[balanceKindDeepSeek]; got != "/user/balance" {
		t.Fatalf("deepseek path = %q", got)
	}
	if got := balanceEndpointPaths[balanceKindMoonshot]; got != "/users/me/balance" {
		t.Fatalf("moonshot path = %q", got)
	}
}

// TestQueryCredentialBalanceValidatesInput guards the cheap guards before any
// database or upstream work happens.
func TestQueryCredentialBalanceValidatesInput(t *testing.T) {
	t.Parallel()

	service := &Service{httpClient: &http.Client{Timeout: time.Second}}
	for _, tc := range []struct{ group, credential uint }{
		{0, 1},
		{1, 0},
		{0, 0},
	} {
		if _, err := service.QueryCredentialBalance(
			context.Background(), tc.group, tc.credential, false,
		); err == nil {
			t.Fatalf("expected error for group=%d credential=%d", tc.group, tc.credential)
		}
	}
}

// TestQueryCredentialBalanceWithoutDependencies keeps the nil-safety contract:
// a partially constructed Service must not panic.
func TestQueryCredentialBalanceWithoutDependencies(t *testing.T) {
	t.Parallel()

	service := &Service{}
	_, err := service.QueryCredentialBalance(context.Background(), 1, 1, false)
	if err == nil {
		t.Fatal("expected error when service dependencies are missing")
	}
	if !strings.Contains(err.Error(), "INTERNAL") && !strings.Contains(err.Error(), "internal") {
		// The exact message is i18n-rendered elsewhere; only assert it failed.
		t.Logf("error = %v", err)
	}
}

func TestSumBalanceEntries(t *testing.T) {
	t.Parallel()

	t.Run("empty input yields empty slice", func(t *testing.T) {
		t.Parallel()
		if got := sumBalanceEntries(nil); len(got) != 0 {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("sums per currency without float drift", func(t *testing.T) {
		t.Parallel()
		got := sumBalanceEntries([]CredentialBalanceEntry{
			{Currency: "CNY", Total: "0.87", Granted: "0.00", ToppedUp: "0.87"},
			{Currency: "CNY", Total: "0.13", Granted: "0.10", ToppedUp: "0.03"},
			{Currency: "USD", Total: "1.005", Granted: "0", ToppedUp: "1.005"},
		})
		if len(got) != 2 {
			t.Fatalf("entries = %+v", got)
		}
		// CNY sorts before USD.
		if got[0].Currency != "CNY" || got[0].Total != "1.00" {
			t.Fatalf("CNY total = %q", got[0].Total)
		}
		if got[0].Granted != "0.10" || got[0].ToppedUp != "0.90" {
			t.Fatalf("CNY breakdown = %+v", got[0])
		}
		if got[1].Currency != "USD" || got[1].Total != "1.005" {
			t.Fatalf("USD = %+v", got[1])
		}
	})

	t.Run("ignores unparsable values", func(t *testing.T) {
		t.Parallel()
		got := sumBalanceEntries([]CredentialBalanceEntry{
			{Currency: "CNY", Total: "1.50"},
			{Currency: "CNY", Total: ""},
			{Currency: "CNY", Total: "not-a-number"},
		})
		if len(got) != 1 || got[0].Total != "1.50" {
			t.Fatalf("entries = %+v", got)
		}
	})
}

func TestBalanceCacheRespectsTTL(t *testing.T) {
	t.Parallel()

	current := time.Unix(0, 0)
	cache := newBalanceCache(time.Minute, func() time.Time { return current })

	if _, ok := cache.fresh(7); ok {
		t.Fatal("empty cache reported a fresh entry")
	}
	cache.put(7, CredentialBalanceResponse{CredentialID: 7, Available: true})
	if _, ok := cache.fresh(7); !ok {
		t.Fatal("entry should be fresh immediately after put")
	}

	current = current.Add(59 * time.Second)
	if _, ok := cache.fresh(7); !ok {
		t.Fatal("entry should still be fresh before the TTL elapses")
	}

	current = current.Add(2 * time.Second)
	if _, ok := cache.fresh(7); ok {
		t.Fatal("entry should expire after the TTL")
	}
	if _, ok := cache.get(7); !ok {
		t.Fatal("expired entry should remain readable via get")
	}
}

// TestBalanceCacheNilSafety keeps a Service built without the cache (as some
// tests do) from panicking.
func TestBalanceCacheNilSafety(t *testing.T) {
	t.Parallel()

	var cache *balanceCache
	if _, ok := cache.fresh(1); ok {
		t.Fatal("nil cache reported a fresh entry")
	}
	cache.put(1, CredentialBalanceResponse{})
	if _, ok := cache.get(1); ok {
		t.Fatal("nil cache returned an entry")
	}
}
