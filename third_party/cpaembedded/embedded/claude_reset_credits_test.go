package embedded

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClaudeResetCreditUsesSelectedGrantAndStableRequest(t *testing.T) {
	credential := testClaudeExecutionCredential()
	var profileCalls, usageCalls, consumeCalls atomic.Int32
	const requestID = "c2059d98-b55d-4271-b993-66c820e9f5e3"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+credential.AccessToken {
			t.Error("reset request lost OAuth authentication")
		}
		switch r.URL.Path {
		case "/team/api/oauth/profile":
			profileCalls.Add(1)
			_, _ = w.Write([]byte(`{"account":{"uuid":"account-one"},"organization":{"uuid":"organization-one"}}`))
		case "/team/api/oauth/usage":
			usageCalls.Add(1)
			if r.Method != http.MethodGet || r.URL.Query().Get("cedar_ember") != "1" {
				t.Error("reset selection did not opt into grants")
			}
			_, _ = w.Write([]byte(`{"cedar_ember":{"eligible":true,"next_grant_id":"grant-selected","grants":[
				{"id":"grant-other","resets_left":1,"usable_now":true},
				{"id":"grant-selected","resets_left":1,"usable_now":true,"ends_at":"2030-10-22T00:00:00Z"}
			]}}`))
		case "/team/api/organizations/organization-one/reset_rate_limits":
			call := consumeCalls.Add(1)
			if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" ||
				r.Header.Get("User-Agent") != claudeUsageUserAgent || r.Header.Get("Anthropic-Beta") != claudeBootstrapBeta {
				t.Error("reset request has incorrect method or headers")
			}
			var payload map[string]string
			if json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload) != 3 ||
				payload["program"] != "cedar_ember" || payload["grant_id"] != "grant-selected" || payload["request_id"] != requestID {
				t.Fatalf("reset request payload = %v", payload)
			}
			if call == 1 {
				http.Error(w, "upstream-private-response", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"result":"reset","cleared":["five_hour","seven_day"],"resets_left":0}`))
		default:
			t.Errorf("unexpected reset request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	options := ClaudeOptions{
		ProfileURL: server.URL + "/team/api/oauth/profile", UsageURL: server.URL + "/team/api/oauth/usage",
		ResetBaseURL: server.URL + "/team", HTTPClient: server.Client(),
		Now: func() time.Time { return time.Date(2026, time.October, 11, 0, 0, 0, 0, time.UTC) },
	}
	prepared, err := PrepareClaudeResetCredit(t.Context(), credential, options)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(prepared), credential.AccessToken) || strings.Contains(string(prepared), credential.RefreshToken) {
		t.Fatal("prepared reset contains authentication material")
	}
	_, err = ConsumeClaudeResetCredit(t.Context(), credential, prepared, requestID, options)
	var upstream *ClaudeUpstreamHTTPError
	if !errors.As(err, &upstream) || upstream.StatusCode != http.StatusServiceUnavailable || consumeCalls.Load() != 1 || strings.Contains(err.Error(), "upstream-private-response") {
		t.Fatalf("failed reset was retried or leaked a response: %v", err)
	}
	result, err := ConsumeClaudeResetCredit(t.Context(), credential, prepared, requestID, options)
	if err != nil || result.Result != "reset" || len(result.Cleared) != 2 || profileCalls.Load() != 1 || usageCalls.Load() != 1 || consumeCalls.Load() != 2 {
		t.Fatalf("retry did not retain selection: result=%#v err=%v requests=%d/%d/%d", result, err, profileCalls.Load(), usageCalls.Load(), consumeCalls.Load())
	}
	credential.OrganizationUUID = "another-organization"
	if _, err := ConsumeClaudeResetCredit(t.Context(), credential, prepared, requestID, options); !errors.Is(err, ErrClaudeOrganizationIdentityChanged) || consumeCalls.Load() != 2 {
		t.Fatalf("reset crossed organization identity: %v", err)
	}
}

func TestPrepareClaudeResetCreditRejectsUnavailableSelection(t *testing.T) {
	for _, block := range []string{
		`null`,
		`{"eligible":false}`,
		`{"eligible":true,"next_grant_id":"missing","grants":[{"id":"other","resets_left":1,"usable_now":true}]}`,
		`{"eligible":true,"next_grant_id":"grant","grants":[{"id":"grant","resets_left":0,"usable_now":true}]}`,
		`{"eligible":true,"next_grant_id":"grant","grants":[{"id":"grant","resets_left":1,"usable_now":false}]}`,
		`{"eligible":true,"next_grant_id":"grant","grants":[{"id":"grant","resets_left":1,"usable_now":true,"paused":true}]}`,
		`{"eligible":true,"next_grant_id":"grant","grants":[{"id":"grant","resets_left":1,"usable_now":true,"ends_at":"2020-01-01T00:00:00Z"}]}`,
		`{"eligible":true,"next_grant_id":"grant","cooldown_until":"2040-01-01T00:00:00Z","grants":[{"id":"grant","resets_left":1,"usable_now":true}]}`,
	} {
		t.Run(block, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("selection must never consume a credit")
				}
				if r.URL.Path == "/profile" {
					_, _ = w.Write([]byte(`{"account":{"uuid":"account-one"},"organization":{"uuid":"organization-one"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"cedar_ember":` + block + `}`))
			}))
			defer server.Close()
			_, err := PrepareClaudeResetCredit(t.Context(), testClaudeExecutionCredential(), ClaudeOptions{
				ProfileURL: server.URL + "/profile", UsageURL: server.URL + "/usage", HTTPClient: server.Client(),
			})
			if !errors.Is(err, ErrClaudeResetCreditUnavailable) {
				t.Fatalf("unavailable grant was accepted: %v", err)
			}
		})
	}
}
