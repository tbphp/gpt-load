package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/state"
)

func TestModelCooldownHandlesSSERejectionAndCanceledResponse(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		result := UpstreamResult{StatusCode: 200, Header: http.Header{"Retry-After": {"3600"}},
			Body: []byte(`{"error":{"type":"rate_limit_error"}}`), ProviderErrorBeforeCommit: true,
			RequestWritten: true, DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			ExecutionError: &execution.ErrorEvidence{Kind: execution.ErrorKindProvider, Hint: execution.FailureHintRateLimited,
				ScopeHint: execution.ErrorScopeModel, StatusCode: 429, ReplaySafety: execution.ReplaySafetyRejectedBeforeProcessing}}
		forwarder := &scriptedForwarder{results: []UpstreamResult{result}}
		if canceled {
			forwarder.onCall = func(int) { cancel() }
		}
		engine, _, _, registry := newRequestLogHandlerTestRuntime(t, forwarder, &recordingAccessKeyRPMLimiter{}, &recordingRequestLogSink{}, "sk-one")
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[]}`)).WithContext(ctx)
		request.Header.Set("Authorization", "Bearer gl-client")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		cancel()
		if !canceled && response.Code != 429 {
			t.Fatalf("SSE limit became %d", response.Code)
		}
		if len(registry.ModelCooldowns(1, time.Now())) != 1 {
			t.Fatalf("canceled=%t: lost decoded quota rejection", canceled)
		}
	}
}

func TestModelCooldownRetriesAndFiltersFutureRequests(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{StatusCode: 429, Header: http.Header{"Retry-After": {"3600"}}, Body: []byte(`{"error":{"code":"rate_limit_exceeded"}}`),
			RequestWritten: true, DispatchState: execution.DispatchMaybeSent, ExecutionError: &execution.ErrorEvidence{
				Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintRateLimited, StatusCode: 429}},
		successfulAffinityResult(), successfulAffinityResult(),
	}}
	engine, _, _, registry := newRequestLogHandlerTestRuntime(t, forwarder, &recordingAccessKeyRPMLimiter{}, &recordingRequestLogSink{}, "sk-one", "sk-two")
	for range 2 {
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[]}`))
		request.Header.Set("Authorization", "Bearer gl-client")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatalf("response=%d %s", response.Code, response.Body.String())
		}
	}
	if len(forwarder.inputs) != 3 || forwarder.inputs[0].Credential.ID == forwarder.inputs[1].Credential.ID || forwarder.inputs[0].Credential.ID == forwarder.inputs[2].Credential.ID {
		t.Fatalf("unexpected candidate retry chain: %#v", forwarder.inputs)
	}
	limited := forwarder.inputs[0].Credential.ID
	if len(registry.ModelCooldowns(limited, time.Now())) != 1 {
		t.Fatal("no model cooldown")
	}
	if until, _ := registry.CredentialCooldownUntil(limited); !until.IsZero() {
		t.Fatal("limited the whole credential")
	}
}

func TestOnlyModelCooledCandidatesReturn429WithoutDispatch(t *testing.T) {
	forwarder := &scriptedForwarder{}
	engine, _, _, registry := newRequestLogHandlerTestRuntime(t, forwarder, &recordingAccessKeyRPMLimiter{}, &recordingRequestLogSink{}, "sk-one")
	now := time.Now()
	ref, _ := registry.CredentialRef(1)
	registry.SetModelCooldown(ref, "gpt-4o", now.Add(time.Hour), now)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[]}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != 429 || response.Header().Get("Retry-After") == "" || len(forwarder.inputs) != 0 {
		t.Fatalf("limited candidate response = %d %s; attempts=%d", response.Code, response.Body.String(), len(forwarder.inputs))
	}
}

func TestQuotaRecoveryAllowsGatewayRequestsAgain(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{StatusCode: 429, Header: http.Header{"Retry-After": {"172800"}}, Body: []byte(`{"error":{"type":"usage_limit_reached"}}`),
			RequestWritten: true, DispatchState: execution.DispatchMaybeSent, ExecutionError: &execution.ErrorEvidence{
				Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintRateLimited, StatusCode: 429, Type: "usage_limit_reached"}},
		successfulAffinityResult(),
	}}
	engine, _, _, registry := newRequestLogHandlerTestRuntime(t, forwarder, &recordingAccessKeyRPMLimiter{}, &recordingRequestLogSink{}, "sk-one")
	for index, want := range []int{429, 200} {
		if index == 1 {
			registry.RestoreQuotaCooldowns(1)
		}
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[]}`))
		request.Header.Set("Authorization", "Bearer gl-client")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("request %d returned %d, want %d: %s", index, response.Code, want, response.Body.String())
		}
	}
	if len(forwarder.inputs) != 2 {
		t.Fatalf("attempts = %d, want 2", len(forwarder.inputs))
	}
}

func TestModelQuotaCooldownWithoutModelDoesNotCooldownAccount(t *testing.T) {
	handler, _, registry := newHandlerForTest(t, &scriptedForwarder{})
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{ID: 1, GroupID: 1,
		Version: 1, IdentityGeneration: 1, Status: state.CredentialStatusActive,
		Fingerprint: "fixture", EncryptedValue: "fixture"}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ref, _ := registry.CredentialRef(1)
	handler.applyDecisionEffectWithBlacklistPolicy(ref, 0, health.Decision{
		Category: health.FailureCategoryRateLimited, Effect: health.EffectCooldownModel,
		CooldownUntil: now.Add(time.Hour), RuleID: "rate_limit.retry_after",
	}, 429, now, 0, "")
	view := registry.Snapshot()[0]
	if !view.CooldownUntil.IsZero() || len(view.ModelCooldowns) != 0 {
		t.Fatalf("missing model created a cooldown: %#v", view)
	}
}
