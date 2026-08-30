package embedded

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBeginGrokDeviceAuthorizationUsesDiscoveryAndReturnsOneChallenge(t *testing.T) {
	now := time.Date(2026, time.August, 18, 8, 0, 0, 0, time.UTC)
	var deviceCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/discovery":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"device_authorization_endpoint": serverURL(r) + "/device",
				"token_endpoint":                serverURL(r) + "/token",
				"userinfo_endpoint":             serverURL(r) + "/userinfo",
			})
		case "/device":
			deviceCalls.Add(1)
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("client_id") == "" || !strings.Contains(r.Form.Get("scope"), "offline_access") {
				t.Fatalf("device form = %v", r.Form)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code": "device-secret", "user_code": "ABCD-EFGH",
				"verification_uri":          "https://auth.x.ai/device",
				"verification_uri_complete": "https://auth.x.ai/device?user_code=ABCD-EFGH",
				"expires_in":                1200, "interval": 5,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	challenge, err := BeginGrokDeviceAuthorization(t.Context(), GrokOptions{
		DiscoveryURL: server.URL + "/discovery", HTTPClient: server.Client(), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if challenge.VerificationURL != "https://auth.x.ai/device?user_code=ABCD-EFGH" || challenge.UserCode != "ABCD-EFGH" ||
		challenge.ExpiresAt != now.Add(20*time.Minute) || challenge.PollInterval != 5*time.Second ||
		challenge.State.DeviceCode != "device-secret" || deviceCalls.Load() != 1 {
		t.Fatalf("challenge = %#v, calls = %d", challenge, deviceCalls.Load())
	}
}

func TestParseGrokCredentialRejectsUntrustedTokenEndpoint(t *testing.T) {
	raw := []byte(`{
		"type":"grok","access_token":"access","refresh_token":"refresh",
		"account_id":"account-1","email":"owner@example.com",
		"expired":"2028-01-01T00:00:00Z","token_endpoint":"http://127.0.0.1/token"
	}`)
	if _, err := ParseGrokCredentialJSON(raw); err == nil {
		t.Fatal("untrusted token endpoint was accepted")
	}
}

func TestPollGrokDeviceAuthorizationPerformsExactlyOneRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"slow_down"}`))
	}))
	defer server.Close()

	result, err := PollGrokDeviceAuthorizationOnce(t.Context(), GrokDeviceState{
		DeviceCode: "device-secret", TokenEndpoint: server.URL, UserInfoEndpoint: server.URL + "/userinfo",
		PollIntervalSeconds: 5,
	}, GrokOptions{HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != GrokDevicePending || result.PollInterval != 10*time.Second ||
		result.State.PollIntervalSeconds != 10 || calls.Load() != 1 {
		t.Fatalf("poll = %#v, calls = %d", result, calls.Load())
	}
}

func TestPollGrokDeviceAuthorizationMapsTerminalDeviceStates(t *testing.T) {
	for _, test := range []struct {
		name   string
		code   string
		status GrokDeviceStatus
	}{
		{name: "denied", code: "access_denied", status: GrokDeviceDenied},
		{name: "expired", code: "expired_token", status: GrokDeviceExpired},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": test.code})
			}))
			defer server.Close()

			result, err := PollGrokDeviceAuthorizationOnce(t.Context(), GrokDeviceState{
				DeviceCode: "device-secret", TokenEndpoint: server.URL,
				UserInfoEndpoint: server.URL + "/userinfo", PollIntervalSeconds: 5,
			}, GrokOptions{HTTPClient: server.Client()})
			if err != nil || result.Status != test.status {
				t.Fatalf("poll = %#v, %v", result, err)
			}
		})
	}
}

func TestPollGrokDeviceAuthorizationVerifiesUserInfoIdentity(t *testing.T) {
	now := time.Date(2026, time.August, 18, 8, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "access-secret", "refresh_token": "refresh-secret",
				"id_token":   testUnsignedJWT(map[string]any{"sub": "account-1"}),
				"token_type": "Bearer", "expires_in": 3600,
			})
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer access-secret" {
				t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sub": "account-1", "email": "owner@example.com", "email_verified": true,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := PollGrokDeviceAuthorizationOnce(t.Context(), GrokDeviceState{
		DeviceCode: "device-secret", TokenEndpoint: server.URL + "/token",
		UserInfoEndpoint: server.URL + "/userinfo", PollIntervalSeconds: 5,
	}, GrokOptions{HTTPClient: server.Client(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != GrokDeviceAuthorized || result.Credential.AccountID != "account-1" ||
		result.Credential.Email != "owner@example.com" || result.Credential.Type != ProviderGrok ||
		result.Credential.Expire != now.Add(time.Hour).Format(time.RFC3339) {
		t.Fatalf("poll = %#v", result)
	}
}

func TestImportGrokCanonicalUsesAccountIDAndUpdatesEmail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sub": "account-1", "email": "new@example.com", "email_verified": true,
		})
	}))
	defer server.Close()
	raw := []byte(`{
		"type":"grok","access_token":"access","refresh_token":"refresh",
		"account_id":"account-1","email":"old@example.com","expired":"2028-01-01T00:00:00Z",
		"token_endpoint":"https://auth.x.ai/oauth/token"
	}`)
	credential, err := ImportGrokCredential(t.Context(), raw, GrokOptions{
		UserInfoURL: server.URL, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccountID != "account-1" || credential.Email != "new@example.com" {
		t.Fatalf("credential = %#v", credential)
	}
}

func TestImportGrokNativeCPAFileVerifiesSubjectAndStripsControlFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sub": "account-1", "email": "owner@example.com", "email_verified": true,
		})
	}))
	defer server.Close()
	raw := []byte(`{
		"type":"xai","access_token":"access","refresh_token":"refresh",
		"sub":"account-1","email":"owner@example.com","expired":"2028-01-01T00:00:00Z",
		"token_endpoint":"https://auth.x.ai/oauth2/token","base_url":"https://api.x.ai/v1",
		"auth_kind":"oauth","disabled":false,"websockets":true,"prefix":"legacy","note":"backup"
	}`)
	credential, err := ImportGrokCredential(t.Context(), raw, GrokOptions{
		UserInfoURL: server.URL, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := MarshalGrokCredential(credential)
	if err != nil {
		t.Fatal(err)
	}
	for _, discarded := range []string{"base_url", "auth_kind", "disabled", "websockets", "prefix", "note", `"sub"`} {
		if strings.Contains(string(canonical), discarded) {
			t.Fatalf("canonical credential retained %q: %s", discarded, canonical)
		}
	}
}

func TestRefreshGrokCredentialOncePreservesRotatingTokensAndIdentity(t *testing.T) {
	now := time.Date(2026, time.August, 18, 8, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "new-access", "refresh_token": "new-refresh",
				"token_type": "Bearer", "expires_in": 3600,
			})
		case "/userinfo":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sub": "account-1", "email": "new@example.com", "email_verified": true,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	current := GrokCredential{
		Type: ProviderGrok, AccessToken: "old-access", RefreshToken: "old-refresh", IDToken: "old-id",
		AccountID: "account-1", Email: "old@example.com", Expire: "2026-08-18T07:00:00Z",
		TokenEndpoint: server.URL + "/token",
	}
	refreshed, err := RefreshGrokCredentialOnce(t.Context(), current, GrokOptions{
		UserInfoURL: server.URL + "/userinfo", HTTPClient: server.Client(), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken != "new-access" || refreshed.RefreshToken != "new-refresh" ||
		refreshed.IDToken != "old-id" || refreshed.AccountID != "account-1" || refreshed.Email != "new@example.com" {
		t.Fatalf("refreshed = %#v", refreshed)
	}
}

func TestRefreshGrokCredentialOncePreservesNonJSONTokenEndpointFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Retry-After", "1800")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("temporarily unavailable"))
	}))
	defer server.Close()
	current := GrokCredential{
		Type: ProviderGrok, AccessToken: "old-access", RefreshToken: "old-refresh",
		AccountID: "account-1", Email: "owner@example.com", Expire: "2026-08-18T07:00:00Z",
		TokenEndpoint: server.URL + "/token",
	}

	_, err := RefreshGrokCredentialOnce(t.Context(), current, GrokOptions{
		UserInfoURL: server.URL + "/userinfo", HTTPClient: server.Client(),
	})
	var tokenErr *GrokTokenEndpointError
	if !errors.As(err, &tokenErr) || tokenErr.StatusCode != http.StatusServiceUnavailable ||
		tokenErr.Code != "" || tokenErr.RetryAfter != 30*time.Minute {
		t.Fatalf("RefreshGrokCredentialOnce() error = %#v", err)
	}
}

func TestDiscoverGrokModelsUsesOAuthExecutionIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-secret" ||
			r.Header.Get("X-XAI-Token-Auth") != "xai-grok-cli" ||
			r.Header.Get("x-grok-client-identifier") != "grok-shell" {
			t.Fatalf("model headers = %#v", r.Header)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"grok-4.3"},{"id":"grok-code-fast-1"},{"id":"grok-4.3"}]}`))
	}))
	defer server.Close()
	models, err := DiscoverGrokModels(t.Context(), GrokCredential{AccessToken: "access-secret"}, GrokOptions{
		ModelsURL: server.URL, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "grok-4.3" || models[1] != "grok-code-fast-1" {
		t.Fatalf("models = %v", models)
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}

func testUnsignedJWT(claims map[string]any) string {
	header, _ := json.Marshal(map[string]string{"alg": "none"})
	payload, _ := json.Marshal(claims)
	return base64RawURL(header) + "." + base64RawURL(payload) + "."
}

func base64RawURL(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}
