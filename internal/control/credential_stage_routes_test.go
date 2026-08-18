package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription/providers/codex"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func TestCredentialStageRoutesRequireAuthAndNeverReturnSecrets(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	useEphemeralOAuthCallbackListeners(fixture.service.oauthCallback)
	t.Cleanup(func() { _ = fixture.service.oauthCallback.Stop(t.Context()) })
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	unauthorized := httptest.NewRequest(http.MethodPost, "/api/credential-stages/authorizations", strings.NewReader(`{"channel_id":"codex"}`))
	unauthorized.Header.Set("Content-Type", "application/json")
	unauthorizedResponse := httptest.NewRecorder()
	engine.ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized = %d %s", unauthorizedResponse.Code, unauthorizedResponse.Body)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/credential-stages/authorizations", strings.NewReader(`{"channel_id":"codex"}`))
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "verifier") || strings.Contains(response.Body.String(), "oauth-state") {
		t.Fatalf("authorization response = %d %s", response.Code, response.Body)
	}
	var envelope struct {
		Data CredentialStageResult `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.StageID == "" || envelope.Data.AuthorizationURL == "" {
		t.Fatalf("authorization data = %#v", envelope.Data)
	}
	if !strings.Contains(response.Body.String(), `"redirect_uri":"http://localhost:1455/auth/callback"`) {
		t.Fatalf("authorization response omits the compiled callback endpoint: %s", response.Body)
	}

	get := httptest.NewRequest(http.MethodGet, "/api/credential-stages/"+envelope.Data.StageID, nil)
	get.Header.Set("Authorization", "Bearer test-auth-key")
	getResponse := httptest.NewRecorder()
	engine.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK || strings.Contains(getResponse.Body.String(), "verifier") {
		t.Fatalf("get response = %d %s", getResponse.Code, getResponse.Body)
	}

	remove := httptest.NewRequest(http.MethodDelete, "/api/credential-stages/"+envelope.Data.StageID, nil)
	remove.Header.Set("Authorization", "Bearer test-auth-key")
	removeResponse := httptest.NewRecorder()
	engine.ServeHTTP(removeResponse, remove)
	if removeResponse.Code != http.StatusOK {
		t.Fatalf("delete response = %d %s", removeResponse.Code, removeResponse.Body)
	}
}

func TestBeginCredentialAuthorizationAllowsManualCallbackWhenListenerIsUnavailable(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.oauthCallback.listen = func(string, string) (net.Listener, error) {
		return nil, fmt.Errorf("port is already in use")
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	request := httptest.NewRequest(http.MethodPost, "/api/credential-stages/authorizations", strings.NewReader(`{"channel_id":"codex"}`))
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"authorization_url"`) {
		t.Fatalf("authorization response = %d %s", response.Code, response.Body)
	}
}

func TestBeginCredentialAuthorizationStartsOnlyTheCallbackRequestedByTheDriver(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	originalBegin := fixture.service.beginSubscriptionAuthorization
	fixture.service.beginSubscriptionAuthorization = func(channelID channel.ID) (subscriptionruntime.Authorization, error) {
		authorization, err := originalBegin(channelID)
		authorization.RedirectURI = ""
		return authorization, err
	}
	listenerCalled := false
	fixture.service.oauthCallback.listen = func(string, string) (net.Listener, error) {
		listenerCalled = true
		return nil, fmt.Errorf("listener must not start")
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	request := httptest.NewRequest(http.MethodPost, "/api/credential-stages/authorizations", strings.NewReader(`{"channel_id":"codex"}`))
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("authorization response = %d %s", response.Code, response.Body)
	}
	if listenerCalled {
		t.Fatal("local callback listener started for an authorization that did not request it")
	}
}

func TestManualOAuthCallbackCompletesOnlyItsBoundStageOnce(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	first, err := fixture.service.BeginCredentialAuthorization(t.Context(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.service.BeginCredentialAuthorization(t.Context(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	authorizationURL, err := url.Parse(first.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	state := authorizationURL.Query().Get("state")
	setCodexAuthorizationCompletion(t, fixture.service, func(_ context.Context, completion codex.BrowserAuthorizationCompletion) (codex.Credential, error) {
		if completion.ReturnedState != state || completion.Code != "authorization-code" {
			t.Fatalf("completion = %#v", completion)
		}
		return codex.Credential{Type: "codex", AccessToken: "access", RefreshToken: "refresh", AccountID: "account-one", Email: "one@example.com"}, nil
	})
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	callbackURL := "http://localhost:1455/auth/callback?code=authorization-code&state=" + url.QueryEscape(state)

	mismatched := postManualOAuthCallback(engine, second.StageID, callbackURL)
	if mismatched.Code != http.StatusBadRequest {
		t.Fatalf("mismatched callback = %d %s", mismatched.Code, mismatched.Body)
	}
	completed := postManualOAuthCallback(engine, first.StageID, callbackURL)
	if completed.Code != http.StatusOK || !strings.Contains(completed.Body.String(), `"status":"ready"`) || strings.Contains(completed.Body.String(), "access") {
		t.Fatalf("completed callback = %d %s", completed.Code, completed.Body)
	}
	replayed := postManualOAuthCallback(engine, first.StageID, callbackURL)
	if replayed.Code != http.StatusBadRequest {
		t.Fatalf("replayed callback = %d %s", replayed.Code, replayed.Body)
	}
	remaining, err := fixture.service.GetCredentialStage(t.Context(), second.StageID)
	if err != nil || remaining.Status != string(models.CredentialStagePendingAuthorization) {
		t.Fatalf("second stage = %#v, %v", remaining, err)
	}
}

func TestParseManualOAuthCallbackURLRequiresFixedLocalCallback(t *testing.T) {
	valid := "http://localhost:1455/auth/callback?code=authorization-code&state=state-one"
	parsed, err := parseManualOAuthCallbackURL(valid, subscriptionruntime.LocalCallbackSpec{
		RedirectURI: "http://localhost:1455/auth/callback",
	})
	if err != nil || parsed.Code != "authorization-code" || parsed.State != "state-one" {
		t.Fatalf("valid callback = %#v, %v", parsed, err)
	}
	for _, callbackURL := range []string{
		"https://localhost:1455/auth/callback?code=authorization-code&state=state-one",
		"http://127.0.0.1:1455/auth/callback?code=authorization-code&state=state-one",
		"http://localhost:1456/auth/callback?code=authorization-code&state=state-one",
		"http://localhost:1455/other?code=authorization-code&state=state-one",
		"http://user@localhost:1455/auth/callback?code=authorization-code&state=state-one",
		"http://localhost:1455/auth/callback?code=authorization-code&state=state-one#fragment",
		"http://localhost:1455/auth/callback?code=authorization-code&state=one&state=two",
	} {
		if _, err := parseManualOAuthCallbackURL(callbackURL, subscriptionruntime.LocalCallbackSpec{
			RedirectURI: "http://localhost:1455/auth/callback",
		}); err == nil {
			t.Fatalf("parseManualOAuthCallbackURL(%q) succeeded", callbackURL)
		}
	}
}

func TestParseManualOAuthCallbackURLUsesDriverCallback(t *testing.T) {
	claudeCallback := subscriptionruntime.LocalCallbackSpec{
		RedirectURI: "http://localhost:54545/callback",
	}
	parsed, err := parseManualOAuthCallbackURL(
		"http://localhost:54545/callback?code=authorization-code&state=state-one",
		claudeCallback,
	)
	if err != nil || parsed.Code != "authorization-code" || parsed.State != "state-one" {
		t.Fatalf("Claude callback = %#v, %v", parsed, err)
	}
	if _, err := parseManualOAuthCallbackURL(
		"http://localhost:1455/auth/callback?code=authorization-code&state=state-one",
		claudeCallback,
	); err == nil {
		t.Fatal("Codex callback succeeded against the Claude callback contract")
	}
}

func TestOAuthCallbackServerStartsIndependentDriverEndpoints(t *testing.T) {
	fixture := newServiceFixture(t)
	useEphemeralOAuthCallbackListeners(fixture.service.oauthCallback)
	callbacks := []subscriptionruntime.LocalCallbackSpec{
		{RedirectURI: "http://localhost:1455/auth/callback"},
		{RedirectURI: "http://localhost:54545/callback"},
		{RedirectURI: "http://localhost:51121/oauth-callback"},
	}
	for _, callback := range callbacks {
		if err := fixture.service.oauthCallback.EnsureStarted(callback); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = fixture.service.oauthCallback.Stop(t.Context()) })

	addresses := make([]string, len(callbacks))
	seen := make(map[string]struct{}, len(callbacks))
	for index, callback := range callbacks {
		addresses[index] = fixture.service.oauthCallback.Addr(callback)
		if addresses[index] == "" {
			t.Fatalf("callback %d has no listener address", index)
		}
		if _, duplicate := seen[addresses[index]]; duplicate {
			t.Fatalf("callback listeners share address %q", addresses[index])
		}
		seen[addresses[index]] = struct{}{}
	}
	for index, callback := range callbacks {
		if err := fixture.service.oauthCallback.EnsureStarted(callback); err != nil {
			t.Fatal(err)
		}
		if got := fixture.service.oauthCallback.Addr(callback); got != addresses[index] {
			t.Fatalf("idempotent callback address = %q", got)
		}
	}
}

func postManualOAuthCallback(engine *gin.Engine, stageID string, callbackURL string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"callback_url": callbackURL})
	request := httptest.NewRequest(http.MethodPost, "/api/credential-stages/"+stageID+"/oauth-callback", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func TestOAuthFileImportRouteStreamsOneFileIntoReadyStage(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("channel_id", "codex"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "codex.json")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte(`{"type":"codex","access_token":"access-secret","refresh_token":"refresh-secret","account_id":"account-one","email":"one@example.com"}`))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/credential-stages/import", &body)
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "access-secret") || strings.Contains(response.Body.String(), "refresh-secret") {
		t.Fatalf("import response = %d %s", response.Code, response.Body)
	}
	var envelope struct {
		Data CredentialStageResult `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Status != string(models.CredentialStageReady) || envelope.Data.Account.EmailMask == "" {
		t.Fatalf("stage = %#v", envelope.Data)
	}
}

func TestOAuthFileImportRouteRequiresChannelID(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "codex.json")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte(`{"type":"codex","access_token":"access-secret","refresh_token":"refresh-secret","account_id":"account-one"}`))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/credential-stages/import", &body)
	request.Header.Set("Authorization", "Bearer test-auth-key")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("import without channel_id = %d %s", response.Code, response.Body)
	}
}

func TestOAuthCallbackServerIsPublicStateBoundAndNoStore(t *testing.T) {
	fixture := newServiceFixture(t)
	started, err := fixture.service.BeginCredentialAuthorization(t.Context(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	var row models.CredentialStage
	if err := fixture.db.Take(&row, "id = ?", started.StageID).Error; err != nil {
		t.Fatal(err)
	}
	plaintext, err := fixture.encryption.Decrypt(row.EncryptedPayload)
	if err != nil {
		t.Fatal(err)
	}
	var payload stagedSubscriptionPayload
	if err := json.Unmarshal([]byte(plaintext), &payload); err != nil {
		t.Fatal(err)
	}
	setCodexAuthorizationCompletion(t, fixture.service, successfulBrowserCompletion(payload, t))
	useEphemeralOAuthCallbackListeners(fixture.service.oauthCallback)
	callbackSpec := subscriptionruntime.LocalCallbackSpec{RedirectURI: "http://localhost:1455/auth/callback"}
	if err := fixture.service.oauthCallback.EnsureStarted(callbackSpec); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fixture.service.oauthCallback.Stop(t.Context()) })

	callbackURL := "http://" + fixture.service.oauthCallback.Addr(callbackSpec) + "/auth/callback?state=" + payload.State + "&code=authorization-code&iss=" + url.QueryEscape("https://auth.openai.com")
	response, body := getOAuthCallbackResponse(t, callbackURL)
	contentSecurityPolicy := response.Header.Get("Content-Security-Policy")
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" ||
		response.Header.Get("Referrer-Policy") != "no-referrer" || !strings.Contains(contentSecurityPolicy, "script-src 'sha256-") ||
		response.Header.Get("Location") != "" || !strings.Contains(body, "授权已完成") ||
		!strings.Contains(body, "返回 GPT-Load 添加账号") || !strings.Contains(body, "关闭") {
		t.Fatalf("callback response = %d %#v", response.StatusCode, response.Header)
	}
	completed, err := fixture.service.GetCredentialStage(t.Context(), started.StageID)
	if err != nil || completed.Status != string(models.CredentialStageReady) {
		t.Fatalf("completed = %#v, %v", completed, err)
	}
}

func TestOAuthCallbackServerRejectsStateFromAnotherDriverEndpoint(t *testing.T) {
	fixture := newServiceFixture(t)
	started, err := fixture.service.BeginCredentialAuthorization(t.Context(), channel.Codex)
	if err != nil {
		t.Fatal(err)
	}
	var row models.CredentialStage
	if err := fixture.db.Take(&row, "id = ?", started.StageID).Error; err != nil {
		t.Fatal(err)
	}
	plaintext, err := fixture.encryption.Decrypt(row.EncryptedPayload)
	if err != nil {
		t.Fatal(err)
	}
	var payload stagedSubscriptionPayload
	if err := json.Unmarshal([]byte(plaintext), &payload); err != nil {
		t.Fatal(err)
	}
	setCodexAuthorizationCompletion(t, fixture.service, successfulBrowserCompletion(payload, t))
	useEphemeralOAuthCallbackListeners(fixture.service.oauthCallback)
	claudeCallback := subscriptionruntime.LocalCallbackSpec{RedirectURI: "http://localhost:54545/callback"}
	if err := fixture.service.oauthCallback.EnsureStarted(claudeCallback); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fixture.service.oauthCallback.Stop(t.Context()) })

	callbackURL := "http://" + fixture.service.oauthCallback.Addr(claudeCallback) +
		"/callback?state=" + url.QueryEscape(payload.State) + "&code=authorization-code"
	response, body := getOAuthCallbackResponse(t, callbackURL)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "无法识别这次授权") {
		t.Fatalf("cross-endpoint callback response = %d %s", response.StatusCode, body)
	}
	pending, err := fixture.service.GetCredentialStage(t.Context(), started.StageID)
	if err != nil || pending.Status != string(models.CredentialStagePendingAuthorization) {
		t.Fatalf("cross-endpoint callback consumed stage = %#v, %v", pending, err)
	}
}

func TestOAuthCallbackServerMarksDeniedAuthorizationFailed(t *testing.T) {
	fixture := newServiceFixture(t)
	started, err := fixture.service.BeginCredentialAuthorization(t.Context(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	var row models.CredentialStage
	if err := fixture.db.Take(&row, "id = ?", started.StageID).Error; err != nil {
		t.Fatal(err)
	}
	plaintext, err := fixture.encryption.Decrypt(row.EncryptedPayload)
	if err != nil {
		t.Fatal(err)
	}
	var payload stagedSubscriptionPayload
	if err := json.Unmarshal([]byte(plaintext), &payload); err != nil {
		t.Fatal(err)
	}
	useEphemeralOAuthCallbackListeners(fixture.service.oauthCallback)
	callbackSpec := subscriptionruntime.LocalCallbackSpec{RedirectURI: "http://localhost:1455/auth/callback"}
	if err := fixture.service.oauthCallback.EnsureStarted(callbackSpec); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fixture.service.oauthCallback.Stop(t.Context()) })

	callbackURL := "http://" + fixture.service.oauthCallback.Addr(callbackSpec) + "/auth/callback?state=" + payload.State + "&error=access_denied"
	response, body := getOAuthCallbackResponse(t, callbackURL)
	if response.StatusCode != http.StatusOK || response.Header.Get("Location") != "" ||
		!strings.Contains(body, "授权未完成") || !strings.Contains(body, "关闭") {
		t.Fatalf("callback response = %d %#v", response.StatusCode, response.Header)
	}
	failed, err := fixture.service.GetCredentialStage(t.Context(), started.StageID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != string(models.CredentialStageFailed) {
		t.Fatalf("status = %q, want failed", failed.Status)
	}
}

func TestOAuthCallbackExchangeFailureDoesNotOfferConsumedCallbackRetry(t *testing.T) {
	fixture := newServiceFixture(t)
	started, err := fixture.service.BeginCredentialAuthorization(t.Context(), channel.Codex)
	if err != nil {
		t.Fatal(err)
	}
	var row models.CredentialStage
	if err := fixture.db.Take(&row, "id = ?", started.StageID).Error; err != nil {
		t.Fatal(err)
	}
	plaintext, err := fixture.encryption.Decrypt(row.EncryptedPayload)
	if err != nil {
		t.Fatal(err)
	}
	var payload stagedSubscriptionPayload
	if err := json.Unmarshal([]byte(plaintext), &payload); err != nil {
		t.Fatal(err)
	}
	setCodexAuthorizationCompletion(t, fixture.service, func(context.Context, codex.BrowserAuthorizationCompletion) (codex.Credential, error) {
		return codex.Credential{}, fmt.Errorf("temporary token exchange failure")
	})
	useEphemeralOAuthCallbackListeners(fixture.service.oauthCallback)
	callbackSpec := subscriptionruntime.LocalCallbackSpec{RedirectURI: "http://localhost:1455/auth/callback"}
	if err := fixture.service.oauthCallback.EnsureStarted(callbackSpec); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fixture.service.oauthCallback.Stop(t.Context()) })

	callbackURL := "http://" + fixture.service.oauthCallback.Addr(callbackSpec) + "/auth/callback?state=" + payload.State + "&code=one-time-authorization-code"
	response, body := getOAuthCallbackResponse(t, callbackURL)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "换取凭据时失败") ||
		!strings.Contains(body, "不能重复使用") || strings.Contains(body, "copy-callback-url") ||
		strings.Contains(body, "one-time-authorization-code") {
		t.Fatalf("callback response = %d %s", response.StatusCode, body)
	}
	if err := fixture.db.Take(&row, "id = ?", started.StageID).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != models.CredentialStageOutcomeUnknown {
		t.Fatalf("stage status = %q, want outcome_unknown", row.Status)
	}
}

func getOAuthCallbackResponse(t *testing.T, callbackURL string) (*http.Response, string) {
	t.Helper()
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	response, err := client.Get(callbackURL) // #nosec G107 -- local test listener
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, string(body)
}

func useEphemeralOAuthCallbackListeners(manager *OAuthCallbackManager) {
	manager.listen = func(network, _ string) (net.Listener, error) {
		return net.Listen(network, "127.0.0.1:0")
	}
}

func TestNewServerConfiguresOAuthCallbackForWildcardContainerHost(t *testing.T) {
	fixture := newServiceFixture(t)
	NewServer(&config.Config{AuthKey: "test-auth-key", Server: config.ServerConfig{Host: "0.0.0.0"}}, fixture.service)
	if got := fixture.service.oauthCallback.host; got != "0.0.0.0" {
		t.Fatalf("callback host = %q, want 0.0.0.0", got)
	}
}

func TestCredentialObservationRoutesReadCacheAndRefreshExplicitly(t *testing.T) {
	initControlI18n(t)
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{"plan_type":"pro","rate_limit":{"primary_window":{"limit_window_seconds":604800,"used_percent":35}}}`)}, nil
	})
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	detailRequest := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/groups/%d/credentials/%d", groupID, credentialID), nil)
	detailRequest.Header.Set("Authorization", "Bearer test-auth-key")
	detailResponse := httptest.NewRecorder()
	engine.ServeHTTP(detailResponse, detailRequest)
	if detailResponse.Code != http.StatusOK || !strings.Contains(detailResponse.Body.String(), `"state":"unavailable"`) {
		t.Fatalf("initial detail = %d %s", detailResponse.Code, detailResponse.Body)
	}

	refreshRequest := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/groups/%d/credentials/%d/observation-refresh", groupID, credentialID), strings.NewReader(`{}`))
	refreshRequest.Header.Set("Authorization", "Bearer test-auth-key")
	refreshRequest.Header.Set("Content-Type", "application/json")
	refreshResponse := httptest.NewRecorder()
	engine.ServeHTTP(refreshResponse, refreshRequest)
	if refreshResponse.Code != http.StatusOK || !strings.Contains(refreshResponse.Body.String(), `"state":"fresh"`) ||
		!strings.Contains(refreshResponse.Body.String(), `"plan_summary":{"name":"Pro 20x","level":"elite"}`) {
		t.Fatalf("refresh = %d %s", refreshResponse.Code, refreshResponse.Body)
	}

	secondRequest := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/groups/%d/credentials/%d/observation-refresh", groupID, credentialID), strings.NewReader(`{}`))
	secondRequest.Header.Set("Authorization", "Bearer test-auth-key")
	secondRequest.Header.Set("Content-Type", "application/json")
	secondResponse := httptest.NewRecorder()
	engine.ServeHTTP(secondResponse, secondRequest)
	if secondResponse.Code != http.StatusOK || !strings.Contains(secondResponse.Body.String(), `"state":"fresh"`) ||
		strings.Contains(secondResponse.Body.String(), `"next_allowed_at_ms"`) {
		t.Fatalf("second refresh = %d %s", secondResponse.Code, secondResponse.Body)
	}
}

func TestCredentialResetCreditRouteRequiresIdempotencyAndReplays(t *testing.T) {
	initControlI18n(t)
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	consumeCalls := 0
	setCodexResetCreditConsume(t, fixture.service, func(context.Context, codex.Credential, string) (codex.AccountObservation, error) {
		consumeCalls++
		return codex.AccountObservation{Payload: []byte(`{"code":"reset","windows_reset":1}`)}, nil
	})
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		return codex.AccountObservation{Payload: []byte(`{}`)}, nil
	})
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	path := fmt.Sprintf("/api/groups/%d/credentials/%d/reset-credits/consume", groupID, credentialID)

	missingKey := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
	missingKey.Header.Set("Authorization", "Bearer test-auth-key")
	missingKey.Header.Set("Content-Type", "application/json")
	missingResponse := httptest.NewRecorder()
	engine.ServeHTTP(missingResponse, missingKey)
	if missingResponse.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing idempotency = %d %s", missingResponse.Code, missingResponse.Body)
	}

	for attempt := 0; attempt < 2; attempt++ {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer test-auth-key")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", resetCreditTestKey)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"succeeded"`) {
			t.Fatalf("attempt %d = %d %s", attempt, response.Code, response.Body)
		}
		if attempt == 1 && !strings.Contains(response.Body.String(), `"replayed":true`) {
			t.Fatalf("replay = %s", response.Body)
		}
	}
	if consumeCalls != 1 {
		t.Fatalf("consume calls = %d", consumeCalls)
	}
}

func successfulBrowserCompletion(payload stagedSubscriptionPayload, t *testing.T) func(context.Context, codex.BrowserAuthorizationCompletion) (codex.Credential, error) {
	t.Helper()
	return func(_ context.Context, completion codex.BrowserAuthorizationCompletion) (codex.Credential, error) {
		if completion.ExpectedState != payload.State || completion.ReturnedState != payload.State || completion.Code != "authorization-code" {
			t.Fatalf("completion = %#v", completion)
		}
		return codex.Credential{Type: "codex", AccessToken: "access", RefreshToken: "refresh", AccountID: "account-one", Email: "one@example.com"}, nil
	}
}
