package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/storage/models"
)

func TestCredentialStageRoutesRequireAuthAndNeverReturnSecrets(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.oauthCallback.address = "127.0.0.1:0"
	t.Cleanup(func() { _ = fixture.service.oauthCallback.Stop(t.Context()) })
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	unauthorized := httptest.NewRequest(http.MethodPost, "/api/credential-stages/authorizations", strings.NewReader(`{"channel_id":"openai"}`))
	unauthorized.Header.Set("Content-Type", "application/json")
	unauthorizedResponse := httptest.NewRecorder()
	engine.ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized = %d %s", unauthorizedResponse.Code, unauthorizedResponse.Body)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/credential-stages/authorizations", strings.NewReader(`{"channel_id":"openai"}`))
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

func TestOAuthFileImportRouteStreamsOneFileIntoReadyStage(t *testing.T) {
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

func TestOAuthCallbackServerIsPublicStateBoundAndNoStore(t *testing.T) {
	fixture := newServiceFixture(t)
	started, err := fixture.service.BeginCredentialAuthorization(t.Context(), "openai")
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
	var payload stagedCodexPayload
	if err := json.Unmarshal([]byte(plaintext), &payload); err != nil {
		t.Fatal(err)
	}
	fixture.service.completeBrowserAuthorization = successfulBrowserCompletion(payload, t)
	fixture.service.oauthCallback.address = "127.0.0.1:0"
	fixture.service.oauthCallback.listen = net.Listen
	if err := fixture.service.oauthCallback.EnsureStarted(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fixture.service.oauthCallback.Stop(t.Context()) })

	callbackURL := "http://" + fixture.service.oauthCallback.Addr() + "/auth/callback?state=" + payload.State + "&code=authorization-code"
	response, err := http.Get(callbackURL) // #nosec G107 -- local test listener
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" ||
		response.Header.Get("Referrer-Policy") != "no-referrer" || response.Header.Get("Content-Security-Policy") == "" {
		t.Fatalf("callback response = %d %#v", response.StatusCode, response.Header)
	}
	completed, err := fixture.service.GetCredentialStage(t.Context(), started.StageID)
	if err != nil || completed.Status != string(models.CredentialStageReady) {
		t.Fatalf("completed = %#v, %v", completed, err)
	}
}

func TestOAuthCallbackServerMarksDeniedAuthorizationFailed(t *testing.T) {
	fixture := newServiceFixture(t)
	started, err := fixture.service.BeginCredentialAuthorization(t.Context(), "openai")
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
	var payload stagedCodexPayload
	if err := json.Unmarshal([]byte(plaintext), &payload); err != nil {
		t.Fatal(err)
	}
	fixture.service.oauthCallback.address = "127.0.0.1:0"
	if err := fixture.service.oauthCallback.EnsureStarted(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fixture.service.oauthCallback.Stop(t.Context()) })

	callbackURL := "http://" + fixture.service.oauthCallback.Addr() + "/auth/callback?state=" + payload.State + "&error=access_denied"
	response, err := http.Get(callbackURL) // #nosec G107 -- local test listener
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d", response.StatusCode)
	}
	failed, err := fixture.service.GetCredentialStage(t.Context(), started.StageID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != string(models.CredentialStageFailed) {
		t.Fatalf("status = %q, want failed", failed.Status)
	}
}

func TestNewServerConfiguresOAuthCallbackForWildcardContainerHost(t *testing.T) {
	fixture := newServiceFixture(t)
	NewServer(&config.Config{AuthKey: "test-auth-key", Server: config.ServerConfig{Host: "0.0.0.0"}}, fixture.service)
	if got := fixture.service.oauthCallback.address; got != "0.0.0.0:1455" {
		t.Fatalf("callback address = %q, want 0.0.0.0:1455", got)
	}
}

func TestCredentialObservationRoutesReadCacheAndRefreshExplicitly(t *testing.T) {
	initControlI18n(t)
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	fixture.service.observeCodexAccount = func(context.Context, cpaembedded.CodexCredential) (cpaembedded.AccountObservation, error) {
		return cpaembedded.AccountObservation{Payload: []byte(`{"plan_type":"pro","rate_limit":{"primary_window":{"limit_window_seconds":604800,"used_percent":35}}}`)}, nil
	}
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
		!strings.Contains(refreshResponse.Body.String(), `"plan_summary":{"name":"pro"}`) {
		t.Fatalf("refresh = %d %s", refreshResponse.Code, refreshResponse.Body)
	}

	secondRequest := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/groups/%d/credentials/%d/observation-refresh", groupID, credentialID), strings.NewReader(`{}`))
	secondRequest.Header.Set("Authorization", "Bearer test-auth-key")
	secondRequest.Header.Set("Content-Type", "application/json")
	secondResponse := httptest.NewRecorder()
	engine.ServeHTTP(secondResponse, secondRequest)
	if secondResponse.Code != http.StatusTooManyRequests || !strings.Contains(secondResponse.Body.String(), `"retry_at_ms"`) {
		t.Fatalf("throttled = %d %s", secondResponse.Code, secondResponse.Body)
	}
}

func successfulBrowserCompletion(payload stagedCodexPayload, t *testing.T) func(context.Context, cpaembedded.BrowserAuthorizationCompletion) (cpaembedded.CodexCredential, error) {
	t.Helper()
	return func(_ context.Context, completion cpaembedded.BrowserAuthorizationCompletion) (cpaembedded.CodexCredential, error) {
		if completion.ExpectedState != payload.State || completion.ReturnedState != payload.State || completion.Code != "authorization-code" {
			t.Fatalf("completion = %#v", completion)
		}
		return cpaembedded.CodexCredential{Type: "codex", AccessToken: "access", RefreshToken: "refresh", AccountID: "account-one", Email: "one@example.com"}, nil
	}
}
