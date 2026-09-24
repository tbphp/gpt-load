package embedded

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	internalexecutor "github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	chatgptCPAProvider        = "chatgpt"
	defaultChatGPTBaseURL     = "https://bps.openai.com/basispoints/api"
	chatgptAuthModeHeader     = "x-basispoints-auth-mode"
	chatgptAuthModeChatGPT    = "chatgpt"
	chatgptAccountIDHeader    = "chatgpt-account-id"
	openaiAccountIDHeader     = "x-openai-account-id"
	openaiAccountUserIDHeader = "x-openai-account-user-id"
	chatgptBrowserUserAgent   = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

// BPS only injects run_officejs when the request looks like the Excel add-in.
var chatgptExcelClientHeaders = [][2]string{
	{"X-Openai-Internal-Basispoints-Client-Product", "basispoints-excel-plugin"},
	{"X-Openai-Internal-Basispoints-Client-Platform", "excel"},
	{"X-Openai-Internal-Basispoints-Client-Agent-Profile", "excel"},
	{"X-Openai-Internal-Basispoints-Client-Editor", "excel"},
	{"X-Openai-Internal-Basispoints-Client-Host", "office"},
	{"X-Openai-Internal-Basispoints-Client-Runtime", "web"},
	{"X-Openai-Internal-Basispoints-Client-Platform-Class", "OfficeOnline"},
	{"X-Openai-Internal-Basispoints-Office-Host", "Excel"},
	{"X-Openai-Internal-Basispoints-Office-Platform", "OfficeOnline"},
	{"X-Openai-Internal-Basispoints-Browser-Name", "chrome"},
}

// ChatGPTHTTPExecutor is the execution-only basispoints Responses facade.
type ChatGPTHTTPExecutor interface {
	ExecuteCanonical(context.Context, string, CodexCredential, ExecuteRequest) (ExecuteResponse, error)
	CountTokensCanonical(context.Context, ExecuteRequest) (ExecuteResponse, error)
	ExecuteStreamCanonical(context.Context, string, CodexCredential, ExecuteRequest) (*ExecuteStreamResponse, error)
}

type chatgptHTTPExecutor struct {
	cfg     *internalconfig.Config
	inner   *internalexecutor.XAIExecutor
	baseURL string
}

// NewChatGPTHTTPExecutor constructs a Responses executor aimed at basispoints.
func NewChatGPTHTTPExecutor() ChatGPTHTTPExecutor {
	cfg := &internalconfig.Config{}
	return &chatgptHTTPExecutor{cfg: cfg, inner: internalexecutor.NewXAIExecutor(cfg)}
}

func (executor *chatgptHTTPExecutor) executionBaseURL(apiRoot string) (string, error) {
	apiRoot = strings.TrimSpace(apiRoot)
	if apiRoot != "" {
		return strings.TrimRight(apiRoot, "/"), nil
	}
	if executor != nil && strings.TrimSpace(executor.baseURL) != "" {
		return strings.TrimRight(executor.baseURL, "/"), nil
	}
	return defaultChatGPTBaseURL, nil
}

func NewChatGPTAuth(id string, credential CodexCredential, baseURL string) *cliproxyauth.Auth {
	accountID := chatgptAccountID(credential)
	metadata := map[string]any{
		"type":          ProviderCodex,
		"access_token":  credential.AccessToken,
		"refresh_token": credential.RefreshToken,
		"account_id":    accountID,
		"auth_kind":     "oauth",
		"using_api":     true,
	}
	if credential.IDToken != "" {
		metadata["id_token"] = credential.IDToken
	}
	if credential.Email != "" {
		metadata["email"] = credential.Email
	}
	attributes := map[string]string{
		"api_key":                          strings.TrimSpace(credential.AccessToken),
		"auth_kind":                        "oauth",
		"using_api":                        "true",
		"header:" + chatgptAccountIDHeader: accountID,
		"header:" + openaiAccountIDHeader:  accountID,
		"header:" + chatgptAuthModeHeader:  chatgptAuthModeChatGPT,
	}
	if value := strings.TrimSpace(baseURL); value != "" {
		attributes["base_url"] = value
	}
	return &cliproxyauth.Auth{
		ID:         strings.TrimSpace(id),
		Provider:   "xai",
		Attributes: attributes,
		Metadata:   metadata,
	}
}

func (executor *chatgptHTTPExecutor) ExecuteCanonical(
	ctx context.Context,
	credentialID string,
	credential CodexCredential,
	request ExecuteRequest,
) (ExecuteResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCredential(credential); err != nil {
		return ExecuteResponse{}, err
	}
	baseURL, err := executor.executionBaseURL(request.BaseURL)
	if err != nil {
		return ExecuteResponse{}, err
	}
	request.Payload = clampBasispointsEffort(request.Payload)
	request.OriginalRequest = clampBasispointsEffort(request.OriginalRequest)
	if chatgptNativeResponses(request.Format) {
		return executor.executeNative(ctx, credentialID, credential, request, false)
	}
	format := sdktranslator.FromString(request.Format)
	auth := NewChatGPTAuth(credentialID, credential, baseURL)
	auth.ProxyURL = request.ProxyURL
	observation := newProviderExecutionObservation(request, chatgptCPAProvider)
	executionCtx := executor.executionContext(ctx, auth, observation, credential)
	response, err := executor.inner.Execute(executionCtx, authWithoutProxyURL(auth), cliproxyexecutor.Request{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: format,
	}, chatgptExecutorOptions(request, format, false))
	if err != nil {
		return ExecuteResponse{Headers: observation.responseHeaders(), AppliedReasoningEffort: observation.reasoningEffort()}, normalizeChatGPTExecutionError(err)
	}
	return ExecuteResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
		AppliedReasoningEffort: observation.reasoningEffort(),
	}, nil
}

func (executor *chatgptHTTPExecutor) CountTokensCanonical(
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
	format := sdktranslator.FromString(request.Format)
	response, err := executor.inner.CountTokens(ctx, NewChatGPTAuth("local-token-count", CodexCredential{}, baseURL), cliproxyexecutor.Request{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: format,
	}, chatgptExecutorOptions(request, format, false))
	if err != nil {
		return ExecuteResponse{}, normalizeChatGPTExecutionError(err)
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

func (executor *chatgptHTTPExecutor) ExecuteStreamCanonical(
	ctx context.Context,
	credentialID string,
	credential CodexCredential,
	request ExecuteRequest,
) (*ExecuteStreamResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCredential(credential); err != nil {
		return nil, err
	}
	baseURL, err := executor.executionBaseURL(request.BaseURL)
	if err != nil {
		return nil, err
	}
	request.Payload = clampBasispointsEffort(request.Payload)
	request.OriginalRequest = clampBasispointsEffort(request.OriginalRequest)
	if chatgptNativeResponses(request.Format) {
		return executor.executeNativeStream(ctx, credentialID, credential, request)
	}
	format := sdktranslator.FromString(request.Format)
	auth := NewChatGPTAuth(credentialID, credential, baseURL)
	auth.ProxyURL = request.ProxyURL
	observation := newProviderExecutionObservation(request, chatgptCPAProvider)
	executionCtx := executor.executionContext(ctx, auth, observation, credential)
	response, err := executor.inner.ExecuteStream(executionCtx, authWithoutProxyURL(auth), cliproxyexecutor.Request{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: format,
	}, chatgptExecutorOptions(request, format, true))
	if err != nil {
		return &ExecuteStreamResponse{Headers: observation.responseHeaders(), AppliedReasoningEffort: observation.reasoningEffort()}, normalizeChatGPTExecutionError(err)
	}
	chunks := make(chan ExecuteStreamChunk)
	go func() {
		defer close(chunks)
		for chunk := range response.Chunks {
			converted := ExecuteStreamChunk{Payload: append([]byte(nil), chunk.Payload...)}
			if chunk.Err != nil {
				converted.Err = normalizeChatGPTExecutionError(chunk.Err)
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

func chatgptNativeResponses(format string) bool {
	switch strings.TrimSpace(strings.ToLower(format)) {
	case "", "openai-response", "openai-responses":
		return true
	default:
		return false
	}
}

func (executor *chatgptHTTPExecutor) executeNative(
	ctx context.Context,
	credentialID string,
	credential CodexCredential,
	request ExecuteRequest,
	stream bool,
) (ExecuteResponse, error) {
	body, url, auth, observation, err := executor.prepareNativeRequest(credentialID, credential, request, stream)
	if err != nil {
		return ExecuteResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return ExecuteResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	} else {
		httpReq.Header.Set("Accept", "application/json")
	}
	applyChatGPTHeaders(httpReq, credential)
	resp, err := executor.nativeHTTPClient(ctx, auth, observation, credential).Do(httpReq)
	if err != nil {
		return ExecuteResponse{Headers: observation.responseHeaders(), AppliedReasoningEffort: observation.reasoningEffort()}, normalizeChatGPTExecutionError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxObservedBodyBytes+1))
	if err != nil {
		return ExecuteResponse{Headers: resp.Header.Clone(), AppliedReasoningEffort: observation.reasoningEffort()}, err
	}
	if len(payload) > maxObservedBodyBytes {
		return ExecuteResponse{Headers: resp.Header.Clone(), AppliedReasoningEffort: observation.reasoningEffort()}, fmt.Errorf("ChatGPT response exceeded size limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ExecuteResponse{StatusCode: resp.StatusCode, Payload: payload, Headers: resp.Header.Clone(), AppliedReasoningEffort: observation.reasoningEffort()}, &GrokExecutionError{
			status: resp.StatusCode, summary: chatgptExecutionSummary(resp.StatusCode),
		}
	}
	payload = rewriteBasispointsResponse(payload)
	return ExecuteResponse{
		StatusCode: resp.StatusCode, Payload: payload, Headers: resp.Header.Clone(),
		AppliedReasoningEffort: observation.reasoningEffort(),
	}, nil
}

func (executor *chatgptHTTPExecutor) executeNativeStream(
	ctx context.Context,
	credentialID string,
	credential CodexCredential,
	request ExecuteRequest,
) (*ExecuteStreamResponse, error) {
	body, url, auth, observation, err := executor.prepareNativeRequest(credentialID, credential, request, true)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	applyChatGPTHeaders(httpReq, credential)
	resp, err := executor.nativeHTTPClient(ctx, auth, observation, credential).Do(httpReq)
	if err != nil {
		return &ExecuteStreamResponse{Headers: observation.responseHeaders(), AppliedReasoningEffort: observation.reasoningEffort()}, normalizeChatGPTExecutionError(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxObservedBodyBytes+1))
		_ = resp.Body.Close()
		return &ExecuteStreamResponse{Headers: resp.Header.Clone(), AppliedReasoningEffort: observation.reasoningEffort()}, &GrokExecutionError{
			status: resp.StatusCode, summary: chatgptExecutionSummary(resp.StatusCode),
		}
	}
	chunks := make(chan ExecuteStreamChunk)
	go func() {
		defer close(chunks)
		defer func() { _ = resp.Body.Close() }()
		buf := make([]byte, 32*1024)
		var pending []byte
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				pending = append(pending, buf[:n]...)
				rewritten, rest := rewriteCompleteSSEEvents(pending)
				pending = rest
				if len(rewritten) > 0 {
					select {
					case chunks <- ExecuteStreamChunk{Payload: rewritten}:
					case <-ctx.Done():
						return
					}
				}
			}
			if readErr != nil {
				if len(pending) > 0 {
					select {
					case chunks <- ExecuteStreamChunk{Payload: rewriteBasispointsResponse(pending)}:
					case <-ctx.Done():
						return
					}
				}
				if !errors.Is(readErr, io.EOF) {
					select {
					case chunks <- ExecuteStreamChunk{Err: readErr}:
					case <-ctx.Done():
					}
				}
				return
			}
		}
	}()
	return &ExecuteStreamResponse{
		Headers: resp.Header.Clone(), Chunks: chunks,
		AppliedReasoningEffort: observation.reasoningEffort(),
	}, nil
}

func (executor *chatgptHTTPExecutor) prepareNativeRequest(
	credentialID string,
	credential CodexCredential,
	request ExecuteRequest,
	stream bool,
) ([]byte, string, *cliproxyauth.Auth, *executionObservation, error) {
	baseURL, err := executor.executionBaseURL(request.BaseURL)
	if err != nil {
		return nil, "", nil, nil, err
	}
	body := prepareBasispointsBody(append([]byte(nil), request.Payload...))
	if stream {
		body, _ = sjson.SetBytes(body, "stream", true)
	} else if gjson.GetBytes(body, "stream").Exists() {
		body, _ = sjson.SetBytes(body, "stream", false)
	}
	auth := NewChatGPTAuth(credentialID, credential, baseURL)
	auth.ProxyURL = request.ProxyURL
	return body, strings.TrimRight(baseURL, "/") + "/responses", auth, newProviderExecutionObservation(request, chatgptCPAProvider), nil
}

func (executor *chatgptHTTPExecutor) nativeHTTPClient(
	ctx context.Context,
	auth *cliproxyauth.Auth,
	observation *executionObservation,
	credential CodexCredential,
) *http.Client {
	client := helps.NewProxyAwareHTTPClient(ctx, executor.cfg, auth, 0)
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Transport = noRedirectRoundTripper{
		base: chatgptHeadersRoundTripper{base: transport, credential: credential}, observation: observation,
	}
	return client
}

func chatgptExecutorOptions(request ExecuteRequest, format sdktranslator.Format, stream bool) cliproxyexecutor.Options {
	return cliproxyexecutor.Options{
		Stream: stream, Headers: request.Headers.Clone(),
		OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		SourceFormat:    format, ResponseFormat: format,
	}
}

func (executor *chatgptHTTPExecutor) executionContext(
	ctx context.Context,
	auth *cliproxyauth.Auth,
	observation *executionObservation,
	credential CodexCredential,
) context.Context {
	baseClient := helps.NewProxyAwareHTTPClient(ctx, executor.cfg, auth, 0)
	transport := baseClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	return context.WithValue(ctx, "cliproxy.roundtripper", noRedirectRoundTripper{
		base:        chatgptHeadersRoundTripper{base: transport, credential: credential},
		observation: observation,
	})
}

type chatgptHeadersRoundTripper struct {
	base       http.RoundTripper
	credential CodexCredential
}

func (transport chatgptHeadersRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil {
		return nil, fmt.Errorf("chatgpt request is nil")
	}
	request = request.Clone(request.Context())
	if request.Body != nil && request.Body != http.NoBody {
		body, err := io.ReadAll(request.Body)
		_ = request.Body.Close()
		if err != nil {
			return nil, err
		}
		body = prepareBasispointsBody(body)
		request.Body = io.NopCloser(bytes.NewReader(body))
		request.ContentLength = int64(len(body))
		request.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	}
	for name := range request.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-grok-") || strings.HasPrefix(lower, "x-xai-") {
			request.Header.Del(name)
		}
	}
	applyChatGPTHeaders(request, transport.credential)
	if request.Header.Get("User-Agent") == "" {
		request.Header.Set("User-Agent", chatgptBrowserUserAgent)
	}
	if transport.base == nil {
		return http.DefaultTransport.RoundTrip(request)
	}
	return transport.base.RoundTrip(request)
}

func applyChatGPTHeaders(request *http.Request, credential CodexCredential) {
	if request == nil {
		return
	}
	token := strings.TrimSpace(credential.AccessToken)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	accountID := chatgptAccountID(credential)
	if accountID != "" {
		request.Header.Set(chatgptAccountIDHeader, accountID)
		request.Header.Set(openaiAccountIDHeader, accountID)
	}
	if userID := chatgptAccountUserID(credential); userID != "" {
		request.Header.Set(openaiAccountUserIDHeader, userID)
	}
	request.Header.Set(chatgptAuthModeHeader, chatgptAuthModeChatGPT)
	if request.Header.Get("User-Agent") == "" {
		request.Header.Set("User-Agent", chatgptBrowserUserAgent)
	}
	if request.Header.Get("Origin") == "" {
		request.Header.Set("Origin", "https://bps.openai.com")
	}
	if request.Header.Get("Referer") == "" {
		request.Header.Set("Referer", "https://bps.openai.com/")
	}
	for _, header := range chatgptExcelClientHeaders {
		if request.Header.Get(header[0]) == "" {
			request.Header.Set(header[0], header[1])
		}
	}
}

func prepareBasispointsBody(body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	body = clampBasispointsEffort(body)
	body = smuggleClientTools(body)
	body = restoreOfficeJSIdentity(body)
	return ensureBasispointsMetadata(body)
}

func ensureBasispointsMetadata(body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	taskID := strings.TrimSpace(gjson.GetBytes(body, "metadata.task_id").String())
	if taskID == "" {
		taskID = uuid.NewString()
		if updated, err := sjson.SetBytes(body, "metadata.task_id", taskID); err == nil {
			body = updated
		}
	}
	if strings.TrimSpace(gjson.GetBytes(body, "metadata.turn_id").String()) == "" {
		if updated, err := sjson.SetBytes(body, "metadata.turn_id", taskID); err == nil {
			body = updated
		}
	}
	return body
}

const officeJSToolName = "run_officejs"

func smuggleClientTools(body []byte) []byte {
	if len(body) == 0 || !gjson.GetBytes(body, "tools").Exists() {
		return body
	}
	catalog := clientToolCatalog(gjson.GetBytes(body, "tools"))
	if updated, err := sjson.DeleteBytes(body, "tools"); err == nil {
		body = updated
	}
	if updated, err := sjson.DeleteBytes(body, "tool_choice"); err == nil {
		body = updated
	}
	if catalog == "" {
		return body
	}
	return prependDeveloperMessage(body, officeJSSmugglePrompt(catalog))
}

func clientToolCatalog(tools gjson.Result) string {
	if !tools.IsArray() {
		return ""
	}
	var b strings.Builder
	for _, tool := range tools.Array() {
		name := strings.TrimSpace(tool.Get("name").String())
		if name == "" {
			name = strings.TrimSpace(tool.Get("function.name").String())
		}
		if name == "" || name == officeJSToolName {
			continue
		}
		desc := strings.TrimSpace(tool.Get("description").String())
		if desc == "" {
			desc = strings.TrimSpace(tool.Get("function.description").String())
		}
		if desc == "" {
			fmt.Fprintf(&b, "- %s\n", name)
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", name, desc)
	}
	return strings.TrimSpace(b.String())
}

func officeJSSmugglePrompt(catalog string) string {
	return "Client tools are not declared as schema. To call one, invoke run_officejs and put a JSON object in its code field: {\"tool\":\"<name>\",\"args\":{...}}. Do not execute Office JavaScript. Do not use web_search for these tools. Available tools:\n" + catalog
}

func prependDeveloperMessage(body []byte, text string) []byte {
	if strings.TrimSpace(text) == "" {
		return body
	}
	msg := map[string]any{
		"type": "message",
		"role": "developer",
		"content": []any{
			map[string]any{"type": "input_text", "text": text},
		},
	}
	input := gjson.GetBytes(body, "input")
	switch {
	case !input.Exists():
		updated, err := sjson.SetBytes(body, "input", []any{msg})
		if err != nil {
			return body
		}
		return updated
	case input.IsArray():
		items := make([]any, 0, input.Get("#").Int()+1)
		items = append(items, msg)
		for _, item := range input.Array() {
			var raw any
			if json.Unmarshal([]byte(item.Raw), &raw) == nil {
				items = append(items, raw)
			}
		}
		updated, err := sjson.SetBytes(body, "input", items)
		if err != nil {
			return body
		}
		return updated
	default:
		updated, err := sjson.SetBytes(body, "input", []any{
			msg,
			map[string]any{
				"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": input.String()}},
			},
		})
		if err != nil {
			return body
		}
		return updated
	}
}

var officeJSCalls sync.Map

func rememberOfficeJSCall(node gjson.Result) {
	callID := strings.TrimSpace(node.Get("call_id").String())
	if callID == "" {
		return
	}
	var raw any
	if json.Unmarshal([]byte(node.Raw), &raw) != nil {
		return
	}
	officeJSCalls.Store(callID, raw)
}

func lookupOfficeJSCall(callID string) map[string]any {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return nil
	}
	value, ok := officeJSCalls.Load(callID)
	if !ok {
		return nil
	}
	original, ok := value.(map[string]any)
	if !ok || original == nil {
		return nil
	}
	clone := make(map[string]any, len(original))
	for key, item := range original {
		clone[key] = item
	}
	return clone
}

func restoreOfficeJSIdentity(body []byte) []byte {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body
	}
	items := make([]any, 0, input.Get("#").Int())
	changed := false
	hasOutput := false
	for _, item := range input.Array() {
		var raw any
		if json.Unmarshal([]byte(item.Raw), &raw) != nil {
			continue
		}
		switch item.Get("type").String() {
		case "function_call":
			if original := lookupOfficeJSCall(item.Get("call_id").String()); original != nil {
				items = append(items, original)
				changed = true
				continue
			}
		case "function_call_output":
			hasOutput = true
			if restored := restoreOfficeJSOutput(item, raw); restored != nil {
				items = append(items, restored)
				changed = true
				continue
			}
		}
		items = append(items, raw)
	}
	if hasOutput && !gjson.GetBytes(body, "metadata.agent_iteration").Exists() {
		if updated, err := sjson.SetBytes(body, "metadata.agent_iteration", 1); err == nil {
			body = updated
			changed = true
		}
	}
	if !changed {
		return body
	}
	updated, err := sjson.SetBytes(body, "input", items)
	if err != nil {
		return body
	}
	return updated
}

func restoreOfficeJSOutput(item gjson.Result, raw any) map[string]any {
	callID := strings.TrimSpace(item.Get("call_id").String())
	original := lookupOfficeJSCall(callID)
	out, _ := raw.(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	if original == nil && item.Get("id").String() == "" && !item.Get("summary").Exists() {
		return nil
	}
	out["type"] = "function_call_output"
	out["call_id"] = callID
	if _, ok := out["output"]; !ok {
		out["output"] = item.Get("output").String()
	}
	delete(out, "name")
	copyOfficeJSField(out, original, item, "id")
	copyOfficeJSField(out, original, item, "summary")
	copyOfficeJSField(out, original, item, "references")
	return out
}

func copyOfficeJSField(out map[string]any, original map[string]any, item gjson.Result, key string) {
	if _, exists := out[key]; exists {
		return
	}
	if original != nil {
		if value, ok := original[key]; ok {
			out[key] = value
			return
		}
	}
	if !item.Get(key).Exists() {
		return
	}
	var value any
	if json.Unmarshal([]byte(item.Get(key).Raw), &value) == nil {
		out[key] = value
	}
}

func rewriteBasispointsResponse(payload []byte) []byte {
	if len(payload) == 0 {
		return payload
	}
	if bytes.Contains(payload, []byte("event:")) || bytes.Contains(payload, []byte("data:")) {
		return rewriteBasispointsSSE(payload)
	}
	rewritten, changed := rewriteOfficeJSValue(gjson.ParseBytes(payload))
	if !changed {
		return payload
	}
	out, err := json.Marshal(rewritten)
	if err != nil {
		return payload
	}
	return out
}

func rewriteCompleteSSEEvents(pending []byte) (out, rest []byte) {
	for {
		idx := bytes.Index(pending, []byte("\n\n"))
		if idx < 0 {
			return out, pending
		}
		event := pending[:idx+2]
		pending = pending[idx+2:]
		out = append(out, rewriteBasispointsSSE(event)...)
	}
}

func rewriteBasispointsSSE(payload []byte) []byte {
	var b strings.Builder
	changed := false
	for _, block := range bytes.Split(payload, []byte("\n\n")) {
		if len(block) == 0 {
			continue
		}
		lines := bytes.Split(block, []byte("\n"))
		for i, line := range lines {
			if bytes.HasPrefix(line, []byte("data:")) {
				data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
				if len(data) > 0 && data[0] == '{' {
					rewritten, ok := rewriteOfficeJSValue(gjson.ParseBytes(data))
					if ok {
						encoded, err := json.Marshal(rewritten)
						if err == nil {
							lines[i] = append([]byte("data: "), encoded...)
							changed = true
						}
					}
				}
			}
		}
		b.Write(bytes.Join(lines, []byte("\n")))
		b.WriteString("\n\n")
	}
	if !changed {
		return payload
	}
	return []byte(b.String())
}

func rewriteOfficeJSValue(node gjson.Result) (any, bool) {
	if node.IsArray() {
		out := make([]any, 0, node.Get("#").Int())
		changed := false
		for _, item := range node.Array() {
			rewritten, ok := rewriteOfficeJSValue(item)
			out = append(out, rewritten)
			changed = changed || ok
		}
		return out, changed
	}
	if !node.IsObject() {
		var raw any
		if json.Unmarshal([]byte(node.Raw), &raw) != nil {
			return node.Value(), false
		}
		return raw, false
	}
	if node.Get("type").String() == "function_call" && node.Get("name").String() == officeJSToolName {
		rememberOfficeJSCall(node)
		if rewritten, ok := unwrapOfficeJSCall(node); ok {
			return rewritten, true
		}
	}
	out := make(map[string]any, len(node.Map()))
	changed := false
	node.ForEach(func(key, value gjson.Result) bool {
		rewritten, ok := rewriteOfficeJSValue(value)
		out[key.String()] = rewritten
		changed = changed || ok
		return true
	})
	return out, changed
}

func unwrapOfficeJSCall(node gjson.Result) (map[string]any, bool) {
	argsRaw := strings.TrimSpace(node.Get("arguments").Raw)
	if node.Get("arguments").Type == gjson.String {
		argsRaw = node.Get("arguments").String()
	}
	if strings.TrimSpace(argsRaw) == "" {
		return nil, false
	}
	code := node.Get("arguments.code")
	if !code.Exists() {
		code = gjson.Get(argsRaw, "code")
	}
	if code.Type == gjson.String {
		code = gjson.Parse(code.String())
	}
	payload, ok := parseOfficeJSCode(code.Raw)
	if !ok {
		payload, ok = parseOfficeJSCode(code.String())
	}
	if !ok {
		payload, ok = parseOfficeJSCode(argsRaw)
	}
	if !ok {
		return nil, false
	}
	tool := strings.TrimSpace(payload.Get("tool").String())
	if tool == "" || tool == officeJSToolName {
		return nil, false
	}
	args := payload.Get("args")
	encodedArgs := "{}"
	if args.Exists() {
		if args.IsObject() || args.IsArray() {
			encodedArgs = args.Raw
		} else if args.Type == gjson.String {
			encodedArgs = args.String()
		} else {
			encodedArgs = args.Raw
		}
	}
	out := map[string]any{
		"type":      "function_call",
		"name":      tool,
		"call_id":   node.Get("call_id").String(),
		"arguments": encodedArgs,
	}
	if id := strings.TrimSpace(node.Get("id").String()); id != "" {
		out["id"] = id
	}
	if node.Get("summary").Exists() {
		var summary any
		if json.Unmarshal([]byte(node.Get("summary").Raw), &summary) == nil {
			out["summary"] = summary
		}
	}
	if node.Get("references").Exists() {
		var refs any
		if json.Unmarshal([]byte(node.Get("references").Raw), &refs) == nil {
			out["references"] = refs
		}
	}
	return out, true
}

func parseOfficeJSCode(raw string) (gjson.Result, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return gjson.Result{}, false
	}
	parsed := gjson.Parse(raw)
	if parsed.IsObject() {
		return parsed, true
	}
	if parsed.Type == gjson.String {
		inner := gjson.Parse(parsed.String())
		if inner.IsObject() {
			return inner, true
		}
	}
	unquoted, err := strconv.Unquote(`"` + strings.ReplaceAll(raw, `"`, `\"`) + `"`)
	if err == nil {
		inner := gjson.Parse(unquoted)
		if inner.IsObject() {
			return inner, true
		}
	}
	return gjson.Result{}, false
}

func chatgptAccountID(credential CodexCredential) string {
	if id := strings.TrimSpace(credential.AccountID); id != "" {
		return id
	}
	id, _ := chatgptJWTAuth(credential.AccessToken)
	return id
}

func chatgptAccountUserID(credential CodexCredential) string {
	_, userID := chatgptJWTAuth(credential.AccessToken)
	return userID
}

func jwtChatGPTAccountID(token string) string {
	id, _ := chatgptJWTAuth(token)
	return id
}

func chatgptJWTAuth(token string) (accountID, accountUserID string) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return "", ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return "", ""
		}
	}
	var claims struct {
		Auth struct {
			ChatGPTAccountID     string `json:"chatgpt_account_id"`
			ChatGPTAccountUserID string `json:"chatgpt_account_user_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return "", ""
	}
	return strings.TrimSpace(claims.Auth.ChatGPTAccountID), strings.TrimSpace(claims.Auth.ChatGPTAccountUserID)
}

func clampBasispointsEffort(body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	for _, path := range []string{"reasoning.effort", "reasoning_effort"} {
		value := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, path).String()))
		switch value {
		case "max", "xhigh", "x-high", "ultra":
			updated, err := sjson.SetBytes(body, path, "high")
			if err == nil {
				body = updated
			}
		}
	}
	return body
}

func normalizeChatGPTExecutionError(err error) error {
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
	return &GrokExecutionError{
		status:  statusError.StatusCode(),
		summary: chatgptExecutionSummary(statusError.StatusCode()),
	}
}

func chatgptExecutionSummary(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return "ChatGPT authorization was rejected"
	case status == http.StatusForbidden:
		return "ChatGPT access was denied"
	case status == http.StatusTooManyRequests:
		return "ChatGPT upstream rate limit was reached"
	case status >= http.StatusInternalServerError:
		return "ChatGPT upstream service failed"
	case status >= http.StatusBadRequest:
		return "ChatGPT upstream request was rejected"
	default:
		return "ChatGPT upstream request failed"
	}
}
