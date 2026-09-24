package cpa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/subscription/providers/chatgpt"
	"gpt-load/internal/subscription/providers/codex"
)

type chatgptProviderCredential struct{ value codex.Credential }

func (credential chatgptProviderCredential) redactionValues() []string {
	values := credential.value.SecretValues()
	return append(values, credential.value.AccountID, credential.value.Email)
}

type chatgptProviderBridge struct{ executor chatgpt.Executor }

func newChatGPTProviderBridge() *chatgptProviderBridge {
	return &chatgptProviderBridge{executor: chatgpt.NewExecutor()}
}

func (*chatgptProviderBridge) ProviderKind() channel.ProviderKind  { return channel.ProviderChatGPT }
func (*chatgptProviderBridge) UpstreamProtocol() protocol.Protocol { return protocol.OpenAIResponses }

func (*chatgptProviderBridge) ValidateRouteCapability(route channel.RouteDescriptor) error {
	valid := route.ClientProtocol == protocol.OpenAIResponses &&
		(route.Operation == execution.OperationResponsesCreate ||
			route.Operation == execution.OperationResponsesInputTokens) &&
		route.RouteMode == execution.RouteNative
	if route.ClientProtocol == protocol.OpenAICompletions {
		valid = route.Operation == execution.OperationChatCompletion && route.RouteMode == execution.RouteConverted
	}
	if route.ClientProtocol == protocol.Anthropic || route.ClientProtocol == protocol.Gemini {
		valid = (route.Operation == execution.OperationChatCompletion || route.Operation == execution.OperationCountTokens) &&
			route.RouteMode == execution.RouteConverted
	}
	if !valid {
		return fmt.Errorf("route is not implemented by ChatGPT")
	}
	return nil
}

func (*chatgptProviderBridge) ParseCredential(raw []byte) (providerCredential, error) {
	credential, err := codex.ParseCredentialJSON(raw)
	if err != nil {
		return nil, err
	}
	return chatgptProviderCredential{value: credential}, nil
}

func (bridge *chatgptProviderBridge) Execute(
	ctx context.Context,
	credentialID string,
	credential providerCredential,
	request providerRequest,
) (providerResponse, error) {
	value, ok := credential.(chatgptProviderCredential)
	if !ok || bridge == nil || bridge.executor == nil {
		return providerResponse{}, errors.New("ChatGPT provider bridge credential mismatch")
	}
	response, err := bridge.executor.Execute(ctx, credentialID, value.value, chatgptRequest(request))
	return providerResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
		AppliedReasoningEffort: response.AppliedReasoningEffort,
	}, err
}

func (bridge *chatgptProviderBridge) ExecuteStream(
	ctx context.Context,
	credentialID string,
	credential providerCredential,
	request providerRequest,
) (*providerStreamResponse, error) {
	value, ok := credential.(chatgptProviderCredential)
	if !ok || bridge == nil || bridge.executor == nil {
		return nil, errors.New("ChatGPT provider bridge credential mismatch")
	}
	response, err := bridge.executor.ExecuteStream(ctx, credentialID, value.value, chatgptRequest(request))
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
	}, nil
}

func (bridge *chatgptProviderBridge) ValidateLocalTokenCount(request providerRequest) error {
	if !localTokenCountModelSupported(request.Model) {
		return errors.New("ChatGPT local token count tokenizer is unavailable for model")
	}
	var root map[string]any
	if err := json.Unmarshal(request.Payload, &root); err != nil || root == nil {
		return errors.New("ChatGPT local token count request is invalid")
	}
	switch request.Format {
	case "openai-response":
		return validateLocalResponsesTokenCount(root)
	case "claude":
		return validateLocalClaudeTokenCount(root)
	case "gemini":
		return validateLocalGeminiTokenCount(root)
	default:
		return errors.New("ChatGPT local token count format is unsupported")
	}
}

func (bridge *chatgptProviderBridge) CountTokensLocal(
	ctx context.Context,
	request providerRequest,
) (providerResponse, error) {
	if bridge == nil || bridge.executor == nil {
		return providerResponse{}, errors.New("ChatGPT provider bridge is unavailable")
	}
	response, err := bridge.executor.CountTokens(ctx, chatgptRequest(request))
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

func chatgptRequest(request providerRequest) chatgpt.ExecuteRequest {
	return chatgpt.ExecuteRequest{
		AttemptID: request.AttemptID, Model: request.Model,
		Payload: append([]byte(nil), request.Payload...), Format: request.Format,
		Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		ContinuityKey: request.ContinuityKey,
		BaseURL:       request.BaseURL, ProxyURL: request.ProxyURL,
	}
}

func (*chatgptProviderBridge) ClassifyError(
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
	code := ""
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) && coded != nil {
		code = safeScalar(coded.ErrorCode())
	}
	evidence := &execution.ErrorEvidence{
		Kind: kind, StatusCode: status, Code: code,
		Summary: chatgptProviderErrorSummary(status),
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
	case status == http.StatusForbidden || status == http.StatusPaymentRequired:
		evidence.Hint = execution.FailureHintCandidateUnavailable
		evidence.ReplaySafety = execution.ReplaySafetyRejectedBeforeProcessing
	case status == http.StatusTooManyRequests:
		evidence.Hint = execution.FailureHintRateLimited
	case status >= http.StatusInternalServerError:
		evidence.Hint = execution.FailureHintHostError
	}
	if evidence.Summary == "" {
		evidence.Summary = safeErrorSummary(err, credential.redactionValues())
	}
	annotateProviderErrorEvidence(evidence, err)
	return status, evidence
}

func chatgptProviderErrorSummary(status int) string {
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

var _ providerBridge = (*chatgptProviderBridge)(nil)
var _ providerLocalTokenCounter = (*chatgptProviderBridge)(nil)
