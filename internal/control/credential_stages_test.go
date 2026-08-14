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
	fixture.service.refreshCodexCredential = func(_ context.Context, credential codex.Credential) (codex.Credential, error) {
		refreshCalls++
		if credential.AccessToken != "expired-access" {
			t.Fatalf("credential = %#v", credential)
		}
		credential.AccessToken = "fresh-access"
		credential.RefreshToken = "fresh-refresh"
		credential.Expire = now.Add(time.Hour).Format(time.RFC3339)
		return credential, nil
	}
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
	credential, err := fixture.service.decodeStageCodexCredential(row)
	if err != nil || credential.AccessToken != "fresh-access" || credential.RefreshToken != "fresh-refresh" {
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
	var payload stagedCodexPayload
	if err := json.Unmarshal([]byte(plaintext), &payload); err != nil {
		t.Fatal(err)
	}
	fixture.service.completeBrowserAuthorization = func(_ context.Context, completion codex.BrowserAuthorizationCompletion) (codex.Credential, error) {
		if completion.ExpectedState != payload.State || completion.ReturnedState != payload.State ||
			completion.Code != "authorization-code" || completion.CodeVerifier != payload.Verifier {
			t.Fatalf("completion = %#v", completion)
		}
		return codex.Credential{
			Type: codex.Provider, AccessToken: "new-access", RefreshToken: "new-refresh",
			AccountID: "account-123", Email: "admin@example.com",
		}, nil
	}
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
	var payload stagedCodexPayload
	if err := json.Unmarshal([]byte(plaintext), &payload); err != nil {
		t.Fatal(err)
	}
	fixture.service.completeBrowserAuthorization = func(context.Context, codex.BrowserAuthorizationCompletion) (codex.Credential, error) {
		return codex.Credential{}, &codex.TokenEndpointError{StatusCode: http.StatusBadRequest, Code: "invalid_grant"}
	}

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
	var payload stagedCodexPayload
	if err := json.Unmarshal([]byte(plaintext), &payload); err != nil {
		t.Fatal(err)
	}
	hasBoundedDeadline := false
	fixture.service.completeBrowserAuthorization = func(ctx context.Context, _ codex.BrowserAuthorizationCompletion) (codex.Credential, error) {
		deadline, ok := ctx.Deadline()
		hasBoundedDeadline = ok && time.Until(deadline) > 0 && time.Until(deadline) <= 31*time.Second
		return codex.Credential{}, &codex.TokenEndpointError{StatusCode: http.StatusBadRequest, Code: "invalid_grant"}
	}

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
	var payload stagedCodexPayload
	if err := json.Unmarshal([]byte(plaintext), &payload); err != nil {
		t.Fatal(err)
	}
	callbackContext, cancelCallback := context.WithCancel(context.Background())
	fixture.service.completeBrowserAuthorization = func(ctx context.Context, _ codex.BrowserAuthorizationCompletion) (codex.Credential, error) {
		cancelCallback()
		if ctx.Err() != nil {
			return codex.Credential{}, ctx.Err()
		}
		return codex.Credential{}, errors.New("connection reset")
	}

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
	var payload stagedCodexPayload
	if err := json.Unmarshal([]byte(plaintext), &payload); err != nil {
		t.Fatal(err)
	}
	fixture.service.completeBrowserAuthorization = func(context.Context, codex.BrowserAuthorizationCompletion) (codex.Credential, error) {
		return codex.Credential{}, &codex.TokenEndpointError{StatusCode: http.StatusTooManyRequests, Code: "rate_limit_exceeded"}
	}

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
		StagedCredentialIDs: []string{stage.StageID},
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

func TestReauthorizeSubscriptionCredentialPreservesIdentityAndAdvancesSecretVersion(t *testing.T) {
	t.Parallel()

	fixture := newServiceFixture(t)
	stage := mustImportSubscriptionStage(t, fixture, "account-one", "one@example.com")
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("subscription reauthorize"), ChannelID: channel.Codex,
		ConnectionType:      models.ConnectionTypeSubscription,
		Models:              optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-5.2"}}},
		StagedCredentialIDs: []string{stage.StageID},
	})
	if err != nil {
		t.Fatal(err)
	}
	var before models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&before).Error; err != nil {
		t.Fatal(err)
	}
	reauthorized := mustImportSubscriptionStage(t, fixture, "account-one", "renamed@example.com")
	item, err := fixture.service.ReauthorizeGroupCredential(t.Context(), created.GroupID, before.ID, CredentialReauthorizationRequest{
		StageID: reauthorized.StageID, ExpectedSecretVersion: before.SecretVersion,
	})
	if err != nil {
		t.Fatalf("ReauthorizeGroupCredential() error = %v", err)
	}
	if item.AuthState != string(models.CredentialAuthStateReady) {
		t.Fatalf("item = %#v", item)
	}
	var after models.Credential
	if err := fixture.db.Take(&after, before.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.IdentityFingerprint != before.IdentityFingerprint || after.SecretVersion != before.SecretVersion+1 || after.Fingerprint == before.Fingerprint {
		t.Fatalf("before/after = %#v / %#v", before, after)
	}
	if view, ok := findRuntimeCredential(fixture.registry.Snapshot(), before.ID); !ok || view.Version != after.SecretVersion || view.IdentityGeneration != groupCollectionCredentialIdentity(after.IdentityFingerprint, models.Group{ID: created.GroupID, ChannelID: string(channel.Codex), ConnectionType: models.ConnectionTypeSubscription, Params: models.JSON(`{}`)}) {
		t.Fatalf("runtime view = %#v, %v", view, ok)
	}
}

func TestReauthorizeSubscriptionCredentialIdempotentReplaysConsumedStage(t *testing.T) {
	t.Parallel()

	fixture := newServiceFixture(t)
	stage := mustImportSubscriptionStage(t, fixture, "account-one", "one@example.com")
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("subscription reauthorize replay"), ChannelID: channel.Codex,
		ConnectionType:      models.ConnectionTypeSubscription,
		Models:              optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-5.2"}}},
		StagedCredentialIDs: []string{stage.StageID},
	})
	if err != nil {
		t.Fatal(err)
	}
	var before models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&before).Error; err != nil {
		t.Fatal(err)
	}
	ready := mustImportSubscriptionStage(t, fixture, "account-one", "renamed@example.com")
	request := CredentialReauthorizationRequest{
		StageID: ready.StageID, ExpectedSecretVersion: before.SecretVersion,
	}
	key := "123e4567-e89b-42d3-a456-426614174001"
	first, err := fixture.service.ReauthorizeGroupCredentialIdempotent(
		t.Context(), key, created.GroupID, before.ID, request,
	)
	if err != nil {
		t.Fatalf("first reauthorization error = %v", err)
	}
	replayed, err := fixture.service.ReauthorizeGroupCredentialIdempotent(
		t.Context(), key, created.GroupID, before.ID, request,
	)
	if err != nil {
		t.Fatalf("replayed reauthorization error = %v", err)
	}
	if first.SecretVersion != before.SecretVersion+1 || replayed.SecretVersion != first.SecretVersion {
		t.Fatalf("first = %#v, replayed = %#v", first, replayed)
	}
	var after models.Credential
	if err := fixture.db.Take(&after, before.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.SecretVersion != before.SecretVersion+1 {
		t.Fatalf("secret version = %d, want %d", after.SecretVersion, before.SecretVersion+1)
	}
}

func TestReauthorizeSubscriptionCredentialIdempotencyKeyIsBoundToCredential(t *testing.T) {
	fixture := newServiceFixture(t)
	firstStage := mustImportSubscriptionStage(t, fixture, "account-one", "one@example.com")
	secondStage := mustImportSubscriptionStage(t, fixture, "account-two", "two@example.com")
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("subscription reauthorize credential binding"), ChannelID: channel.Codex,
		ConnectionType: models.ConnectionTypeSubscription,
		Models:         optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-5.2"}}},
		StagedCredentialIDs: []string{
			firstStage.StageID,
			secondStage.StageID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var credentials []models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Order("id ASC").Find(&credentials).Error; err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 2 {
		t.Fatalf("credential count = %d", len(credentials))
	}
	firstIndex := 0
	if credentials[firstIndex].IdentityFingerprint != fixture.service.subscriptionIdentityFingerprint(channel.Codex, "account-one") {
		firstIndex = 1
	}
	secondIndex := 1 - firstIndex
	ready := mustImportSubscriptionStage(t, fixture, "account-one", "renamed@example.com")
	request := CredentialReauthorizationRequest{
		StageID: ready.StageID, ExpectedSecretVersion: credentials[firstIndex].SecretVersion,
	}
	key := "123e4567-e89b-42d3-a456-426614174011"
	if _, err := fixture.service.ReauthorizeGroupCredentialIdempotent(
		t.Context(), key, created.GroupID, credentials[firstIndex].ID, request,
	); err != nil {
		t.Fatalf("first reauthorization error = %v", err)
	}
	_, err = fixture.service.ReauthorizeGroupCredentialIdempotent(
		t.Context(), key, created.GroupID, credentials[secondIndex].ID, request,
	)
	assertAPIErrorCode(t, err, app_errors.ErrIdempotencyKeyReused.Code)
}

func TestReauthorizeSubscriptionCredentialRejectsStaleSecretVersionWithoutConsumingStage(t *testing.T) {
	fixture := newServiceFixture(t)
	stage := mustImportSubscriptionStage(t, fixture, "account-stale-version", "stale@example.com")
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("subscription stale version"), ChannelID: channel.Codex,
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
	ready := mustImportSubscriptionStage(t, fixture, "account-stale-version", "new@example.com")
	_, err = fixture.service.ReauthorizeGroupCredential(t.Context(), created.GroupID, credential.ID, CredentialReauthorizationRequest{
		StageID: ready.StageID, ExpectedSecretVersion: credential.SecretVersion + 1,
	})
	var apiErr *app_errors.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "CREDENTIAL_VERSION_CONFLICT" {
		t.Fatalf("ReauthorizeGroupCredential() error = %v", err)
	}
	row, loadErr := fixture.service.loadCredentialStage(t.Context(), ready.StageID)
	if loadErr != nil || row.Status != models.CredentialStageReady {
		t.Fatalf("staged credential = %#v, %v", row, loadErr)
	}
}

func TestReauthorizeSubscriptionCredentialSerializesSecretMutation(t *testing.T) {
	fixture := newServiceFixture(t)
	stage := mustImportSubscriptionStage(t, fixture, "account-one", "one@example.com")
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("subscription reauthorize serialized"), ChannelID: channel.Codex,
		ConnectionType:      models.ConnectionTypeSubscription,
		Models:              optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-5.2"}}},
		StagedCredentialIDs: []string{stage.StageID},
	})
	if err != nil {
		t.Fatal(err)
	}
	var before models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&before).Error; err != nil {
		t.Fatal(err)
	}
	ready := mustImportSubscriptionStage(t, fixture, "account-one", "renamed@example.com")
	coordinator := &blockingCredentialMutationCoordinator{
		entered: make(chan uint, 1), release: make(chan struct{}),
	}
	fixture.service.mutations = coordinator
	type result struct {
		item CredentialItemResponse
		err  error
	}
	done := make(chan result, 1)
	go func() {
		item, callErr := fixture.service.ReauthorizeGroupCredentialIdempotent(
			context.Background(), "123e4567-e89b-42d3-a456-426614174002", created.GroupID, before.ID,
			CredentialReauthorizationRequest{StageID: ready.StageID, ExpectedSecretVersion: before.SecretVersion},
		)
		done <- result{item: item, err: callErr}
	}()
	select {
	case enteredID := <-coordinator.entered:
		if enteredID != before.ID {
			t.Fatalf("mutation credential id = %d, want %d", enteredID, before.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reauthorization did not enter the credential mutation boundary")
	}
	var blocked models.Credential
	if err := fixture.db.Take(&blocked, before.ID).Error; err != nil {
		t.Fatal(err)
	}
	if blocked.SecretVersion != before.SecretVersion {
		t.Fatalf("secret version changed before mutation boundary released: %d", blocked.SecretVersion)
	}
	close(coordinator.release)
	select {
	case result := <-done:
		if result.err != nil || result.item.SecretVersion != before.SecretVersion+1 {
			t.Fatalf("reauthorization result = %#v, %v", result.item, result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reauthorization did not finish after mutation boundary released")
	}
}

func TestReauthorizeSubscriptionCredentialAcquiresControlWriteLockBeforeCredentialMutation(t *testing.T) {
	fixture := newServiceFixture(t)
	stage := mustImportSubscriptionStage(t, fixture, "account-one", "one@example.com")
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("subscription reauthorize lock order"), ChannelID: channel.Codex,
		ConnectionType:      models.ConnectionTypeSubscription,
		Models:              optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-5.2"}}},
		StagedCredentialIDs: []string{stage.StageID},
	})
	if err != nil {
		t.Fatal(err)
	}
	var before models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&before).Error; err != nil {
		t.Fatal(err)
	}
	ready := mustImportSubscriptionStage(t, fixture, "account-one", "renamed@example.com")
	coordinator := &signalingCredentialMutationCoordinator{entered: make(chan uint, 1)}
	fixture.service.mutations = coordinator

	fixture.service.writeMu.Lock()
	locked := true
	defer func() {
		if locked {
			fixture.service.writeMu.Unlock()
		}
	}()
	done := make(chan error, 1)
	go func() {
		_, callErr := fixture.service.ReauthorizeGroupCredentialIdempotent(
			context.Background(), "123e4567-e89b-42d3-a456-426614174003", created.GroupID, before.ID,
			CredentialReauthorizationRequest{StageID: ready.StageID, ExpectedSecretVersion: before.SecretVersion},
		)
		done <- callErr
	}()
	select {
	case enteredID := <-coordinator.entered:
		fixture.service.writeMu.Unlock()
		locked = false
		<-done
		t.Fatalf("credential mutation %d entered before the control write lock", enteredID)
	case <-time.After(100 * time.Millisecond):
	}
	fixture.service.writeMu.Unlock()
	locked = false
	select {
	case enteredID := <-coordinator.entered:
		if enteredID != before.ID {
			t.Fatalf("mutation credential id = %d, want %d", enteredID, before.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("credential mutation did not enter after the control write lock was released")
	}
	select {
	case callErr := <-done:
		if callErr != nil {
			t.Fatalf("reauthorization error = %v", callErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reauthorization did not finish")
	}
}

type blockingCredentialMutationCoordinator struct {
	entered chan uint
	release chan struct{}
}

func (coordinator *blockingCredentialMutationCoordinator) Do(credentialID uint, fn func()) {
	coordinator.entered <- credentialID
	<-coordinator.release
	fn()
}

type signalingCredentialMutationCoordinator struct {
	entered chan uint
}

func (coordinator *signalingCredentialMutationCoordinator) Do(credentialID uint, fn func()) {
	coordinator.entered <- credentialID
	fn()
}

func TestReauthorizeSubscriptionCredentialRejectsDifferentAccount(t *testing.T) {
	t.Parallel()

	fixture := newServiceFixture(t)
	stage := mustImportSubscriptionStage(t, fixture, "account-one", "one@example.com")
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("subscription reauthorize mismatch"), ChannelID: channel.Codex,
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
	mismatch := mustImportSubscriptionStage(t, fixture, "account-two", "two@example.com")
	_, err = fixture.service.ReauthorizeGroupCredential(t.Context(), created.GroupID, credential.ID, CredentialReauthorizationRequest{
		StageID: mismatch.StageID, ExpectedSecretVersion: credential.SecretVersion,
	})
	if !errors.Is(err, app_errors.ErrStagedCredentialMismatch) {
		t.Fatalf("ReauthorizeGroupCredential() error = %v", err)
	}
	row, err := fixture.service.loadCredentialStage(t.Context(), mismatch.StageID)
	if err != nil || row.Status != models.CredentialStageReady {
		t.Fatalf("mismatched stage = %#v, %v", row, err)
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
