package claude

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"
)

type fakeBridgeExecutionError struct {
	status           int
	typeValue        string
	codeValue        string
	retryAfter       time.Duration
	requestScoped    bool
	credentialScoped bool
}

func (err *fakeBridgeExecutionError) Error() string            { return "safe Claude error" }
func (err *fakeBridgeExecutionError) StatusCode() int          { return err.status }
func (err *fakeBridgeExecutionError) ErrorType() string        { return err.typeValue }
func (err *fakeBridgeExecutionError) ErrorCode() string        { return err.codeValue }
func (err *fakeBridgeExecutionError) IsRequestScoped() bool    { return err.requestScoped }
func (err *fakeBridgeExecutionError) IsCredentialScoped() bool { return err.credentialScoped }
func (err *fakeBridgeExecutionError) RetryAfter() *time.Duration {
	return &err.retryAfter
}

type fakeClaudeBridgeExecutor struct {
	response cpaembedded.ExecuteResponse
	err      error
}

func (executor *fakeClaudeBridgeExecutor) ExecuteCanonical(
	context.Context,
	string,
	cpaembedded.ClaudeCredential,
	cpaembedded.ExecuteRequest,
) (cpaembedded.ExecuteResponse, error) {
	return executor.response, executor.err
}

func (executor *fakeClaudeBridgeExecutor) CountTokensCanonical(
	context.Context,
	string,
	cpaembedded.ClaudeCredential,
	cpaembedded.ExecuteRequest,
) (cpaembedded.ExecuteResponse, error) {
	return executor.response, executor.err
}

func (executor *fakeClaudeBridgeExecutor) ExecuteStreamCanonical(
	context.Context,
	string,
	cpaembedded.ClaudeCredential,
	cpaembedded.ExecuteRequest,
) (*cpaembedded.ExecuteStreamResponse, error) {
	return nil, executor.err
}

func TestCredentialCanonicalizationPreservesStableAccountAndDevice(t *testing.T) {
	credential, err := ParseCredentialJSON([]byte(`{
		"type":"claude",
		"access_token":"access-secret",
		"refresh_token":"refresh-secret",
		"account_uuid":"account-one",
		"organization_uuid":"org-one",
		"email":"owner@example.com",
		"expired":"2026-08-16T09:00:00Z"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := MarshalCredential(credential)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(canonical) || credential.AccountUUID != "account-one" || len(credential.DeviceIDs) != 1 {
		t.Fatalf("credential = %#v canonical=%s", credential, canonical)
	}
	reparsed, err := ParseCredentialJSON(canonical)
	if err != nil || reparsed.DeviceIDs[0] != credential.DeviceIDs[0] {
		t.Fatalf("reparsed = %#v, %v", reparsed, err)
	}
	expiresAt, known := CredentialExpiresAt(reparsed)
	if !known || !expiresAt.Equal(time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("expires = %s, %t", expiresAt, known)
	}
	if got := credential.SecretValues(); len(got) != 7 || got[0] != "access-secret" || got[1] != "refresh-secret" ||
		got[3] != "account-one" || got[4] != "org-one" || got[5] != "owner@example.com" ||
		got[6] != credential.DeviceIDs[0] {
		t.Fatalf("secrets = %q", got)
	}
}

func TestBeginBrowserAuthorizationUsesClaudeCallback(t *testing.T) {
	authorization, err := BeginBrowserAuthorization()
	if err != nil {
		t.Fatal(err)
	}
	if authorization.State == "" || authorization.CodeVerifier == "" ||
		!strings.Contains(authorization.AuthorizationURL, "localhost%3A54545%2Fcallback") {
		t.Fatalf("authorization = %#v", authorization)
	}
}

func TestExecutorMapsResponsesAndPreservesBoundedClassification(t *testing.T) {
	credential, err := ParseCredentialJSON([]byte(`{
		"type":"claude",
		"access_token":"sk-ant-oat-access",
		"refresh_token":"refresh-secret",
		"account_uuid":"account-one",
		"expired":"2030-01-01T00:00:00Z"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	observedAt := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	bridge := &fakeClaudeBridgeExecutor{response: cpaembedded.ExecuteResponse{
		Payload: []byte(`{"ok":true}`), Headers: http.Header{"X-Request-Id": {"request-one"}},
		AppliedReasoningEffort: "high",
		QuotaSignals: cpaembedded.QuotaSignalObservation{
			ObservedAt: observedAt,
			Signals:    map[string]string{"Anthropic-Ratelimit-Unified-5h-Utilization": "0.4"},
		},
	}}
	executor := &executor{bridge: bridge}
	response, err := executor.Execute(t.Context(), "1", credential, ExecuteRequest{
		Model: "claude-sonnet-4-5", Payload: []byte(`{"messages":[]}`), Format: "claude",
	})
	if err != nil || string(response.Payload) != `{"ok":true}` ||
		response.Headers.Get("X-Request-Id") != "request-one" || response.AppliedReasoningEffort != "high" {
		t.Fatalf("response/error = %#v / %v", response, err)
	}
	if !response.QuotaObservedAt.Equal(observedAt) ||
		response.QuotaSignals["Anthropic-Ratelimit-Unified-5h-Utilization"] != "0.4" {
		t.Fatalf("response quota signals = %#v", response)
	}

	bridge.err = &fakeBridgeExecutionError{
		status: http.StatusTooManyRequests, typeValue: "rate_limit_error",
		codeValue: "fast_mode_credits", retryAfter: 17 * time.Second, requestScoped: true,
	}
	_, err = executor.Execute(t.Context(), "1", credential, ExecuteRequest{})
	var executionErr *ExecutionError
	if !errors.As(err, &executionErr) || executionErr.StatusCode() != http.StatusTooManyRequests ||
		executionErr.ErrorType() != "rate_limit_error" || executionErr.ErrorCode() != "fast_mode_credits" ||
		!executionErr.IsRequestScoped() || executionErr.RetryAfter() == nil ||
		*executionErr.RetryAfter() != 17*time.Second {
		t.Fatalf("execution error = %#v / %v", executionErr, err)
	}
	var credentialScoped interface{ IsCredentialScoped() bool }
	if !errors.As(err, &credentialScoped) || credentialScoped == nil || credentialScoped.IsCredentialScoped() {
		t.Fatalf("credential scope was not preserved: %#v / %v", credentialScoped, err)
	}
}

func TestAccountObservationPreservesIncompleteSourceMarkers(t *testing.T) {
	observed := accountObservationFromBridge(cpaembedded.ClaudeAccountObservation{
		IncompleteSources: []string{"roles", "usage"},
	})
	if strings.Join(observed.IncompleteSources, ",") != "roles,usage" {
		t.Fatalf("incomplete sources = %q", observed.IncompleteSources)
	}
}

func TestNormalizeAuthorizationErrorPreservesCompanionHTTPStatus(t *testing.T) {
	err := normalizeAuthorizationError(&cpaembedded.ClaudeUpstreamHTTPError{StatusCode: http.StatusUnauthorized})
	var upstream *UpstreamHTTPError
	if !errors.As(err, &upstream) || upstream.StatusCode != http.StatusUnauthorized {
		t.Fatalf("normalized error = %#v / %v", upstream, err)
	}
}

func TestNormalizeAuthorizationErrorPreservesTokenRetryAfter(t *testing.T) {
	err := normalizeAuthorizationError(&cpaembedded.TokenEndpointError{
		StatusCode: http.StatusTooManyRequests, Code: "rate_limit_exceeded",
		RetryAfter: 30 * time.Minute,
	})
	var tokenErr *TokenEndpointError
	if !errors.As(err, &tokenErr) || tokenErr.StatusCode != http.StatusTooManyRequests ||
		tokenErr.Code != "rate_limit_exceeded" || tokenErr.RetryAfter != 30*time.Minute {
		t.Fatalf("normalized error = %#v / %v", tokenErr, err)
	}
}

func TestExecuteRequestToBridgePreservesEnvironmentProxyPolicy(t *testing.T) {
	t.Parallel()

	request := executeRequestToBridge(ExecuteRequest{ProxyFromEnvironment: true})
	if !request.ProxyFromEnvironment || request.ProxyURL != "" {
		t.Fatalf("bridge proxy request = %#v", request)
	}
}

var _ cpaembedded.ClaudeHTTPExecutor = (*fakeClaudeBridgeExecutor)(nil)
