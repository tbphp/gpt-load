package embedded

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	internalexecutor "github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

// ClaudeExecutionError is the bounded error shape allowed to cross the
// embedded boundary. It never retains the original provider response body.
type ClaudeExecutionError struct {
	status        int
	typeValue     string
	codeValue     string
	summary       string
	retryAfter    time.Duration
	requestScoped bool
}

type claudeCredentialScopedExecutionError struct {
	*ClaudeExecutionError
	credentialScoped bool
}

func (e *ClaudeExecutionError) Error() string {
	if e == nil || e.summary == "" {
		return "Claude upstream request failed"
	}
	return e.summary
}

func (e *ClaudeExecutionError) StatusCode() int {
	if e == nil {
		return 0
	}
	return e.status
}

func (e *ClaudeExecutionError) ErrorType() string {
	if e == nil {
		return ""
	}
	return e.typeValue
}

func (e *ClaudeExecutionError) ErrorCode() string {
	if e == nil {
		return ""
	}
	return e.codeValue
}

func (e *ClaudeExecutionError) RetryAfter() *time.Duration {
	if e == nil || e.retryAfter <= 0 {
		return nil
	}
	value := e.retryAfter
	return &value
}

func (e *ClaudeExecutionError) IsRequestScoped() bool {
	return e != nil && e.requestScoped
}

func (e *claudeCredentialScopedExecutionError) IsCredentialScoped() bool {
	return e != nil && e.credentialScoped
}

func (e *claudeCredentialScopedExecutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.ClaudeExecutionError
}

// ClaudeHTTPExecutor is the stateless Claude execution surface exposed to
// GPT-Load. CPA manager, retry, refresh, storage, and selection are excluded.
type ClaudeHTTPExecutor interface {
	ExecuteCanonical(context.Context, string, ClaudeCredential, ExecuteRequest) (ExecuteResponse, error)
	CountTokensCanonical(context.Context, string, ClaudeCredential, ExecuteRequest) (ExecuteResponse, error)
	ExecuteStreamCanonical(context.Context, string, ClaudeCredential, ExecuteRequest) (*ExecuteStreamResponse, error)
}

type claudeHTTPExecutor struct {
	cfg   *internalconfig.Config
	inner *internalexecutor.ClaudeExecutor
}

// NewClaudeHTTPExecutor constructs a stateless executor with no CPA manager.
func NewClaudeHTTPExecutor() ClaudeHTTPExecutor {
	cfg := &internalconfig.Config{}
	return &claudeHTTPExecutor{cfg: cfg, inner: internalexecutor.NewClaudeExecutor(cfg)}
}

// NewClaudeAuth maps a canonical credential to CPA's per-request auth value.
// Access tokens remain metadata so CPA selects OAuth Bearer semantics rather
// than the API-key header path.
func NewClaudeAuth(id string, credential ClaudeCredential, baseURL string) *cliproxyauth.Auth {
	metadata := map[string]any{
		"type":              ProviderClaude,
		"access_token":      credential.AccessToken,
		"refresh_token":     credential.RefreshToken,
		"account_uuid":      credential.AccountUUID,
		"claude_device_ids": append([]string(nil), credential.DeviceIDs...),
	}
	if credential.IDToken != "" {
		metadata["id_token"] = credential.IDToken
	}
	if credential.Email != "" {
		metadata["email"] = credential.Email
	}
	if credential.OrganizationUUID != "" {
		metadata["organization_uuid"] = credential.OrganizationUUID
	}
	if credential.OrganizationName != "" {
		metadata["organization_name"] = credential.OrganizationName
	}
	if credential.Expire != "" {
		metadata["expired"] = credential.Expire
	}
	if credential.LastRefresh != "" {
		metadata["last_refresh"] = credential.LastRefresh
	}
	attributes := map[string]string{}
	if value := strings.TrimSpace(baseURL); value != "" {
		attributes["base_url"] = value
	}
	return &cliproxyauth.Auth{
		ID:         strings.TrimSpace(id),
		Provider:   ProviderClaude,
		Attributes: attributes,
		Metadata:   metadata,
	}
}

func (e *claudeHTTPExecutor) ExecuteCanonical(
	ctx context.Context,
	credentialID string,
	credential ClaudeCredential,
	request ExecuteRequest,
) (ExecuteResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateClaudeCredential(credential); err != nil {
		return ExecuteResponse{}, err
	}
	format := sdktranslator.FromString(request.Format)
	auth := NewClaudeAuth(credentialID, credential, "")
	auth.ProxyURL = request.ProxyURL
	observation := newProviderExecutionObservation(request, ProviderClaude)
	response, err := e.inner.Execute(e.executionContext(ctx, auth, observation), auth, cliproxyexecutor.Request{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: format,
	}, cliproxyexecutor.Options{
		Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		SourceFormat: format, ResponseFormat: format,
	})
	if err != nil {
		return ExecuteResponse{AppliedReasoningEffort: observation.reasoningEffort()}, normalizeClaudeExecutionError(err)
	}
	return ExecuteResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
		AppliedReasoningEffort: observation.reasoningEffort(),
	}, nil
}

func (e *claudeHTTPExecutor) CountTokensCanonical(
	ctx context.Context,
	credentialID string,
	credential ClaudeCredential,
	request ExecuteRequest,
) (ExecuteResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateClaudeCredential(credential); err != nil {
		return ExecuteResponse{}, err
	}
	format := sdktranslator.FromString(request.Format)
	auth := NewClaudeAuth(credentialID, credential, "")
	auth.ProxyURL = request.ProxyURL
	observation := newProviderExecutionObservation(request, ProviderClaude)
	response, err := e.inner.CountTokens(e.executionContext(ctx, auth, observation), auth, cliproxyexecutor.Request{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: format,
	}, cliproxyexecutor.Options{
		Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		SourceFormat: format, ResponseFormat: format,
	})
	if err != nil {
		return ExecuteResponse{}, normalizeClaudeExecutionError(err)
	}
	return ExecuteResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
	}, nil
}

func (e *claudeHTTPExecutor) ExecuteStreamCanonical(
	ctx context.Context,
	credentialID string,
	credential ClaudeCredential,
	request ExecuteRequest,
) (*ExecuteStreamResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateClaudeCredential(credential); err != nil {
		return nil, err
	}
	format := sdktranslator.FromString(request.Format)
	auth := NewClaudeAuth(credentialID, credential, "")
	auth.ProxyURL = request.ProxyURL
	observation := newProviderExecutionObservation(request, ProviderClaude)
	response, err := e.inner.ExecuteStream(e.executionContext(ctx, auth, observation), auth, cliproxyexecutor.Request{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: format,
	}, cliproxyexecutor.Options{
		Stream: true, Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		SourceFormat: format, ResponseFormat: format,
	})
	if err != nil {
		return &ExecuteStreamResponse{AppliedReasoningEffort: observation.reasoningEffort()}, normalizeClaudeExecutionError(err)
	}
	chunks := make(chan ExecuteStreamChunk)
	go func() {
		defer close(chunks)
		for chunk := range response.Chunks {
			converted := ExecuteStreamChunk{Payload: append([]byte(nil), chunk.Payload...)}
			if chunk.Err != nil {
				converted.Err = normalizeClaudeExecutionError(chunk.Err)
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

func (e *claudeHTTPExecutor) executionContext(
	ctx context.Context,
	auth *cliproxyauth.Auth,
	observation *executionObservation,
) context.Context {
	baseClient := helps.NewUtlsHTTPClient(ctx, e.cfg, auth, 0)
	transport := baseClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	return context.WithValue(ctx, "cliproxy.roundtripper", noRedirectRoundTripper{
		base: transport, observation: observation,
	})
}

func normalizeClaudeExecutionError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	status := 0
	var statusErr interface{ StatusCode() int }
	if errors.As(err, &statusErr) && statusErr != nil {
		status = statusErr.StatusCode()
	}
	if status == 0 {
		return err
	}
	requestScoped := false
	var scoped interface{ IsRequestScoped() bool }
	if errors.As(err, &scoped) && scoped != nil {
		requestScoped = scoped.IsRequestScoped()
	}
	credentialScoped, credentialScopeKnown := false, false
	var credentialScope interface{ IsCredentialScoped() bool }
	if errors.As(err, &credentialScope) && credentialScope != nil {
		credentialScopeKnown = true
		credentialScoped = credentialScope.IsCredentialScoped()
	}
	retryAfter := time.Duration(0)
	var retry interface{ RetryAfter() *time.Duration }
	if errors.As(err, &retry) && retry != nil {
		if value := retry.RetryAfter(); value != nil && *value > 0 {
			retryAfter = *value
		}
	}
	body := []byte(err.Error())
	var headers http.Header
	var direct interface {
		ResponseBody() []byte
		ResponseHeaders() http.Header
	}
	if errors.As(err, &direct) && direct != nil {
		body = direct.ResponseBody()
		headers = direct.ResponseHeaders()
	}
	defer clear(body)
	typeValue, codeValue, providerMessage := claudeExecutionErrorFields(body)
	bounded := &ClaudeExecutionError{
		status: status, typeValue: typeValue, codeValue: codeValue,
		summary:    claudeExecutionSummary(status, typeValue, providerMessage),
		retryAfter: retryAfter, requestScoped: requestScoped,
	}
	if bounded.retryAfter == 0 {
		bounded.retryAfter = claudeRetryAfter(headers)
	}
	if credentialScopeKnown {
		return &claudeCredentialScopedExecutionError{
			ClaudeExecutionError: bounded,
			credentialScoped:     credentialScoped,
		}
	}
	return bounded
}

func claudeExecutionErrorFields(body []byte) (string, string, string) {
	var payload struct {
		Error struct {
			Type    json.RawMessage `json:"type"`
			Code    json.RawMessage `json:"code"`
			Message string          `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return "", "", ""
	}
	return boundedClaudeScalar(payload.Error.Type), boundedClaudeScalar(payload.Error.Code), strings.TrimSpace(payload.Error.Message)
}

func boundedClaudeScalar(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}

func claudeExecutionSummary(status int, typeValue, providerMessage string) string {
	message := strings.ToLower(providerMessage)
	if status == http.StatusTooManyRequests && strings.Contains(message, "fast mode") &&
		(strings.Contains(message, "usage credits") || strings.Contains(message, "credits are required")) {
		return "Usage credits are required for Claude fast mode."
	}
	switch {
	case typeValue == "authentication_error" || status == http.StatusUnauthorized:
		return "Claude authorization was rejected"
	case typeValue == "rate_limit_error" || status == http.StatusTooManyRequests:
		return "Claude upstream rate limit was reached"
	case status >= http.StatusInternalServerError:
		return "Claude upstream service failed"
	default:
		return fmt.Sprintf("Claude upstream request failed with status %d", status)
	}
}

func claudeRetryAfter(headers http.Header) time.Duration {
	if headers == nil {
		return 0
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(headers.Get("Retry-After")), 10, 64)
	if err != nil || seconds <= 0 || seconds > 3600 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

var _ ClaudeHTTPExecutor = (*claudeHTTPExecutor)(nil)
var _ cliproxyexecutor.RequestScopedError = (*ClaudeExecutionError)(nil)
var _ cliproxyexecutor.StatusError = (*ClaudeExecutionError)(nil)
