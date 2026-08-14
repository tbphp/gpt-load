package cpa

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"
	"gorm.io/gorm"

	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

type fakeExecutor struct {
	mu      sync.Mutex
	calls   int
	last    cpaembedded.CodexCredential
	request cpaembedded.ExecuteRequest
	result  cpaembedded.ExecuteResponse
	err     error
	stream  *cpaembedded.ExecuteStreamResponse
}

type failingEncryptService struct {
	encryption.Service
}

func (failingEncryptService) Encrypt(string) (string, error) {
	return "", errors.New("encrypt failed")
}

func (f *fakeExecutor) ExecuteCanonical(_ context.Context, _ string, credential cpaembedded.CodexCredential, request cpaembedded.ExecuteRequest) (cpaembedded.ExecuteResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.last = credential
	f.request = request
	return f.result, f.err
}

func (f *fakeExecutor) ExecuteStreamCanonical(_ context.Context, _ string, credential cpaembedded.CodexCredential, request cpaembedded.ExecuteRequest) (*cpaembedded.ExecuteStreamResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.last = credential
	f.request = request
	return f.stream, f.err
}

func TestAdapterRefreshesExpiringCredentialOnceAndPreservesIdentity(t *testing.T) {
	adapter, db, registry, keyService, row := newAdapterFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Minute)))
	var refreshCalls int
	adapter.refresh = func(_ context.Context, current cpaembedded.CodexCredential) (cpaembedded.CodexCredential, error) {
		refreshCalls++
		current.AccessToken = "new-access"
		current.RefreshToken = "new-refresh"
		current.Expire = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		return current, nil
	}
	fake := &fakeExecutor{result: cpaembedded.ExecuteResponse{Payload: []byte(`{"id":"resp_1","model":"gpt-5","usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`), Headers: http.Header{"X-Request-Id": {"upstream-1"}}}}
	adapter.executor = fake

	result := adapter.Execute(t.Context(), validSpec(t, row, keyService))
	if result.Error != nil || result.StatusCode != http.StatusOK || refreshCalls != 1 || fake.calls != 1 || fake.last.AccessToken != "new-access" {
		t.Fatalf("result=%#v refresh=%d execute=%d credential=%#v", result, refreshCalls, fake.calls, fake.last)
	}
	var stored models.Credential
	if err := db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.SecretVersion != 2 || stored.IdentityFingerprint != row.IdentityFingerprint || stored.AuthState != models.CredentialAuthStateReady {
		t.Fatalf("stored credential = %#v", stored)
	}
	entry, ok := registry.CredentialRef(row.ID)
	if !ok || entry.Version != 2 || entry.IdentityGeneration != stateloader.CredentialIdentityGeneration(row.IdentityFingerprint, "openai", "subscription", json.RawMessage(`{}`)) {
		t.Fatalf("registry entry = %#v", entry)
	}
}

func TestAdapterForceRefreshesCredentialAfterExplicitRejection(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Hour)))
	var refreshCalls int
	adapter.refresh = func(_ context.Context, current cpaembedded.CodexCredential) (cpaembedded.CodexCredential, error) {
		refreshCalls++
		current.AccessToken = "new-access"
		current.RefreshToken = "new-refresh"
		current.Expire = time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
		return current, nil
	}
	fake := &fakeExecutor{result: cpaembedded.ExecuteResponse{Payload: []byte(`{"id":"resp_1","model":"gpt-5"}`)}}
	adapter.executor = fake
	spec := validSpec(t, row, keyService)
	spec.ForceCredentialRefresh = true

	result := adapter.Execute(t.Context(), spec)
	if result.Error != nil || refreshCalls != 1 || fake.calls != 1 || fake.last.AccessToken != "new-access" {
		t.Fatalf("result=%#v refresh=%d execute=%d credential=%#v", result, refreshCalls, fake.calls, fake.last)
	}
}

func TestAdapterForceRefreshUsesNewerSecretWrittenByConcurrentRequest(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Hour)))
	var refreshCalls int
	adapter.refresh = func(_ context.Context, current cpaembedded.CodexCredential) (cpaembedded.CodexCredential, error) {
		refreshCalls++
		current.AccessToken = "new-access"
		current.RefreshToken = "new-refresh"
		current.Expire = time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
		return current, nil
	}
	fake := &fakeExecutor{result: cpaembedded.ExecuteResponse{Payload: []byte(`{"id":"resp_1","model":"gpt-5"}`)}}
	adapter.executor = fake
	spec := validSpec(t, row, keyService)
	spec.ForceCredentialRefresh = true

	first := adapter.Execute(t.Context(), spec)
	second := adapter.Execute(t.Context(), spec)
	if first.Error != nil || second.Error != nil || refreshCalls != 1 || fake.calls != 2 || fake.last.AccessToken != "new-access" {
		t.Fatalf("first=%#v second=%#v refresh=%d execute=%d credential=%#v", first, second, refreshCalls, fake.calls, fake.last)
	}
}

func TestAdapterConcurrentPrepareUsesOneRefresh(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Minute)))
	var mu sync.Mutex
	refreshCalls := 0
	adapter.refresh = func(_ context.Context, current cpaembedded.CodexCredential) (cpaembedded.CodexCredential, error) {
		mu.Lock()
		refreshCalls++
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		current.AccessToken = "new-access"
		current.Expire = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		return current, nil
	}
	adapter.executor = &fakeExecutor{result: cpaembedded.ExecuteResponse{Payload: []byte(`{"id":"resp","model":"gpt-5"}`)}}
	spec := validSpec(t, row, keyService)
	var wait sync.WaitGroup
	wait.Add(2)
	for range 2 {
		go func() { defer wait.Done(); _ = adapter.Execute(context.Background(), spec) }()
	}
	wait.Wait()
	mu.Lock()
	defer mu.Unlock()
	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls)
	}
}

func TestAdapterReconcilesRegistryWhenIncrementalRefreshPublicationFails(t *testing.T) {
	adapter, db, registry, keyService, row := newAdapterFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Minute)))
	adapter.refresh = func(_ context.Context, current cpaembedded.CodexCredential) (cpaembedded.CodexCredential, error) {
		current.AccessToken = "new-access"
		current.RefreshToken = "new-refresh"
		current.Expire = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		return current, nil
	}
	adapter.executor = &fakeExecutor{result: cpaembedded.ExecuteResponse{Payload: []byte(`{"id":"resp"}`)}}
	adapter.replaceSecret = func(uint, uint64, uint64, string, string) bool { return false }

	result := adapter.Execute(t.Context(), validSpec(t, row, keyService))
	if result.Error != nil {
		t.Fatalf("result = %#v, evidence = %#v", result, result.Error)
	}
	var stored models.Credential
	if err := db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.SecretVersion != row.SecretVersion+1 || stored.AuthState != models.CredentialAuthStateReady {
		t.Fatalf("stored credential = %#v", stored)
	}
	plaintext, err := keyService.Decrypt(stored.Data)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := cpaembedded.ParseCodexCredentialJSON([]byte(plaintext))
	if err != nil || credential.AccessToken != "new-access" {
		t.Fatalf("durable credential = %#v, %v", credential, err)
	}
	ref, ok := registry.CredentialRef(row.ID)
	if !ok || ref.Version != row.SecretVersion+1 || ref.Fingerprint != stored.Fingerprint || ref.EncryptedValue != stored.Data {
		t.Fatalf("registry ref = %#v, ok = %t", ref, ok)
	}
}

func TestAdapterRestoresReadyWhenRegistryCannotPublishRefreshStart(t *testing.T) {
	adapter, db, _, keyService, row := newAdapterFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Minute)))
	adapter.registry.RemoveCredential(row.ID)
	refreshCalls := 0
	adapter.refresh = func(context.Context, cpaembedded.CodexCredential) (cpaembedded.CodexCredential, error) {
		refreshCalls++
		return cpaembedded.CodexCredential{}, errors.New("must not be called")
	}

	result := adapter.Execute(t.Context(), validSpec(t, row, keyService))
	if result.Error == nil || result.Error.Code != "refresh_registry_mismatch" || refreshCalls != 0 {
		t.Fatalf("result = %#v, evidence = %#v, refresh calls = %d", result, result.Error, refreshCalls)
	}
	var stored models.Credential
	if err := db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.SecretVersion != row.SecretVersion || stored.AuthState != models.CredentialAuthStateReady {
		t.Fatalf("stored credential = %#v", stored)
	}
}

func TestAdapterDefinitiveRefreshRejectionRequiresReauthorization(t *testing.T) {
	adapter, db, _, keyService, row := newAdapterFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Minute)))
	adapter.refresh = func(context.Context, cpaembedded.CodexCredential) (cpaembedded.CodexCredential, error) {
		return cpaembedded.CodexCredential{}, &cpaembedded.TokenEndpointError{StatusCode: http.StatusBadRequest, Code: "invalid_grant"}
	}
	result := adapter.Execute(t.Context(), validSpec(t, row, keyService))
	if result.DispatchState != execution.DispatchNotSent || result.Error == nil || result.Error.Hint != execution.FailureHintReauthorizationRequired {
		t.Fatalf("result = %#v", result)
	}
	var stored models.Credential
	if err := db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.AuthState != models.CredentialAuthStateReauthorizationRequired {
		t.Fatalf("auth state = %q", stored.AuthState)
	}
}

func TestAdapterTransientRefreshRejectionDoesNotRequireReauthorization(t *testing.T) {
	adapter, db, _, keyService, row := newAdapterFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Minute)))
	adapter.refresh = func(context.Context, cpaembedded.CodexCredential) (cpaembedded.CodexCredential, error) {
		return cpaembedded.CodexCredential{}, &cpaembedded.TokenEndpointError{StatusCode: http.StatusTooManyRequests, Code: "rate_limit_exceeded"}
	}

	result := adapter.Execute(t.Context(), validSpec(t, row, keyService))
	if result.DispatchState != execution.DispatchNotSent || result.Error == nil || result.Error.Code != "refresh_outcome_unknown" {
		t.Fatalf("result = %#v", result)
	}
	var stored models.Credential
	if err := db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.AuthState != models.CredentialAuthStateOutcomeUnknown {
		t.Fatalf("auth state = %q", stored.AuthState)
	}
}

func TestAdapterAmbiguousRefreshFailureStopsWithoutReplay(t *testing.T) {
	adapter, db, _, keyService, row := newAdapterFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Minute)))
	adapter.refresh = func(context.Context, cpaembedded.CodexCredential) (cpaembedded.CodexCredential, error) {
		return cpaembedded.CodexCredential{}, errors.New("connection reset")
	}
	result := adapter.Execute(t.Context(), validSpec(t, row, keyService))
	if result.DispatchState != execution.DispatchNotSent || result.Error == nil || result.Error.Hint != execution.FailureHintReauthorizationRequired {
		t.Fatalf("result = %#v", result)
	}
	var stored models.Credential
	if err := db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.AuthState != models.CredentialAuthStateOutcomeUnknown {
		t.Fatalf("auth state = %q", stored.AuthState)
	}
}

func TestAdapterMarksOutcomeUnknownWhenRotatedTokenCannotBeEncrypted(t *testing.T) {
	adapter, db, registry, keyService, row := newAdapterFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Minute)))
	adapter.encryption = failingEncryptService{Service: keyService}
	adapter.refresh = func(_ context.Context, current cpaembedded.CodexCredential) (cpaembedded.CodexCredential, error) {
		current.AccessToken = "new-access"
		current.RefreshToken = "new-refresh"
		return current, nil
	}

	result := adapter.Execute(t.Context(), validSpec(t, row, keyService))
	if result.DispatchState != execution.DispatchNotSent || result.Error == nil ||
		result.Error.Code != "refresh_persist_failed" || result.Error.Hint != execution.FailureHintReauthorizationRequired {
		t.Fatalf("result = %#v", result)
	}
	var stored models.Credential
	if err := db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.SecretVersion != row.SecretVersion || stored.AuthState != models.CredentialAuthStateOutcomeUnknown {
		t.Fatalf("stored credential = %#v", stored)
	}
	views := registry.Snapshot()
	if len(views) != 1 || views[0].AuthState != state.CredentialAuthStateOutcomeUnknown {
		t.Fatalf("registry views = %#v", views)
	}
}

func TestAdapterKeepsUnknownSubscription401NonReplayable(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access-secret", "refresh-secret", time.Now().Add(time.Hour)))
	adapter.executor = &fakeExecutor{err: statusError{status: http.StatusUnauthorized, message: `{"error":{"type":"authentication_error","code":"auth_unavailable","message":"access-secret expired"}}`}}
	result := adapter.Execute(t.Context(), validSpec(t, row, keyService))
	if result.Error == nil || result.Error.Hint != "" || result.Error.ReplaySafety != execution.ReplaySafetyUnknown {
		t.Fatalf("error evidence = %#v", result.Error)
	}
	if result.Error.Summary == "" || result.Error.Summary == "access-secret expired" {
		t.Fatalf("unsafe summary = %q", result.Error.Summary)
	}
}

func TestAdapterMapsExplicitExpiredTokenToSafeRefresh(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access-secret", "refresh-secret", time.Now().Add(time.Hour)))
	adapter.executor = &fakeExecutor{err: statusError{status: http.StatusUnauthorized, message: `{"error":{"type":"authentication_error","code":"token_expired","message":"access token expired"}}`}}
	result := adapter.Execute(t.Context(), validSpec(t, row, keyService))
	if result.Error == nil || result.Error.Hint != execution.FailureHintRefreshRequired || result.Error.ReplaySafety != execution.ReplaySafetyRejectedBeforeProcessing {
		t.Fatalf("error evidence = %#v", result.Error)
	}
}

func TestAdapterStreamsReadyThenFramedData(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	chunks := make(chan cpaembedded.ExecuteStreamChunk, 1)
	chunks <- cpaembedded.ExecuteStreamChunk{Payload: []byte(`data: {"type":"response.completed","response":{"id":"resp_1"}}`)}
	close(chunks)
	adapter.executor = &fakeExecutor{stream: &cpaembedded.ExecuteStreamResponse{Headers: http.Header{"Content-Type": {"text/event-stream"}}, Chunks: chunks}}
	var events []execution.StreamEvent
	result := adapter.ExecuteStream(t.Context(), validSpec(t, row, keyService), func(event execution.StreamEvent) error {
		events = append(events, event.Clone())
		return nil
	})
	if result.Error != nil || len(events) != 2 || events[0].Kind != execution.StreamEventReady || events[1].Kind != execution.StreamEventData || string(events[1].Data[len(events[1].Data)-2:]) != "\n\n" {
		t.Fatalf("result=%#v events=%#v", result, events)
	}
}

type statusError struct {
	status  int
	message string
}

func (e statusError) Error() string   { return e.message }
func (e statusError) StatusCode() int { return e.status }

func newAdapterFixture(t *testing.T, canonical []byte) (*Adapter, *gorm.DB, *state.CredentialRegistry, encryption.Service, models.Credential) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Group{}, &models.Credential{}); err != nil {
		t.Fatal(err)
	}
	keyService, err := encryption.NewService("cpa-adapter-test-encryption-key-material")
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := keyService.Encrypt(string(canonical))
	if err != nil {
		t.Fatal(err)
	}
	group := models.Group{Name: "subscription", ChannelID: "openai", ConnectionType: models.ConnectionTypeSubscription, Params: models.JSON(`{}`), Models: models.JSON(`[]`), Overrides: models.JSON(`{}`), Enabled: true}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	credential, err := cpaembedded.ParseCodexCredentialJSON(canonical)
	if err != nil {
		t.Fatal(err)
	}
	row := models.Credential{GroupID: group.ID, Data: ciphertext, Fingerprint: keyService.Hash(string(canonical)), IdentityFingerprint: keyService.Hash("identity|" + credential.AccountID), SecretVersion: 1, AuthState: models.CredentialAuthStateReady, Status: models.CredentialStatusActive}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	registry := state.NewCredentialRegistry()
	identityGeneration := stateloader.CredentialIdentityGeneration(row.IdentityFingerprint, group.ChannelID, string(group.ConnectionType), json.RawMessage(group.Params))
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{ID: row.ID, GroupID: group.ID, Version: 1, IdentityGeneration: identityGeneration, Fingerprint: row.Fingerprint, Status: state.CredentialStatusActive, WeightAuto: state.DefaultWeight, EncryptedValue: row.Data}}); err != nil {
		t.Fatal(err)
	}
	return NewAdapter(db, keyService, registry, health.NewMutationCoordinator()), db, registry, keyService, row
}

func credentialJSON(access, refresh string, expires time.Time) []byte {
	value, _ := json.Marshal(cpaembedded.CodexCredential{Type: cpaembedded.ProviderCodex, AccessToken: access, RefreshToken: refresh, AccountID: "account-1", Email: "a@example.com", Expire: expires.UTC().Format(time.RFC3339)})
	return value
}

func validSpec(t *testing.T, row models.Credential, keyService encryption.Service) execution.AttemptSpec {
	t.Helper()
	plaintext, err := keyService.Decrypt(row.Data)
	if err != nil {
		t.Fatal(err)
	}
	return execution.NewAttemptSpec(execution.AttemptSpec{RequestID: "request-1", AttemptID: "attempt-1", Sequence: 1, ChannelID: "openai", ConnectionType: "subscription", TargetKind: "openai", RouteMode: execution.RouteNative, ClientProtocol: protocol.OpenAIResponses, Operation: execution.OperationResponsesCreate, ClientModel: "gpt-5", UpstreamModel: "gpt-5", Method: http.MethodPost, Path: "/v1/responses", Body: []byte(`{"model":"gpt-5","input":"hi"}`), Credential: execution.NewCredentialSnapshot(row.ID, row.SecretVersion, 1, []byte(plaintext))})
}
