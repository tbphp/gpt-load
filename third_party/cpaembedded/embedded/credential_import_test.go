package embedded

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func importTestJWT(account, email string) string {
	raw, _ := json.Marshal(map[string]any{
		"https://api.openai.com/auth":    map[string]string{"chatgpt_account_id": account},
		"https://api.openai.com/profile": map[string]string{"email": email},
		"exp":                            int64(2000000000),
	})
	return "e30." + base64.RawURLEncoding.EncodeToString(raw) + ".c2ln"
}

func TestImportCodexCredentialCompletesClaimsWithoutRefresh(t *testing.T) {
	raw, _ := json.Marshal(map[string]string{
		"type": "codex", "access_token": importTestJWT("account-one", "owner@example.com"),
		"refresh_token": "refresh", "client_id": "app_EMoamEEZ73f0CkXaXp7hrann",
	})
	value, err := ImportCodexCredential(t.Context(), raw, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if value.AccountID != "account-one" || value.Email != "owner@example.com" {
		t.Fatal("import did not recover the token identity")
	}
	canonical, _ := json.Marshal(value)
	if strings.Contains(string(canonical), "client_id") {
		t.Fatal("temporary OAuth client entered canonical data")
	}
}

func TestImportCodexCredentialBootstrapsRefreshOnlyOnce(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh" || r.Form.Get("client_id") != "app_EMoamEEZ73f0CkXaXp7hrann" {
			t.Fatal("unexpected token exchange contract")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  importTestJWT("account-one", "owner@example.com"),
			"refresh_token": "rotated", "expires_in": 3600,
		})
	}))
	defer server.Close()
	value, err := ImportCodexCredential(t.Context(), []byte(`{"type":"codex","refresh_token":"refresh"}`), Options{TokenURL: server.URL, HTTPClient: server.Client()})
	if err != nil || calls != 1 || value.AccountID != "account-one" || value.RefreshToken != "rotated" {
		t.Fatalf("bootstrap calls=%d error=%v", calls, err)
	}
}

func TestImportCodexCredentialRejectsConflictingAccount(t *testing.T) {
	raw, _ := json.Marshal(map[string]string{
		"type": "codex", "access_token": importTestJWT("account-other", ""),
		"account_id": "account-one", "refresh_token": "refresh",
	})
	if _, err := ImportCodexCredential(t.Context(), raw, Options{}); err == nil {
		t.Fatal("accepted conflicting account identity")
	}
}

func TestImportClaudeCredentialCompletesProfileWithoutRefresh(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer access" {
			t.Fatal("unexpected profile request")
		}
		_, _ = fmt.Fprint(w, `{"account":{"uuid":"account-one","email":"owner@example.com"},"organization":{"uuid":"org-one"}}`)
	}))
	defer server.Close()
	value, err := ImportClaudeCredential(t.Context(), []byte(`{"type":"claude","access_token":"access","refresh_token":"refresh","expired":"2030-01-01T00:00:00Z","client_id":"9d1c250a-e61b-44d9-88ed-5944d1962f5e"}`), ClaudeOptions{ProfileURL: server.URL, HTTPClient: server.Client()})
	if err != nil || calls != 1 || value.AccountUUID != "account-one" || value.Email != "owner@example.com" || value.OrganizationUUID != "org-one" || len(value.DeviceIDs) != 1 {
		t.Fatalf("profile calls=%d error=%v", calls, err)
	}
}

func TestImportClaudeCredentialRefreshOnlyBootstrapsOnce(t *testing.T) {
	tokenCalls, profileCalls := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			tokenCalls++
			var payload map[string]string
			if json.NewDecoder(r.Body).Decode(&payload) != nil || payload["refresh_token"] != "refresh" || payload["client_id"] != "9d1c250a-e61b-44d9-88ed-5944d1962f5e" {
				t.Fatal("unexpected token exchange contract")
			}
			_, _ = fmt.Fprint(w, `{"access_token":"fresh","refresh_token":"rotated","expires_in":3600}`)
			return
		}
		profileCalls++
		if r.Header.Get("Authorization") != "Bearer fresh" {
			t.Fatal("profile used stale access token")
		}
		_, _ = fmt.Fprint(w, `{"account":{"uuid":"account-one"},"organization":{"uuid":"org-one"}}`)
	}))
	defer server.Close()
	now := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	value, err := ImportClaudeCredential(t.Context(), []byte(`{"type":"claude","refresh_token":"refresh"}`), ClaudeOptions{TokenURL: server.URL, ProfileURL: server.URL, HTTPClient: server.Client(), Now: func() time.Time { return now }})
	if err != nil || tokenCalls != 1 || profileCalls != 1 || value.AccountUUID != "account-one" || value.RefreshToken != "rotated" || value.Expire != now.Add(time.Hour).Format(time.RFC3339) {
		t.Fatalf("token calls=%d profile calls=%d error=%v", tokenCalls, profileCalls, err)
	}
}

func TestImportCredentialsRejectDifferentOAuthClient(t *testing.T) {
	if _, err := ImportCodexCredential(t.Context(), []byte(`{"type":"codex","access_token":"access","refresh_token":"refresh","account_id":"account","client_id":"different"}`), Options{}); err == nil {
		t.Fatal("Codex accepted a different OAuth client")
	}
	if _, err := ImportClaudeCredential(t.Context(), []byte(`{"type":"claude","access_token":"access","refresh_token":"refresh","account_uuid":"account","expired":"2030-01-01T00:00:00Z","client_id":"different"}`), ClaudeOptions{}); err == nil {
		t.Fatal("Claude accepted a different OAuth client")
	}
}

func TestImportCredentialsPreserveCompleteExpiredCPAWithoutNetwork(t *testing.T) {
	client := &http.Client{Transport: claudeRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("complete CPA import must leave refresh scheduling to the caller")
		return nil, nil
	})}
	codex, err := ImportCodexCredential(t.Context(), []byte(`{"type":"codex","access_token":"access","refresh_token":"refresh","account_id":"account","expired":"2000-01-01T00:00:00Z"}`), Options{HTTPClient: client})
	if err != nil || codex.Expire != "2000-01-01T00:00:00Z" {
		t.Fatalf("complete Codex import: %v", err)
	}
	claude, err := ImportClaudeCredential(t.Context(), []byte(`{"type":"claude","access_token":"access","refresh_token":"refresh","account_uuid":"account","expired":"2000-01-01T00:00:00Z"}`), ClaudeOptions{HTTPClient: client})
	if err != nil || claude.Expire != "2000-01-01T00:00:00Z" {
		t.Fatalf("complete Claude import: %v", err)
	}
}

func TestImportCredentialsRejectInvalidDataBeforeNetwork(t *testing.T) {
	client := &http.Client{Transport: claudeRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid input reached the token endpoint")
		return nil, nil
	})}
	for _, raw := range []string{
		`{"type":"codex","refresh_token":"one","refresh_token":"two"}`,
		`{"type":"codex","access_token":"access"}`,
		`{"type":"codex","refresh_token":42}`,
		`{"type":"codex","refresh_token":"refresh","expired":"invalid"}`,
		`{"type":"codex","refresh_token":"refresh","proxy_url":"https://example.test"}`,
		`{"type":"codex","refresh_token":"refresh","client_id":null}`,
	} {
		if _, err := ImportCodexCredential(t.Context(), []byte(raw), Options{HTTPClient: client}); err == nil {
			t.Fatal("accepted invalid Codex import")
		}
	}
	for _, raw := range []string{
		`{"type":"claude","refresh_token":"one","refresh_token":"two"}`,
		`{"type":"claude","access_token":"access"}`,
		`{"type":"claude","refresh_token":42}`,
		`{"type":"claude","refresh_token":"refresh","expired":"invalid"}`,
		`{"type":"claude","refresh_token":"refresh","proxy_url":"https://example.test"}`,
		`{"type":"claude","refresh_token":"refresh","client_id":null}`,
	} {
		if _, err := ImportClaudeCredential(t.Context(), []byte(raw), ClaudeOptions{HTTPClient: client}); err == nil {
			t.Fatal("accepted invalid Claude import")
		}
	}
}

func TestImportClaudeCredentialRejectsProfileOrganizationChange(t *testing.T) {
	client := &http.Client{Transport: claudeRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"account":{"uuid":"account"},"organization":{"uuid":"other"}}`))}, nil
	})}
	_, err := ImportClaudeCredential(t.Context(), []byte(`{"type":"claude","access_token":"access","refresh_token":"refresh","organization_uuid":"expected","expired":"2030-01-01T00:00:00Z"}`), ClaudeOptions{HTTPClient: client})
	if err != ErrClaudeOrganizationIdentityChanged {
		t.Fatalf("organization mismatch error = %v", err)
	}
}

func TestImportClaudeRefreshChecksExistingIdentityWhenTokenMetadataIsMissing(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: claudeRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		body := `{"access_token":"fresh","expires_in":3600}`
		if r.Method == http.MethodGet {
			body = `{"account":{"uuid":"other"}}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	_, err := ImportClaudeCredential(t.Context(), []byte(`{"type":"claude","refresh_token":"refresh","account_uuid":"expected"}`), ClaudeOptions{HTTPClient: client})
	if err != ErrClaudeCredentialIdentityChanged || calls != 2 {
		t.Fatalf("identity check calls=%d error=%v", calls, err)
	}
}

func TestImportClaudeCredentialRefreshesProfileUnauthorizedOnlyOnce(t *testing.T) {
	for _, initialStatus := range []int{401, 403, 503} {
		t.Run(fmt.Sprint(initialStatus), func(t *testing.T) {
			profileCalls, tokenCalls := 0, 0
			client := &http.Client{Transport: claudeRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				status, body := 200, `{"access_token":"fresh","refresh_token":"rotated","expires_in":3600}`
				if r.Method == http.MethodPost {
					tokenCalls++
				} else {
					profileCalls++
					if profileCalls == 1 {
						status, body = initialStatus, `{}`
					} else {
						if r.Header.Get("Authorization") != "Bearer fresh" {
							t.Fatal("profile retry did not use refreshed credentials")
						}
						body = `{"account":{"uuid":"account"}}`
					}
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			value, err := ImportClaudeCredential(t.Context(), []byte(`{"type":"claude","access_token":"stale","refresh_token":"refresh","expired":"2030-01-01T00:00:00Z"}`), ClaudeOptions{HTTPClient: client})
			if initialStatus == 401 {
				if err != nil || tokenCalls != 1 || profileCalls != 2 || value.RefreshToken != "rotated" {
					t.Fatalf("recover profile: tokens=%d profiles=%d err=%v", tokenCalls, profileCalls, err)
				}
			} else if err == nil || tokenCalls != 0 || profileCalls != 1 {
				t.Fatalf("unexpected refresh: tokens=%d profiles=%d err=%v", tokenCalls, profileCalls, err)
			}
		})
	}
}

func TestImportCredentialFailureIsBoundedAndPreservesContext(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			type contextKey struct{}
			ctx := context.WithValue(t.Context(), contextKey{}, "retained")
			calls := 0
			client := &http.Client{Transport: claudeRoundTripperFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Context().Value(contextKey{}) != "retained" {
					t.Fatal("bootstrap discarded caller context")
				}
				return &http.Response{StatusCode: 400, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"invalid_grant","detail":"do-not-expose-token"}`))}, nil
			})}
			var err error
			if provider == "codex" {
				_, err = ImportCodexCredential(ctx, []byte(`{"type":"codex","refresh_token":"refresh"}`), Options{HTTPClient: client})
			} else {
				_, err = ImportClaudeCredential(ctx, []byte(`{"type":"claude","refresh_token":"refresh"}`), ClaudeOptions{HTTPClient: client})
			}
			if calls != 1 || err == nil || strings.Contains(err.Error(), "do-not-expose-token") {
				t.Fatalf("failure calls=%d error=%v", calls, err)
			}
		})
	}
}
