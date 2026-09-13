package codex

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func identityTestToken(t *testing.T, claims map[string]string, padded bool) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"https://api.openai.com/auth": claims})
	if err != nil {
		t.Fatal(err)
	}
	encoding := base64.RawURLEncoding
	if padded {
		encoding = base64.URLEncoding
	}
	return "e30." + encoding.EncodeToString(raw) + ".signature"
}

func TestCodexIdentityUsesUserClaimsWithoutChangingCanonical(t *testing.T) {
	t.Parallel()
	user := identityTestToken(t, map[string]string{"chatgpt_user_id": "user-one"}, false)
	alias := identityTestToken(t, map[string]string{"user_id": "user-two"}, true)
	for _, test := range []struct{ name, idToken, accessToken, want string }{
		{"id token", user, "access", "workspace/user-one"},
		{"access token", "not-a-jwt", user, "workspace/user-one"},
		{"user id alias", alias, "access", "workspace/user-two"},
		{"access token alias", "", alias, "workspace/user-two"},
		{"claim precedence", identityTestToken(t, map[string]string{"chatgpt_user_id": "user-one", "user_id": "user-two"}, false), "access", "workspace/user-one"},
		{"blank primary claim", identityTestToken(t, map[string]string{"chatgpt_user_id": " ", "user_id": "user-two"}, false), "access", "workspace/user-two"},
		{"missing claims", identityTestToken(t, map[string]string{}, false), "access", "workspace"},
		{"invalid token", "e30.!.signature", "access", "workspace"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// 固定旧格式的字段顺序，身份推导不得改变 canonical 字节或持久化派生字段。
			raw := []byte(fmt.Sprintf(`{"type":"codex"%s,"access_token":%q,"refresh_token":"refresh","account_id":"workspace","email":"owner@example.com"}`, identityTestIDTokenField(test.idToken), test.accessToken))
			credential, err := newCodexDriver().Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			if string(credential.Canonical()) != string(raw) {
				t.Fatal("identity derivation changed canonical credential bytes")
			}
			if credential.Identity() != test.want {
				t.Fatalf("identity = %q, want %q", credential.Identity(), test.want)
			}
		})
	}
}

func identityTestIDTokenField(token string) string {
	if token == "" {
		return ""
	}
	return fmt.Sprintf(`,"id_token":%q`, token)
}

func TestCodexIdentityRejectsConflictingTokenUsers(t *testing.T) {
	t.Parallel()
	for _, accessClaim := range []string{"chatgpt_user_id", "user_id"} {
		t.Run(accessClaim, func(t *testing.T) {
			value := Credential{Type: "codex", AccountID: "workspace", RefreshToken: "refresh", IDToken: identityTestToken(t, map[string]string{"chatgpt_user_id": "user-one"}, false), AccessToken: identityTestToken(t, map[string]string{accessClaim: "user-two"}, false)}
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := newCodexDriver().Parse(raw); err == nil {
				t.Fatal("conflicting token identities were accepted")
			}
			if _, err := MarshalCredential(value); err == nil {
				t.Fatal("conflicting token identities were canonicalized")
			}
		})
	}
}

func TestCodexRefreshWithoutNewIDTokenPreservesKnownIdentity(t *testing.T) {
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	http.DefaultTransport = targetRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.String() != "https://auth.openai.com/oauth/token" {
			t.Errorf("unexpected refresh request: %s %s", r.Method, r.URL)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header), Request: r,
			Body: io.NopCloser(strings.NewReader(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`)),
		}, nil
	})
	userToken := identityTestToken(t, map[string]string{"chatgpt_account_id": "workspace", "chatgpt_user_id": "user-one"}, false)
	for _, test := range []struct {
		name, idToken, accessToken string
		allowed                    bool
	}{
		{"retained ID token", userToken, "old-access", true},
		{"last user claim only in replaced access token", "", userToken, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := MarshalCredential(Credential{Type: "codex", AccountID: "workspace", IDToken: test.idToken, AccessToken: test.accessToken, RefreshToken: "old-refresh"})
			if err != nil {
				t.Fatal(err)
			}
			driver := newCodexDriver()
			current, err := driver.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			refreshed, err := driver.Refresh(t.Context(), current)
			if err != nil {
				t.Fatal(err)
			}
			value, err := ParseCredentialJSON(refreshed.Canonical())
			if err != nil {
				t.Fatal(err)
			}
			if value.IDToken != test.idToken || value.AccessToken != "new-access" || value.RefreshToken != "new-refresh" {
				t.Fatal("refresh did not retain the old ID token while replacing access and refresh tokens")
			}
			if allowed := subscriptionruntime.RefreshPreservesIdentity(driver, current, refreshed); allowed != test.allowed {
				t.Fatalf("refresh allowed = %v, want %v", allowed, test.allowed)
			}
		})
	}
}
