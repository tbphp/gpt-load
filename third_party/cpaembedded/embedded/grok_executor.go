package embedded

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	internalexecutor "github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/sjson"
)

const grokCPAProvider = "xai"

type GrokExecutionError struct {
	status     int
	code       string
	summary    string
	retryAfter time.Duration
}

func (err *GrokExecutionError) Error() string {
	if err == nil || err.summary == "" {
		return "Grok upstream request failed"
	}
	return err.summary
}

func (err *GrokExecutionError) StatusCode() int {
	if err == nil {
		return 0
	}
	return err.status
}

func (err *GrokExecutionError) ErrorCode() string {
	if err == nil {
		return ""
	}
	return err.code
}

func (err *GrokExecutionError) RetryAfter() *time.Duration {
	if err == nil || err.retryAfter <= 0 {
		return nil
	}
	value := err.retryAfter
	return &value
}

type GrokHTTPExecutor interface {
	ExecuteCanonical(context.Context, string, GrokCredential, ExecuteRequest) (ExecuteResponse, error)
	CountTokensCanonical(context.Context, ExecuteRequest) (ExecuteResponse, error)
	ExecuteStreamCanonical(context.Context, string, GrokCredential, ExecuteRequest) (*ExecuteStreamResponse, error)
}

type grokHTTPExecutor struct {
	cfg     *internalconfig.Config
	inner   *internalexecutor.XAIExecutor
	baseURL string
}

func NewGrokHTTPExecutor() GrokHTTPExecutor {
	cfg := &internalconfig.Config{}
	return &grokHTTPExecutor{cfg: cfg, inner: internalexecutor.NewXAIExecutor(cfg)}
}

func NewGrokAuth(id string, credential GrokCredential, baseURL string) *cliproxyauth.Auth {
	metadata := map[string]any{
		"type": ProviderGrok, "access_token": credential.AccessToken,
		"auth_kind": "oauth", "using_api": false,
	}
	attributes := map[string]string{
		"auth_kind": "oauth", "using_api": "false",
	}
	if value := strings.TrimSpace(baseURL); value != "" {
		attributes["base_url"] = value
	}
	return &cliproxyauth.Auth{
		ID: strings.TrimSpace(id), Provider: grokCPAProvider, Attributes: attributes, Metadata: metadata,
	}
}

func (executor *grokHTTPExecutor) ExecuteCanonical(
	ctx context.Context,
	credentialID string,
	credential GrokCredential,
	request ExecuteRequest,
) (ExecuteResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateGrokCredential(credential); err != nil {
		return ExecuteResponse{}, err
	}
	request = prepareGrokExecutionRequest(request)
	format := sdktranslator.FromString(request.Format)
	auth := NewGrokAuth(credentialID, credential, executor.baseURL)
	observation := newProviderExecutionObservation(request, grokCPAProvider)
	response, err := executor.inner.Execute(executor.executionContext(ctx, auth, observation), auth, cliproxyexecutor.Request{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: format,
	}, grokExecutorOptions(request, format, false))
	if err != nil {
		return ExecuteResponse{AppliedReasoningEffort: observation.reasoningEffort()}, normalizeGrokExecutionError(err)
	}
	return ExecuteResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
		AppliedReasoningEffort: observation.reasoningEffort(),
	}, nil
}

func (executor *grokHTTPExecutor) CountTokensCanonical(
	ctx context.Context,
	request ExecuteRequest,
) (ExecuteResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	request = prepareGrokExecutionRequest(request)
	format := sdktranslator.FromString(request.Format)
	response, err := executor.inner.CountTokens(ctx, NewGrokAuth("local-token-count", GrokCredential{}, executor.baseURL), cliproxyexecutor.Request{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: format,
	}, grokExecutorOptions(request, format, false))
	if err != nil {
		return ExecuteResponse{}, normalizeGrokExecutionError(err)
	}
	payload := append([]byte(nil), response.Payload...)
	if format == sdktranslator.FormatOpenAIResponse {
		payload, err = normalizeCodexResponsesTokenCount(payload)
		if err != nil {
			return ExecuteResponse{}, err
		}
	}
	return ExecuteResponse{Payload: payload, Headers: response.Headers.Clone()}, nil
}

func (executor *grokHTTPExecutor) ExecuteStreamCanonical(
	ctx context.Context,
	credentialID string,
	credential GrokCredential,
	request ExecuteRequest,
) (*ExecuteStreamResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateGrokCredential(credential); err != nil {
		return nil, err
	}
	request = prepareGrokExecutionRequest(request)
	format := sdktranslator.FromString(request.Format)
	auth := NewGrokAuth(credentialID, credential, executor.baseURL)
	observation := newProviderExecutionObservation(request, grokCPAProvider)
	response, err := executor.inner.ExecuteStream(executor.executionContext(ctx, auth, observation), auth, cliproxyexecutor.Request{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: format,
	}, grokExecutorOptions(request, format, true))
	if err != nil {
		return &ExecuteStreamResponse{AppliedReasoningEffort: observation.reasoningEffort()}, normalizeGrokExecutionError(err)
	}
	chunks := make(chan ExecuteStreamChunk)
	go func() {
		defer close(chunks)
		for chunk := range response.Chunks {
			converted := ExecuteStreamChunk{Payload: append([]byte(nil), chunk.Payload...)}
			if chunk.Err != nil {
				converted.Err = normalizeGrokExecutionError(chunk.Err)
			}
			select {
			case chunks <- converted:
			case <-ctx.Done():
				return
			}
		}
	}()
	return &ExecuteStreamResponse{
		Headers: response.Headers.Clone(), Chunks: chunks,
		AppliedReasoningEffort: observation.reasoningEffort(),
	}, nil
}

func grokExecutorOptions(request ExecuteRequest, format sdktranslator.Format, stream bool) cliproxyexecutor.Options {
	options := cliproxyexecutor.Options{
		Stream: stream, Headers: request.Headers.Clone(),
		OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		SourceFormat:    format, ResponseFormat: format,
	}
	if scope := strings.TrimSpace(request.ContinuityKey); scope != "" {
		options.Metadata = map[string]any{
			cliproxyexecutor.ExecutionSessionMetadataKey: grokConversationID(scope),
		}
	}
	return options
}

func grokConversationID(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return ""
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("gpt-load-grok\x00"+scope)).String()
}

func prepareGrokExecutionRequest(request ExecuteRequest) ExecuteRequest {
	if strings.TrimSpace(request.ContinuityKey) == "" && strings.TrimSpace(request.AttemptID) != "" {
		request.ContinuityKey = "gpt-load-grok-attempt\x00" + strings.TrimSpace(request.AttemptID)
	}
	request.Payload = stripGrokCallerContinuity(request.Payload)
	request.OriginalRequest = stripGrokCallerContinuity(request.OriginalRequest)
	request.Headers = request.Headers.Clone()
	for name := range request.Headers {
		if strings.EqualFold(name, "Session-Id") || strings.EqualFold(name, "X-Grok-Conv-Id") {
			delete(request.Headers, name)
		}
	}
	return request
}

func stripGrokCallerContinuity(raw []byte) []byte {
	result := append([]byte(nil), raw...)
	for _, path := range []string{
		"session_id", "sessionId", "prompt_cache_key", "metadata.session_id", "metadata.sessionId",
	} {
		updated, err := sjson.DeleteBytes(result, path)
		if err == nil {
			result = updated
		}
	}
	return result
}

func (executor *grokHTTPExecutor) executionContext(
	ctx context.Context,
	auth *cliproxyauth.Auth,
	observation *executionObservation,
) context.Context {
	baseClient := helps.NewProxyAwareHTTPClient(ctx, executor.cfg, auth, 0)
	transport := baseClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	return context.WithValue(ctx, "cliproxy.roundtripper", noRedirectRoundTripper{
		base: transport, observation: observation,
	})
}

func normalizeGrokExecutionError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var statusError interface {
		error
		StatusCode() int
	}
	if !errors.As(err, &statusError) || statusError == nil || statusError.StatusCode() == 0 {
		return err
	}
	status := statusError.StatusCode()
	retryAfter := time.Duration(0)
	var retry interface{ RetryAfter() *time.Duration }
	if errors.As(err, &retry) && retry != nil {
		if value := retry.RetryAfter(); value != nil && *value > 0 {
			retryAfter = *value
		}
	}
	code := boundedGrokExecutionCode([]byte(statusError.Error()))
	return &GrokExecutionError{
		status: status, code: code, retryAfter: retryAfter,
		summary: grokExecutionSummary(status, code),
	}
}

func boundedGrokExecutionCode(raw []byte) string {
	var payload struct {
		Code  string `json:"code"`
		Error any    `json:"error"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return ""
	}
	code := strings.TrimSpace(payload.Code)
	if code == "" {
		if object, ok := payload.Error.(map[string]any); ok {
			code, _ = object["code"].(string)
		}
	}
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 128 || strings.ContainsAny(code, "\r\n\x00") {
		return ""
	}
	return code
}

func grokExecutionSummary(status int, code string) string {
	if strings.Contains(strings.ToLower(code), "free-usage-exhausted") {
		return "Grok included free usage is exhausted."
	}
	return fmt.Sprintf("Grok upstream returned status %d", status)
}

var _ GrokHTTPExecutor = (*grokHTTPExecutor)(nil)
