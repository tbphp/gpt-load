package embedded

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

func TestParseCodexCredentialJSON(t *testing.T) {
	t.Parallel()

	raw := []byte(`{
		"type":"codex",
		"access_token":"access-secret",
		"refresh_token":"refresh-secret",
		"id_token":"id-secret",
		"account_id":"account-123",
		"email":"admin@example.com",
		"expired":"2026-08-14T00:00:00Z",
		"last_refresh":"2026-08-13T00:00:00Z",
		"disabled":false,
		"prefix":"team-a",
		"note":"ignored",
		"websockets":true
	}`)

	credential, err := ParseCodexCredentialJSON(raw)
	if err != nil {
		t.Fatalf("ParseCodexCredentialJSON() error = %v", err)
	}
	if credential.AccountID != "account-123" || credential.Email != "admin@example.com" {
		t.Fatalf("credential identity = %#v", credential)
	}
	if credential.AccessToken != "access-secret" || credential.RefreshToken != "refresh-secret" {
		t.Fatal("credential tokens were not retained")
	}
	if credential.Type != ProviderCodex {
		t.Fatalf("credential type = %q", credential.Type)
	}
	encoded, err := json.Marshal(credential)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, field := range []string{"disabled", "prefix", "note", "websockets"} {
		if strings.Contains(string(encoded), field) {
			t.Fatalf("canonical credential retained CPA control %q: %s", field, encoded)
		}
	}
}

func TestParseCodexCredentialJSONRejectsMalformedCPAControlMetadata(t *testing.T) {
	t.Parallel()

	raw := []byte(`{
		"type":"codex",
		"access_token":"access-secret",
		"refresh_token":"refresh-secret",
		"account_id":"account-123",
		"prefix":{"unexpected":true}
	}`)
	if _, err := ParseCodexCredentialJSON(raw); err == nil {
		t.Fatal("ParseCodexCredentialJSON() accepted malformed CPA prefix metadata")
	}
}

func TestParseCodexCredentialJSONRejectsInvalidOrDangerousInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
	}{
		{name: "wrong provider", raw: `{"type":"claude","access_token":"a","refresh_token":"r","account_id":"id"}`},
		{name: "missing account", raw: `{"type":"codex","access_token":"a","refresh_token":"r"}`},
		{name: "missing access token", raw: `{"type":"codex","refresh_token":"r","account_id":"id"}`},
		{name: "proxy override", raw: `{"type":"codex","access_token":"a","refresh_token":"r","account_id":"id","proxy_url":"http://127.0.0.1"}`},
		{name: "headers override", raw: `{"type":"codex","access_token":"a","refresh_token":"r","account_id":"id","headers":{"x":"y"}}`},
		{name: "not object", raw: `[]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseCodexCredentialJSON([]byte(tt.raw)); err == nil {
				t.Fatal("ParseCodexCredentialJSON() error = nil")
			}
		})
	}
}

func TestCodexCredentialExpiresAtUsesStoredTimestampOrJWT(t *testing.T) {
	t.Parallel()
	stored := CodexCredential{Expire: "2026-08-14T00:00:00Z"}
	if got, ok := CodexCredentialExpiresAt(stored); !ok || !got.Equal(time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("stored expiration = %v/%t", got, ok)
	}
	jwt := "e30.eyJleHAiOjE3ODY2NTQ4MDB9.signature"
	if got, ok := CodexCredentialExpiresAt(CodexCredential{AccessToken: jwt}); !ok || got.Unix() != 1786654800 {
		t.Fatalf("JWT expiration = %v/%t", got, ok)
	}
}

func TestBeginCodexBrowserAuthorization(t *testing.T) {
	t.Parallel()

	result, err := BeginCodexBrowserAuthorization()
	if err != nil {
		t.Fatalf("BeginCodexBrowserAuthorization() error = %v", err)
	}
	parsed, err := url.Parse(result.AuthorizationURL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	query := parsed.Query()
	if query.Get("redirect_uri") != CodexRedirectURI {
		t.Fatalf("redirect_uri = %q", query.Get("redirect_uri"))
	}
	if query.Get("state") != result.State || len(result.State) < 32 {
		t.Fatalf("state mismatch or too short: %q", result.State)
	}
	if query.Get("code_challenge") != result.CodeChallenge || result.CodeVerifier == "" {
		t.Fatal("PKCE values were not returned consistently")
	}
	if query.Get("code_challenge_method") != "S256" {
		t.Fatalf("code_challenge_method = %q", query.Get("code_challenge_method"))
	}
}

func TestCompleteCodexBrowserAuthorizationExchangesOnce(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		values, err := url.ParseQuery(string(body))
		if err != nil {
			t.Errorf("ParseQuery() error = %v", err)
		}
		if values.Get("code") != "auth-code" || values.Get("code_verifier") != "verifier" {
			t.Errorf("form = %v", values)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access","refresh_token":"refresh","id_token":"","expires_in":3600}`)
	}))
	defer server.Close()

	credential, err := CompleteCodexBrowserAuthorization(context.Background(), BrowserAuthorizationCompletion{
		ExpectedState: "state",
		ReturnedState: "state",
		Code:          "auth-code",
		CodeVerifier:  "verifier",
	}, Options{TokenURL: server.URL, HTTPClient: server.Client(), AccountID: "account-123"})
	if err != nil {
		t.Fatalf("CompleteCodexBrowserAuthorization() error = %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("token requests = %d, want 1", requests.Load())
	}
	if credential.AccessToken != "access" || credential.RefreshToken != "refresh" || credential.AccountID != "account-123" {
		t.Fatalf("credential = %#v", credential)
	}
}

func TestCompleteCodexBrowserAuthorizationRejectsStateBeforeExchange(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	_, err := CompleteCodexBrowserAuthorization(context.Background(), BrowserAuthorizationCompletion{
		ExpectedState: "expected",
		ReturnedState: "different",
		Code:          "code",
		CodeVerifier:  "verifier",
	}, Options{TokenURL: server.URL, HTTPClient: server.Client(), AccountID: "account-123"})
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("error = %v, want ErrInvalidState", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("token requests = %d, want 0", requests.Load())
	}
}

func TestCompleteCodexBrowserAuthorizationRejectsIncompleteCredential(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access","refresh_token":"refresh","expires_in":3600}`)
	}))
	defer server.Close()

	_, err := CompleteCodexBrowserAuthorization(context.Background(), BrowserAuthorizationCompletion{
		ExpectedState: "state",
		ReturnedState: "state",
		Code:          "auth-code",
		CodeVerifier:  "verifier",
	}, Options{TokenURL: server.URL, HTTPClient: server.Client()})
	if err == nil || !strings.Contains(err.Error(), "account_id") {
		t.Fatalf("error = %v, want missing account_id", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("token requests = %d, want 1", requests.Load())
	}
}

func TestRefreshCodexCredentialOnce(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		body, _ := io.ReadAll(r.Body)
		values, _ := url.ParseQuery(string(body))
		if values.Get("refresh_token") != "old-refresh" {
			t.Errorf("refresh_token = %q", values.Get("refresh_token"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"new-access","refresh_token":"new-refresh","id_token":"","expires_in":7200}`)
	}))
	defer server.Close()

	credential, err := RefreshCodexCredentialOnce(context.Background(), CodexCredential{
		Type:         ProviderCodex,
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		AccountID:    "account-123",
		Email:        "admin@example.com",
	}, Options{TokenURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("RefreshCodexCredentialOnce() error = %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("refresh requests = %d, want 1", requests.Load())
	}
	if credential.AccessToken != "new-access" || credential.RefreshToken != "new-refresh" {
		t.Fatalf("credential = %#v", credential)
	}
	if credential.AccountID != "account-123" || credential.Email != "admin@example.com" {
		t.Fatalf("identity changed: %#v", credential)
	}
}

func TestRefreshCodexCredentialOnceHonorsCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := RefreshCodexCredentialOnce(ctx, CodexCredential{
		Type:         ProviderCodex,
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		AccountID:    "account-123",
	}, Options{TokenURL: server.URL, HTTPClient: server.Client()})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestRefreshCodexCredentialOnceClassifiesTokenEndpointError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_grant","error_description":"refresh token expired","refresh_token":"must-not-escape"}`)
	}))
	defer server.Close()

	_, err := RefreshCodexCredentialOnce(context.Background(), CodexCredential{
		Type:         ProviderCodex,
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		AccountID:    "account-123",
	}, Options{TokenURL: server.URL, HTTPClient: server.Client()})
	var tokenErr *TokenEndpointError
	if !errors.As(err, &tokenErr) || tokenErr.StatusCode != http.StatusBadRequest || tokenErr.Code != "invalid_grant" {
		t.Fatalf("error = %#v, want sanitized invalid_grant", err)
	}
	if strings.Contains(err.Error(), "refresh token expired") || strings.Contains(err.Error(), "must-not-escape") {
		t.Fatalf("TokenEndpointError leaked provider response: %v", err)
	}
}

func TestCodexHTTPExecutorDoesNotFollowRedirects(t *testing.T) {
	for _, mode := range []string{"direct", "custom"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				count := requests.Add(1)
				if count == 1 {
					w.Header().Set("Location", "/second")
					w.WriteHeader(http.StatusTemporaryRedirect)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			executor := NewCodexHTTPExecutor()
			auth := NewCodexAuth("credential-1", CodexCredential{
				Type:         ProviderCodex,
				AccessToken:  "access",
				RefreshToken: "refresh",
				AccountID:    "account-123",
			}, server.URL)
			auth.ProxyURL = "direct"
			if mode == "custom" {
				auth.ProxyURL = server.URL
			}
			_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
				Model:   "gpt-5.2",
				Payload: []byte(`{"model":"gpt-5.2","input":"hello"}`),
				Format:  sdktranslator.FormatOpenAIResponse,
			}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse})
			if !errors.Is(err, ErrRedirectNotAllowed) {
				t.Fatalf("Execute() error = %v, want ErrRedirectNotAllowed", err)
			}
			if requests.Load() != 1 {
				t.Fatalf("upstream requests = %d, want 1", requests.Load())
			}
		})
	}
}

func TestCodexHTTPExecutorCanonicalFacadeExecutesOnce(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5.2\",\"output\":[]}}\n\n")
	}))
	defer server.Close()
	executor := NewCodexHTTPExecutor()
	credential := CodexCredential{Type: ProviderCodex, AccessToken: "access", RefreshToken: "refresh", AccountID: "account-123"}
	// This lower-level auth call only changes the test endpoint. Production
	// canonical facade deliberately has no base URL input.
	_, _ = executor.Execute(context.Background(), NewCodexAuth("probe", credential, server.URL), cliproxyexecutor.Request{
		Model: "gpt-5.2", Payload: []byte(`{"model":"gpt-5.2","input":"hello"}`), Format: sdktranslator.FormatOpenAIResponse,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse})
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
	var _ HTTPExecutor = executor
}

func TestCodexHTTPExecutorCanonicalFacadeCountsTokensLocally(t *testing.T) {
	t.Parallel()

	executor := NewCodexHTTPExecutor()
	counter, ok := any(executor).(interface {
		CountTokensCanonical(context.Context, string, CodexCredential, ExecuteRequest) (ExecuteResponse, error)
	})
	if !ok {
		t.Fatal("Codex HTTP executor does not expose local CountTokens")
	}
	credential := CodexCredential{
		Type: ProviderCodex, AccessToken: "access", RefreshToken: "refresh", AccountID: "account-123",
	}
	tests := []struct {
		name      string
		format    string
		payload   string
		wantField string
	}{
		{
			name: "OpenAI Responses", format: "openai-response",
			payload: `{"model":"gpt-5.2","input":"hello"}`, wantField: "input_tokens",
		},
		{
			name: "Anthropic", format: "claude",
			payload: `{"model":"gpt-5.2","messages":[{"role":"user","content":"hello"}]}`, wantField: "input_tokens",
		},
		{
			name: "Gemini", format: "gemini",
			payload: `{"model":"gpt-5.2","contents":[{"role":"user","parts":[{"text":"hello"}]}]}`, wantField: "totalTokens",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := counter.CountTokensCanonical(
				t.Context(), "credential-1", credential,
				ExecuteRequest{Model: "gpt-5.2", Format: test.format, Payload: []byte(test.payload)},
			)
			if err != nil {
				t.Fatalf("CountTokensCanonical() error = %v", err)
			}
			var body map[string]any
			if err := json.Unmarshal(response.Payload, &body); err != nil {
				t.Fatalf("decode response %q: %v", response.Payload, err)
			}
			count, ok := body[test.wantField].(float64)
			if !ok || count <= 0 {
				t.Fatalf("CountTokensCanonical() = %s, want positive %q", response.Payload, test.wantField)
			}
			if test.format == "openai-response" && body["object"] != "response.input_tokens" {
				t.Fatalf("OpenAI Responses CountTokens = %s", response.Payload)
			}
		})
	}
}

func TestCodexHTTPExecutorLoadsSupportedClientTranslators(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		format        sdktranslator.Format
		payload       string
		sourceField   string
		responseField string
	}{
		{name: "OpenAI chat", format: sdktranslator.FormatOpenAI, payload: `{"messages":[{"role":"user","content":"hello"}],"store":false}`, sourceField: "messages", responseField: "choices"},
		{name: "Anthropic", format: sdktranslator.FormatClaude, payload: `{"messages":[{"role":"user","content":"hello"}],"max_tokens":64}`, sourceField: "messages", responseField: "content"},
		{name: "Gemini", format: sdktranslator.FormatGemini, payload: `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`, sourceField: "contents", responseField: "candidates"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var upstreamBody []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamBody, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5.2\",\"output\":[]}}\n\n")
			}))
			defer server.Close()
			executor := NewCodexHTTPExecutor()
			auth := NewCodexAuth("probe", CodexCredential{
				Type: ProviderCodex, AccessToken: "access", RefreshToken: "refresh", AccountID: "account-123",
			}, server.URL)
			response, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
				Model: "gpt-5.2", Payload: []byte(test.payload), Format: test.format,
			}, cliproxyexecutor.Options{SourceFormat: test.format, ResponseFormat: test.format})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			var upstream map[string]any
			if err := json.Unmarshal(upstreamBody, &upstream); err != nil {
				t.Fatalf("decode upstream body: %v", err)
			}
			if _, exists := upstream[test.sourceField]; exists {
				t.Fatalf("source request was not translated: %s", upstreamBody)
			}
			if input, exists := upstream["input"].([]any); !exists || len(input) == 0 {
				t.Fatalf("translated input is missing: %s", upstreamBody)
			}
			var clientResponse map[string]any
			if err := json.Unmarshal(response.Payload, &clientResponse); err != nil {
				t.Fatalf("decode client response %q: %v", response.Payload, err)
			}
			if _, exists := clientResponse[test.responseField]; !exists {
				t.Fatalf("response was not translated to %s: %s", test.format, response.Payload)
			}
		})
	}
}

func TestListCodexModelsRequestsOnceAndNormalizesIDs(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/models" || r.URL.Query().Get("client_version") == "" ||
			r.Header.Get("Authorization") != "Bearer access" || r.Header.Get("Chatgpt-Account-Id") != "account-123" {
			t.Errorf("request = %s %s %#v", r.Method, r.URL.String(), r.Header)
		}
		_, _ = io.WriteString(w, `{"models":[{"slug":"gpt-5.2"},{"id":"gpt-5.3"},{"slug":"gpt-5.2"}]}`)
	}))
	defer server.Close()

	models, err := ListCodexModels(context.Background(), CodexCredential{
		Type: ProviderCodex, AccessToken: "access", RefreshToken: "refresh", AccountID: "account-123",
	}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || len(models) != 2 || models[0].ID != "gpt-5.2" || models[1].ID != "gpt-5.3" {
		t.Fatalf("requests=%d models=%#v", requests.Load(), models)
	}
}

func TestObserveCodexAccountUsesFixedUsagePathOnce(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/wham/usage" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Retry-After", "30")
		_, _ = io.WriteString(w, `{"plan_type":"plus","rate_limit":{"primary_window":{"limit_window_seconds":604800,"used_percent":25}}}`)
	}))
	defer server.Close()

	observation, err := ObserveCodexAccount(context.Background(), CodexCredential{
		Type: ProviderCodex, AccessToken: "access", RefreshToken: "refresh", AccountID: "account-123",
	}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || !json.Valid(observation.Payload) || observation.Header.Get("Retry-After") != "30" {
		t.Fatalf("requests=%d observation=%#v", requests.Load(), observation)
	}
}

func TestObserveCodexResetCreditsUsesFixedDetailsPathOnce(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/wham/rate-limit-reset-credits" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer access" || r.Header.Get("Chatgpt-Account-Id") != "account-123" {
			t.Errorf("headers = %#v", r.Header)
		}
		_, _ = io.WriteString(w, `{"available_count":1,"credits":[{"status":"available","expires_at":"2026-09-01T00:00:00Z"}]}`)
	}))
	defer server.Close()

	observation, err := ObserveCodexResetCredits(context.Background(), CodexCredential{
		Type: ProviderCodex, AccessToken: "access", RefreshToken: "refresh", AccountID: "account-123",
	}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || !json.Valid(observation.Payload) {
		t.Fatalf("requests=%d observation=%#v", requests.Load(), observation)
	}
}

func TestConsumeCodexResetCreditUsesStableRedeemRequestIDOnce(t *testing.T) {
	t.Parallel()

	const redeemRequestID = "9f0f4c32-89d2-4bcb-9e19-052940dc2f16"
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/wham/rate-limit-reset-credits/consume" ||
			r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request = %s %s %#v", r.Method, r.URL.Path, r.Header)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["redeem_request_id"] != redeemRequestID || len(body) != 1 {
			t.Errorf("body = %#v, err = %v", body, err)
		}
		_, _ = io.WriteString(w, `{"code":"reset","windows_reset":1}`)
	}))
	defer server.Close()

	result, err := ConsumeCodexResetCredit(context.Background(), CodexCredential{
		Type: ProviderCodex, AccessToken: "access", RefreshToken: "refresh", AccountID: "account-123",
	}, server.URL, redeemRequestID)
	if err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 || !json.Valid(result.Payload) {
		t.Fatalf("requests=%d result=%#v", requests.Load(), result)
	}
}

func TestNewCodexAuthContainsOnlyCanonicalExecutionMetadata(t *testing.T) {
	t.Parallel()

	auth := NewCodexAuth("credential-1", CodexCredential{
		Type:         ProviderCodex,
		AccessToken:  "access",
		RefreshToken: "refresh",
		IDToken:      "id",
		AccountID:    "account-123",
		Email:        "admin@example.com",
		Expire:       time.Now().UTC().Format(time.RFC3339),
	}, "")
	if auth.Provider != ProviderCodex || auth.ID != "credential-1" {
		t.Fatalf("auth identity = %#v", auth)
	}
	if _, ok := auth.Metadata["access_token"]; !ok {
		t.Fatal("access_token metadata missing")
	}
	for _, forbidden := range []string{"proxy_url", "headers", "request_retry", "websockets"} {
		if _, ok := auth.Metadata[forbidden]; ok {
			t.Fatalf("forbidden metadata %q present", forbidden)
		}
	}
	var _ cliproxyauth.ProviderExecutor = NewCodexHTTPExecutor()
}
