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
	"gpt-load/internal/subscription/providers/grok"
)

type grokProviderCredential struct{ value grok.Credential }

func (credential grokProviderCredential) redactionValues() []string {
	return credential.value.SecretValues()
}

type grokProviderBridge struct{ executor grok.Executor }

func newGrokProviderBridge() *grokProviderBridge {
	return &grokProviderBridge{executor: grok.NewExecutor()}
}

func (*grokProviderBridge) ProviderKind() channel.ProviderKind  { return channel.ProviderGrok }
func (*grokProviderBridge) UpstreamProtocol() protocol.Protocol { return protocol.OpenAIResponses }

func (*grokProviderBridge) ValidateRouteCapability(route channel.RouteDescriptor) error {
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
		return fmt.Errorf("route is not implemented by Grok")
	}
	return nil
}

func (*grokProviderBridge) ParseCredential(raw []byte) (providerCredential, error) {
	credential, err := grok.ParseCredentialJSON(raw)
	if err != nil {
		return nil, err
	}
	return grokProviderCredential{value: credential}, nil
}

func (bridge *grokProviderBridge) Execute(
	ctx context.Context,
	credentialID string,
	credential providerCredential,
	request providerRequest,
) (providerResponse, error) {
	value, ok := credential.(grokProviderCredential)
	if !ok || bridge == nil || bridge.executor == nil {
		return providerResponse{}, errors.New("Grok provider bridge credential mismatch")
	}
	response, err := bridge.executor.Execute(ctx, credentialID, value.value, grokRequest(request, credentialID))
	return providerResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
		AppliedReasoningEffort: response.AppliedReasoningEffort,
	}, err
}

func (bridge *grokProviderBridge) ExecuteStream(
	ctx context.Context,
	credentialID string,
	credential providerCredential,
	request providerRequest,
) (*providerStreamResponse, error) {
	value, ok := credential.(grokProviderCredential)
	if !ok || bridge == nil || bridge.executor == nil {
		return nil, errors.New("Grok provider bridge credential mismatch")
	}
	response, err := bridge.executor.ExecuteStream(ctx, credentialID, value.value, grokRequest(request, credentialID))
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

func (bridge *grokProviderBridge) ValidateLocalTokenCount(request providerRequest) error {
	if !grokLocalTokenCountModelSupported(request.Model) {
		return errors.New("Grok local token count tokenizer is unavailable for model")
	}
	var root map[string]any
	if err := json.Unmarshal(request.Payload, &root); err != nil || root == nil {
		return errors.New("Grok local token count request is invalid")
	}
	switch request.Format {
	case "openai-response":
		return validateLocalResponsesTokenCount(root)
	case "claude":
		return validateLocalClaudeTokenCount(root)
	case "gemini":
		return validateLocalGeminiTokenCount(root)
	default:
		return errors.New("Grok local token count format is unsupported")
	}
}

func grokLocalTokenCountModelSupported(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "grok-")
}

func (bridge *grokProviderBridge) CountTokensLocal(
	ctx context.Context,
	request providerRequest,
) (providerResponse, error) {
	if bridge == nil || bridge.executor == nil {
		return providerResponse{}, errors.New("Grok provider bridge is unavailable")
	}
	response, err := bridge.executor.CountTokens(ctx, grok.ExecuteRequest{
		AttemptID: request.AttemptID, Model: request.Model,
		Payload: append([]byte(nil), request.Payload...), Format: request.Format,
		Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
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

func grokRequest(request providerRequest, credentialID string) grok.ExecuteRequest {
	return grok.ExecuteRequest{
		AttemptID: request.AttemptID, Model: request.Model,
		Payload: append([]byte(nil), request.Payload...), Format: request.Format,
		Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		ContinuityKey: grokContinuityScope(request.ContinuityKey, credentialID, request.Model, request.AttemptID),
	}
}

func grokContinuityScope(base, credentialID, model, attemptID string) string {
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
		return strings.Join([]string{"gpt-load-grok-attempt", attemptID, credentialID, model}, "\x00")
	}
	return strings.Join([]string{"gpt-load-grok", base, credentialID, model}, "\x00")
}

func (*grokProviderBridge) ClassifyError(
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
		Summary: grokProviderErrorSummary(status, code),
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
	case status == http.StatusBadRequest:
		evidence.Hint = execution.FailureHintRequestRejected
	case status == http.StatusTooManyRequests:
		evidence.Hint = execution.FailureHintRateLimited
	case status >= http.StatusInternalServerError:
		evidence.Hint = execution.FailureHintHostError
	}
	if evidence.Summary == "" {
		evidence.Summary = safeErrorSummary(err, credential.redactionValues())
	}
	return status, evidence
}

func grokProviderErrorSummary(status int, code string) string {
	switch {
	case status == http.StatusUnauthorized:
		return "Grok authorization was rejected"
	case status == http.StatusForbidden:
		return "Grok access was denied"
	case status == http.StatusTooManyRequests && strings.Contains(strings.ToLower(code), "free-usage-exhausted"):
		return "Grok included free usage is exhausted"
	case status == http.StatusTooManyRequests:
		return "Grok upstream rate limit was reached"
	case status >= http.StatusInternalServerError:
		return "Grok upstream service failed"
	case status >= http.StatusBadRequest:
		return "Grok upstream request was rejected"
	default:
		return "Grok upstream request failed"
	}
}

var _ providerBridge = (*grokProviderBridge)(nil)
var _ providerLocalTokenCounter = (*grokProviderBridge)(nil)
