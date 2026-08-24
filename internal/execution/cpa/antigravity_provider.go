package cpa

import (
	"bytes"
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
	"gpt-load/internal/execution/responsealias"
	"gpt-load/internal/protocol"
	"gpt-load/internal/subscription/providers/antigravity"
)

type antigravityProviderCredential struct{ value antigravity.Credential }

func (credential antigravityProviderCredential) redactionValues() []string {
	return credential.value.SecretValues()
}

type antigravityProviderBridge struct{ executor antigravity.Executor }

func newAntigravityProviderBridge() *antigravityProviderBridge {
	return &antigravityProviderBridge{executor: antigravity.NewExecutor()}
}

func (*antigravityProviderBridge) ProviderKind() channel.ProviderKind {
	return channel.ProviderAntigravity
}

func (*antigravityProviderBridge) UpstreamProtocol() protocol.Protocol { return protocol.Gemini }

func (*antigravityProviderBridge) ValidateRouteCapability(route channel.RouteDescriptor) error {
	valid := route.ClientProtocol == protocol.Gemini &&
		(route.Operation == execution.OperationChatCompletion || route.Operation == execution.OperationCountTokens) &&
		route.RouteMode == execution.RouteNative
	if route.ClientProtocol == protocol.Anthropic {
		valid = (route.Operation == execution.OperationChatCompletion || route.Operation == execution.OperationCountTokens) &&
			route.RouteMode == execution.RouteConverted
	}
	if route.ClientProtocol == protocol.OpenAICompletions {
		valid = route.Operation == execution.OperationChatCompletion && route.RouteMode == execution.RouteConverted
	}
	if route.ClientProtocol == protocol.OpenAIResponses {
		valid = (route.Operation == execution.OperationResponsesCreate || route.Operation == execution.OperationResponsesInputTokens) &&
			route.RouteMode == execution.RouteConverted
	}
	if !valid {
		return fmt.Errorf("route is not implemented by Antigravity")
	}
	return nil
}

func (*antigravityProviderBridge) ParseCredential(raw []byte) (providerCredential, error) {
	credential, err := antigravity.ParseCredentialJSON(raw)
	if err != nil {
		return nil, err
	}
	return antigravityProviderCredential{value: credential}, nil
}

// ValidateRequest rejects only inputs CPA is known to silently omit or change
// for Antigravity. General protocol validation stays with the existing
// dialect layer so this does not become a second request schema.
func (*antigravityProviderBridge) ValidateRequest(request providerRequest) error {
	if strings.TrimSpace(request.Format) == "openai-response" &&
		strings.Contains(strings.ToLower(strings.TrimSpace(request.Model)), "image") {
		return errors.New("Antigravity does not support Responses image output")
	}
	var payload any
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		return errors.New("Antigravity request payload is invalid")
	}
	if strings.TrimSpace(request.Format) == "openai-response" {
		if root, ok := payload.(map[string]any); ok {
			if tools, ok := root["tools"].([]any); ok {
				for _, rawTool := range tools {
					tool, ok := rawTool.(map[string]any)
					if !ok {
						continue
					}
					toolType, _ := tool["type"].(string)
					if toolType = strings.TrimSpace(toolType); toolType != "" && toolType != "function" {
						return errors.New("Antigravity does not support Responses built-in tools")
					}
				}
			}
		}
	}
	if antigravityPayloadContainsRemoteImage(payload, strings.TrimSpace(request.Format)) {
		return errors.New("Antigravity does not support remote image URLs")
	}
	return nil
}

func antigravityPayloadContainsRemoteImage(value any, format string) bool {
	switch current := value.(type) {
	case []any:
		for _, item := range current {
			if antigravityPayloadContainsRemoteImage(item, format) {
				return true
			}
		}
	case map[string]any:
		itemType, _ := current["type"].(string)
		itemType = strings.TrimSpace(itemType)
		if format == "openai" || format == "openai-response" {
			if itemType == "image_url" || itemType == "input_image" {
				if antigravityRemoteImageURL(current["image_url"]) {
					return true
				}
			}
		}
		if format == "claude" && itemType == "image" {
			if source, ok := current["source"].(map[string]any); ok {
				sourceType, _ := source["type"].(string)
				if strings.EqualFold(strings.TrimSpace(sourceType), "url") && antigravityRemoteImageURL(source["url"]) {
					return true
				}
			}
		}
		for _, child := range current {
			if antigravityPayloadContainsRemoteImage(child, format) {
				return true
			}
		}
	}
	return false
}

func antigravityRemoteImageURL(value any) bool {
	urlValue, ok := value.(string)
	if !ok {
		if object, objectOK := value.(map[string]any); objectOK {
			urlValue, ok = object["url"].(string)
		}
	}
	urlValue = strings.ToLower(strings.TrimSpace(urlValue))
	return ok && (strings.HasPrefix(urlValue, "https://") || strings.HasPrefix(urlValue, "http://"))
}

func (bridge *antigravityProviderBridge) Execute(
	ctx context.Context,
	credentialID string,
	credential providerCredential,
	request providerRequest,
) (providerResponse, error) {
	value, ok := credential.(antigravityProviderCredential)
	if !ok || bridge == nil || bridge.executor == nil {
		return providerResponse{}, errors.New("Antigravity provider bridge credential mismatch")
	}
	response, err := bridge.executor.Execute(ctx, credentialID, value.value, antigravity.ExecuteRequest{
		AttemptID: request.AttemptID, Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: request.Format,
		Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		ContinuityKey: antigravityContinuityScope(request.ContinuityKey, credentialID, request.Model, request.AttemptID),
		ProxyURL:      request.ProxyURL,
	})
	if err == nil {
		response.Payload, err = normalizeAntigravityResponseModel(request.Format, response.Payload, request.Model)
	}
	return providerResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
		AppliedReasoningEffort: response.AppliedReasoningEffort,
	}, err
}

func (bridge *antigravityProviderBridge) CountTokens(
	ctx context.Context,
	credentialID string,
	credential providerCredential,
	request providerRequest,
) (providerResponse, error) {
	value, ok := credential.(antigravityProviderCredential)
	if !ok || bridge == nil || bridge.executor == nil {
		return providerResponse{}, errors.New("Antigravity provider bridge credential mismatch")
	}
	response, err := bridge.executor.CountTokens(ctx, credentialID, value.value, antigravity.ExecuteRequest{
		AttemptID: request.AttemptID, Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: request.Format,
		Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		ContinuityKey: antigravityContinuityScope(request.ContinuityKey, credentialID, request.Model, request.AttemptID),
		ProxyURL:      request.ProxyURL,
	})
	return providerResponse{Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone()}, err
}

func (bridge *antigravityProviderBridge) ExecuteStream(
	ctx context.Context,
	credentialID string,
	credential providerCredential,
	request providerRequest,
) (*providerStreamResponse, error) {
	value, ok := credential.(antigravityProviderCredential)
	if !ok || bridge == nil || bridge.executor == nil {
		return nil, errors.New("Antigravity provider bridge credential mismatch")
	}
	response, err := bridge.executor.ExecuteStream(ctx, credentialID, value.value, antigravity.ExecuteRequest{
		AttemptID: request.AttemptID, Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: request.Format,
		Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		ContinuityKey: antigravityContinuityScope(request.ContinuityKey, credentialID, request.Model, request.AttemptID),
		ProxyURL:      request.ProxyURL,
	})
	if response == nil {
		return nil, err
	}
	if err != nil {
		return &providerStreamResponse{
			Headers: response.Headers.Clone(), AppliedReasoningEffort: response.AppliedReasoningEffort,
		}, err
	}
	chunks := make(chan providerStreamChunk)
	go func() {
		defer close(chunks)
		for chunk := range response.Chunks {
			converted := providerStreamChunk{Payload: append([]byte(nil), chunk.Payload...), Err: chunk.Err}
			if converted.Err == nil {
				converted.Payload, converted.Err = normalizeAntigravityResponseModel(request.Format, converted.Payload, request.Model)
			}
			select {
			case chunks <- converted:
			case <-ctx.Done():
				return
			}
		}
	}()
	return &providerStreamResponse{
		Headers: response.Headers.Clone(), Chunks: chunks, AppliedReasoningEffort: response.AppliedReasoningEffort,
	}, nil
}

func normalizeAntigravityResponseModel(format string, payload []byte, model string) ([]byte, error) {
	if len(payload) == 0 || strings.TrimSpace(model) == "" {
		return bytes.Clone(payload), nil
	}
	if !antigravityResponseUsesPlaceholderModel(payload) {
		return bytes.Clone(payload), nil
	}
	var clientProtocol protocol.Protocol
	switch strings.TrimSpace(format) {
	case "openai":
		clientProtocol = protocol.OpenAICompletions
	case "openai-response":
		clientProtocol = protocol.OpenAIResponses
	case "claude":
		clientProtocol = protocol.Anthropic
	case "gemini":
		clientProtocol = protocol.Gemini
	default:
		return nil, fmt.Errorf("unsupported Antigravity response format")
	}
	trimmed := bytes.TrimSpace(payload)
	if json.Valid(trimmed) {
		return responsealias.RewriteJSON(clientProtocol, payload, model)
	}
	if bytes.Equal(trimmed, []byte("[DONE]")) {
		return bytes.Clone(payload), nil
	}
	return responsealias.RewriteSSE(clientProtocol, payload, model)
}

func antigravityResponseUsesPlaceholderModel(payload []byte) bool {
	trimmed := bytes.TrimSpace(payload)
	if json.Valid(trimmed) {
		return antigravityJSONUsesPlaceholderModel(trimmed)
	}
	for _, line := range bytes.Split(payload, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if json.Valid(data) && antigravityJSONUsesPlaceholderModel(data) {
			return true
		}
	}
	return false
}

func antigravityJSONUsesPlaceholderModel(payload []byte) bool {
	var root map[string]json.RawMessage
	if json.Unmarshal(payload, &root) != nil || root == nil {
		return false
	}
	if antigravityRawModelIsPlaceholder(root["model"]) || antigravityRawModelIsPlaceholder(root["modelVersion"]) {
		return true
	}
	for _, field := range []string{"message", "response"} {
		var nested map[string]json.RawMessage
		if json.Unmarshal(root[field], &nested) == nil && antigravityRawModelIsPlaceholder(nested["model"]) {
			return true
		}
	}
	return false
}

func antigravityRawModelIsPlaceholder(raw json.RawMessage) bool {
	var value string
	return json.Unmarshal(raw, &value) == nil && strings.EqualFold(strings.TrimSpace(value), "gemini-default")
}

func (*antigravityProviderBridge) ClassifyError(
	ctx context.Context,
	err error,
	credential providerCredential,
) (int, *execution.ErrorEvidence) {
	if err == nil {
		return 0, nil
	}
	status := 0
	var statusError interface{ StatusCode() int }
	if errors.As(err, &statusError) && statusError != nil {
		status = statusError.StatusCode()
	}
	kind := execution.ErrorKindTransport
	if errors.Is(err, context.DeadlineExceeded) || ctx != nil && errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
		kind = execution.ErrorKindTimeout
	} else if errors.Is(err, context.Canceled) || ctx != nil && errors.Is(context.Cause(ctx), context.Canceled) {
		kind = execution.ErrorKindCanceled
	} else if status != 0 {
		kind = execution.ErrorKindHTTP
	} else if _, ok := err.(net.Error); ok {
		kind = execution.ErrorKindTransport
	}
	typeValue, codeValue := antigravityErrorTypeCode(err)
	evidence := &execution.ErrorEvidence{
		Kind: kind, StatusCode: status, Type: typeValue, Code: codeValue,
		Summary: antigravityProviderErrorSummary(status, typeValue, codeValue),
	}
	var retry interface{ RetryAfter() *time.Duration }
	if errors.As(err, &retry) && retry != nil {
		if value := retry.RetryAfter(); value != nil && *value > 0 {
			evidence.RetryAfter = *value
		}
	}
	switch {
	case status == http.StatusUnauthorized:
		evidence.Hint = execution.FailureHintRefreshRequired
		evidence.ReplaySafety = execution.ReplaySafetyRejectedBeforeProcessing
	case status == http.StatusForbidden:
		evidence.Hint = execution.FailureHintCandidateUnavailable
		evidence.ReplaySafety = execution.ReplaySafetyRejectedBeforeProcessing
	case status == http.StatusTooManyRequests && strings.EqualFold(codeValue, "INSUFFICIENT_G1_CREDITS_BALANCE"):
		evidence.Hint = execution.FailureHintRateLimited
		evidence.ReplaySafety = execution.ReplaySafetyRejectedBeforeProcessing
	case requestScopedFailure(err):
		evidence.Hint = execution.FailureHintRequestRejected
	case status == http.StatusTooManyRequests:
		evidence.Hint = execution.FailureHintRateLimited
	case status >= http.StatusInternalServerError:
		evidence.Hint = execution.FailureHintHostError
	}
	return status, evidence
}

func antigravityProviderErrorSummary(status int, typeValue, codeValue string) string {
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
	case status >= http.StatusBadRequest:
		return "Antigravity upstream request was rejected"
	case typeValue != "":
		return "Antigravity upstream request failed"
	default:
		return "Antigravity upstream request failed"
	}
}

func antigravityErrorTypeCode(err error) (string, string) {
	var typed interface {
		ErrorType() string
		ErrorCode() string
	}
	if errors.As(err, &typed) && typed != nil {
		return safeScalar(typed.ErrorType()), safeScalar(typed.ErrorCode())
	}
	return errorTypeCode(strings.TrimSpace(err.Error()))
}

func antigravityContinuityScope(base, credentialID, model, attemptID string) string {
	base = strings.TrimSpace(base)
	credentialID = strings.TrimSpace(credentialID)
	model = strings.TrimSpace(model)
	attemptID = strings.TrimSpace(attemptID)
	if credentialID == "" || model == "" {
		return ""
	}
	if base == "" {
		if attemptID == "" {
			return ""
		}
		return strings.Join([]string{"gpt-load-antigravity-attempt", attemptID, credentialID, model}, "\x00")
	}
	return strings.Join([]string{"gpt-load-antigravity", base, credentialID, model}, "\x00")
}

var _ providerBridge = (*antigravityProviderBridge)(nil)
var _ providerTokenCounter = (*antigravityProviderBridge)(nil)
var _ providerRequestValidator = (*antigravityProviderBridge)(nil)
