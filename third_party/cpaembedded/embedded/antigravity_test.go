package embedded

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	internalcache "github.com/router-for-me/CLIProxyAPI/v7/internal/cache"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/misc"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

type antigravityStatusTestError struct {
	status int
	body   string
}

func (err antigravityStatusTestError) Error() string   { return err.body }
func (err antigravityStatusTestError) StatusCode() int { return err.status }

func TestParseAntigravityCredentialJSONRequiresCanonicalStableIdentity(t *testing.T) {
	credential, err := ParseAntigravityCredentialJSON([]byte(`{
		"type":"antigravity",
		"access_token":"access-secret",
		"refresh_token":"refresh-secret",
		"account_id":"google-account-one",
		"email":"owner@example.com",
		"project_id":"project-one",
		"expires_in":3600,
		"timestamp":1770000000000,
		"expired":"2026-08-17T10:00:00Z",
		"last_refresh":"2026-08-17T09:00:00Z"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccountID != "google-account-one" || credential.ProjectID != "project-one" ||
		credential.Email != "owner@example.com" {
		t.Fatalf("credential = %#v", credential)
	}

	for _, raw := range [][]byte{
		[]byte(`{"type":"antigravity","access_token":"a","refresh_token":"r","email":"owner@example.com","project_id":"project-one","expired":"2026-08-17T10:00:00Z"}`),
		[]byte(`{"type":"antigravity","access_token":"a","refresh_token":"r","account_id":"account-one","email":"owner@example.com","project_id":"project-one","expired":"2026-08-17T10:00:00Z","proxy":"http://example.invalid"}`),
		[]byte(`{"type":"antigravity","access_token":"a","access_token":"b","refresh_token":"r","account_id":"account-one","email":"owner@example.com","project_id":"project-one","expired":"2026-08-17T10:00:00Z"}`),
	} {
		if _, err := ParseAntigravityCredentialJSON(raw); err == nil {
			t.Fatalf("ParseAntigravityCredentialJSON(%s) error = nil", raw)
		}
	}
}

func TestAntigravityCredentialSecretValuesAreRedactable(t *testing.T) {
	credential, err := ParseAntigravityCredentialJSON([]byte(`{
		"type":"antigravity",
		"access_token":"access-secret",
		"refresh_token":"refresh-secret",
		"account_id":"google-account-one",
		"email":"owner@example.com",
		"project_id":"project-one",
		"expired":"2026-08-17T10:00:00Z"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range credential.SecretValues() {
		if secret == "" || !strings.Contains("access-secret refresh-secret google-account-one owner@example.com project-one", secret) {
			t.Fatalf("unexpected secret value %q", secret)
		}
	}
}

func TestCompleteAntigravityBrowserAuthorizationEnrichesIdentityAndProject(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/token":
			if err := request.ParseForm(); err != nil || request.Form.Get("grant_type") != "authorization_code" ||
				request.Form.Get("code") != "authorization-code" || request.Form.Get("redirect_uri") != AntigravityRedirectURI {
				t.Fatalf("token form = %#v, err=%v", request.Form, err)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"access_token":"access-secret","refresh_token":"refresh-secret","expires_in":3600}`))
		case "/userinfo":
			if got := request.Header.Get("Authorization"); got != "Bearer access-secret" {
				t.Fatalf("userinfo authorization = %q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"id":"google-account-one","email":"owner@example.com","verified_email":true}`))
		case "/load":
			if got := request.Header.Get("Authorization"); got != "Bearer access-secret" {
				t.Fatalf("load authorization = %q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"cloudaicompanionProject":"project-one"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	credential, err := CompleteAntigravityBrowserAuthorization(context.Background(), BrowserAuthorizationCompletion{
		ExpectedState: "expected-state", ReturnedState: "expected-state", Code: "authorization-code",
	}, AntigravityOptions{
		TokenURL: server.URL + "/token", UserInfoURL: server.URL + "/userinfo",
		LoadCodeAssistURL: server.URL + "/load", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccountID != "google-account-one" || credential.Email != "owner@example.com" ||
		credential.ProjectID != "project-one" || credential.AccessToken != "access-secret" ||
		credential.RefreshToken != "refresh-secret" {
		t.Fatalf("credential = %#v", credential)
	}
}

func TestDiscoverAntigravityProjectUsesCPAControlPlaneHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/load":
			if request.Header.Get("Accept") != "*/*" || request.Header.Get("User-Agent") != misc.AntigravityRequestUserAgent("") ||
				request.Header.Get("X-Goog-Api-Client") != "" {
				t.Fatalf("loadCodeAssist headers = %#v", request.Header)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"allowedTiers":[{"id":"free-tier","isDefault":true}]}`))
		case "/onboard":
			if request.Header.Get("Accept") != "*/*" || request.Header.Get("User-Agent") != misc.AntigravityOnboardUserUserAgent("") ||
				request.Header.Get("X-Goog-Api-Client") != misc.AntigravityGoogAPIClientUA {
				t.Fatalf("onboardUser headers = %#v", request.Header)
			}
			var body map[string]any
			err := json.NewDecoder(request.Body).Decode(&body)
			metadata, metadataOK := body["metadata"].(map[string]any)
			if err != nil || body["tier_id"] != "free-tier" ||
				!metadataOK || metadata["ide_version"] != misc.AntigravityVersionFromUserAgent(misc.AntigravityOnboardUserUserAgent("")) {
				t.Fatalf("onboardUser body = %#v, error = %v", body, err)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"done":true,"response":{"projectId":"project-one"}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	projectID, _, err := discoverAntigravityProject(t.Context(), "access-secret", AntigravityOptions{
		LoadCodeAssistURL: server.URL + "/load", OnboardUserURL: server.URL + "/onboard", HTTPClient: server.Client(),
	})
	if err != nil || projectID != "project-one" {
		t.Fatalf("discoverAntigravityProject() = %q, %v", projectID, err)
	}
}

func TestBeginAntigravityBrowserAuthorizationUsesFixedCallback(t *testing.T) {
	t.Parallel()

	authorization, err := BeginAntigravityBrowserAuthorization()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorization.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	if authorization.State == "" || parsed.Query().Get("redirect_uri") != AntigravityRedirectURI ||
		parsed.Query().Get("state") != authorization.State {
		t.Fatalf("authorization = %#v", authorization)
	}
}

func TestRefreshAntigravityCredentialOnceKeepsIdentityAndExistingProject(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/token":
			if err := request.ParseForm(); err != nil || request.Form.Get("grant_type") != "refresh_token" ||
				request.Form.Get("refresh_token") != "refresh-secret" {
				t.Errorf("refresh form = %#v, err=%v", request.Form, err)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"access_token":"new-access-secret","expires_in":3600}`))
		case "/userinfo":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"id":"google-account-one","email":"renamed@example.com","verified_email":true}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	refreshed, err := RefreshAntigravityCredentialOnce(context.Background(), AntigravityCredential{
		Type: ProviderAntigravity, AccessToken: "old-access-secret", RefreshToken: "refresh-secret",
		AccountID: "google-account-one", Email: "owner@example.com", ProjectID: "project-one",
		Expire: "2026-08-17T10:00:00Z",
	}, AntigravityOptions{
		TokenURL: server.URL + "/token", UserInfoURL: server.URL + "/userinfo", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken != "new-access-secret" || refreshed.RefreshToken != "refresh-secret" ||
		refreshed.AccountID != "google-account-one" || refreshed.Email != "renamed@example.com" ||
		refreshed.ProjectID != "project-one" {
		t.Fatalf("refreshed = %#v", refreshed)
	}
}

func TestImportAntigravityCredentialEnrichesNativeCPAFile(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/userinfo":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"id":"google-account-one","email":"owner@example.com","verified_email":true}`))
		case "/load":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"projectId":"verified-project"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	credential, err := ImportAntigravityCredential(context.Background(), []byte(`{
		"type":"antigravity",
		"access_token":"access-secret",
		"refresh_token":"refresh-secret",
		"email":"owner@example.com",
		"project_id":"unverified-project",
		"expires_in":3600,
		"timestamp":1770000000000,
		"expired":"2030-01-01T00:00:00Z",
		"disabled":false
	}`), AntigravityOptions{
		UserInfoURL: server.URL + "/userinfo", LoadCodeAssistURL: server.URL + "/load", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccountID != "google-account-one" || credential.ProjectID != "verified-project" ||
		credential.Email != "owner@example.com" {
		t.Fatalf("credential = %#v", credential)
	}
}

func TestImportAntigravityCredentialAcceptsDownloadedCanonicalFile(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/userinfo":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"id":"google-account-one","email":"owner@example.com","verified_email":true}`))
		case "/load":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"projectId":"verified-project"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	raw := `{
		"type":"antigravity",
		"access_token":"access-secret",
		"refresh_token":"refresh-secret",
		"account_id":"google-account-one",
		"email":"owner@example.com",
		"project_id":"downloaded-project",
		"expires_in":3600,
		"timestamp":1770000000000,
		"expired":"2030-01-01T00:00:00Z",
		"last_refresh":"2029-12-31T23:00:00Z"
	}`
	options := AntigravityOptions{
		UserInfoURL: server.URL + "/userinfo", LoadCodeAssistURL: server.URL + "/load", HTTPClient: server.Client(),
	}
	credential, err := ImportAntigravityCredential(t.Context(), []byte(raw), options)
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccountID != "google-account-one" || credential.ProjectID != "verified-project" {
		t.Fatalf("credential = %#v", credential)
	}
	if _, err := ImportAntigravityCredential(
		t.Context(),
		[]byte(strings.Replace(raw, `"account_id":"google-account-one"`, `"account_id":"different-account"`, 1)),
		options,
	); err == nil {
		t.Fatal("ImportAntigravityCredential() accepted a mismatched canonical account ID")
	}
}

func TestImportAntigravityCredentialRefreshesAtMostOnce(t *testing.T) {
	t.Parallel()

	var tokenCalls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/token":
			tokenCalls++
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"access_token":"new-access-secret","expires_in":3600}`))
		case "/userinfo":
			writer.WriteHeader(http.StatusUnauthorized)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	_, err := ImportAntigravityCredential(t.Context(), []byte(`{
		"type":"antigravity",
		"access_token":"expired-access-secret",
		"refresh_token":"refresh-secret",
		"email":"owner@example.com",
		"expired":"2020-01-01T00:00:00Z"
	}`), AntigravityOptions{
		TokenURL: server.URL + "/token", UserInfoURL: server.URL + "/userinfo", HTTPClient: server.Client(),
	})
	if err == nil {
		t.Fatal("ImportAntigravityCredential() error = nil, want userinfo rejection")
	}
	if tokenCalls != 1 {
		t.Fatalf("token calls = %d, want one refresh attempt", tokenCalls)
	}
}

func TestParseAntigravityImportedCredentialRequiresBooleanDisabled(t *testing.T) {
	base := `{"type":"antigravity","access_token":"access-secret","refresh_token":"refresh-secret","email":"owner@example.com","expires_in":3600,"timestamp":1770000000000}`
	for _, raw := range []string{
		strings.TrimSuffix(base, "}") + `,"disabled":null}`,
		strings.TrimSuffix(base, "}") + `,"disabled":"false"}`,
	} {
		if _, err := parseAntigravityImportedCredential([]byte(raw)); err == nil {
			t.Fatalf("parseAntigravityImportedCredential(%s) error = nil", raw)
		}
	}
}

func TestDiscoverAntigravityModelsUsesOnlyLiveAccountModels(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/models" {
			http.NotFound(writer, request)
			return
		}
		if got := request.Header.Get("Authorization"); got != "Bearer access-secret" {
			t.Errorf("models authorization = %q", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"models":{
				"gemini-live":{"displayName":"Gemini Live"},
				"chat_20706":{"displayName":"Internal"},
				"bad\nmodel":{"displayName":"Invalid"},
				"bad\u0001model":{"displayName":"Invalid"}
			}
		}`))
	}))
	defer server.Close()

	models, err := DiscoverAntigravityModels(context.Background(), AntigravityCredential{
		Type: ProviderAntigravity, AccessToken: "access-secret", RefreshToken: "refresh-secret",
		AccountID: "google-account-one", Email: "owner@example.com", ProjectID: "project-one",
		Expire: "2030-01-01T00:00:00Z",
	}, AntigravityOptions{FetchModelsURL: server.URL + "/models", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "gemini-live" || models[0].DisplayName != "Gemini Live" {
		t.Fatalf("models = %#v", models)
	}
}

func TestDiscoverAntigravityModelsRejectsExcessiveModelMap(t *testing.T) {
	t.Parallel()
	const oversizedModelCount = 20_001

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		models := make(map[string]any, oversizedModelCount)
		for index := 0; index < oversizedModelCount; index++ {
			models[fmt.Sprintf("gemini-test-%d", index)] = map[string]any{}
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"models": models})
	}))
	defer server.Close()

	_, err := DiscoverAntigravityModels(t.Context(), AntigravityCredential{
		Type: ProviderAntigravity, AccessToken: "access-secret", RefreshToken: "refresh-secret",
		AccountID: "google-account-one", Email: "owner@example.com", ProjectID: "project-one",
		Expire: "2030-01-01T00:00:00Z",
	}, AntigravityOptions{FetchModelsURL: server.URL, HTTPClient: server.Client()})
	if err == nil {
		t.Fatal("DiscoverAntigravityModels() error = nil, want oversized live model map rejection")
	}
}

func TestObserveAntigravityAccountDoesNotInventQuota(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/load":
			_, _ = writer.Write([]byte(`{
				"currentTier":{"id":"free-tier"},
				"paidTier":{"id":"google-one-ai","availableCredits":[
					{"creditType":"GOOGLE_ONE_AI","creditAmount":"25000","minimumCreditAmountForUsage":"50"}
				]}
			}`))
		case "/quota":
			_, _ = writer.Write([]byte(`{
				"groups":[{"displayName":"Gemini Models","buckets":[
					{"bucketId":"gemini-weekly","displayName":"Weekly Limit Remaining","window":"weekly","resetTime":"2030-01-01T00:00:00Z","remainingFraction":0.75}
				]}]
			}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	observation, err := ObserveAntigravityAccount(context.Background(), AntigravityCredential{
		Type: ProviderAntigravity, AccessToken: "access-secret", RefreshToken: "refresh-secret",
		AccountID: "google-account-one", Email: "owner@example.com", ProjectID: "project-one",
		Expire: "2030-01-01T00:00:00Z",
	}, AntigravityOptions{
		LoadCodeAssistURL: server.URL + "/load", RetrieveUserQuotaURL: server.URL + "/quota", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.PlanID != "google-one-ai" || observation.GoogleOneAICredits == nil ||
		observation.GoogleOneAICredits.Amount != 25000 || observation.GoogleOneAICredits.MinimumAmount != 50 {
		t.Fatalf("observation = %#v", observation)
	}
	if len(observation.QuotaGroups) != 1 || len(observation.QuotaGroups[0].Buckets) != 1 ||
		observation.QuotaGroups[0].Buckets[0].ID != "gemini-weekly" ||
		observation.QuotaGroups[0].Buckets[0].RemainingFraction == nil ||
		*observation.QuotaGroups[0].Buckets[0].RemainingFraction != 0.75 {
		t.Fatalf("quota groups = %#v", observation.QuotaGroups)
	}
}

func TestObserveAntigravityAccountKeepsSuccessfulPartialSource(t *testing.T) {
	tests := []struct {
		name                string
		loadStatus          int
		quotaStatus         int
		wantAccountObserved bool
		wantQuotaObserved   bool
	}{
		{name: "quota survives plan failure", loadStatus: http.StatusServiceUnavailable, quotaStatus: http.StatusOK, wantQuotaObserved: true},
		{name: "plan survives quota failure", loadStatus: http.StatusOK, quotaStatus: http.StatusServiceUnavailable, wantAccountObserved: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				switch request.URL.Path {
				case "/load":
					writer.WriteHeader(test.loadStatus)
					if test.loadStatus == http.StatusOK {
						_, _ = writer.Write([]byte(`{"currentTier":{"id":"free-tier"}}`))
					}
				case "/quota":
					writer.WriteHeader(test.quotaStatus)
					if test.quotaStatus == http.StatusOK {
						_, _ = writer.Write([]byte(`{"groups":[{"displayName":"Gemini Models","buckets":[{"bucketId":"weekly","window":"weekly","resetTime":"2030-01-01T00:00:00Z","remainingFraction":0.5}]}]}`))
					}
				}
			}))
			defer server.Close()

			observation, err := ObserveAntigravityAccount(t.Context(), AntigravityCredential{
				Type: ProviderAntigravity, AccessToken: "access-secret", RefreshToken: "refresh-secret",
				AccountID: "google-account-one", Email: "owner@example.com", ProjectID: "project-one",
				Expire: "2030-01-01T00:00:00Z",
			}, AntigravityOptions{
				LoadCodeAssistURL: server.URL + "/load", RetrieveUserQuotaURL: server.URL + "/quota", HTTPClient: server.Client(),
			})
			if err != nil || observation.AccountObserved != test.wantAccountObserved ||
				observation.QuotaObserved != test.wantQuotaObserved || len(observation.IncompleteSources) != 1 {
				t.Fatalf("observation = %#v, error = %v", observation, err)
			}
		})
	}
}

func TestObserveAntigravityAccountPreservesAuthorizationFailureWhenAllSourcesReject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	_, err := ObserveAntigravityAccount(t.Context(), AntigravityCredential{
		Type: ProviderAntigravity, AccessToken: "access-secret", RefreshToken: "refresh-secret",
		AccountID: "google-account-one", Email: "owner@example.com", ProjectID: "project-one",
		Expire: "2030-01-01T00:00:00Z",
	}, AntigravityOptions{
		LoadCodeAssistURL: server.URL + "/load", RetrieveUserQuotaURL: server.URL + "/quota", HTTPClient: server.Client(),
	})
	var upstream *AntigravityUpstreamHTTPError
	if !errors.As(err, &upstream) || upstream.StatusCode != http.StatusUnauthorized {
		t.Fatalf("error = %v, want bounded 401", err)
	}
}

func TestAntigravityGoogleOneAICreditsIgnoresUnavailableBalance(t *testing.T) {
	t.Parallel()

	credits, err := antigravityGoogleOneAICredits(map[string]any{
		"availableCredits": []any{
			map[string]any{
				"creditType":                  "GOOGLE_ONE_AI",
				"minimumCreditAmountForUsage": "50",
			},
		},
	})
	if err != nil {
		t.Fatalf("antigravityGoogleOneAICredits() error = %v", err)
	}
	if credits != nil {
		t.Fatalf("antigravityGoogleOneAICredits() = %#v, want no known balance", credits)
	}
}

func TestAntigravityExecutionOnlyBridgeUsesOneFixedUpstreamDispatch(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		if strings.Contains(string(body), `"enabledCreditTypes"`) {
			t.Errorf("execution-only request must not opt into Google One AI credits: %s", body)
		}
		if request.Header.Get("Authorization") != "Bearer access-secret" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		switch request.URL.Path {
		case "/v1internal:generateContent":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"response":{"candidates":[{"content":{"parts":[{"text":"ok"}]}}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}}`))
		case "/v1internal:countTokens":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"totalTokens":3}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	executor := newAntigravityHTTPExecutor(server.URL)
	credential := AntigravityCredential{
		Type: ProviderAntigravity, AccessToken: "access-secret", RefreshToken: "refresh-secret",
		AccountID: "google-account-one", Email: "owner@example.com", ProjectID: "project-one",
		Expire: "2030-01-01T00:00:00Z",
	}
	request := ExecuteRequest{
		Model: "gemini-live", Format: "gemini",
		Payload:         []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`),
		OriginalRequest: []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`),
	}
	response, err := executor.ExecuteCanonical(context.Background(), "credential-one", credential, request)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !strings.Contains(string(response.Payload), `"text":"ok"`) {
		t.Fatalf("calls=%d response=%s", calls, response.Payload)
	}
	count, err := executor.CountTokensCanonical(context.Background(), "credential-one", credential, request)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !strings.Contains(string(count.Payload), "3") {
		t.Fatalf("calls=%d count=%s", calls, count.Payload)
	}
}

func TestAntigravityExecutionOnlyBridgeDoesNotRetryOrKeepPrivateCooldown(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = writer.Write([]byte(`{"error":{"status":"RESOURCE_EXHAUSTED","details":[{"reason":"RATE_LIMIT_EXCEEDED"},{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"0.45s"}]}}`))
	}))
	defer server.Close()

	executor := newAntigravityHTTPExecutor(server.URL)
	credential := AntigravityCredential{
		Type: ProviderAntigravity, AccessToken: "access-secret", RefreshToken: "refresh-secret",
		AccountID: "google-account-one", Email: "owner@example.com", ProjectID: "project-one",
		Expire: "2030-01-01T00:00:00Z",
	}
	request := ExecuteRequest{
		Model: "gemini-live", Format: "gemini",
		Payload:         []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`),
		OriginalRequest: []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`),
	}
	for attempt := 0; attempt < 2; attempt++ {
		_, err := executor.ExecuteCanonical(context.Background(), "credential-one", credential, request)
		var upstream *AntigravityExecutionError
		if !errors.As(err, &upstream) || upstream.StatusCode() != http.StatusTooManyRequests ||
			upstream.ErrorType() != "RESOURCE_EXHAUSTED" || upstream.ErrorCode() != "RATE_LIMIT_EXCEEDED" {
			t.Fatalf("attempt %d error = %v", attempt, err)
		}
		if retryAfter := upstream.RetryAfter(); retryAfter == nil || *retryAfter != 450*time.Millisecond {
			t.Fatalf("attempt %d retry after = %v", attempt, retryAfter)
		}
	}
	if calls != 2 {
		t.Fatalf("upstream calls = %d, want one dispatch for each GPT-Load attempt", calls)
	}
}

func TestAntigravityForbiddenIsNotMarkedRequestScoped(t *testing.T) {
	err := normalizeAntigravityExecutionError(antigravityStatusTestError{
		status: http.StatusForbidden,
		body:   `{"error":{"status":"PERMISSION_DENIED"}}`,
	})
	var scoped interface{ IsRequestScoped() bool }
	if !errors.As(err, &scoped) || scoped.IsRequestScoped() {
		t.Fatalf("normalized error = %#v, want non-request-scoped 403", err)
	}
}

func TestAntigravityExecutionOnlyBridgeNeverRefreshesPreparedNearExpiryToken(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		if request.URL.Path != "/v1internal:generateContent" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"response":{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}}`))
	}))
	defer server.Close()

	response, err := newAntigravityHTTPExecutor(server.URL).ExecuteCanonical(t.Context(), "credential-one", AntigravityCredential{
		Type: ProviderAntigravity, AccessToken: "access-secret", RefreshToken: "refresh-secret",
		AccountID: "google-account-one", Email: "owner@example.com", ProjectID: "project-one",
		Expire: time.Now().UTC().Add(time.Second).Format(time.RFC3339),
	}, ExecuteRequest{
		Model: "gemini-live", Format: "gemini",
		Payload:         []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`),
		OriginalRequest: []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`),
	})
	if err != nil || !strings.Contains(string(response.Payload), `"candidates"`) || calls != 1 {
		t.Fatalf("response=%s calls=%d error=%v", response.Payload, calls, err)
	}
}

func TestAntigravityExecutionOnlyBridgeConvertsDeclaredUnaryProtocols(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1internal:generateContent" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"cachedContentTokenCount":2,"candidatesTokenCount":3,"thoughtsTokenCount":4,"totalTokenCount":12}}`))
	}))
	defer server.Close()

	executor := newAntigravityHTTPExecutor(server.URL)
	credential := AntigravityCredential{
		Type: ProviderAntigravity, AccessToken: "access-secret", RefreshToken: "refresh-secret",
		AccountID: "google-account-one", Email: "owner@example.com", ProjectID: "project-one",
		Expire: "2030-01-01T00:00:00Z",
	}
	tests := []struct {
		name      string
		format    string
		body      string
		want      string
		wantUsage string
	}{
		{
			name: "Gemini", format: "gemini", want: `"candidates"`, wantUsage: `"thoughtsTokenCount":4`,
			body: `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
		},
		{
			name: "Anthropic", format: "claude", want: `"content"`, wantUsage: `"input_tokens":3,"output_tokens":7,"cache_read_input_tokens":2`,
			body: `{"model":"gemini-live","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`,
		},
		{
			name: "OpenAI Chat", format: "openai", want: `"choices"`, wantUsage: `"completion_tokens":7`,
			body: `{"model":"gemini-live","messages":[{"role":"user","content":"hello"}]}`,
		},
		{
			name: "OpenAI Responses", format: "openai-response", want: `"output"`, wantUsage: `"output_tokens":7`,
			body: `{"model":"gemini-live","input":"hello"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := executor.ExecuteCanonical(t.Context(), "credential-one", credential, ExecuteRequest{
				Model: "gemini-live", Format: test.format, Payload: []byte(test.body), OriginalRequest: []byte(test.body),
			})
			if err != nil || !json.Valid(response.Payload) || !strings.Contains(string(response.Payload), test.want) ||
				!strings.Contains(string(response.Payload), test.wantUsage) {
				t.Fatalf("response=%s error=%v", response.Payload, err)
			}
		})
	}
}

func TestAntigravityExecutionOnlyBridgeConvertsDeclaredStreamingProtocols(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1internal:streamGenerateContent" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(`{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"cachedContentTokenCount":2,"candidatesTokenCount":3,"thoughtsTokenCount":4,"totalTokenCount":12}}` + "\n"))
	}))
	defer server.Close()

	executor := newAntigravityHTTPExecutor(server.URL)
	credential := AntigravityCredential{
		Type: ProviderAntigravity, AccessToken: "access-secret", RefreshToken: "refresh-secret",
		AccountID: "google-account-one", Email: "owner@example.com", ProjectID: "project-one",
		Expire: "2030-01-01T00:00:00Z",
	}
	tests := []struct {
		name   string
		format string
		body   string
		want   string
	}{
		{
			name: "Gemini", format: "gemini", want: `"candidates"`,
			body: `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
		},
		{
			name: "Anthropic", format: "claude", want: `"content_block"`,
			body: `{"model":"gemini-live","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`,
		},
		{
			name: "OpenAI Chat", format: "openai", want: `"choices"`,
			body: `{"model":"gemini-live","messages":[{"role":"user","content":"hello"}]}`,
		},
		{
			name: "OpenAI Responses", format: "openai-response", want: "response.",
			body: `{"model":"gemini-live","input":"hello"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := executor.ExecuteStreamCanonical(t.Context(), "credential-one", credential, ExecuteRequest{
				Model: "gemini-live", Format: test.format, Payload: []byte(test.body), OriginalRequest: []byte(test.body),
			})
			if err != nil || response == nil {
				t.Fatalf("ExecuteStreamCanonical() = %#v, %v", response, err)
			}
			var wire strings.Builder
			for chunk := range response.Chunks {
				if chunk.Err != nil {
					t.Fatalf("stream chunk error = %v", chunk.Err)
				}
				wire.Write(chunk.Payload)
			}
			if !strings.Contains(wire.String(), test.want) {
				t.Fatalf("stream wire = %q, want %q", wire.String(), test.want)
			}
		})
	}
}

func TestAntigravityExecutionOnlyBridgeCountsTokensUpstreamForDeclaredProtocols(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		if request.URL.Path != "/v1internal:countTokens" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"totalTokens":42}`))
	}))
	defer server.Close()

	executor := newAntigravityHTTPExecutor(server.URL)
	credential := AntigravityCredential{
		Type: ProviderAntigravity, AccessToken: "access-secret", RefreshToken: "refresh-secret",
		AccountID: "google-account-one", Email: "owner@example.com", ProjectID: "project-one",
		Expire: "2030-01-01T00:00:00Z",
	}
	tests := []struct {
		name   string
		format string
		body   string
		want   string
	}{
		{
			name: "Gemini", format: "gemini", want: `"totalTokens":42`,
			body: `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
		},
		{
			name: "Anthropic", format: "claude", want: `"input_tokens":42`,
			body: `{"model":"gemini-live","messages":[{"role":"user","content":"hello"}]}`,
		},
		{
			name: "OpenAI Responses", format: "openai-response", want: `"object":"response.input_tokens","input_tokens":42`,
			body: `{"model":"gemini-live","input":"hello"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response, err := executor.CountTokensCanonical(t.Context(), "credential-one", credential, ExecuteRequest{
				Model: "gemini-live", Format: test.format, Payload: []byte(test.body), OriginalRequest: []byte(test.body),
			})
			if err != nil || !strings.Contains(string(response.Payload), test.want) {
				t.Fatalf("response=%s error=%v", response.Payload, err)
			}
		})
	}
	if calls != len(tests) {
		t.Fatalf("upstream countTokens calls = %d, want %d", calls, len(tests))
	}
}

func TestNewAntigravityHTTPExecutorDisablesGlobalSignatureCache(t *testing.T) {
	previous := internalcache.SignatureCacheEnabled()
	t.Cleanup(func() { internalcache.SetSignatureCacheEnabled(previous) })
	internalcache.SetSignatureCacheEnabled(true)

	_ = NewAntigravityHTTPExecutor()
	if internalcache.SignatureCacheEnabled() {
		t.Fatal("Antigravity execution must not use CPA's global signature cache")
	}
}

func TestAntigravityExecutionRequestUsesOnlyPrivateContinuityScope(t *testing.T) {
	prepared := prepareAntigravityExecutionRequest(ExecuteRequest{
		Payload:         []byte(`{"contents":[{"parts":[{"text":"keep"}]}],"session_id":"client-session","prompt_cache_key":"client-cache","metadata":{"session_id":"nested"},"request":{"sessionId":"nested-request","session_id":"nested-request-snake"}}`),
		OriginalRequest: []byte(`{"contents":[{"parts":[{"text":"keep"}]}],"session_id":"client-session","prompt_cache_key":"client-cache","metadata":{"session_id":"nested"},"request":{"sessionId":"nested-request","session_id":"nested-request-snake"}}`),
		Headers: http.Header{
			"Session-Id":               {"client-session"},
			"X-Claude-Code-Session-Id": {"client-claude-session"},
			"X-Claude-Code-Agent-Id":   {"client-claude-agent"},
		},
		ContinuityKey: "opaque-tenant-scope",
	})
	if strings.Contains(string(prepared.Payload), "client-session") || strings.Contains(string(prepared.OriginalRequest), "client-cache") ||
		strings.Contains(string(prepared.Payload), "nested-request") || strings.Contains(string(prepared.OriginalRequest), "nested-request-snake") ||
		prepared.Headers.Get("Session-Id") != "" || prepared.Headers.Get("X-Claude-Code-Session-Id") != "" ||
		prepared.Headers.Get("X-Claude-Code-Agent-Id") != "" {
		t.Fatalf("prepared request retained untrusted replay inputs: %#v", prepared)
	}
	if !json.Valid(prepared.Payload) || !json.Valid(prepared.OriginalRequest) ||
		!strings.Contains(string(prepared.Payload), `"text":"keep"`) ||
		!strings.Contains(string(prepared.OriginalRequest), `"text":"keep"`) {
		t.Fatalf("prepared request was corrupted: payload=%q original=%q", prepared.Payload, prepared.OriginalRequest)
	}
	contextWithGin := context.WithValue(t.Context(), "gin", "downstream-request-context")
	if got := newAntigravityHTTPExecutor("").executionContext(contextWithGin, nil).Value("gin"); got != nil {
		t.Fatalf("execution context retained downstream gin value: %#v", got)
	}
	options := antigravityExecutorOptions(prepared, "gemini", false)
	if options.Metadata[cliproxyexecutor.ExecutionSessionMetadataKey] != "opaque-tenant-scope" {
		t.Fatalf("options metadata = %#v", options.Metadata)
	}
	fallback := prepareAntigravityExecutionRequest(ExecuteRequest{AttemptID: "attempt-one"})
	if fallback.ContinuityKey == "" || antigravityExecutorOptions(fallback, "gemini", false).Metadata[cliproxyexecutor.ExecutionSessionMetadataKey] == "" {
		t.Fatalf("fallback continuity = %#v", fallback)
	}
}

func TestNormalizeAntigravityConvertedUsagePreservesReasoningAndCacheSemantics(t *testing.T) {
	tests := []struct {
		name   string
		format string
		stream bool
		body   string
		want   string
	}{
		{
			name: "OpenAI chat adds reasoning tokens", format: "openai",
			body: `{"usage":{"prompt_tokens":10,"completion_tokens":4,"completion_tokens_details":{"reasoning_tokens":6}}}`,
			want: `{"usage":{"prompt_tokens":10,"completion_tokens":10,"completion_tokens_details":{"reasoning_tokens":6}}}`,
		},
		{
			name: "Responses adds reasoning tokens", format: "openai-response",
			body: `{"usage":{"input_tokens":10,"output_tokens":4,"output_tokens_details":{"reasoning_tokens":6}}}`,
			want: `{"usage":{"input_tokens":10,"output_tokens":10,"output_tokens_details":{"reasoning_tokens":6}}}`,
		},
		{
			name: "Anthropic unary subtracts cached input", format: "claude",
			body: `{"usage":{"input_tokens":10,"cache_read_input_tokens":4,"output_tokens":3}}`,
			want: `{"usage":{"input_tokens":6,"cache_read_input_tokens":4,"output_tokens":3}}`,
		},
		{
			name: "Anthropic stream already has uncached input", format: "claude", stream: true,
			body: `{"usage":{"input_tokens":6,"cache_read_input_tokens":4,"output_tokens":3}}`,
			want: `{"usage":{"input_tokens":6,"cache_read_input_tokens":4,"output_tokens":3}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := string(normalizeAntigravityConvertedUsage(test.format, test.stream, []byte(test.body))); got != test.want {
				t.Fatalf("usage = %s, want %s", got, test.want)
			}
		})
	}
}

func TestNormalizeAntigravityResponsesCountTokensContract(t *testing.T) {
	if got := string(normalizeAntigravityCountTokens("openai-response", []byte(`{"totalTokens":42}`))); got != `{"object":"response.input_tokens","input_tokens":42}` {
		t.Fatalf("Responses CountTokens = %s", got)
	}
	if got := string(normalizeAntigravityCountTokens("gemini", []byte(`{"totalTokens":42}`))); got != `{"totalTokens":42}` {
		t.Fatalf("Gemini CountTokens = %s", got)
	}
}
