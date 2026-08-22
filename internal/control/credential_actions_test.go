package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/subscription/providers/codex"
	subscriptionruntime "gpt-load/internal/subscription/runtime"

	"github.com/gin-gonic/gin"
)

func TestDownloadGroupCredentialReturnsCanonicalJSONAndSafeFilename(t *testing.T) {
	t.Parallel()

	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	result, err := fixture.service.DownloadGroupCredential(t.Context(), groupID, credentialID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Filename != "codex-observation-example.com.json" {
		t.Fatalf("filename = %q", result.Filename)
	}
	var credential map[string]any
	if err := json.Unmarshal(result.Credential, &credential); err != nil {
		t.Fatalf("decode credential = %v", err)
	}
	if credential["access_token"] == nil || credential["refresh_token"] == nil ||
		credential["account_id"] != "account-observation" {
		t.Fatalf("credential = %#v", credential)
	}
}

func TestSubscriptionCredentialFilenameFallsBackToCredentialID(t *testing.T) {
	if got := subscriptionCredentialFilename("Claude", "", 42); got != "claude-credential-42.json" {
		t.Fatalf("filename = %q", got)
	}
	if got := subscriptionCredentialFilename("Claude", "Admin+tag@example.com", 42); got != "claude-admin-tag-example.com.json" {
		t.Fatalf("filename = %q", got)
	}
}

func TestDownloadGroupCredentialHTTPReturnsJSONObjectAndNoStoreHeaders(t *testing.T) {
	initControlI18n(t)
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	server := NewServer(&config.Config{AuthKey: "credential-download-auth"}, fixture.service)
	engine := gin.New()
	server.RegisterRoutes(engine)

	response := serveCredentialRequest(
		t,
		engine,
		http.MethodPost,
		fmt.Sprintf("/api/groups/%d/credentials/%d/download", groupID, credentialID),
		"{}",
		"credential-download-auth",
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("download response = %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("secret response headers = %#v", response.Header())
	}
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			Filename   string         `json:"filename"`
			Credential map[string]any `json:"credential"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != 0 || envelope.Data.Filename == "" || envelope.Data.Credential["access_token"] == nil {
		t.Fatalf("download envelope = %#v", envelope)
	}
}

func TestDownloadAllGroupCredentialsHTTPReturnsEveryAccountAndNoStoreHeaders(t *testing.T) {
	initControlI18n(t)
	fixture, groupID, _ := newSubscriptionCredentialFixture(t)
	stageIDs := make([]string, 0, 24)
	for index := 1; index <= 24; index++ {
		stage := mustImportSubscriptionStage(
			t,
			fixture,
			fmt.Sprintf("account-export-%02d", index),
			fmt.Sprintf("export-%02d@example.com", index),
		)
		stageIDs = append(stageIDs, stage.StageID)
	}
	if _, err := fixture.service.ConnectGroupCredentials(t.Context(), groupID, stageIDs); err != nil {
		t.Fatal(err)
	}
	server := NewServer(&config.Config{AuthKey: "credential-download-all-auth"}, fixture.service)
	engine := gin.New()
	server.RegisterRoutes(engine)

	response := serveCredentialRequest(
		t,
		engine,
		http.MethodPost,
		fmt.Sprintf("/api/groups/%d/credentials/download-all", groupID),
		"{}",
		"credential-download-all-auth",
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("download-all response = %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("secret response headers = %#v", response.Header())
	}
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			Filename    string           `json:"filename"`
			Credentials []map[string]any `json:"credentials"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != 0 || envelope.Data.Filename != fmt.Sprintf("codex-group-%d-accounts.json", groupID) {
		t.Fatalf("download-all envelope = %#v", envelope)
	}
	accountIDs := make(map[string]struct{}, len(envelope.Data.Credentials))
	for _, credential := range envelope.Data.Credentials {
		accountID, _ := credential["account_id"].(string)
		accountIDs[accountID] = struct{}{}
	}
	if len(envelope.Data.Credentials) != 25 || len(accountIDs) != 25 {
		t.Fatalf("downloaded credentials = %#v", envelope.Data.Credentials)
	}
	for _, accountID := range []string{"account-observation", "account-export-01", "account-export-24"} {
		if _, exists := accountIDs[accountID]; exists {
			continue
		}
		t.Fatalf("downloaded credentials = %#v", envelope.Data.Credentials)
	}
}

func TestDownloadAllGroupCredentialsRejectsAPIKeyGroup(t *testing.T) {
	t.Parallel()

	fixture := newServiceFixture(t)
	groupID := createGroupForCredentialImport(t, fixture, "sk-download-all-forbidden")
	if _, err := fixture.service.DownloadAllGroupCredentials(t.Context(), groupID); !errors.Is(err, app_errors.ErrForbidden) {
		t.Fatalf("DownloadAllGroupCredentials() error = %v, want forbidden", err)
	}
}

func TestRefreshGroupCredentialOnlyRefreshesToken(t *testing.T) {
	t.Parallel()

	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	forces := make([]bool, 0, 2)
	fixture.service.prepareSubscriptionCredential = func(
		_ context.Context,
		channelID channel.ID,
		snapshot execution.CredentialSnapshot,
		force bool,
	) (subscriptionruntime.Credential, *execution.ErrorEvidence) {
		forces = append(forces, force)
		if channelID != channel.Codex {
			return subscriptionruntime.Credential{}, &execution.ErrorEvidence{
				Kind: execution.ErrorKindInternal,
				Code: "unexpected_subscription_channel",
			}
		}
		credential, err := codex.ParseCredentialJSON(snapshot.Data())
		if err != nil {
			return subscriptionruntime.Credential{}, &execution.ErrorEvidence{
				Kind: execution.ErrorKindInternal,
				Code: "invalid_test_credential",
			}
		}
		runtimeCredential, err := testRuntimeCredential(fixture.service, credential)
		if err != nil {
			return subscriptionruntime.Credential{}, &execution.ErrorEvidence{
				Kind: execution.ErrorKindInternal,
				Code: "invalid_test_credential",
			}
		}
		return runtimeCredential, nil
	}
	observationCalls := 0
	setCodexAccountObservation(fixture.service, func(context.Context, codex.Credential) (codex.AccountObservation, error) {
		observationCalls++
		return codex.AccountObservation{Payload: []byte(`{"quota_windows":[]}`)}, nil
	})

	if _, err := fixture.service.RefreshGroupCredential(t.Context(), groupID, credentialID); err != nil {
		t.Fatal(err)
	}
	if len(forces) != 1 || !forces[0] || observationCalls != 0 {
		t.Fatalf("prepare force calls = %#v, observation calls = %d", forces, observationCalls)
	}
}
