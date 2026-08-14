package cpa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
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

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
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
	if !ok || entry.Version != 2 || entry.IdentityGeneration != stateloader.CredentialIdentityGeneration(row.IdentityFingerprint, "codex", "subscription", json.RawMessage(`{}`)) {
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

func TestAdapterFailsClosedWhenRefreshedSecretCannotReachRegistry(t *testing.T) {
	adapter, db, registry, keyService, row := newAdapterFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Minute)))
	adapter.refresh = func(_ context.Context, current cpaembedded.CodexCredential) (cpaembedded.CodexCredential, error) {
		current.AccessToken = "new-access"
		current.RefreshToken = "new-refresh"
		current.Expire = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		return current, nil
	}
	adapter.replaceSecret = func(uint, uint64, uint64, string, string) bool { return false }
	adapter.reconcileGroup = func(uint, []state.CredentialEntry) (bool, error) {
		return false, errors.New("registry unavailable")
	}
	adapter.executor = &fakeExecutor{result: cpaembedded.ExecuteResponse{Payload: []byte(`{"id":"must-not-run"}`)}}

	result := adapter.Execute(t.Context(), validSpec(t, row, keyService))
	if result.Error == nil || result.Error.Code != "refresh_registry_mismatch" {
		t.Fatalf("result = %#v", result)
	}
	var stored models.Credential
	if err := db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.SecretVersion != row.SecretVersion+1 || stored.AuthState != models.CredentialAuthStateOutcomeUnknown {
		t.Fatalf("stored credential = %#v", stored)
	}
	views := registry.Snapshot()
	if len(views) != 1 || views[0].ID != row.ID || views[0].AuthState != state.CredentialAuthStateOutcomeUnknown {
		t.Fatalf("registry views = %#v", views)
	}
}

func TestAdapterRestoresReadyWhenRegistryCannotPublishRefreshStart(t *testing.T) {
	adapter, db, _, keyService, row := newAdapterFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Minute)))
	adapter.registry.RemoveCredential(row.ID)
	adapter.reconcileGroup = func(uint, []state.CredentialEntry) (bool, error) {
		return false, errors.New("registry unavailable")
	}
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

func TestAdapterRefreshIdentityChangeRequiresReauthorization(t *testing.T) {
	adapter, db, _, keyService, row := newAdapterFixture(t, credentialJSON("old-access", "old-refresh", time.Now().Add(time.Minute)))
	adapter.refresh = func(context.Context, cpaembedded.CodexCredential) (cpaembedded.CodexCredential, error) {
		return cpaembedded.CodexCredential{}, cpaembedded.ErrCredentialIdentityChanged
	}

	result := adapter.Execute(t.Context(), validSpec(t, row, keyService))
	if result.Error == nil || result.Error.Code != "refresh_identity_changed" || result.Error.Hint != execution.FailureHintReauthorizationRequired {
		t.Fatalf("result = %#v", result)
	}
	var stored models.Credential
	if err := db.First(&stored, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.AuthState != models.CredentialAuthStateReauthorizationRequired || stored.AuthErrorCode != "refresh_identity_changed" {
		t.Fatalf("stored credential = %#v", stored)
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
	adapter.executor = &fakeExecutor{err: statusError{status: http.StatusUnauthorized, message: `{"error":{"type":"authentication_error","code":"auth_unavailable","message":"access-secret expired for a@example.com (account-1)"}}`}}
	result := adapter.Execute(t.Context(), validSpec(t, row, keyService))
	if result.Error == nil || result.Error.Hint != "" || result.Error.ReplaySafety != execution.ReplaySafetyUnknown {
		t.Fatalf("error evidence = %#v", result.Error)
	}
	if result.Error.Summary == "" || strings.Contains(result.Error.Summary, "access-secret") ||
		strings.Contains(result.Error.Summary, "a@example.com") || strings.Contains(result.Error.Summary, "account-1") {
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

func TestAdapterExecutesEverySupportedClientProtocolThroughCPA(t *testing.T) {
	tests := []struct {
		name       string
		protocol   protocol.Protocol
		operation  execution.Operation
		wantFormat string
		wantAPI    execution.UpstreamAPI
	}{
		{name: "OpenAI Chat", protocol: protocol.OpenAICompletions, operation: execution.OperationChatCompletion, wantFormat: "openai", wantAPI: execution.UpstreamAPIOpenAIResponses},
		{name: "OpenAI Responses", protocol: protocol.OpenAIResponses, operation: execution.OperationResponsesCreate, wantFormat: "openai-response", wantAPI: execution.UpstreamAPIOpenAIResponses},
		{name: "Anthropic Messages", protocol: protocol.Anthropic, operation: execution.OperationChatCompletion, wantFormat: "claude", wantAPI: execution.UpstreamAPIOpenAIResponses},
		{name: "Gemini GenerateContent", protocol: protocol.Gemini, operation: execution.OperationChatCompletion, wantFormat: "gemini", wantAPI: execution.UpstreamAPIOpenAIResponses},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			fake := &fakeExecutor{result: cpaembedded.ExecuteResponse{
				Payload: []byte(`{"model":"gpt-5"}`),
				Headers: http.Header{
					"Content-Type":     {"text/event-stream"},
					"Content-Encoding": {"gzip"},
					"Content-Length":   {"999"},
					"ETag":             {`"upstream"`},
					"Digest":           {"sha-256=upstream"},
					"Content-MD5":      {"upstream"},
					"Content-Range":    {"bytes 0-998/999"},
					"Content-Digest":   {"sha-256=:dXBzdHJlYW0=:"},
					"Repr-Digest":      {"sha-256=:dXBzdHJlYW0=:"},
					"Signature":        {"sig1=:dXBzdHJlYW0=:"},
					"Signature-Input":  {`sig1=("content-digest")`},
					"X-Request-Id":     {"upstream-1"},
				},
			}}
			adapter.executor = fake
			spec := validSpec(t, row, keyService)
			spec.ClientProtocol = test.protocol
			spec.Operation = test.operation
			if test.protocol != protocol.OpenAIResponses {
				spec.RouteMode = execution.RouteConverted
			}

			result := adapter.Execute(t.Context(), spec)
			if result.Error != nil || fake.calls != 1 || fake.request.Format != test.wantFormat || result.UpstreamAPI != test.wantAPI {
				t.Fatalf("result=%#v calls=%d request=%#v", result, fake.calls, fake.request)
			}
			if got := result.Header.Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", got)
			}
			for _, name := range []string{"Content-Encoding", "Content-Length", "ETag", "Digest", "Content-MD5", "Content-Range", "Content-Digest", "Repr-Digest", "Signature", "Signature-Input"} {
				if value := result.Header.Get(name); value != "" {
					t.Fatalf("stale %s = %q", name, value)
				}
			}
			if result.Header.Get("X-Request-Id") != "upstream-1" {
				t.Fatalf("request ID header = %q", result.Header.Get("X-Request-Id"))
			}
		})
	}
}

func TestAdapterStreamsEverySupportedClientProtocolThroughCPA(t *testing.T) {
	tests := []struct {
		name       string
		protocol   protocol.Protocol
		operation  execution.Operation
		wantFormat string
		wantAPI    execution.UpstreamAPI
		payload    string
		wantData   []string
	}{
		{
			name: "OpenAI Chat", protocol: protocol.OpenAICompletions, operation: execution.OperationChatCompletion,
			wantFormat: "openai", wantAPI: execution.UpstreamAPIOpenAIResponses,
			payload:  `{"id":"chatcmpl_1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			wantData: []string{`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n", "data: [DONE]\n\n"},
		},
		{
			name: "OpenAI Responses", protocol: protocol.OpenAIResponses, operation: execution.OperationResponsesCreate,
			wantFormat: "openai-response", wantAPI: execution.UpstreamAPIOpenAIResponses,
			payload:  `data: {"type":"response.completed","response":{"id":"resp_1"}}`,
			wantData: []string{`data: {"type":"response.completed","response":{"id":"resp_1"}}` + "\n\n"},
		},
		{
			name: "Anthropic Messages", protocol: protocol.Anthropic, operation: execution.OperationChatCompletion,
			wantFormat: "claude", wantAPI: execution.UpstreamAPIOpenAIResponses,
			payload:  "event: message_stop\ndata: {\"type\":\"message_stop\"}",
			wantData: []string{"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
		},
		{
			name: "Gemini GenerateContent", protocol: protocol.Gemini, operation: execution.OperationChatCompletion,
			wantFormat: "gemini", wantAPI: execution.UpstreamAPIOpenAIResponses,
			payload: `{"candidates":[{"content":{"role":"model","parts":[]}}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`,
			wantData: []string{
				`data: {"candidates":[{"content":{"role":"model","parts":[]}}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}` + "\n\n",
				`data: {"candidates":[{"finishReason":"STOP"}]}` + "\n\n",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			chunks := make(chan cpaembedded.ExecuteStreamChunk, 1)
			chunks <- cpaembedded.ExecuteStreamChunk{Payload: []byte(test.payload)}
			close(chunks)
			fake := &fakeExecutor{stream: &cpaembedded.ExecuteStreamResponse{Chunks: chunks}}
			adapter.executor = fake
			spec := validSpec(t, row, keyService)
			spec.ClientProtocol = test.protocol
			spec.Operation = test.operation
			if test.protocol != protocol.OpenAIResponses {
				spec.RouteMode = execution.RouteConverted
			}

			var data []string
			result := adapter.ExecuteStream(t.Context(), spec, func(event execution.StreamEvent) error {
				if event.Kind == execution.StreamEventData {
					data = append(data, string(event.Data))
				}
				return nil
			})
			if result.Error != nil || fake.calls != 1 || fake.request.Format != test.wantFormat || result.UpstreamAPI != test.wantAPI {
				t.Fatalf("result=%#v calls=%d request=%#v", result, fake.calls, fake.request)
			}
			if strings.Join(data, "") != strings.Join(test.wantData, "") {
				t.Fatalf("stream data = %q, want %q", data, test.wantData)
			}
		})
	}
}

func TestAdapterDoesNotCompleteStreamAfterContextCancellation(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	chunks := make(chan cpaembedded.ExecuteStreamChunk)
	close(chunks)
	adapter.executor = &fakeExecutor{stream: &cpaembedded.ExecuteStreamResponse{Chunks: chunks}}
	spec := validSpec(t, row, keyService)
	spec.ClientProtocol = protocol.OpenAICompletions
	spec.Operation = execution.OperationChatCompletion
	spec.RouteMode = execution.RouteConverted
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var dataEvents int
	result := adapter.ExecuteStream(ctx, spec, func(event execution.StreamEvent) error {
		if event.Kind == execution.StreamEventData {
			dataEvents++
		}
		return nil
	})
	if result.Error == nil || dataEvents != 0 {
		t.Fatalf("result = %#v, data events = %d", result, dataEvents)
	}
}

func TestAdapterUsesFirstByteTimeoutBeforeFirstData(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	chunks := make(chan cpaembedded.ExecuteStreamChunk, 1)
	adapter.executor = &fakeExecutor{stream: &cpaembedded.ExecuteStreamResponse{Chunks: chunks}}
	spec := validSpec(t, row, keyService)
	spec.Timeouts.FirstByte = 20 * time.Millisecond
	spec.Timeouts.StreamIdle = 250 * time.Millisecond
	spec.Timeouts.Request = time.Second

	started := time.Now()
	var events []execution.StreamEvent
	result := adapter.ExecuteStream(t.Context(), spec, func(event execution.StreamEvent) error {
		events = append(events, event.Clone())
		return nil
	})
	if elapsed := time.Since(started); elapsed >= 150*time.Millisecond {
		t.Fatalf("first-byte timeout took %s", elapsed)
	}
	if result.Error == nil || result.Error.Kind != execution.ErrorKindTimeout || result.ResponseStarted || result.StatusCode != 0 || len(events) != 0 {
		t.Fatalf("result = %#v, events = %#v", result, events)
	}
}

func TestAdapterSwitchesToStreamIdleTimeoutAfterFirstData(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	chunks := make(chan cpaembedded.ExecuteStreamChunk, 1)
	chunks <- cpaembedded.ExecuteStreamChunk{Payload: []byte(`data: {"type":"response.created","response":{"id":"resp_1"}}`)}
	adapter.executor = &fakeExecutor{stream: &cpaembedded.ExecuteStreamResponse{Chunks: chunks}}
	spec := validSpec(t, row, keyService)
	spec.Timeouts.FirstByte = 200 * time.Millisecond
	spec.Timeouts.StreamIdle = 20 * time.Millisecond
	spec.Timeouts.Request = time.Second

	var events []execution.StreamEvent
	result := adapter.ExecuteStream(t.Context(), spec, func(event execution.StreamEvent) error {
		events = append(events, event.Clone())
		return nil
	})
	if result.Error == nil || result.Error.Kind != execution.ErrorKindTimeout || !result.ResponseStarted || result.StatusCode != http.StatusOK {
		t.Fatalf("result = %#v", result)
	}
	if len(events) != 2 || events[0].Kind != execution.StreamEventReady || events[1].Kind != execution.StreamEventData {
		t.Fatalf("events = %#v", events)
	}
}

func TestAdapterReturnsFirstStreamErrorBeforeReady(t *testing.T) {
	adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
	chunks := make(chan cpaembedded.ExecuteStreamChunk, 1)
	chunks <- cpaembedded.ExecuteStreamChunk{Err: statusError{status: http.StatusBadRequest, message: `{"error":{"type":"invalid_request_error","code":"bad_request"}}`}}
	close(chunks)
	adapter.executor = &fakeExecutor{stream: &cpaembedded.ExecuteStreamResponse{
		Headers: http.Header{"Content-Type": {"text/event-stream"}}, Chunks: chunks,
	}}

	var events []execution.StreamEvent
	result := adapter.ExecuteStream(t.Context(), validSpec(t, row, keyService), func(event execution.StreamEvent) error {
		events = append(events, event.Clone())
		return nil
	})
	if result.Error == nil || result.StatusCode != http.StatusBadRequest || len(events) != 0 {
		t.Fatalf("result = %#v, events = %#v", result, events)
	}
}

func TestAdapterRewritesStreamModelAliasForEveryProtocol(t *testing.T) {
	tests := []struct {
		name      string
		protocol  protocol.Protocol
		operation execution.Operation
		payload   string
	}{
		{name: "OpenAI Chat", protocol: protocol.OpenAICompletions, operation: execution.OperationChatCompletion, payload: `{"model":"upstream-model","choices":[{"finish_reason":"stop"}]}`},
		{name: "OpenAI Responses", protocol: protocol.OpenAIResponses, operation: execution.OperationResponsesCreate, payload: `data: {"type":"response.completed","response":{"model":"upstream-model"}}`},
		{name: "Anthropic", protocol: protocol.Anthropic, operation: execution.OperationChatCompletion, payload: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"upstream-model\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}"},
		{name: "Gemini", protocol: protocol.Gemini, operation: execution.OperationChatCompletion, payload: `{"modelVersion":"upstream-model","candidates":[{"finishReason":"STOP"}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			chunks := make(chan cpaembedded.ExecuteStreamChunk, 1)
			chunks <- cpaembedded.ExecuteStreamChunk{Payload: []byte(test.payload)}
			close(chunks)
			adapter.executor = &fakeExecutor{stream: &cpaembedded.ExecuteStreamResponse{Chunks: chunks}}
			spec := validSpec(t, row, keyService)
			spec.ClientProtocol = test.protocol
			spec.Operation = test.operation
			spec.ClientModel = "public-model"
			spec.UpstreamModel = "upstream-model"
			if test.protocol != protocol.OpenAIResponses {
				spec.RouteMode = execution.RouteConverted
			}

			var wire strings.Builder
			result := adapter.ExecuteStream(t.Context(), spec, func(event execution.StreamEvent) error {
				if event.Kind == execution.StreamEventData {
					_, _ = wire.Write(event.Data)
				}
				return nil
			})
			if result.Error != nil || !strings.Contains(wire.String(), "public-model") || strings.Contains(wire.String(), "upstream-model") {
				t.Fatalf("result = %#v, wire = %q", result, wire.String())
			}
		})
	}
}

func TestAdapterReportsFinalCPAResponsesReasoning(t *testing.T) {
	tests := []struct {
		name       string
		stream     bool
		model      string
		body       string
		wantEffort string
	}{
		{
			name:       "adaptive effort",
			model:      "gpt-5.6-sol",
			body:       `{"model":"gpt-5.6-sol","max_tokens":64,"messages":[{"role":"user","content":"hello"}],"thinking":{"type":"adaptive"},"output_config":{"effort":"max"}}`,
			wantEffort: "max",
		},
		{
			name:       "budget effort stream",
			stream:     true,
			model:      "gpt-5.6-luna",
			body:       `{"model":"gpt-5.6-luna","max_tokens":16000,"messages":[{"role":"user","content":"hello"}],"thinking":{"type":"enabled","budget_tokens":14848},"stream":true}`,
			wantEffort: "high",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			wireEffort := make(chan string, 1)
			transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				if got := request.URL.String(); got != "https://chatgpt.com/backend-api/codex/responses" {
					t.Errorf("upstream URL = %q", got)
				}
				var payload struct {
					Reasoning struct {
						Effort string `json:"effort"`
					} `json:"reasoning"`
				}
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Errorf("decode upstream request: %v", err)
				}
				wireEffort <- payload.Reasoning.Effort
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     http.Header{"Content-Type": {"text/event-stream"}},
					Body: io.NopCloser(strings.NewReader(
						`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","model":"` + test.model + `","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}` + "\n\n",
					)),
					Request: request,
				}, nil
			})
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
			spec := validSpec(t, row, keyService)
			spec.ClientProtocol = protocol.Anthropic
			spec.Operation = execution.OperationChatCompletion
			spec.RouteMode = execution.RouteConverted
			spec.UpstreamModel = test.model
			spec.Body = []byte(test.body)

			var resultReasoning string
			var upstreamAPI execution.UpstreamAPI
			if test.stream {
				result := adapter.ExecuteStream(ctx, spec, func(execution.StreamEvent) error { return nil })
				if result.Error != nil {
					t.Fatalf("ExecuteStream() error = %#v", result.Error)
				}
				upstreamAPI = result.UpstreamAPI
				if result.AppliedReasoning != nil {
					resultReasoning = result.AppliedReasoning.Effort
				}
			} else {
				result := adapter.Execute(ctx, spec)
				if result.Error != nil {
					t.Fatalf("Execute() error = %#v", result.Error)
				}
				upstreamAPI = result.UpstreamAPI
				if result.AppliedReasoning != nil {
					resultReasoning = result.AppliedReasoning.Effort
				}
			}
			if got := <-wireEffort; got != test.wantEffort {
				t.Fatalf("wire reasoning effort = %q, want %q", got, test.wantEffort)
			}
			if upstreamAPI != execution.UpstreamAPIOpenAIResponses {
				t.Fatalf("upstream API = %q, want %q", upstreamAPI, execution.UpstreamAPIOpenAIResponses)
			}
			if resultReasoning != test.wantEffort {
				t.Fatalf("applied reasoning effort = %q, want %q", resultReasoning, test.wantEffort)
			}
		})
	}
}

func TestAdapterStreamsRealCPAResponsesAsCompleteClientSSE(t *testing.T) {
	tests := []struct {
		name         string
		protocol     protocol.Protocol
		body         string
		wantFragment string
		wantTerminal string
	}{
		{
			name: "OpenAI Chat", protocol: protocol.OpenAICompletions,
			body:         `{"model":"gpt-5.6-luna","messages":[{"role":"user","content":"hello"}],"stream":true}`,
			wantFragment: `"object":"chat.completion.chunk"`,
			wantTerminal: "data: [DONE]\n\n",
		},
		{
			name: "Gemini", protocol: protocol.Gemini,
			body:         `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
			wantFragment: `"text":"hello"`,
			wantTerminal: `data: {"candidates":[{"finishReason":"STOP"}]}` + "\n\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, _, _, keyService, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
			transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				if got := request.URL.String(); got != "https://chatgpt.com/backend-api/codex/responses" {
					t.Errorf("upstream URL = %q", got)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Header:     http.Header{"Content-Type": {"text/event-stream"}},
					Body: io.NopCloser(strings.NewReader(strings.Join([]string{
						`data: {"type":"response.created","response":{"id":"resp_1","object":"response","status":"in_progress","created_at":1,"model":"gpt-5.6-luna","output":[]}}`,
						`data: {"type":"response.output_text.delta","response_id":"resp_1","output_index":0,"item_id":"msg_1","content_index":0,"delta":"hello"}`,
						`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","model":"gpt-5.6-luna","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
					}, "\n\n") + "\n\n")),
					Request: request,
				}, nil
			})
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
			spec := validSpec(t, row, keyService)
			spec.ClientProtocol = test.protocol
			spec.Operation = execution.OperationChatCompletion
			spec.RouteMode = execution.RouteConverted
			spec.UpstreamModel = "gpt-5.6-luna"
			spec.ClientModel = "gpt-5.6-luna"
			spec.Body = []byte(test.body)

			var wire strings.Builder
			result := adapter.ExecuteStream(ctx, spec, func(event execution.StreamEvent) error {
				if event.Kind == execution.StreamEventData {
					if !bytes.HasPrefix(event.Data, []byte("data:")) {
						t.Errorf("unframed client event = %q", event.Data)
					}
					_, _ = wire.Write(event.Data)
				}
				return nil
			})
			if result.Error != nil || result.UpstreamAPI != execution.UpstreamAPIOpenAIResponses {
				t.Fatalf("result = %#v", result)
			}
			if !strings.Contains(wire.String(), test.wantFragment) || !strings.HasSuffix(wire.String(), test.wantTerminal) {
				t.Fatalf("client wire = %q", wire.String())
			}
		})
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
	group := models.Group{Name: "subscription", ChannelID: "codex", ConnectionType: models.ConnectionTypeSubscription, Params: models.JSON(`{}`), Models: models.JSON(`[]`), Overrides: models.JSON(`{}`), Enabled: true}
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
	return execution.NewAttemptSpec(execution.AttemptSpec{RequestID: "request-1", AttemptID: "attempt-1", Sequence: 1, ChannelID: "codex", ConnectionType: "subscription", TargetKind: "openai", RouteMode: execution.RouteNative, ClientProtocol: protocol.OpenAIResponses, Operation: execution.OperationResponsesCreate, ClientModel: "gpt-5", UpstreamModel: "gpt-5", Method: http.MethodPost, Path: "/v1/responses", Body: []byte(`{"model":"gpt-5","input":"hi"}`), Credential: execution.NewCredentialSnapshot(row.ID, row.SecretVersion, 1, []byte(plaintext))})
}
