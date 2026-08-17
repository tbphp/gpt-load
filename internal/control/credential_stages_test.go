package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/codex"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
)

func TestImportCodexOAuthJSONCreatesEncryptedReadyStage(t *testing.T) {
	t.Parallel()

	fixture := newServiceFixture(t)
	fixture.service.now = func() time.Time { return time.UnixMilli(1_800_000_000_000) }
	raw := []byte(`{
		"type":"codex","access_token":"access-secret","refresh_token":"refresh-secret",
		"id_token":"id-secret","account_id":"account-123","email":"admin@example.com",
		"expired":"2028-01-01T00:00:00Z","last_refresh":"2026-12-01T00:00:00Z"
	}`)
	result, err := fixture.service.ImportCredentialStage(t.Context(), channel.Codex, raw)
	if err != nil {
		t.Fatalf("ImportCredentialStage() error = %v", err)
	}
	if result.StageID == "" || result.Status != string(models.CredentialStageReady) ||
		result.Account.EmailMask == "" || result.Account.ExpiresAtMS == nil ||
		result.Account.LastRefreshAtMS == nil || result.ExpiresAtMS <= fixture.service.now().UnixMilli() {
		t.Fatalf("stage result = %#v", result)
	}
	encoded, _ := json.Marshal(result)
	for _, secret := range []string{"access-secret", "refresh-secret", "id-secret", "account-123"} {
		if string(encoded) == "" || strings.Contains(string(encoded), secret) {
			t.Fatalf("result leaked secret %q: %s", secret, encoded)
		}
	}
	var row models.CredentialStage
	if err := fixture.db.Take(&row, "id = ?", result.StageID).Error; err != nil {
		t.Fatal(err)
	}
	if row.EncryptedPayload == "" || string(row.SafeSummaryJSON) == "" ||
		strings.Contains(string(row.SafeSummaryJSON), "access-secret") {
		t.Fatalf("stored stage is unsafe: %#v", row)
	}
	plaintext, err := fixture.encryption.Decrypt(row.EncryptedPayload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plaintext, "access-secret") || row.IdentityFingerprint == "account-123" {
		t.Fatal("stage did not encrypt credential or exposed raw account identity")
	}
}

func TestImportCodexOAuthJSONRefreshesExpiredCredentialBeforeReady(t *testing.T) {
	fixture := newServiceFixture(t)
	now := time.Date(2026, time.August, 14, 8, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	refreshCalls := 0
	setCodexCredentialRefresh(t, fixture.service, func(_ context.Context, credential codex.Credential) (codex.Credential, error) {
		refreshCalls++
		if credential.AccessToken != "expired-access" {
			t.Fatalf("credential = %#v", credential)
		}
		credential.AccessToken = "fresh-access"
		credential.RefreshToken = "fresh-refresh"
		credential.Expire = now.Add(time.Hour).Format(time.RFC3339)
		return credential, nil
	})
	stage, err := fixture.service.ImportCredentialStage(t.Context(), channel.Codex, []byte(
		`{"type":"codex","access_token":"expired-access","refresh_token":"expired-refresh","account_id":"account-expired","expired":"2026-08-14T07:00:00Z"}`,
	))
	if err != nil || refreshCalls != 1 {
		t.Fatalf("ImportCredentialStage() result/error/calls = %#v/%v/%d", stage, err, refreshCalls)
	}
	row, err := fixture.service.loadCredentialStage(t.Context(), stage.StageID)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := fixture.service.decodeStageSubscriptionCredential(channel.Codex, row)
	parsed, parseErr := testCodexCredential(credential)
	if err != nil || parseErr != nil || parsed.AccessToken != "fresh-access" || parsed.RefreshToken != "fresh-refresh" {
		t.Fatalf("stored credential = %#v, %v", credential, err)
	}
}

func TestImportCodexOAuthJSONRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	fixture := newServiceFixture(t)
	for _, raw := range [][]byte{
		[]byte(`{"type":"codex","access_token":"a"}`),
		[]byte(`{"type":"codex","access_token":"a","refresh_token":"r","account_id":"id","proxy_url":"http://127.0.0.1"}`),
		make([]byte, 64*1024+1),
	} {
		if _, err := fixture.service.ImportCredentialStage(t.Context(), channel.Codex, raw); !errors.Is(err, app_errors.ErrOAuthFileInvalid) &&
			!errors.Is(err, app_errors.ErrOAuthFileTooLarge) {
			t.Fatalf("invalid import error = %v", err)
		}
	}
}

func TestImportClaudeOAuthJSONRequiresExpiredTimestamp(t *testing.T) {
	fixture := newServiceFixture(t)
	_, err := fixture.service.ImportCredentialStage(t.Context(), channel.Claude, []byte(
		`{"type":"claude","access_token":"access","refresh_token":"refresh","account_uuid":"account"}`,
	))
	if !errors.Is(err, app_errors.ErrOAuthFileInvalid) {
		t.Fatalf("missing Claude expired error = %v", err)
	}
}

func TestBeginBrowserAuthorizationCreatesPendingStage(t *testing.T) {
	t.Parallel()

	fixture := newServiceFixture(t)
	fixture.service.now = func() time.Time { return time.UnixMilli(1_800_000_000_000) }
	result, err := fixture.service.BeginCredentialAuthorization(t.Context(), channel.Codex)
	if err != nil {
		t.Fatalf("BeginCredentialAuthorization() error = %v", err)
	}
	if result.StageID == "" || result.AuthorizationURL == "" ||
		result.Status != string(models.CredentialStagePendingAuthorization) {
		t.Fatalf("authorization result = %#v", result)
	}
	var row models.CredentialStage
	if err := fixture.db.Take(&row, "id = ?", result.StageID).Error; err != nil {
		t.Fatal(err)
	}
	if row.OAuthStateHash == nil || *row.OAuthStateHash == "" || row.EncryptedPayload == "" {
		t.Fatalf("pending stage = %#v", row)
	}
	if strings.Contains(row.EncryptedPayload, "code_verifier") {
		t.Fatal("PKCE payload was stored as plaintext")
	}
}

func TestCancelCredentialStageClearsPayload(t *testing.T) {
	t.Parallel()

	fixture := newServiceFixture(t)
	result, err := fixture.service.ImportCredentialStage(t.Context(), channel.Codex, []byte(
		`{"type":"codex","access_token":"a","refresh_token":"r","account_id":"id"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.CancelCredentialStage(t.Context(), result.StageID); err != nil {
		t.Fatal(err)
	}
	var row models.CredentialStage
	if err := fixture.db.Take(&row, "id = ?", result.StageID).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != models.CredentialStageCancelled || row.EncryptedPayload != "" {
		t.Fatalf("cancelled stage = %#v", row)
	}
}

func TestCompleteBrowserAuthorizationConsumesStateOnce(t *testing.T) {
	t.Parallel()

	fixture := newServiceFixture(t)
	fixture.service.now = func() time.Time { return time.UnixMilli(1_800_000_000_000) }
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
	setCodexAuthorizationCompletion(t, fixture.service, func(_ context.Context, completion codex.BrowserAuthorizationCompletion) (codex.Credential, error) {
		if completion.ExpectedState != payload.State || completion.ReturnedState != payload.State ||
			completion.Code != "authorization-code" || completion.CodeVerifier != codexTestVerifier(payload.DriverState) {
			t.Fatalf("completion = %#v", completion)
		}
		return codex.Credential{
			Type: codex.Provider, AccessToken: "new-access", RefreshToken: "new-refresh",
			AccountID: "account-123", Email: "admin@example.com",
		}, nil
	})
	result, err := fixture.service.CompleteCredentialAuthorization(t.Context(), payload.State, "authorization-code")
	if err != nil {
		t.Fatalf("CompleteCredentialAuthorization() error = %v", err)
	}
	if result.StageID != started.StageID || result.Status != string(models.CredentialStageReady) {
		t.Fatalf("completed result = %#v", result)
	}
	if _, err := fixture.service.CompleteCredentialAuthorization(t.Context(), payload.State, "authorization-code"); !errors.Is(err, app_errors.ErrAuthorizationStateInvalid) {
		t.Fatalf("replayed callback error = %v", err)
	}
}

func TestCompleteBrowserAuthorizationAcceptsVersionOnePendingStage(t *testing.T) {
	fixture := newServiceFixture(t)
	now := time.UnixMilli(1_800_000_000_000)
	fixture.service.now = func() time.Time { return now }
	const (
		state    = "legacy-oauth-state"
		verifier = "legacy-pkce-verifier"
	)
	payload, err := json.Marshal(struct {
		State    string `json:"state"`
		Verifier string `json:"verifier"`
	}{State: state, Verifier: verifier})
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := fixture.encryption.Encrypt(string(payload))
	if err != nil {
		t.Fatal(err)
	}
	row := models.CredentialStage{
		ID: "legacy-pending-stage", ChannelID: string(channel.Codex),
		ConnectionType:       models.ConnectionTypeSubscription,
		AuthorizationMethod:  "browser_oauth",
		Status:               models.CredentialStagePendingAuthorization,
		EncryptedPayload:     ciphertext,
		PayloadSchemaVersion: 1,
		SafeSummaryJSON:      models.JSON(`{}`),
		OAuthStateHash:       pointerTo(fixture.encryption.Hash("oauth-state/v1|" + state)),
		ExpiresAtMS:          now.Add(credentialStageAuthTTL).UnixMilli(),
		CreatedAtMS:          now.UnixMilli(),
		UpdatedAtMS:          now.UnixMilli(),
	}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	setCodexAuthorizationCompletion(t, fixture.service, func(_ context.Context, completion codex.BrowserAuthorizationCompletion) (codex.Credential, error) {
		if completion.ExpectedState != state || completion.ReturnedState != state ||
			completion.Code != "authorization-code" || completion.CodeVerifier != verifier {
			t.Fatalf("completion = %#v", completion)
		}
		return codex.Credential{
			Type: codex.Provider, AccessToken: "new-access", RefreshToken: "new-refresh",
			AccountID: "account-123", Email: "admin@example.com",
		}, nil
	})

	result, err := fixture.service.CompleteCredentialAuthorization(t.Context(), state, "authorization-code")
	if err != nil {
		t.Fatalf("CompleteCredentialAuthorization() error = %v", err)
	}
	if result.StageID != row.ID || result.Status != string(models.CredentialStageReady) {
		t.Fatalf("completed result = %#v", result)
	}
	if err := fixture.db.Take(&row, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.PayloadSchemaVersion != 2 {
		t.Fatalf("PayloadSchemaVersion = %d, want 2", row.PayloadSchemaVersion)
	}
}

func TestCompleteBrowserAuthorizationMarksDefinitiveExchangeRejectionFailed(t *testing.T) {
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
		return codex.Credential{}, &codex.TokenEndpointError{StatusCode: http.StatusBadRequest, Code: "invalid_grant"}
	})

	if _, err := fixture.service.CompleteCredentialAuthorization(t.Context(), payload.State, "rejected-code"); !errors.Is(err, app_errors.ErrAuthorizationExchangeFailed) {
		t.Fatalf("CompleteCredentialAuthorization() error = %v", err)
	}
	if err := fixture.db.Take(&row, "id = ?", started.StageID).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != models.CredentialStageFailed || row.EncryptedPayload != "" || row.ErrorCode != "authorization_exchange_rejected" {
		t.Fatalf("rejected stage = %#v", row)
	}
	result, err := fixture.service.GetCredentialStage(t.Context(), started.StageID)
	if err != nil || result.ErrorCode != "authorization_exchange_rejected" {
		t.Fatalf("failed stage result = %#v, %v", result, err)
	}
}

func TestCompleteBrowserAuthorizationUsesBoundedUpstreamContext(t *testing.T) {
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
	hasBoundedDeadline := false
	setCodexAuthorizationCompletion(t, fixture.service, func(ctx context.Context, _ codex.BrowserAuthorizationCompletion) (codex.Credential, error) {
		deadline, ok := ctx.Deadline()
		hasBoundedDeadline = ok && time.Until(deadline) > 0 && time.Until(deadline) <= 31*time.Second
		return codex.Credential{}, &codex.TokenEndpointError{StatusCode: http.StatusBadRequest, Code: "invalid_grant"}
	})

	_, _ = fixture.service.CompleteCredentialAuthorization(context.Background(), payload.State, "rejected-code")
	if !hasBoundedDeadline {
		t.Fatal("browser authorization exchange did not receive a bounded upstream context")
	}
}

func TestCompleteBrowserAuthorizationSurvivesCallbackCancellationAfterClaim(t *testing.T) {
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
	callbackContext, cancelCallback := context.WithCancel(context.Background())
	setCodexAuthorizationCompletion(t, fixture.service, func(ctx context.Context, _ codex.BrowserAuthorizationCompletion) (codex.Credential, error) {
		cancelCallback()
		if ctx.Err() != nil {
			return codex.Credential{}, ctx.Err()
		}
		return codex.Credential{}, errors.New("connection reset")
	})

	if _, err := fixture.service.CompleteCredentialAuthorization(callbackContext, payload.State, "authorization-code"); !errors.Is(err, app_errors.ErrAuthorizationExchangeFailed) {
		t.Fatalf("CompleteCredentialAuthorization() error = %v", err)
	}
	if err := fixture.db.Take(&row, "id = ?", started.StageID).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != models.CredentialStageOutcomeUnknown || row.EncryptedPayload != "" {
		t.Fatalf("interrupted stage = %#v", row)
	}
}

func TestCompleteBrowserAuthorizationTreatsTransientEndpointRejectionAsUnknown(t *testing.T) {
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
		return codex.Credential{}, &codex.TokenEndpointError{StatusCode: http.StatusTooManyRequests, Code: "rate_limit_exceeded"}
	})

	if _, err := fixture.service.CompleteCredentialAuthorization(t.Context(), payload.State, "authorization-code"); !errors.Is(err, app_errors.ErrAuthorizationExchangeFailed) {
		t.Fatalf("CompleteCredentialAuthorization() error = %v", err)
	}
	if err := fixture.db.Take(&row, "id = ?", started.StageID).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != models.CredentialStageOutcomeUnknown || row.EncryptedPayload != "" {
		t.Fatalf("transiently rejected stage = %#v", row)
	}
}

func TestCreateSubscriptionGroupConsumesReadyStageAtomically(t *testing.T) {
	t.Parallel()

	fixture := newServiceFixture(t)
	stage, err := fixture.service.ImportCredentialStage(t.Context(), channel.Codex, []byte(
		`{"type":"codex","access_token":"access","refresh_token":"refresh","account_id":"account-123","email":"admin@example.com"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	request := GroupCreateRequest{
		Name: stringPointer("subscription group"), ChannelID: channel.Codex,
		Models:              optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-5.2"}}},
		StagedCredentialIDs: []string{stage.StageID}, ConnectionType: "subscription",
	}
	key := "00000000-0000-4000-8000-000000000777"
	created, err := fixture.service.CreateGroupIdempotent(t.Context(), key, request)
	if err != nil {
		t.Fatalf("CreateGroupIdempotent() error = %v", err)
	}
	if created.CredentialsAdded != 1 || created.CredentialsDuplicated != 0 {
		t.Fatalf("created = %#v", created)
	}
	var group models.Group
	if err := fixture.db.Take(&group, created.GroupID).Error; err != nil {
		t.Fatal(err)
	}
	if group.ConnectionType != models.ConnectionTypeSubscription {
		t.Fatalf("group connection_type = %q", group.ConnectionType)
	}
	var credential models.Credential
	if err := fixture.db.Where("group_id = ?", group.ID).Take(&credential).Error; err != nil {
		t.Fatal(err)
	}
	if credential.IdentityFingerprint == "" || credential.IdentityFingerprint == credential.Fingerprint ||
		credential.SecretVersion != 1 || credential.AuthState != models.CredentialAuthStateReady {
		t.Fatalf("credential = %#v", credential)
	}
	var consumed models.CredentialStage
	if err := fixture.db.Take(&consumed, "id = ?", stage.StageID).Error; err != nil {
		t.Fatal(err)
	}
	if consumed.Status != models.CredentialStageConsumed || consumed.EncryptedPayload != "" ||
		consumed.ConsumedGroupID == nil || *consumed.ConsumedGroupID != group.ID {
		t.Fatalf("consumed stage = %#v", consumed)
	}
	replayed, err := fixture.service.CreateGroupIdempotent(t.Context(), key, request)
	if err != nil || replayed != created {
		t.Fatalf("replay = %#v, %v", replayed, err)
	}
}

func TestCreateSubscriptionGroupRollsBackStageOnFailure(t *testing.T) {
	t.Parallel()

	fixture := newServiceFixture(t)
	stage, err := fixture.service.ImportCredentialStage(t.Context(), channel.Codex, []byte(
		`{"type":"codex","access_token":"access","refresh_token":"refresh","account_id":"account-123"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.CreateGroupIdempotent(t.Context(), "00000000-0000-4000-8000-000000000778", GroupCreateRequest{
		Name: stringPointer("invalid subscription group"), ChannelID: channel.Codex,
		ConnectionType:      models.ConnectionTypeSubscription,
		Models:              optionalGroupModels{Set: true, Values: []GroupModel{{ID: "", Alias: "invalid"}}},
		StagedCredentialIDs: []string{stage.StageID},
	})
	if err == nil {
		t.Fatal("invalid group creation error = nil")
	}
	var row models.CredentialStage
	if err := fixture.db.Take(&row, "id = ?", stage.StageID).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != models.CredentialStageReady || row.EncryptedPayload == "" {
		t.Fatalf("stage was consumed after rollback: %#v", row)
	}
}

func TestConnectSubscriptionGroupConsumesReadyStage(t *testing.T) {
	t.Parallel()

	fixture := newServiceFixture(t)
	first := mustImportSubscriptionStage(t, fixture, "account-one", "one@example.com")
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("subscription connect"), ChannelID: channel.Codex,
		ConnectionType:      models.ConnectionTypeSubscription,
		Models:              optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-5.2"}}},
		StagedCredentialIDs: []string{first.StageID},
	})
	if err != nil {
		t.Fatal(err)
	}
	second := mustImportSubscriptionStage(t, fixture, "account-two", "two@example.com")
	result, err := fixture.service.ConnectGroupCredentials(t.Context(), created.GroupID, []string{second.StageID})
	if err != nil {
		t.Fatalf("ConnectGroupCredentials() error = %v", err)
	}
	if result.CredentialsAdded != 1 || result.GroupID != created.GroupID {
		t.Fatalf("result = %#v", result)
	}
	var count int64
	if err := fixture.db.Model(&models.Credential{}).Where("group_id = ?", created.GroupID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 || len(fixture.registry.CaptureActiveCredentialRefs([]uint{created.GroupID})) != 2 {
		t.Fatalf("connected state = db %d registry %#v", count, fixture.registry.Snapshot())
	}
}

func TestConnectSubscriptionGroupIdempotentReplaysConsumedStage(t *testing.T) {
	t.Parallel()

	fixture := newServiceFixture(t)
	first := mustImportSubscriptionStage(t, fixture, "account-one", "one@example.com")
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("subscription connect replay"), ChannelID: channel.Codex,
		ConnectionType:      models.ConnectionTypeSubscription,
		Models:              optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-5.2"}}},
		StagedCredentialIDs: []string{first.StageID},
	})
	if err != nil {
		t.Fatal(err)
	}
	second := mustImportSubscriptionStage(t, fixture, "account-two", "two@example.com")
	key := "123e4567-e89b-42d3-a456-426614174000"
	firstResult, err := fixture.service.ConnectGroupCredentialsIdempotent(
		t.Context(), key, created.GroupID, []string{second.StageID},
	)
	if err != nil {
		t.Fatalf("first connect error = %v", err)
	}
	replayed, err := fixture.service.ConnectGroupCredentialsIdempotent(
		t.Context(), key, created.GroupID, []string{second.StageID},
	)
	if err != nil {
		t.Fatalf("replayed connect error = %v", err)
	}
	if firstResult != replayed || replayed.CredentialsAdded != 1 {
		t.Fatalf("first = %#v, replayed = %#v", firstResult, replayed)
	}
	var count int64
	if err := fixture.db.Model(&models.Credential{}).Where("group_id = ?", created.GroupID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("credential count = %d, want 2", count)
	}
}

func TestSubscriptionCredentialCannotBeRevealedOrImportedAsText(t *testing.T) {
	t.Parallel()

	fixture := newServiceFixture(t)
	stage := mustImportSubscriptionStage(t, fixture, "account-one", "one@example.com")
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("subscription secret boundary"), ChannelID: channel.Codex,
		ConnectionType:      models.ConnectionTypeSubscription,
		Models:              optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-5.2"}}},
		StagedCredentialIDs: []string{stage.StageID},
	})
	if err != nil {
		t.Fatal(err)
	}
	var credential models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&credential).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.RevealGroupCredential(t.Context(), created.GroupID, credential.ID); !errors.Is(err, app_errors.ErrForbidden) {
		t.Fatalf("RevealGroupCredential() error = %v", err)
	}
	if _, err := fixture.service.ImportGroupCredentials(t.Context(), created.GroupID, CredentialImportRequest{Credentials: "secret"}); !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("ImportGroupCredentials() error = %v", err)
	}
}

func TestCleanupCredentialStagesExpiresSecretsAndRemovesOldTombstones(t *testing.T) {
	t.Parallel()

	fixture := newServiceFixture(t)
	now := time.Date(2026, time.August, 13, 8, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	expiredReady := mustImportSubscriptionStage(t, fixture, "expired-account", "expired@example.com")
	expiredExchanging := mustImportSubscriptionStage(t, fixture, "exchanging-account", "exchanging@example.com")
	oldCancelled := mustImportSubscriptionStage(t, fixture, "cancelled-account", "cancelled@example.com")
	if err := fixture.service.CancelCredentialStage(t.Context(), oldCancelled.StageID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.CredentialStage{}).Where("id = ?", expiredReady.StageID).
		Update("expires_at_ms", now.Add(-time.Minute).UnixMilli()).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.CredentialStage{}).Where("id = ?", expiredExchanging.StageID).
		Updates(map[string]any{
			"status": models.CredentialStageExchanging, "expires_at_ms": now.Add(-time.Minute).UnixMilli(),
		}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.CredentialStage{}).Where("id = ?", oldCancelled.StageID).
		Update("updated_at_ms", now.Add(-25*time.Hour).UnixMilli()).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.CleanupCredentialStages(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	var expired models.CredentialStage
	if err := fixture.db.Take(&expired, "id = ?", expiredReady.StageID).Error; err != nil {
		t.Fatal(err)
	}
	if expired.Status != models.CredentialStageExpired || expired.EncryptedPayload != "" || expired.OAuthStateHash != nil {
		t.Fatalf("expired stage = %#v", expired)
	}
	var interrupted models.CredentialStage
	if err := fixture.db.Take(&interrupted, "id = ?", expiredExchanging.StageID).Error; err != nil {
		t.Fatal(err)
	}
	if interrupted.Status != models.CredentialStageOutcomeUnknown || interrupted.EncryptedPayload != "" ||
		interrupted.ErrorCode != "authorization_exchange_interrupted" {
		t.Fatalf("expired exchanging stage = %#v", interrupted)
	}
	interruptedResult, err := fixture.service.GetCredentialStage(t.Context(), expiredExchanging.StageID)
	if err != nil || interruptedResult.Status != string(models.CredentialStageOutcomeUnknown) {
		t.Fatalf("expired exchanging stage result = %#v, %v", interruptedResult, err)
	}
	var count int64
	if err := fixture.db.Model(&models.CredentialStage{}).Where("id = ?", oldCancelled.StageID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("old tombstone count = %d", count)
	}
}

func TestGetCredentialStageFinalizesExpiredExchange(t *testing.T) {
	fixture := newServiceFixture(t)
	now := time.Date(2026, time.August, 14, 8, 0, 0, 0, time.UTC)
	fixture.service.now = func() time.Time { return now }
	stage := mustImportSubscriptionStage(t, fixture, "interrupted-account", "interrupted@example.com")
	if err := fixture.db.Model(&models.CredentialStage{}).Where("id = ?", stage.StageID).
		Updates(map[string]any{
			"status": models.CredentialStageExchanging, "expires_at_ms": now.Add(-time.Second).UnixMilli(),
		}).Error; err != nil {
		t.Fatal(err)
	}

	result, err := fixture.service.GetCredentialStage(t.Context(), stage.StageID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != string(models.CredentialStageOutcomeUnknown) ||
		result.ErrorCode != "authorization_exchange_interrupted" {
		t.Fatalf("expired exchange result = %#v", result)
	}
	var stored models.CredentialStage
	if err := fixture.db.Take(&stored, "id = ?", stage.StageID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.EncryptedPayload != "" || stored.OAuthStateHash != nil {
		t.Fatalf("expired exchange retained secret material: %#v", stored)
	}
}

func mustImportSubscriptionStage(t *testing.T, fixture serviceFixture, accountID, email string) CredentialStageResult {
	t.Helper()
	stage, err := fixture.service.ImportCredentialStage(t.Context(), channel.Codex, []byte(fmt.Sprintf(
		`{"type":"codex","access_token":"access-%s","refresh_token":"refresh-%s","account_id":%q,"email":%q}`,
		accountID, accountID, accountID, email,
	)))
	if err != nil {
		t.Fatal(err)
	}
	return stage
}
