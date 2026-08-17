package cpa

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/claude"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

type fakeClaudeExecutor struct {
	response       claude.ExecuteResponse
	countResponse  claude.ExecuteResponse
	streamResponse *claude.ExecuteStreamResponse
	err            error
	requests       []claude.ExecuteRequest
	countRequests  []claude.ExecuteRequest
}

func (executor *fakeClaudeExecutor) CountTokens(
	_ context.Context,
	_ string,
	_ claude.Credential,
	request claude.ExecuteRequest,
) (claude.ExecuteResponse, error) {
	executor.countRequests = append(executor.countRequests, request)
	return executor.countResponse, executor.err
}

func (executor *fakeClaudeExecutor) Execute(
	_ context.Context,
	_ string,
	_ claude.Credential,
	request claude.ExecuteRequest,
) (claude.ExecuteResponse, error) {
	executor.requests = append(executor.requests, request)
	return executor.response, executor.err
}

func (executor *fakeClaudeExecutor) ExecuteStream(
	_ context.Context,
	_ string,
	_ claude.Credential,
	request claude.ExecuteRequest,
) (*claude.ExecuteStreamResponse, error) {
	executor.requests = append(executor.requests, request)
	return executor.streamResponse, executor.err
}

type classifiedClaudeError struct {
	status           int
	typeValue        string
	codeValue        string
	retryAfter       time.Duration
	requestScoped    bool
	credentialScoped bool
	summary          string
}

func (err *classifiedClaudeError) Error() string              { return err.summary }
func (err *classifiedClaudeError) StatusCode() int            { return err.status }
func (err *classifiedClaudeError) ErrorType() string          { return err.typeValue }
func (err *classifiedClaudeError) ErrorCode() string          { return err.codeValue }
func (err *classifiedClaudeError) IsRequestScoped() bool      { return err.requestScoped }
func (err *classifiedClaudeError) IsCredentialScoped() bool   { return err.credentialScoped }
func (err *classifiedClaudeError) RetryAfter() *time.Duration { return &err.retryAfter }

func claudeProviderCredentialForTest(t *testing.T) claudeProviderCredential {
	t.Helper()
	parsed, err := claude.ParseCredentialJSON([]byte(`{
		"type":"claude",
		"access_token":"sk-ant-oat-access",
		"refresh_token":"refresh-secret",
		"account_uuid":"account-one",
		"expired":"2030-01-01T00:00:00Z"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	return claudeProviderCredential{value: parsed}
}

func TestClaudeProviderValidatesOnlyDeclaredRoutes(t *testing.T) {
	bridge := newClaudeProviderBridge()
	valid := []channel.RouteDescriptor{
		{ClientProtocol: protocol.Anthropic, Operation: execution.OperationChatCompletion, RouteMode: execution.RouteNative},
		{ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, RouteMode: execution.RouteConverted},
		{ClientProtocol: protocol.OpenAIResponses, Operation: execution.OperationResponsesCreate, RouteMode: execution.RouteConverted},
		{ClientProtocol: protocol.Gemini, Operation: execution.OperationChatCompletion, RouteMode: execution.RouteConverted},
		{ClientProtocol: protocol.Anthropic, Operation: execution.OperationCountTokens, RouteMode: execution.RouteNative},
		{ClientProtocol: protocol.OpenAIResponses, Operation: execution.OperationResponsesInputTokens, RouteMode: execution.RouteConverted},
		{ClientProtocol: protocol.Gemini, Operation: execution.OperationCountTokens, RouteMode: execution.RouteConverted},
	}
	for _, route := range valid {
		if err := bridge.ValidateRouteCapability(route); err != nil {
			t.Fatalf("valid route %#v: %v", route, err)
		}
	}
	for _, route := range []channel.RouteDescriptor{
		{ClientProtocol: protocol.OpenAIResponses, Operation: execution.OperationResponsesCompact, RouteMode: execution.RouteConverted},
		{ClientProtocol: protocol.Anthropic, Operation: execution.OperationChatCompletion, RouteMode: execution.RouteConverted},
	} {
		if err := bridge.ValidateRouteCapability(route); err == nil {
			t.Fatalf("unsupported route accepted: %#v", route)
		}
	}
}

func TestClaudeProviderMapsExecutionAndRequestScopedErrors(t *testing.T) {
	executor := &fakeClaudeExecutor{response: claude.ExecuteResponse{
		Payload: []byte(`{"id":"message-one"}`), Headers: http.Header{"X-Request-Id": {"request-one"}},
		AppliedReasoningEffort: "high",
	}}
	bridge := &claudeProviderBridge{executor: executor}
	credential := claudeProviderCredentialForTest(t)
	response, err := bridge.Execute(t.Context(), "1", credential, providerRequest{
		Model: "claude-sonnet-4-5", Payload: []byte(`{"messages":[]}`), Format: "claude",
	})
	if err != nil || string(response.Payload) != `{"id":"message-one"}` ||
		response.Headers.Get("X-Request-Id") != "request-one" || response.AppliedReasoningEffort != "high" {
		t.Fatalf("response/error = %#v / %v", response, err)
	}

	requestRejected := &classifiedClaudeError{
		status: http.StatusTooManyRequests, typeValue: "rate_limit_error",
		codeValue: "fast_mode_credits", retryAfter: 17 * time.Second,
		requestScoped: true, summary: "Usage credits are required for Claude fast mode.",
	}
	status, evidence := bridge.ClassifyError(t.Context(), errors.Join(errors.New("outer"), requestRejected), credential)
	if status != http.StatusTooManyRequests || evidence == nil ||
		evidence.Hint != execution.FailureHintRequestRejected || evidence.Type != "rate_limit_error" ||
		evidence.Code != "fast_mode_credits" || evidence.RetryAfter != 17*time.Second {
		t.Fatalf("request-scoped evidence = %d / %#v", status, evidence)
	}

	requestRejected.requestScoped = false
	_, evidence = bridge.ClassifyError(t.Context(), requestRejected, credential)
	if evidence == nil || evidence.Hint != execution.FailureHintRequestRejected {
		t.Fatalf("model-scoped rate-limit evidence = %#v", evidence)
	}

	requestRejected.credentialScoped = true
	_, evidence = bridge.ClassifyError(t.Context(), requestRejected, credential)
	if evidence == nil || evidence.Hint != execution.FailureHintRateLimited {
		t.Fatalf("credential-scoped rate-limit evidence = %#v", evidence)
	}
}

func TestClaudeProviderRefreshesAnyUnauthorizedOAuthRequest(t *testing.T) {
	bridge := newClaudeProviderBridge()
	credential := claudeProviderCredentialForTest(t)
	for _, code := range []string{"", "auth_unavailable", "token_expired"} {
		_, evidence := bridge.ClassifyError(t.Context(), &classifiedClaudeError{
			status: http.StatusUnauthorized, typeValue: "authentication_error",
			codeValue: code, summary: "Claude authorization was rejected",
		}, credential)
		if evidence == nil || evidence.Hint != execution.FailureHintRefreshRequired ||
			evidence.ReplaySafety != execution.ReplaySafetyRejectedBeforeProcessing {
			t.Fatalf("code %q evidence = %#v", code, evidence)
		}
	}
}

func TestClaudeProviderClassifiesUnsupportedCountTokensEvidence(t *testing.T) {
	bridge := newClaudeProviderBridge()
	credential := claudeProviderCredentialForTest(t)
	for _, test := range []struct {
		status int
		code   string
	}{
		{status: http.StatusNotFound},
		{status: http.StatusMethodNotAllowed},
		{status: http.StatusNotImplemented},
		{status: http.StatusBadRequest, code: "unsupported_operation"},
	} {
		_, evidence := bridge.ClassifyError(t.Context(), &classifiedClaudeError{
			status: test.status, typeValue: "invalid_request_error", codeValue: test.code,
			summary: "count tokens unsupported",
		}, credential)
		if evidence == nil || !execution.UpstreamCountTokensUnsupported(
			execution.OperationCountTokens,
			evidence.StatusCode,
			evidence.Type,
			evidence.Code,
		) {
			t.Fatalf("unsupported evidence = %#v", evidence)
		}
	}
}

func TestNewAdapterIndexesClaudeProviderBridge(t *testing.T) {
	adapter := NewAdapter(nil, nil)
	if adapter.providers[channel.ProviderClaude] == nil || adapter.providers[channel.ProviderCodex] == nil {
		t.Fatalf("CPA providers = %#v", adapter.providers)
	}
}

var _ claude.Executor = (*fakeClaudeExecutor)(nil)
