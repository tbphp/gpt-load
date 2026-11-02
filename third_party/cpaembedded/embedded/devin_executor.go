package embedded

import (
	"bytes"
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
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/sjson"
	"google.golang.org/protobuf/encoding/protowire"
)

const devinCPAProvider = "devin"

type DevinExecutionError struct {
	status           int
	code             string
	summary          string
	retryAfter       time.Duration
	credentialScoped bool
	requestScoped    bool
}

func (err *DevinExecutionError) Error() string {
	if err == nil || err.summary == "" {
		return "Devin upstream request failed"
	}
	return err.summary
}

func (err *DevinExecutionError) StatusCode() int {
	if err == nil {
		return 0
	}
	return err.status
}

func (err *DevinExecutionError) ErrorCode() string {
	if err == nil {
		return ""
	}
	return err.code
}

func (err *DevinExecutionError) IsCredentialScoped() bool { return err != nil && err.credentialScoped }
func (err *DevinExecutionError) IsRequestScoped() bool    { return err != nil && err.requestScoped }

func (err *DevinExecutionError) RetryAfter() *time.Duration {
	if err == nil || err.retryAfter <= 0 {
		return nil
	}
	value := err.retryAfter
	return &value
}

type DevinHTTPExecutor interface {
	ExecuteCanonical(context.Context, string, DevinCredential, ExecuteRequest) (ExecuteResponse, error)
	ExecuteStreamCanonical(context.Context, string, DevinCredential, ExecuteRequest) (*ExecuteStreamResponse, error)
}

type devinHTTPExecutor struct {
	cfg     *internalconfig.Config
	inner   *internalexecutor.DevinExecutor
	baseURL string
}

func NewDevinHTTPExecutor() DevinHTTPExecutor {
	cfg := &internalconfig.Config{}
	return &devinHTTPExecutor{cfg: cfg, inner: internalexecutor.NewDevinExecutor(cfg)}
}

func (executor *devinHTTPExecutor) executionBaseURL(apiRoot string) (string, error) {
	if strings.TrimSpace(apiRoot) == "" && executor.baseURL != "" {
		return executor.baseURL, nil
	}
	return ResolveAPIEndpoint(apiRoot, "https://server.codeium.com")
}

// NewDevinAuth 仅提供本次执行凭据；CPA 不管理持久化、刷新或调度。
func NewDevinAuth(id string, credential DevinCredential, baseURL string) *cliproxyauth.Auth {
	return &cliproxyauth.Auth{ID: strings.TrimSpace(id), Provider: devinCPAProvider,
		Attributes: map[string]string{"session_token": credential.SessionToken, "base_url": baseURL, "device_seed": credential.DeviceSeed},
	}
}

func (executor *devinHTTPExecutor) ExecuteCanonical(
	ctx context.Context,
	credentialID string,
	credential DevinCredential,
	request ExecuteRequest,
) (ExecuteResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateDevinCredential(credential); err != nil {
		return ExecuteResponse{}, err
	}
	baseURL, err := executor.executionBaseURL(request.BaseURL)
	if err != nil {
		return ExecuteResponse{}, err
	}
	request, err = scopeDevinExecutionRequest(request, credentialID, credential, baseURL)
	if err != nil {
		return ExecuteResponse{}, err
	}
	format := sdktranslator.FromString(request.Format)
	auth := NewDevinAuth(credentialID, credential, baseURL)
	auth.ProxyURL = request.ProxyURL
	observation := newProviderExecutionObservation(request, devinCPAProvider)
	executionCtx := executor.executionContext(ctx, auth, observation, request, credential.SecretValues())
	response, err := executor.inner.Execute(executionCtx, authWithoutProxyURL(auth), cliproxyexecutor.Request{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: format,
	}, devinExecutorOptions(request, format, false))
	if err != nil {
		return ExecuteResponse{Headers: observation.responseHeaders(), AppliedReasoningEffort: observation.reasoningEffort(),
			AppliedReasoningMode:         observation.reasoningMode(),
			AppliedReasoningBudgetTokens: observation.reasoningBudgetTokens()}, normalizeDevinExecutionError(err)
	}
	return ExecuteResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
		AppliedReasoningEffort:       observation.reasoningEffort(),
		AppliedReasoningMode:         observation.reasoningMode(),
		AppliedReasoningBudgetTokens: observation.reasoningBudgetTokens(),
	}, nil
}

func (executor *devinHTTPExecutor) ExecuteStreamCanonical(
	ctx context.Context,
	credentialID string,
	credential DevinCredential,
	request ExecuteRequest,
) (*ExecuteStreamResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateDevinCredential(credential); err != nil {
		return nil, err
	}
	baseURL, err := executor.executionBaseURL(request.BaseURL)
	if err != nil {
		return nil, err
	}
	request, err = scopeDevinExecutionRequest(request, credentialID, credential, baseURL)
	if err != nil {
		return nil, err
	}
	format := sdktranslator.FromString(request.Format)
	auth := NewDevinAuth(credentialID, credential, baseURL)
	auth.ProxyURL = request.ProxyURL
	observation := newProviderExecutionObservation(request, devinCPAProvider)
	executionCtx := executor.executionContext(ctx, auth, observation, request, credential.SecretValues())
	terminal := &devinStreamState{}
	executionCtx = withDevinStreamState(executionCtx, terminal)
	response, err := executor.inner.ExecuteStream(executionCtx, authWithoutProxyURL(auth), cliproxyexecutor.Request{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: format,
	}, devinExecutorOptions(request, format, true))
	if err != nil {
		return &ExecuteStreamResponse{Headers: observation.responseHeaders(), AppliedReasoningEffort: observation.reasoningEffort(),
			AppliedReasoningMode:         observation.reasoningMode(),
			AppliedReasoningBudgetTokens: observation.reasoningBudgetTokens()}, normalizeDevinExecutionError(err)
	}
	chunks := make(chan ExecuteStreamChunk)
	go func() {
		defer close(chunks)
		failed := false
		for chunk := range response.Chunks {
			converted := ExecuteStreamChunk{Payload: append([]byte(nil), chunk.Payload...), Incomplete: chunk.Incomplete}
			if chunk.Err != nil {
				failed = true
				converted.Err = normalizeDevinExecutionError(chunk.Err)
			}
			select {
			case chunks <- converted:
			case <-ctx.Done():
				return
			}
		}

		if !failed && ctx.Err() == nil {
			final := ExecuteStreamChunk{Incomplete: terminal.incomplete()}
			if terminal.invalid || !terminal.terminal {
				final.Err = &DevinExecutionError{status: 502, code: "upstream_stream_incomplete", summary: "Devin stream ended without a valid terminal frame"}
			}
			if final.Incomplete || final.Err != nil {
				select {
				case chunks <- final:
				case <-ctx.Done():
				}
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

func devinExecutorOptions(request ExecuteRequest, format sdktranslator.Format, stream bool) cliproxyexecutor.Options {
	return cliproxyexecutor.Options{Stream: stream, Headers: request.Headers.Clone(),
		OriginalRequest: append([]byte(nil), request.OriginalRequest...), SourceFormat: format, ResponseFormat: format}
}

func scopeDevinExecutionRequest(request ExecuteRequest, credentialID string, credential DevinCredential, baseURL string) (ExecuteRequest, error) {
	scope := request.ContinuityKey
	if scope == "" {
		scope = request.AttemptID
	}
	if scope == "" {
		scope = uuid.NewString()
	}
	sessionID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("gpt-load-devin\x00"+credentialID+"\x00"+credential.UserID+"\x00"+credential.OrgID+"\x00"+baseURL+"\x00"+request.Model+"\x00"+scope)).String()
	// SDK 优先读取正文与 OriginalRequest 中的 session_id，二者必须使用账号隔离后的 ID。
	var err error
	request.Payload, err = sjson.SetBytes(request.Payload, "session_id", sessionID)
	if err != nil {
		return ExecuteRequest{}, errors.New("invalid Devin request payload")
	}
	if len(request.OriginalRequest) > 0 {
		request.OriginalRequest, err = sjson.SetBytes(request.OriginalRequest, "session_id", sessionID)
		if err != nil {
			return ExecuteRequest{}, errors.New("invalid Devin original request")
		}
	} else {
		request.OriginalRequest = append([]byte(nil), request.Payload...)
	}
	return request, nil
}

func (executor *devinHTTPExecutor) executionContext(
	ctx context.Context,
	auth *cliproxyauth.Auth,
	observation *executionObservation,
	request ExecuteRequest,
	secrets []string,
) context.Context {
	transport := executionRoundTripper(ctx, executor.cfg, auth, request.ProxyFromEnvironment)
	transport = devinRoundTripper{base: transport, source: request.Headers.Clone(), configured: append([]string(nil), request.ConfiguredHeaders...), secrets: secrets}
	return context.WithValue(ctx, "cliproxy.roundtripper", noRedirectRoundTripper{
		base: transport, observation: observation,
	})
}

// 分组规则在 SDK 补齐默认头之后生效；认证及账号会话仍由渠道持有。
type devinRoundTripper struct {
	base       http.RoundTripper
	source     http.Header
	configured []string
	secrets    []string
}

func (transport devinRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	for _, name := range transport.configured {
		lower := strings.ToLower(name)
		if lower == "authorization" || lower == "host" || lower == "content-length" || lower == "content-type" || lower == "connect-protocol-version" {
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
	response, err := transport.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}

	if response.StatusCode >= 200 && response.StatusCode < 300 {
		decoded, decodeErr := helps.DecodeResponseBody(response.Body, response.Header)
		if decodeErr != nil {
			return nil, decodeErr
		}
		response.Body = &devinResponseBody{ReadCloser: decoded, secrets: transport.secrets}
		response.Header = response.Header.Clone()
		response.Header.Del("Content-Encoding")
		response.Header.Del("Content-Length")
		response.ContentLength = -1
	}
	return response, nil
}

func normalizeDevinExecutionError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var normalized *DevinExecutionError
	if errors.As(err, &normalized) {
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
	code := boundedDevinExecutionCode([]byte(statusError.Error()))
	var credentialScope interface{ IsCredentialScoped() bool }
	var requestScope interface{ IsRequestScoped() bool }
	credentialScoped := errors.As(err, &credentialScope) && credentialScope.IsCredentialScoped()
	requestScoped := errors.As(err, &requestScope) && requestScope.IsRequestScoped()
	return &DevinExecutionError{
		status: status, code: code, retryAfter: retryAfter, credentialScoped: credentialScoped, requestScoped: requestScoped,
		summary: fmt.Sprintf("Devin upstream returned status %d", status),
	}
}

func boundedDevinExecutionCode(raw []byte) string {
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
	// 对外 code 只保留已知机器标识，不传播上游任意字符串或回显的令牌。
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "unsupported_model", "model_not_found", "invalid_parameter", "invalid_request", "invalid_request_error", "context_length_exceeded", "rate_limit_exceeded", "insufficient_quota", "permission_denied", "unauthenticated", "failed_precondition", "resource_exhausted", "unavailable", "internal", "upstream_stream_incomplete":
		return strings.ToLower(strings.TrimSpace(code))
	default:
		return ""
	}
}

var _ DevinHTTPExecutor = (*devinHTTPExecutor)(nil)

// 只从实际发送的模型 UID 采样已知强度；不能解读的私有别名保持未知。
func devinWireReasoningEffort(body []byte) string {
	_, payload, err := helps.ReadConnectFrame(bytes.NewReader(body))
	if err != nil {
		return ""
	}
	model, _, err := devinProtoField(payload, 21, protowire.BytesType)
	if err != nil {
		return ""
	}
	uid := strings.ToLower(string(model))
	for _, suffix := range []string{"-priority", "-fast", "-1m", "-lightning"} {
		uid = strings.TrimSuffix(uid, suffix)
	}
	for _, effort := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"} {
		if strings.HasSuffix(uid, "-"+effort) || strings.HasSuffix(uid, "_"+effort) {
			return effort
		}
	}
	return ""
}
