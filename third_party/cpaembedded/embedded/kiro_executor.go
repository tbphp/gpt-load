package embedded

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/sjson"
)

const (
	kiroAMZTarget       = "AmazonCodeWhispererStreamingService.GenerateAssistantResponse"
	kiroUserAgent       = "aws-sdk-rust/1.3.15 ua/2.1 api/codewhispererstreaming/0.1.17593 os/macos lang/rust/1.92.0 md/appVersion-2.10.0 app/AmazonQ-For-CLI"
	kiroAMZUserAgent    = "aws-sdk-rust/1.3.15 ua/2.1 api/codewhispererstreaming/0.1.17593 os/macos lang/rust/1.92.0 m/F app/AmazonQ-For-CLI"
	kiroCPAProvider     = "kiro"
	kiroMaxAttempts     = 3
	kiroBodyReadIdle    = 180 * time.Second
	kiroUpstreamBodyMax = 4 * 1024 * 1024
)

type KiroExecutionError struct {
	status     int
	code       string
	summary    string
	retryAfter time.Duration
}

func (err *KiroExecutionError) Error() string {
	if err == nil || strings.TrimSpace(err.summary) == "" {
		return "Kiro upstream request failed"
	}
	return err.summary
}

func (err *KiroExecutionError) StatusCode() int {
	if err == nil {
		return 0
	}
	return err.status
}

func (err *KiroExecutionError) ErrorCode() string {
	if err == nil {
		return ""
	}
	return err.code
}

func (err *KiroExecutionError) RetryAfter() *time.Duration {
	if err == nil || err.retryAfter <= 0 {
		return nil
	}
	value := err.retryAfter
	return &value
}

// KiroHTTPExecutor executes Anthropic-format requests against the Kiro runtime.
type KiroHTTPExecutor interface {
	ExecuteCanonical(context.Context, string, KiroCredential, ExecuteRequest) (ExecuteResponse, error)
	CountTokensCanonical(context.Context, string, KiroCredential, ExecuteRequest) (ExecuteResponse, error)
	ExecuteStreamCanonical(context.Context, string, KiroCredential, ExecuteRequest) (*ExecuteStreamResponse, error)
}

type kiroHTTPExecutor struct {
	baseURL string
	client  *http.Client
}

// NewKiroHTTPExecutor returns a Kiro executor using the production HTTP client.
func NewKiroHTTPExecutor() KiroHTTPExecutor {
	return &kiroHTTPExecutor{baseURL: "", client: &http.Client{}}
}

func (executor *kiroHTTPExecutor) endpoint(credential KiroCredential) (string, error) {
	if strings.TrimSpace(executor.baseURL) != "" {
		return strings.TrimRight(executor.baseURL, "/"), nil
	}
	region := strings.TrimSpace(credential.Region)
	if region == "" {
		region = DefaultKiroRegion
	}
	return KiroRuntimeURL(region)
}

func (executor *kiroHTTPExecutor) requestBody(credential KiroCredential, request kiroRequest) ([]byte, error) {
	payload, err := buildKiroPayload(request, credential.ProfileARN)
	if err != nil {
		return nil, err
	}
	// When this is an API-key credential the runtime requires the TokenType
	// header instead of a profileArn in the body.
	if KiroAuthKind(credential.AuthKind) == KiroAuthAPIKey {
		payload, err = sjson.DeleteBytes(payload, "profileArn")
		if err != nil {
			return nil, err
		}
	}
	return payload, nil
}

// kiroRequestHeaders builds the amz-sdk headers Kiro expects.
func (executor *kiroHTTPExecutor) newRequest(ctx context.Context, method, url string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("X-Amz-Target", kiroAMZTarget)
	req.Header.Set("User-Agent", kiroUserAgent)
	req.Header.Set("x-amz-user-agent", kiroAMZUserAgent)
	req.Header.Set("x-amzn-codewhisperer-optout", "false")
	req.Header.Set("amz-sdk-invocation-id", randomKiroHex(16))
	return req, nil
}

func (executor *kiroHTTPExecutor) ExecuteCanonical(
	ctx context.Context,
	credentialID string,
	credential KiroCredential,
	request ExecuteRequest,
) (ExecuteResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalizeKiroCredential(&credential)
	if err := validateKiroCredential(credential); err != nil {
		return ExecuteResponse{}, err
	}
	if !isKiroClaudeFormat(request.Format) {
		return ExecuteResponse{}, fmt.Errorf("Kiro executor requires Anthropic format")
	}
	parsed, err := parseKiroRequest(request.Payload)
	if err != nil {
		return ExecuteResponse{}, err
	}
	body, err := executor.requestBody(credential, parsed)
	if err != nil {
		return ExecuteResponse{}, err
	}
	endpoint, err := executor.endpoint(credential)
	if err != nil {
		return ExecuteResponse{}, err
	}
	req, err := executor.newRequest(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return ExecuteResponse{}, err
	}
	executor.setAuth(req, credential)
	response, err := executor.client.Do(req)
	if err != nil {
		return ExecuteResponse{}, convertKiroDoError(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return ExecuteResponse{}, kiroHTTPErrorFromResponse(response)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.Contains(contentType, "amazon.eventstream") {
		return ExecuteResponse{}, kiroJSONEnvelopeError(response, response.Body)
	}
	var (
		blocks     []map[string]any
		usage      map[string]any
		stopReason = "stop_sequence"
		thinking   []string
		sig        string
	)
	_ = parseKiroStream(response.Body, func(event kiroEvent) bool {
		switch event.Type {
		case kiroEventAssistantResponse:
			if strings.TrimSpace(event.Content) != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": event.Content})
			}
		case kiroEventReasoningContent:
			if strings.TrimSpace(event.ThinkingText) != "" {
				thinking = append(thinking, event.ThinkingText)
			}
			if strings.TrimSpace(event.Signature) != "" {
				sig = event.Signature
			}
		case kiroEventToolUse:
			var input any = map[string]any{}
			if strings.TrimSpace(event.ToolInput) != "" {
				_ = json.Unmarshal([]byte(event.ToolInput), &input)
			}
			blocks = append(blocks, map[string]any{
				"type": "tool_use", "id": event.ToolUseID, "name": event.ToolName, "input": input,
			})
		case kiroEventMetadata:
			usage = kiroAnthropicUsage(event)
			stopReason = "end_turn"
		case kiroEventInvalidState, kiroEventException:
			if msg := event.ErrorText(); msg != "" {
				// Surface as an upstream error after emitting what we have.
			}
		}
		return false
	})
	// Prepend thinking block when present.
	if len(thinking) > 0 {
		content := make([]map[string]any, 0, len(blocks)+1)
		block := map[string]any{"type": "thinking", "thinking": strings.Join(thinking, "")}
		if sig != "" {
			block["signature"] = sig
		}
		content = append(content, block)
		content = append(content, blocks...)
		blocks = content
	}
	if usage == nil {
		usage = map[string]any{"input_tokens": 0, "output_tokens": 0}
	}
	final := map[string]any{
		"id": "msg_" + randomKiroHex(8), "type": "message", "role": "assistant",
		"content": blocks, "model": parsed.Model, "stop_reason": stopReason, "stop_sequence": nil,
		"usage": usage,
	}
	raw, err := json.Marshal(final)
	if err != nil {
		return ExecuteResponse{}, err
	}
	return ExecuteResponse{Payload: raw, Headers: response.Header.Clone()}, nil
}

func (executor *kiroHTTPExecutor) CountTokensCanonical(
	ctx context.Context,
	credentialID string,
	credential KiroCredential,
	request ExecuteRequest,
) (ExecuteResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalizeKiroCredential(&credential)
	if err := validateKiroCredential(credential); err != nil {
		return ExecuteResponse{}, err
	}
	if !isKiroClaudeFormat(request.Format) {
		return ExecuteResponse{}, fmt.Errorf("Kiro executor requires Anthropic format")
	}
	inputTokens := estimateKiroTokens(request.Payload)
	status := map[string]any{
		"input_tokens": inputTokens, "output_tokens": 0,
		"server_tokens": 0,
	}
	raw, err := json.Marshal(status)
	if err != nil {
		return ExecuteResponse{}, err
	}
	return ExecuteResponse{Payload: raw}, nil
}

// CountKiroTokensLocal is a credential-less local token estimate for the
// Anthropic count-tokens path. Kiro exposes no token-counting endpoint, so the
// value is a whitespace/byte heuristic returned in the standard Anthropic
// count_tokens response shape.
func CountKiroTokensLocal(payload []byte) []byte {
	inputTokens := estimateKiroTokens(payload)
	status := map[string]any{
		"input_tokens": inputTokens, "output_tokens": 0, "server_tokens": 0,
	}
	raw, err := json.Marshal(status)
	if err != nil {
		return []byte(`{"input_tokens":0,"output_tokens":0,"server_tokens":0}`)
	}
	return raw
}

func (executor *kiroHTTPExecutor) ExecuteStreamCanonical(
	ctx context.Context,
	credentialID string,
	credential KiroCredential,
	request ExecuteRequest,
) (*ExecuteStreamResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalizeKiroCredential(&credential)
	if err := validateKiroCredential(credential); err != nil {
		return nil, err
	}
	if !isKiroClaudeFormat(request.Format) {
		return nil, fmt.Errorf("Kiro executor requires Anthropic format")
	}
	parsed, err := parseKiroRequest(request.Payload)
	if err != nil {
		return nil, err
	}
	body, err := executor.requestBody(credential, parsed)
	if err != nil {
		return nil, err
	}
	endpoint, err := executor.endpoint(credential)
	if err != nil {
		return nil, err
	}
	req, err := executor.newRequest(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return nil, err
	}
	executor.setAuth(req, credential)
	response, err := executor.client.Do(req)
	if err != nil {
		return nil, convertKiroDoError(err)
	}
	if response.StatusCode != http.StatusOK {
		defer func() { _ = response.Body.Close() }()
		return nil, kiroHTTPErrorFromResponse(response)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.Contains(contentType, "amazon.eventstream") {
		defer func() { _ = response.Body.Close() }()
		return nil, kiroJSONEnvelopeError(response, response.Body)
	}
	chunks := make(chan ExecuteStreamChunk)
	go func() {
		defer close(chunks)
		_ = executor.emitKiroSSE(ctx, chunks, parsed.Model, response.Body)
		_ = response.Body.Close()
	}()
	return &ExecuteStreamResponse{Headers: response.Header.Clone(), Chunks: chunks}, nil
}

// emitKiroSSE consumes the Kiro event stream and writes Anthropic SSE chunks.
func (executor *kiroHTTPExecutor) emitKiroSSE(ctx context.Context, chunks chan<- ExecuteStreamChunk, model string, reader io.Reader) error {
	emit := func(payload []byte) bool {
		chunk := ExecuteStreamChunk{Payload: payload}
		select {
		case chunks <- chunk:
			return true
		case <-ctx.Done():
			return false
		}
	}
	if !emit(ktoSSE("message_start", map[string]any{
		"type": "message_start", "message": map[string]any{
			"id": "msg_" + randomKiroHex(8), "type": "message", "role": "assistant",
			"model": model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	})) {
		return ctx.Err()
	}
	index := 0
	thinkingIndex := -1
	// parseKiroStream returns immediately when the callback reports true (stop).
	err := parseKiroStream(reader, func(event kiroEvent) bool {
		return emitKiroEventSSE(emit, &index, &thinkingIndex, event)
	})
	_ = err
	emit(message_deltaSSE(map[string]any{
		"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": 0},
	}))
	emit(ktoSSE("message_stop", map[string]any{"type": "message_stop"}))
	return err
}

// emitKiroEventSSE maps one kiroEvent onto Anthropic SSE output. Returns false
// when the consumer wants to continue, true to stop.
func emitKiroEventSSE(emit func([]byte) bool, index *int, thinkingIndex *int, event kiroEvent) bool {
	switch event.Type {
	case kiroEventReasoningContent:
		if *thinkingIndex < 0 {
			*thinkingIndex = *index
			*index++
			if !emit(ktoSSE("content_block_start", map[string]any{
				"type": "content_block_start", "index": *thinkingIndex, "content_block": map[string]any{"type": "thinking", "thinking": "", "signature": ""},
			})) {
				return true
			}
		}
		if strings.TrimSpace(event.ThinkingText) != "" {
			if !emit(ktoSSE("content_block_delta", map[string]any{
				"type": "content_block_delta", "index": *thinkingIndex, "delta": map[string]any{"type": "thinking_delta", "thinking": event.ThinkingText},
			})) {
				return true
			}
		}
		if strings.TrimSpace(event.Signature) != "" {
			if !emit(ktoSSE("content_block_delta", map[string]any{
				"type": "content_block_delta", "index": *thinkingIndex, "delta": map[string]any{"type": "signature_delta", "signature": event.Signature},
			})) {
				return true
			}
		}
	case kiroEventAssistantResponse:
		if strings.TrimSpace(event.Content) != "" {
			blockIndex := *index
			*index++
			if !emit(ktoSSE("content_block_start", map[string]any{
				"type": "content_block_start", "index": blockIndex, "content_block": map[string]any{"type": "text", "text": ""},
			})) {
				return true
			}
			if !emit(ktoSSE("content_block_delta", map[string]any{
				"type": "content_block_delta", "index": blockIndex, "delta": map[string]any{"type": "text_delta", "text": event.Content},
			})) {
				return true
			}
			if !emit(ktoSSE("content_block_stop", map[string]any{"type": "content_block_stop", "index": blockIndex})) {
				return true
			}
		}
	case kiroEventToolUse:
		blockIndex := *index
		*index++
		var input any = map[string]any{}
		if strings.TrimSpace(event.ToolInput) != "" {
			_ = json.Unmarshal([]byte(event.ToolInput), &input)
		}
		if !emit(ktoSSE("content_block_start", map[string]any{
			"type": "content_block_start", "index": blockIndex, "content_block": map[string]any{
				"type": "tool_use", "id": event.ToolUseID, "name": event.ToolName, "input": map[string]any{},
			},
		})) {
			return true
		}
		// Emit the tool input as an input_json_delta for a complete tool call.
		if rawInput, err := json.Marshal(input); err == nil && len(rawInput) > 0 {
			if !emit(ktoSSE("content_block_delta", map[string]any{
				"type": "content_block_delta", "index": blockIndex, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(rawInput)},
			})) {
				return true
			}
		}
		if !emit(ktoSSE("content_block_stop", map[string]any{"type": "content_block_stop", "index": blockIndex})) {
			return true
		}
	case kiroEventMetadata, kiroEventMetering:
		// Usage arrives at the end; folded into message_delta by the caller.
	}
	return false
}

func ktoSSE(eventType string, payload map[string]any) []byte {
	raw, err := json.Marshal(payload)
	if err != nil {
		return []byte{}
	}
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, raw))
}

func message_deltaSSE(payload map[string]any) []byte {
	return ktoSSE("message_delta", payload)
}

func kiroAnthropicUsage(event kiroEvent) map[string]any {
	usage := map[string]any{
		"input_tokens": event.InputTokens, "output_tokens": event.OutputTokens,
		"cache_creation_input_tokens": event.CacheWriteInputTokens,
		"cache_read_input_tokens":     event.CacheReadInputTokens,
	}
	return usage
}

// setAuth attaches the bearer token, adding the TokenType header for API keys.
func (executor *kiroHTTPExecutor) setAuth(request *http.Request, credential KiroCredential) {
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(credential.AccessToken))
	if KiroAuthKind(credential.AuthKind) == KiroAuthAPIKey {
		request.Header.Set("TokenType", "API_KEY")
	}
}

func isKiroClaudeFormat(format string) bool {
	format = strings.ToLower(strings.TrimSpace(format))
	return format == "" || format == "claude" || format == "anthropic"
}

func kiroHTTPErrorFromResponse(response *http.Response) error {
	return &KiroExecutionError{
		status: response.StatusCode, summary: fmt.Sprintf("Kiro upstream returned HTTP %d", response.StatusCode),
	}
}

func kiroJSONEnvelopeError(response *http.Response, body io.Reader) error {
	raw, _ := io.ReadAll(io.LimitReader(body, kiroUpstreamBodyMax))
	var envelope struct {
		Type    string `json:"__type"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &envelope)
	code := strings.TrimPrefix(envelope.Type, "com.amazon.coral.service#")
	message := envelope.Message
	if strings.TrimSpace(message) == "" {
		message = fmt.Sprintf("Kiro upstream returned non-eventstream response (HTTP %d)", response.StatusCode)
	}
	return &KiroExecutionError{status: response.StatusCode, code: code, summary: message}
}

func convertKiroDoError(err error) error {
	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) {
		return &KiroExecutionError{status: 0, code: "network", summary: "Kiro network request failed"}
	}
	return &KiroExecutionError{status: 0, code: "network", summary: err.Error()}
}

// estimateKiroTokens is a crude token estimate for the count-tokens path.
func estimateKiroTokens(raw []byte) int {
	if len(raw) == 0 {
		return 0
	}
	return (len(raw) + 3) / 4
}

func randomKiroHex(n int) string {
	return randomHex(n)
}
