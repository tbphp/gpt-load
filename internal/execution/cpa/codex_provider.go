package cpa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/subscription/providers/codex"
)

type codexProviderCredential struct {
	value codex.Credential
}

func (credential codexProviderCredential) redactionValues() []string {
	values := credential.value.SecretValues()
	return append(values, credential.value.AccountID, credential.value.Email)
}

type codexProviderBridge struct {
	executor codex.Executor
}

const localTokenCountHeader = "X-GPT-Load-Token-Count"

func newCodexProviderBridge() *codexProviderBridge {
	return &codexProviderBridge{executor: codex.NewExecutor()}
}

func (*codexProviderBridge) ProviderKind() channel.ProviderKind {
	return channel.ProviderCodex
}

func (*codexProviderBridge) UpstreamProtocol() protocol.Protocol {
	return protocol.OpenAIResponses
}

func (*codexProviderBridge) ValidateRouteCapability(route channel.RouteDescriptor) error {
	valid := route.ClientProtocol == protocol.OpenAIResponses &&
		(route.Operation == execution.OperationResponsesCreate ||
			route.Operation == execution.OperationResponsesInputTokens) &&
		route.RouteMode == execution.RouteNative
	if route.ClientProtocol == protocol.OpenAICompletions ||
		route.ClientProtocol == protocol.Anthropic ||
		route.ClientProtocol == protocol.Gemini {
		valid = route.Operation == execution.OperationChatCompletion &&
			route.RouteMode == execution.RouteConverted
		if route.ClientProtocol == protocol.Anthropic || route.ClientProtocol == protocol.Gemini {
			valid = valid || (route.Operation == execution.OperationCountTokens &&
				route.RouteMode == execution.RouteConverted)
		}
	}
	if !valid {
		return fmt.Errorf("route is not implemented by Codex")
	}
	return nil
}

func (bridge *codexProviderBridge) CountTokensLocal(
	ctx context.Context,
	request providerRequest,
) (providerResponse, error) {
	if bridge == nil || bridge.executor == nil {
		return providerResponse{}, errors.New("Codex provider bridge is unavailable")
	}
	response, err := bridge.executor.CountTokens(ctx, "local-token-count", codex.Credential{}, codex.ExecuteRequest{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: request.Format,
		Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		ProxyURL: request.ProxyURL, ProxyFromEnvironment: request.ProxyFromEnvironment,
	})
	headers := response.Headers.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	headers.Set(localTokenCountHeader, "local-estimate")
	return providerResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: headers,
		AppliedReasoningEffort: response.AppliedReasoningEffort,
	}, err
}

func (*codexProviderBridge) ValidateLocalTokenCount(request providerRequest) error {
	if !localTokenCountModelSupported(request.Model) {
		return errors.New("local token count tokenizer is unavailable for model")
	}
	var root map[string]any
	if err := json.Unmarshal(request.Payload, &root); err != nil || root == nil {
		return errors.New("local token count request is invalid")
	}
	switch request.Format {
	case "openai-response":
		return validateLocalResponsesTokenCount(root)
	case "claude":
		return validateLocalClaudeTokenCount(root)
	case "gemini":
		return validateLocalGeminiTokenCount(root)
	default:
		return errors.New("local token count request format is unsupported")
	}
}

func localTokenCountModelSupported(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "gpt-5") || strings.HasPrefix(model, "gpt-4.1") ||
		strings.HasPrefix(model, "gpt-4o") || strings.HasPrefix(model, "gpt-4") ||
		strings.HasPrefix(model, "gpt-3.5") || strings.HasPrefix(model, "gpt-3")
}

func validateLocalResponsesTokenCount(root map[string]any) error {
	if !onlyLocalTokenCountFields(root, "model", "input", "instructions") {
		return errors.New("local token count responses fields are unsupported")
	}
	if instructions, ok := root["instructions"]; ok && !isString(instructions) {
		return errors.New("local token count instructions must be text")
	}
	return validateLocalResponseInput(root["input"])
}

func validateLocalResponseInput(value any) error {
	if value == nil || isString(value) {
		return nil
	}
	items, ok := value.([]any)
	if !ok {
		return errors.New("local token count input must be text or messages")
	}
	for _, item := range items {
		message, ok := item.(map[string]any)
		if !ok || !onlyLocalTokenCountFields(message, "type", "role", "content") {
			return errors.New("local token count input item is unsupported")
		}
		if kind, exists := message["type"]; exists && (!isString(kind) || kind.(string) != "message") {
			return errors.New("local token count input item is unsupported")
		}
		if role, exists := message["role"]; exists && !isString(role) {
			return errors.New("local token count message role is invalid")
		}
		if err := validateLocalTextContent(message["content"], "input_text", "text"); err != nil {
			return err
		}
	}
	return nil
}

func validateLocalClaudeTokenCount(root map[string]any) error {
	if !onlyLocalTokenCountFields(root, "model", "system", "messages") {
		return errors.New("local token count Claude fields are unsupported")
	}
	if system, exists := root["system"]; exists {
		if err := validateLocalTextContent(system, "text"); err != nil {
			return err
		}
	}
	messages, exists := root["messages"]
	if !exists {
		return nil
	}
	items, ok := messages.([]any)
	if !ok {
		return errors.New("local token count messages must be an array")
	}
	for _, item := range items {
		message, ok := item.(map[string]any)
		if !ok || !onlyLocalTokenCountFields(message, "role", "content") {
			return errors.New("local token count message is unsupported")
		}
		if err := validateLocalTextContent(message["content"], "text"); err != nil {
			return err
		}
	}
	return nil
}

func validateLocalGeminiTokenCount(root map[string]any) error {
	if !onlyLocalTokenCountFields(root, "contents") {
		return errors.New("local token count Gemini fields are unsupported")
	}
	contents, exists := root["contents"]
	if !exists {
		return nil
	}
	items, ok := contents.([]any)
	if !ok {
		return errors.New("local token count contents must be an array")
	}
	for _, item := range items {
		content, ok := item.(map[string]any)
		if !ok || !onlyLocalTokenCountFields(content, "role", "parts") {
			return errors.New("local token count content is unsupported")
		}
		parts, ok := content["parts"].([]any)
		if !ok {
			return errors.New("local token count parts must be an array")
		}
		for _, part := range parts {
			partObject, ok := part.(map[string]any)
			if !ok || !onlyLocalTokenCountFields(partObject, "text") || !isString(partObject["text"]) {
				return errors.New("local token count part is unsupported")
			}
		}
	}
	return nil
}

func validateLocalTextContent(value any, allowedTypes ...string) error {
	if isString(value) {
		return nil
	}
	parts, ok := value.([]any)
	if !ok {
		return errors.New("local token count content must be text")
	}
	for _, part := range parts {
		partObject, ok := part.(map[string]any)
		if !ok || !onlyLocalTokenCountFields(partObject, "type", "text") || !isString(partObject["text"]) {
			return errors.New("local token count content is unsupported")
		}
		kind, ok := partObject["type"].(string)
		if !ok || !containsLocalTokenCountType(allowedTypes, kind) {
			return errors.New("local token count content is unsupported")
		}
	}
	return nil
}

func onlyLocalTokenCountFields(value map[string]any, allowed ...string) bool {
	for field := range value {
		if !containsLocalTokenCountType(allowed, field) {
			return false
		}
	}
	return true
}

func containsLocalTokenCountType(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func isString(value any) bool {
	_, ok := value.(string)
	return ok
}

func (*codexProviderBridge) ParseCredential(raw []byte) (providerCredential, error) {
	credential, err := codex.ParseCredentialJSON(raw)
	if err != nil {
		return nil, err
	}
	return codexProviderCredential{value: credential}, nil
}

func (bridge *codexProviderBridge) Execute(
	ctx context.Context,
	credentialID string,
	credential providerCredential,
	request providerRequest,
) (providerResponse, error) {
	codexCredential, ok := credential.(codexProviderCredential)
	if !ok || bridge == nil || bridge.executor == nil {
		return providerResponse{}, errors.New("Codex provider bridge credential mismatch")
	}
	response, err := bridge.executor.Execute(ctx, credentialID, codexCredential.value, codex.ExecuteRequest{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: request.Format,
		Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		ProxyURL: request.ProxyURL, ProxyFromEnvironment: request.ProxyFromEnvironment,
	})
	return providerResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
		AppliedReasoningEffort: response.AppliedReasoningEffort,
	}, err
}

func (bridge *codexProviderBridge) ExecuteStream(
	ctx context.Context,
	credentialID string,
	credential providerCredential,
	request providerRequest,
) (*providerStreamResponse, error) {
	codexCredential, ok := credential.(codexProviderCredential)
	if !ok || bridge == nil || bridge.executor == nil {
		return nil, errors.New("Codex provider bridge credential mismatch")
	}
	response, err := bridge.executor.ExecuteStream(ctx, credentialID, codexCredential.value, codex.ExecuteRequest{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: request.Format,
		Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		ProxyURL: request.ProxyURL, ProxyFromEnvironment: request.ProxyFromEnvironment,
	})
	if response == nil {
		return nil, err
	}
	chunks := make(chan providerStreamChunk)
	go func() {
		defer close(chunks)
		for chunk := range response.Chunks {
			converted := providerStreamChunk{Payload: append([]byte(nil), chunk.Payload...), Err: chunk.Err}
			select {
			case chunks <- converted:
			case <-ctx.Done():
				return
			}
		}
	}()
	return &providerStreamResponse{
		Headers: response.Headers.Clone(), Chunks: chunks,
		AppliedReasoningEffort: response.AppliedReasoningEffort,
	}, err
}

func (*codexProviderBridge) ClassifyError(
	ctx context.Context,
	err error,
	credential providerCredential,
) (int, *execution.ErrorEvidence) {
	if err == nil {
		return 0, nil
	}
	status := 0
	if value, ok := err.(interface{ StatusCode() int }); ok {
		status = value.StatusCode()
	}
	kind := execution.ErrorKindTransport
	if status != 0 {
		kind = execution.ErrorKindHTTP
	} else if errors.Is(err, context.DeadlineExceeded) || ctx != nil && errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
		kind = execution.ErrorKindTimeout
	} else if errors.Is(err, context.Canceled) || ctx != nil && errors.Is(context.Cause(ctx), context.Canceled) {
		kind = execution.ErrorKindCanceled
	} else if _, ok := err.(net.Error); ok {
		kind = execution.ErrorKindTransport
	}
	typeValue, codeValue := errorTypeCode(err.Error())
	evidence := &execution.ErrorEvidence{
		Kind: kind, StatusCode: status, Type: typeValue, Code: codeValue,
		Summary: safeErrorSummary(err, credential.redactionValues()),
	}
	if retry, ok := err.(interface{ RetryAfter() *time.Duration }); ok && retry.RetryAfter() != nil && *retry.RetryAfter() > 0 {
		evidence.RetryAfter = *retry.RetryAfter()
	}
	switch {
	case status == http.StatusUnauthorized:
		evidence.Hint = execution.FailureHintRefreshRequired
		evidence.ReplaySafety = execution.ReplaySafetyRejectedBeforeProcessing
	case status == http.StatusTooManyRequests:
		evidence.Hint = execution.FailureHintRateLimited
	default:
		if status >= 500 {
			evidence.Hint = execution.FailureHintHostError
		}
	}
	return status, evidence
}

var _ providerBridge = (*codexProviderBridge)(nil)
var _ providerLocalTokenCounter = (*codexProviderBridge)(nil)
