package claude

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v8/gptload-embedded/embedded"

	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestClaudeResetCreditObservation(t *testing.T) {
	for _, test := range []struct {
		name        string
		block       string
		known       bool
		incomplete  bool
		expirations []string
	}{
		{
			name: "usable grants retain every credit and sort by expiry",
			block: `,"cedar_ember":{"eligible":true,"grants":[
				{"id":"private-redemption-handle","resets_left":2,"usable_now":true,"ends_at":"2030-10-22T16:00:00Z"},
				{"resets_left":1,"usable_now":true,"ends_at":"2030-10-20T16:00:00Z"},
				{"resets_left":1,"usable_now":true,"paused":true},
				{"resets_left":1,"usable_now":false},
				{"resets_left":1},
				{"resets_left":0,"usable_now":true}
			]}`,
			known:       true,
			expirations: []string{"2030-10-20T16:00:00Z", "2030-10-22T16:00:00Z", "2030-10-22T16:00:00Z"},
		},
		{
			name:        "credit without expiry",
			block:       `,"cedar_ember":{"eligible":true,"grants":[{"resets_left":1,"usable_now":true,"ends_at":null}]}`,
			known:       true,
			expirations: []string{""},
		},
		{name: "missing block"},
		{name: "null block", block: `,"cedar_ember":null`},
		{name: "no grants", block: `,"cedar_ember":{"eligible":true,"grants":[]}`, known: true},
		{name: "ineligible", block: `,"cedar_ember":{"eligible":false,"grants":[{"resets_left":1,"usable_now":true}]}`, known: true},
		{
			name: "expired grants do not count at the observation clock",
			block: `,"cedar_ember":{"eligible":true,"grants":[
				{"resets_left":1,"usable_now":true,"ends_at":"2030-10-11T11:59:59Z"},
				{"resets_left":1,"usable_now":true,"ends_at":"2030-10-11T12:00:00Z"},
				{"resets_left":1,"usable_now":true,"ends_at":"2030-10-11T12:00:01Z"}
			]}`,
			known: true, expirations: []string{"2030-10-11T12:00:01Z"},
		},
		{
			name:  "only expired grants reports zero available",
			block: `,"cedar_ember":{"eligible":true,"grants":[{"resets_left":1,"usable_now":true,"ends_at":"2030-10-11T12:00:00Z"}]}`,
			known: true,
		},
		{name: "invalid reset block type preserves quota", block: `,"cedar_ember":[]`, incomplete: true},
		{name: "invalid credit count type preserves quota", block: `,"cedar_ember":{"eligible":true,"grants":[{"resets_left":"1","usable_now":true}]}`, incomplete: true},
		{name: "negative credit count preserves quota", block: `,"cedar_ember":{"eligible":true,"grants":[{"resets_left":-1,"usable_now":true}]}`, incomplete: true},
		{name: "excessive credit count preserves quota", block: `,"cedar_ember":{"eligible":true,"grants":[{"resets_left":257,"usable_now":true}]}`, incomplete: true},
		{name: "invalid expiry type preserves quota", block: `,"cedar_ember":{"eligible":true,"grants":[{"resets_left":1,"usable_now":true,"ends_at":123}]}`, incomplete: true},
		{name: "invalid expiry format preserves quota", block: `,"cedar_ember":{"eligible":true,"grants":[{"resets_left":1,"usable_now":true,"ends_at":"not-a-time"}]}`, incomplete: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/usage" {
					http.NotFound(w, r)
					return
				}
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Query().Get("cedar_ember") != "1" || r.URL.Query().Get("existing") != "value" {
					t.Errorf("usage request = %s %s; want GET with reset opt-in and preserved query", r.Method, r.URL.RequestURI())
				}
				if r.Header.Get("Authorization") != "Bearer test-access" ||
					r.Header.Get("Anthropic-Beta") != "oauth-2025-04-20" ||
					r.Header.Get("User-Agent") != "claude-cli/"+cpaembedded.ClaudeClientVersion+" (external, cli)" {
					t.Error("usage request is missing the OAuth/reset client headers")
				}
				_, _ = w.Write([]byte(`{"five_hour":{"utilization":25}` + test.block + `}`))
			}))
			defer server.Close()
			observed, err := cpaembedded.ObserveClaudeAccount(t.Context(), cpaembedded.ClaudeCredential{
				Type: "claude", AccessToken: "test-access", RefreshToken: "test-refresh", AccountUUID: "account-one",
				Expire: "2030-01-01T00:00:00Z",
			}, cpaembedded.ClaudeOptions{
				ProfileURL: server.URL + "/profile", RolesURL: server.URL + "/roles", BootstrapURL: server.URL + "/bootstrap",
				UsageURL: server.URL + "/usage?existing=value", HTTPClient: server.Client(),
				Now: func() time.Time { return time.Date(2030, time.October, 11, 12, 0, 0, 0, time.UTC) },
			})
			if err != nil {
				t.Fatal(err)
			}
			if !observed.UsageObserved || calls.Load() != 1 {
				t.Fatalf("usage observed = %t, requests = %d", observed.UsageObserved, calls.Load())
			}
			if strings.Contains(strings.Join(observed.IncompleteSources, ","), "reset_credits") != test.incomplete {
				t.Fatalf("reset credits incomplete = %v, want %t", observed.IncompleteSources, test.incomplete)
			}
			raw, err := NormalizeObservation(accountObservationFromBridge(observed))
			if err != nil {
				t.Fatal(err)
			}
			var snapshot quotaSnapshot
			if err := json.Unmarshal(raw, &snapshot); err != nil {
				t.Fatal(err)
			}
			if (snapshot.ResetCreditsAvailable != nil) != test.known ||
				test.known && *snapshot.ResetCreditsAvailable != int64(len(test.expirations)) {
				t.Fatalf("reset credit count = %v, want known=%t count=%d", snapshot.ResetCreditsAvailable, test.known, len(test.expirations))
			}
			var got []string
			for _, credit := range snapshot.ResetCredits {
				expires := ""
				if credit.ExpiresAtMS != nil {
					expires = time.UnixMilli(*credit.ExpiresAtMS).UTC().Format(time.RFC3339)
				}
				got = append(got, expires)
			}
			if !reflect.DeepEqual(got, test.expirations) {
				t.Fatalf("credit expirations = %v, want %v", got, test.expirations)
			}
			if len(snapshot.QuotaWindows) != 1 || snapshot.QuotaWindows[0].Utilization == nil || *snapshot.QuotaWindows[0].Utilization != 0.25 {
				t.Fatalf("existing quota changed: %s", raw)
			}
			bridgeJSON, err := json.Marshal(observed)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(bridgeJSON), "private-redemption-handle") || strings.Contains(string(raw), "private-redemption-handle") {
				t.Fatal("observation leaked a redemption handle")
			}
		})
	}
}

func TestClaudeResetResultClassification(t *testing.T) {
	for _, test := range []struct {
		result, reason string
		status         int
	}{
		{result: "reset"},
		{result: "already_used", status: http.StatusConflict},
		{result: "ineligible", status: http.StatusConflict},
		{result: "unavailable", status: -1},
		{result: "not_limited", status: http.StatusBadRequest},
		{result: "cooldown", status: http.StatusTooManyRequests},
		{result: "unavailable", reason: "stamp_indeterminate", status: -1},
		{result: "unavailable", reason: "reset_unconfirmed", status: -1},
		{result: "unknown-private-response", status: -1},
	} {
		t.Run(test.result+"/"+test.reason, func(t *testing.T) {
			got, err := normalizeClaudeResetResult(cpaembedded.ClaudeResetCreditResult{
				Result: test.result, Reason: test.reason, Cleared: []string{"five_hour", "seven_day"},
			})
			if test.status == 0 {
				if err != nil || got.Status != "succeeded" || got.WindowsReset != 2 {
					t.Fatalf("confirmed reset = %#v, %v", got, err)
				}
				return
			}
			if err == nil || got.Status == "succeeded" || strings.Contains(err.Error(), "unknown-private-response") {
				t.Fatalf("unconfirmed reset was accepted or leaked upstream data: %#v, %v", got, err)
			}
			var upstream *subscriptionruntime.UpstreamHTTPError
			if test.status > 0 {
				if !errors.As(err, &upstream) || upstream.StatusCode != test.status {
					t.Fatalf("reset rejection = %v, want HTTP %d", err, test.status)
				}
			} else if errors.As(err, &upstream) {
				t.Fatalf("unknown outcome classified as a definitive rejection: %v", err)
			}
		})
	}
}
