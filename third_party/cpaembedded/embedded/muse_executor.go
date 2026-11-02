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
	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	internalexecutor "github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

const museCPAProvider = "meta"

type MuseExecutionError struct {
	status           int
	code             string
	summary          string
	retryAfter       time.Duration
	credentialScoped bool
	requestScoped    bool
}

func (err *MuseExecutionError) Error() string {
	if err == nil || err.summary == "" {
		return "Muse upstream request failed"
	}
	return err.summary
}

func (err *MuseExecutionError) StatusCode() int {
	if err == nil {
		return 0
	}
	return err.status
}

func (err *MuseExecutionError) ErrorCode() string {
	if err == nil {
		return ""
	}
	return err.code
}

func (err *MuseExecutionError) IsCredentialScoped() bool { return err != nil && err.credentialScoped }
func (err *MuseExecutionError) IsRequestScoped() bool    { return err != nil && err.requestScoped }

func (err *MuseExecutionError) RetryAfter() *time.Duration {
	if err == nil || err.retryAfter <= 0 {
		return nil
	}
	value := err.retryAfter
	return &value
}

type MuseHTTPExecutor interface {
	ExecuteCanonical(context.Context, string, MuseCredential, ExecuteRequest) (ExecuteResponse, error)
	CountTokensCanonical(context.Context, ExecuteRequest) (ExecuteResponse, error)
	ExecuteStreamCanonical(context.Context, string, MuseCredential, ExecuteRequest) (*ExecuteStreamResponse, error)
}

type museHTTPExecutor struct {
	cfg     *internalconfig.Config
	inner   *internalexecutor.MetaExecutor
	baseURL string
}

func NewMuseHTTPExecutor() MuseHTTPExecutor {
	cfg := &internalconfig.Config{}
	return &museHTTPExecutor{cfg: cfg, inner: internalexecutor.NewMetaExecutor(cfg)}
}

func (executor *museHTTPExecutor) executionBaseURL(apiRoot string) (string, error) {
	if strings.TrimSpace(apiRoot) == "" && executor.baseURL != "" {
		return executor.baseURL, nil
	}
	return ResolveAPIEndpoint(apiRoot, "https://api.meta.ai/v1")
}

// NewMuseAuth 不提供 DCA 令牌或文件存储，密钥兑换只能由 GPT-Load 管理。
func NewMuseAuth(id string, credential MuseCredential, baseURL string) *cliproxyauth.Auth {
	return &cliproxyauth.Auth{ID: strings.TrimSpace(id), Provider: museCPAProvider,
		Attributes: map[string]string{"api_key": credential.AccessToken, "base_url": baseURL,
			"header:X-Tbh-Contributor-Mode": "0"},
	}
}

func (executor *museHTTPExecutor) ExecuteCanonical(
	ctx context.Context,
	credentialID string,
	credential MuseCredential,
	request ExecuteRequest,
) (ExecuteResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateMuseCredential(credential); err != nil {
		return ExecuteResponse{}, err
	}
	baseURL, err := executor.executionBaseURL(request.BaseURL)
	if err != nil {
		return ExecuteResponse{}, err
	}
	request = scopeMuseExecutionRequest(request, baseURL)
	format := sdktranslator.FromString(request.Format)
	auth := museAuthForRequest(credentialID, credential, baseURL, request)
	auth.ProxyURL = request.ProxyURL
	observation := newProviderExecutionObservation(request, museCPAProvider)
	executionCtx := executor.executionContext(ctx, auth, observation, request)
	response, err := executor.inner.Execute(executionCtx, authWithoutProxyURL(auth), cliproxyexecutor.Request{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: format,
	}, museExecutorOptions(request, format, false))
	if err != nil {
		return ExecuteResponse{Headers: observation.responseHeaders(), AppliedReasoningEffort: observation.reasoningEffort(),
			AppliedReasoningMode:         observation.reasoningMode(),
			AppliedReasoningBudgetTokens: observation.reasoningBudgetTokens()}, normalizeMuseExecutionError(err)
	}
	return ExecuteResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
		AppliedReasoningEffort:       observation.reasoningEffort(),
		AppliedReasoningMode:         observation.reasoningMode(),
		AppliedReasoningBudgetTokens: observation.reasoningBudgetTokens(),
	}, nil
}

func (executor *museHTTPExecutor) CountTokensCanonical(
	ctx context.Context,
	request ExecuteRequest,
) (ExecuteResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	baseURL, err := executor.executionBaseURL(request.BaseURL)
	if err != nil {
		return ExecuteResponse{}, err
	}
	request = prepareMuseExecutionRequest(request)
	request.ContinuityKey = targetContinuityScope(request.ContinuityKey, baseURL)
	format := sdktranslator.FromString(request.Format)
	// CPA 的纯本地估算也会检查 key 非空；固定占位值没有上游授权能力。
	response, err := executor.inner.CountTokens(ctx, NewMuseAuth("local-token-count", MuseCredential{AccessToken: "local-token-count"}, baseURL), cliproxyexecutor.Request{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: format,
	}, museExecutorOptions(request, format, false))
	if err != nil {
		return ExecuteResponse{}, normalizeMuseExecutionError(err)
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

func (executor *museHTTPExecutor) ExecuteStreamCanonical(
	ctx context.Context,
	credentialID string,
	credential MuseCredential,
	request ExecuteRequest,
) (*ExecuteStreamResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateMuseCredential(credential); err != nil {
		return nil, err
	}
	baseURL, err := executor.executionBaseURL(request.BaseURL)
	if err != nil {
		return nil, err
	}
	request = scopeMuseExecutionRequest(request, baseURL)
	format := sdktranslator.FromString(request.Format)
	auth := museAuthForRequest(credentialID, credential, baseURL, request)
	auth.ProxyURL = request.ProxyURL
	observation := newProviderExecutionObservation(request, museCPAProvider)
	executionCtx := executor.executionContext(ctx, auth, observation, request)
	terminal := &museStreamTerminal{}
	executionCtx = withMuseStreamTerminal(executionCtx, terminal)
	response, err := executor.inner.ExecuteStream(executionCtx, authWithoutProxyURL(auth), cliproxyexecutor.Request{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: format,
	}, museExecutorOptions(request, format, true))
	if err != nil {
		return &ExecuteStreamResponse{Headers: observation.responseHeaders(), AppliedReasoningEffort: observation.reasoningEffort(),
			AppliedReasoningMode:         observation.reasoningMode(),
			AppliedReasoningBudgetTokens: observation.reasoningBudgetTokens()}, normalizeMuseExecutionError(err)
	}
	chunks := make(chan ExecuteStreamChunk)
	go func() {
		defer close(chunks)
		failed := false
		for chunk := range response.Chunks {
			converted := ExecuteStreamChunk{Payload: append([]byte(nil), chunk.Payload...), Incomplete: chunk.Incomplete}
			if chunk.Err != nil {
				failed = true
				converted.Err = normalizeMuseExecutionError(chunk.Err)
			}
			select {
			case chunks <- converted:
			case <-ctx.Done():
				return
			}
		}
		// SDK 读取协程关闭 Chunks 后，原始流观测已完成，才读取最终状态。
		if failed || ctx.Err() != nil {
			return
		}
		final := ExecuteStreamChunk{Incomplete: terminal.incomplete}
		if !terminal.seen || terminal.failed {
			final.Err = &MuseExecutionError{status: http.StatusBadGateway, code: "upstream_stream_incomplete", summary: "Muse upstream stream ended without a successful terminal response"}
		}
		if final.Incomplete || final.Err != nil {
			select {
			case chunks <- final:
			case <-ctx.Done():
			}
		}
	}()
	return &ExecuteStreamResponse{
		Headers: response.Headers.Clone(), Chunks: chunks,
		AppliedReasoningEffort:       observation.reasoningEffort(),
		AppliedReasoningMode:         observation.reasoningMode(),
		AppliedReasoningBudgetTokens: observation.reasoningBudgetTokens(),
	}, nil
}

func museExecutorOptions(request ExecuteRequest, format sdktranslator.Format, stream bool) cliproxyexecutor.Options {
	return cliproxyexecutor.Options{Stream: stream, Headers: request.Headers.Clone(),
		OriginalRequest: append([]byte(nil), request.OriginalRequest...), SourceFormat: format, ResponseFormat: format}
}

func scopeMuseExecutionRequest(request ExecuteRequest, baseURL string) ExecuteRequest {
	request = prepareMuseExecutionRequest(request)
	request.ContinuityKey = targetContinuityScope(request.ContinuityKey, baseURL)
	return request
}

func prepareMuseExecutionRequest(request ExecuteRequest) ExecuteRequest {
	request.Headers = request.Headers.Clone()
	if request.Headers == nil {
		request.Headers = make(http.Header)
	}
	for name := range request.Headers {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-tbh-") || lower == "x-meta-ai-gateway-session-id" {
			delete(request.Headers, name)
		}
	}
	return request
}

func museAuthForRequest(id string, credential MuseCredential, baseURL string, request ExecuteRequest) *cliproxyauth.Auth {
	auth := NewMuseAuth(id, credential, baseURL)
	scope := request.ContinuityKey
	if scope == "" {
		scope = request.AttemptID
	}
	if scope != "" {
		sessionID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("gpt-load-muse\x00"+id+"\x00"+baseURL+"\x00"+scope)).String()
		for _, name := range []string{"X-Tbh-Session-Id", "X-Meta-Ai-Gateway-Session-Id", "X-Tbh-Thread-Id", "X-Tbh-Root-Session-Id"} {
			auth.Attributes["header:"+name] = sessionID
		}
	}
	return auth
}

func (executor *museHTTPExecutor) executionContext(
	ctx context.Context,
	auth *cliproxyauth.Auth,
	observation *executionObservation,
	request ExecuteRequest,
) context.Context {
	transport := executionRoundTripper(ctx, executor.cfg, auth, request.ProxyFromEnvironment)
	transport = museRoundTripper{base: transport, source: request.Headers.Clone(), configured: append([]string(nil), request.ConfiguredHeaders...)}
	return context.WithValue(ctx, "cliproxy.roundtripper", noRedirectRoundTripper{
		base: transport, observation: observation,
	})
}

// 分组规则在 SDK 补齐默认头之后生效；认证及账号会话仍由渠道持有。
type museRoundTripper struct {
	base       http.RoundTripper
	source     http.Header
	configured []string
}

func (transport museRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	for _, name := range transport.configured {
		lower := strings.ToLower(name)
		if lower == "authorization" || lower == "host" || lower == "content-length" || strings.HasPrefix(lower, "x-tbh-") || lower == "x-meta-ai-gateway-session-id" {
			continue
		}
		request.Header.Del(name)
		for sourceName, values := range transport.source {
			if strings.EqualFold(sourceName, name) {
				request.Header[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
				break
			}
		}
	}
	request.Header.Set("X-Tbh-Contributor-Mode", "0")
	response, err := transport.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		if err := normalizeMuseResponse(response); err != nil {
			return nil, err
		}
	}
	return response, nil
}

func normalizeMuseExecutionError(err error) error {
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
	code := boundedMuseExecutionCode([]byte(statusError.Error()))
	var credentialScope interface{ IsCredentialScoped() bool }
	var requestScope interface{ IsRequestScoped() bool }
	credentialScoped := errors.As(err, &credentialScope) && credentialScope.IsCredentialScoped()
	requestScoped := errors.As(err, &requestScope) && requestScope.IsRequestScoped()
	return &MuseExecutionError{
		status: status, code: code, retryAfter: retryAfter, credentialScoped: credentialScoped, requestScoped: requestScoped,
		summary: museExecutionSummary(status, code),
	}
}

func boundedMuseExecutionCode(raw []byte) string {
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

func museExecutionSummary(status int, code string) string {
	return fmt.Sprintf("Muse upstream returned status %d", status)
}

var _ MuseHTTPExecutor = (*museHTTPExecutor)(nil)
