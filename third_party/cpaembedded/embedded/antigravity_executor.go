package embedded

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	internalcache "github.com/router-for-me/CLIProxyAPI/v7/internal/cache"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	internalexecutor "github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// AntigravityExecutionError is the bounded error shape allowed to cross the
// embedded boundary. The upstream response body is parsed locally then dropped.
type AntigravityExecutionError struct {
	status        int
	typeValue     string
	codeValue     string
	summary       string
	retryAfter    time.Duration
	requestScoped bool
}

func (err *AntigravityExecutionError) Error() string {
	if err == nil || err.summary == "" {
		return "Antigravity upstream request failed"
	}
	return err.summary
}

func (err *AntigravityExecutionError) StatusCode() int {
	if err == nil {
		return 0
	}
	return err.status
}

func (err *AntigravityExecutionError) ErrorType() string {
	if err == nil {
		return ""
	}
	return err.typeValue
}

func (err *AntigravityExecutionError) ErrorCode() string {
	if err == nil {
		return ""
	}
	return err.codeValue
}

func (err *AntigravityExecutionError) RetryAfter() *time.Duration {
	if err == nil || err.retryAfter <= 0 {
		return nil
	}
	value := err.retryAfter
	return &value
}

func (err *AntigravityExecutionError) IsRequestScoped() bool {
	return err != nil && err.requestScoped
}

// AntigravityHTTPExecutor is the execution-only CPA surface consumed by
// GPT-Load. It deliberately excludes CPA's manager, auth refresh persistence,
// selector, cooldown ownership, and credits fallback.
type AntigravityHTTPExecutor interface {
	ExecuteCanonical(context.Context, string, AntigravityCredential, ExecuteRequest) (ExecuteResponse, error)
	CountTokensCanonical(context.Context, string, AntigravityCredential, ExecuteRequest) (ExecuteResponse, error)
	ExecuteStreamCanonical(context.Context, string, AntigravityCredential, ExecuteRequest) (*ExecuteStreamResponse, error)
}

type antigravityHTTPExecutor struct {
	cfg     *internalconfig.Config
	inner   *internalexecutor.AntigravityExecutor
	baseURL string
}

// NewAntigravityHTTPExecutor creates the production execution-only facade.
func NewAntigravityHTTPExecutor() AntigravityHTTPExecutor {
	return newAntigravityHTTPExecutor(antigravityExecutionBase)
}

func newAntigravityHTTPExecutor(baseURL string) *antigravityHTTPExecutor {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = antigravityExecutionBase
	}
	// RequestRetry=0 and a fixed auth base_url guarantee that CPA never retries
	// within a GPT-Load attempt or falls back between daily and production hosts.
	// QuotaExceeded remains zero-valued, so no credits flag can trigger paid use.
	// CPA's global signature cache is only used by its Antigravity translator and
	// is not scoped by GPT-Load tenant or credential. Keep it disabled; the
	// isolated reasoning replay lane below remains available through ContinuityKey.
	internalcache.SetSignatureCacheEnabled(false)
	cfg := &internalconfig.Config{RequestRetry: 0}
	return &antigravityHTTPExecutor{cfg: cfg, inner: internalexecutor.NewAntigravityExecutor(cfg), baseURL: baseURL}
}

// NewAntigravityAuth creates a transient CPA auth object. credentialID is
// intentionally not used as Auth.ID: CPA's private 429 cooldown map is keyed
// by that field, while GPT-Load owns all credential health and cooldown state.
func NewAntigravityAuth(_ string, credential AntigravityCredential, baseURL string) *cliproxyauth.Auth {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = antigravityExecutionBase
	}
	// CPA refreshes at a 50-minute skew. The runtime has already prepared this
	// token and owns durable refresh, so use an ephemeral far-future expiry and
	// never pass a refresh token into this execution-only auth.
	now := time.Now().UTC()
	return &cliproxyauth.Auth{
		Provider: ProviderAntigravity,
		Attributes: map[string]string{
			"base_url": strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		},
		Metadata: map[string]any{
			"type":         ProviderAntigravity,
			"access_token": credential.AccessToken,
			"email":        credential.Email,
			"project_id":   credential.ProjectID,
			"timestamp":    now.UnixMilli(),
			"expires_in":   int64(24 * 60 * 60),
			"expired":      now.Add(24 * time.Hour).Format(time.RFC3339),
		},
	}
}

func (executor *antigravityHTTPExecutor) ExecuteCanonical(
	ctx context.Context,
	credentialID string,
	credential AntigravityCredential,
	request ExecuteRequest,
) (ExecuteResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateAntigravityCredential(credential); err != nil {
		return ExecuteResponse{}, err
	}
	request = prepareAntigravityExecutionRequest(request)
	format := sdktranslator.FromString(request.Format)
	auth := NewAntigravityAuth(credentialID, credential, executor.baseURL)
	observation := newProviderExecutionObservation(request, ProviderAntigravity)
	response, err := executor.inner.Execute(executor.executionContext(ctx, observation), auth, cliproxyexecutor.Request{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: format,
	}, antigravityExecutorOptions(request, format, false))
	if err != nil {
		return ExecuteResponse{AppliedReasoningEffort: observation.reasoningEffort()}, normalizeAntigravityExecutionError(err)
	}
	return ExecuteResponse{
		Payload: normalizeAntigravityConvertedUsage(request.Format, false, response.Payload), Headers: response.Headers.Clone(),
		AppliedReasoningEffort: observation.reasoningEffort(),
	}, nil
}

func (executor *antigravityHTTPExecutor) CountTokensCanonical(
	ctx context.Context,
	credentialID string,
	credential AntigravityCredential,
	request ExecuteRequest,
) (ExecuteResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateAntigravityCredential(credential); err != nil {
		return ExecuteResponse{}, err
	}
	request = prepareAntigravityExecutionRequest(request)
	format := sdktranslator.FromString(request.Format)
	auth := NewAntigravityAuth(credentialID, credential, executor.baseURL)
	response, err := executor.inner.CountTokens(executor.executionContext(ctx, nil), auth, cliproxyexecutor.Request{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: format,
	}, antigravityExecutorOptions(request, format, false))
	if err != nil {
		return ExecuteResponse{}, normalizeAntigravityExecutionError(err)
	}
	return ExecuteResponse{
		Payload: normalizeAntigravityCountTokens(request.Format, response.Payload), Headers: response.Headers.Clone(),
	}, nil
}

func (executor *antigravityHTTPExecutor) ExecuteStreamCanonical(
	ctx context.Context,
	credentialID string,
	credential AntigravityCredential,
	request ExecuteRequest,
) (*ExecuteStreamResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateAntigravityCredential(credential); err != nil {
		return nil, err
	}
	request = prepareAntigravityExecutionRequest(request)
	format := sdktranslator.FromString(request.Format)
	auth := NewAntigravityAuth(credentialID, credential, executor.baseURL)
	observation := newProviderExecutionObservation(request, ProviderAntigravity)
	response, err := executor.inner.ExecuteStream(executor.executionContext(ctx, observation), auth, cliproxyexecutor.Request{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: format,
	}, antigravityExecutorOptions(request, format, true))
	if err != nil {
		return &ExecuteStreamResponse{AppliedReasoningEffort: observation.reasoningEffort()}, normalizeAntigravityExecutionError(err)
	}
	chunks := make(chan ExecuteStreamChunk)
	go func() {
		defer close(chunks)
		for chunk := range response.Chunks {
			converted := ExecuteStreamChunk{Payload: normalizeAntigravityConvertedUsage(request.Format, true, chunk.Payload)}
			if chunk.Err != nil {
				converted.Err = normalizeAntigravityExecutionError(chunk.Err)
			}
			select {
			case chunks <- converted:
			case <-ctx.Done():
				return
			}
		}
	}()
	return &ExecuteStreamResponse{
		Headers: response.Headers.Clone(), Chunks: chunks, AppliedReasoningEffort: observation.reasoningEffort(),
	}, nil
}

func antigravityExecutorOptions(
	request ExecuteRequest,
	format sdktranslator.Format,
	stream bool,
) cliproxyexecutor.Options {
	options := cliproxyexecutor.Options{
		Stream: stream, Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		SourceFormat: format, ResponseFormat: format,
	}
	if scope := strings.TrimSpace(request.ContinuityKey); scope != "" {
		options.Metadata = map[string]any{cliproxyexecutor.ExecutionSessionMetadataKey: scope}
	}
	return options
}

func prepareAntigravityExecutionRequest(request ExecuteRequest) ExecuteRequest {
	if strings.TrimSpace(request.ContinuityKey) == "" && strings.TrimSpace(request.AttemptID) != "" {
		request.ContinuityKey = "gpt-load-antigravity-attempt\x00" + strings.TrimSpace(request.AttemptID)
	}
	request.Payload = stripAntigravityReplayInputs(request.Payload)
	request.OriginalRequest = stripAntigravityReplayInputs(request.OriginalRequest)
	request.Headers = request.Headers.Clone()
	stripAntigravityReplayHeaders(request.Headers)
	return request
}

func stripAntigravityReplayInputs(raw []byte) []byte {
	if len(raw) == 0 {
		return nil
	}
	result := append([]byte(nil), raw...)
	for _, path := range []string{
		"session_id", "sessionId", "prompt_cache_key",
		"metadata.session_id", "metadata.sessionId", "metadata.user_id",
		"request.sessionId", "request.session_id",
	} {
		updated, err := sjson.DeleteBytes(result, path)
		if err != nil {
			continue
		}
		result = updated
	}
	return result
}

func stripAntigravityReplayHeaders(headers http.Header) {
	for name := range headers {
		switch {
		case strings.EqualFold(name, "Session-Id"),
			strings.EqualFold(name, "X-Claude-Code-Session-Id"),
			strings.EqualFold(name, "X-Claude-Code-Agent-Id"):
			delete(headers, name)
		}
	}
}

func normalizeAntigravityConvertedUsage(format string, stream bool, raw []byte) []byte {
	result := append([]byte(nil), raw...)
	format = strings.TrimSpace(format)
	switch format {
	case "openai":
		return addAntigravityUsageIntegers(result, "usage.completion_tokens", "usage.completion_tokens_details.reasoning_tokens")
	case "openai-response":
		return addAntigravityUsageIntegers(result, "usage.output_tokens", "usage.output_tokens_details.reasoning_tokens")
	case "claude":
		if stream {
			return result
		}
		return subtractAntigravityUsageIntegers(result, "usage.input_tokens", "usage.cache_read_input_tokens")
	default:
		return result
	}
}

func addAntigravityUsageIntegers(raw []byte, totalPath, extraPath string) []byte {
	total, totalOK := antigravityUsageInteger(raw, totalPath)
	extra, extraOK := antigravityUsageInteger(raw, extraPath)
	if !totalOK || !extraOK || extra > 0 && total > int64(^uint64(0)>>1)-extra {
		return raw
	}
	updated, err := sjson.SetBytes(raw, totalPath, total+extra)
	if err != nil {
		return raw
	}
	clear(raw)
	return updated
}

func subtractAntigravityUsageIntegers(raw []byte, totalPath, cachedPath string) []byte {
	total, totalOK := antigravityUsageInteger(raw, totalPath)
	cached, cachedOK := antigravityUsageInteger(raw, cachedPath)
	if !totalOK || !cachedOK {
		return raw
	}
	uncached := total - cached
	if uncached < 0 {
		uncached = 0
	}
	updated, err := sjson.SetBytes(raw, totalPath, uncached)
	if err != nil {
		return raw
	}
	clear(raw)
	return updated
}

func antigravityUsageInteger(raw []byte, path string) (int64, bool) {
	value := gjson.GetBytes(raw, path)
	if !value.Exists() || value.Type != gjson.Number {
		return 0, false
	}
	parsed, err := strconv.ParseInt(value.Raw, 10, 64)
	if err != nil || parsed < 0 {
		return 0, false
	}
	return parsed, true
}

func normalizeAntigravityCountTokens(format string, raw []byte) []byte {
	if strings.TrimSpace(format) != "openai-response" {
		return append([]byte(nil), raw...)
	}
	count, ok := antigravityUsageInteger(raw, "totalTokens")
	if !ok {
		return append([]byte(nil), raw...)
	}
	encoded, err := json.Marshal(struct {
		Object      string `json:"object"`
		InputTokens int64  `json:"input_tokens"`
	}{Object: "response.input_tokens", InputTokens: count})
	if err != nil {
		return append([]byte(nil), raw...)
	}
	return encoded
}

var (
	antigravityExecutionTransportOnce sync.Once
	antigravityExecutionTransport     *http.Transport
)

type antigravityExecutionContext struct{ context.Context }

func (ctx antigravityExecutionContext) Value(key any) any {
	if key == "gin" {
		return nil
	}
	return ctx.Context.Value(key)
}

func (executor *antigravityHTTPExecutor) executionContext(ctx context.Context, observation *executionObservation) context.Context {
	ctx = antigravityExecutionContext{Context: ctx}
	antigravityExecutionTransportOnce.Do(func() {
		base, ok := http.DefaultTransport.(*http.Transport)
		if !ok || base == nil {
			base = &http.Transport{}
		}
		transport := base.Clone()
		transport.ForceAttemptHTTP2 = false
		transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		} else {
			transport.TLSClientConfig = transport.TLSClientConfig.Clone()
		}
		transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
		antigravityExecutionTransport = transport
	})
	return context.WithValue(ctx, "cliproxy.roundtripper", noRedirectRoundTripper{
		base: antigravityExecutionTransport, observation: observation,
	})
}

func normalizeAntigravityExecutionError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	status := 0
	var statusError interface{ StatusCode() int }
	if errors.As(err, &statusError) && statusError != nil {
		status = statusError.StatusCode()
	}
	if status == 0 {
		return err
	}
	retryAfter := time.Duration(0)
	var retry interface{ RetryAfter() *time.Duration }
	if errors.As(err, &retry) && retry != nil {
		if value := retry.RetryAfter(); value != nil && *value > 0 {
			retryAfter = *value
		}
	}
	typeValue, codeValue := antigravityExecutionErrorFields([]byte(err.Error()))
	return &AntigravityExecutionError{
		status: status, typeValue: typeValue, codeValue: codeValue, retryAfter: retryAfter,
		requestScoped: status >= http.StatusBadRequest && status < http.StatusInternalServerError &&
			status != http.StatusUnauthorized && status != http.StatusForbidden && status != http.StatusTooManyRequests,
		summary: antigravityExecutionSummary(status, typeValue, codeValue),
	}
}

func antigravityExecutionErrorFields(raw []byte) (string, string) {
	defer clear(raw)
	var payload struct {
		Error struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			Details []struct {
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return "", ""
	}
	code := ""
	for _, detail := range payload.Error.Details {
		if code = boundedAntigravityErrorScalar(detail.Reason); code != "" {
			break
		}
	}
	return boundedAntigravityErrorScalar(payload.Error.Status), code
}

func boundedAntigravityErrorScalar(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 || !utf8.ValidString(value) || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}

func antigravityExecutionSummary(status int, typeValue, codeValue string) string {
	switch {
	case status == http.StatusUnauthorized:
		return "Antigravity authorization was rejected"
	case status == http.StatusForbidden:
		return "Antigravity access was denied"
	case status == http.StatusTooManyRequests && strings.EqualFold(codeValue, "INSUFFICIENT_G1_CREDITS_BALANCE"):
		return "Antigravity Google One AI credits are unavailable"
	case status == http.StatusTooManyRequests:
		return "Antigravity upstream rate limit was reached"
	case status >= http.StatusInternalServerError:
		return "Antigravity upstream service failed"
	case typeValue != "":
		return fmt.Sprintf("Antigravity upstream request failed: %s", typeValue)
	default:
		return fmt.Sprintf("Antigravity upstream request failed with status %d", status)
	}
}

var _ AntigravityHTTPExecutor = (*antigravityHTTPExecutor)(nil)
var _ cliproxyexecutor.RequestScopedError = (*AntigravityExecutionError)(nil)
var _ cliproxyexecutor.StatusError = (*AntigravityExecutionError)(nil)
