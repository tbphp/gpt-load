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
	"gpt-load/internal/subscription/providers/muse"
)

type museProviderCredential struct{ value muse.Credential }

func (credential museProviderCredential) redactionValues() []string {
	return credential.value.SecretValues()
}

type museProviderBridge struct{ executor muse.Executor }

func newMuseProviderBridge() *museProviderBridge {
	return &museProviderBridge{executor: muse.NewExecutor()}
}

func (*museProviderBridge) ProviderKind() channel.ProviderKind  { return channel.ProviderMuse }
func (*museProviderBridge) UpstreamProtocol() protocol.Protocol { return protocol.OpenAIResponses }

func (*museProviderBridge) ValidateRouteCapability(route channel.RouteDescriptor) error {
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
		return fmt.Errorf("route is not implemented by Muse")
	}
	return nil
}

func (*museProviderBridge) ParseCredential(raw []byte) (providerCredential, error) {
	credential, err := muse.ParseCredentialJSON(raw)
	if err != nil {
		return nil, err
	}
	return museProviderCredential{value: credential}, nil
}

func (bridge *museProviderBridge) Execute(
	ctx context.Context,
	credentialID string,
	credential providerCredential,
	request providerRequest,
) (providerResponse, error) {
	value, ok := credential.(museProviderCredential)
	if !ok || bridge == nil || bridge.executor == nil {
		return providerResponse{}, errors.New("Muse provider bridge credential mismatch")
	}
	response, err := bridge.executor.Execute(ctx, credentialID, value.value, museRequest(request, credentialID))
	return providerResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
		AppliedReasoningEffort:       response.AppliedReasoningEffort,
		AppliedReasoningMode:         response.AppliedReasoningMode,
		AppliedReasoningBudgetTokens: response.AppliedReasoningBudgetTokens,
	}, err
}

func (bridge *museProviderBridge) ExecuteStream(
	ctx context.Context,
	credentialID string,
	credential providerCredential,
	request providerRequest,
) (*providerStreamResponse, error) {
	value, ok := credential.(museProviderCredential)
	if !ok || bridge == nil || bridge.executor == nil {
		return nil, errors.New("Muse provider bridge credential mismatch")
	}
	response, err := bridge.executor.ExecuteStream(ctx, credentialID, value.value, museRequest(request, credentialID))
	if response == nil {
		return nil, err
	}
	if err != nil {
		return &providerStreamResponse{
			Headers: response.Headers.Clone(), AppliedReasoningEffort: response.AppliedReasoningEffort,
			AppliedReasoningMode:         response.AppliedReasoningMode,
			AppliedReasoningBudgetTokens: response.AppliedReasoningBudgetTokens,
		}, err
	}
	chunks := make(chan providerStreamChunk)
	go func() {
		defer close(chunks)
		for chunk := range response.Chunks {
			converted := providerStreamChunk{Payload: append([]byte(nil), chunk.Payload...), Incomplete: chunk.Incomplete, Err: chunk.Err}
			select {
			case chunks <- converted:
			case <-ctx.Done():
				return
			}
		}
	}()
	return &providerStreamResponse{
		Headers: response.Headers.Clone(), Chunks: chunks,
		AppliedReasoningEffort:       response.AppliedReasoningEffort,
		AppliedReasoningMode:         response.AppliedReasoningMode,
		AppliedReasoningBudgetTokens: response.AppliedReasoningBudgetTokens,
	}, nil
}

func (bridge *museProviderBridge) ValidateLocalTokenCount(request providerRequest) error {
	if !museLocalTokenCountModelSupported(request.Model) {
		return errors.New("Muse local token count tokenizer is unavailable for model")
	}
	var root map[string]any
	if err := json.Unmarshal(request.Payload, &root); err != nil || root == nil {
		return errors.New("Muse local token count request is invalid")
	}
	switch request.Format {
	case "openai-response":
		return validateLocalResponsesTokenCount(root)
	case "claude":
		return validateLocalClaudeTokenCount(root)
	case "gemini":
		return validateLocalGeminiTokenCount(root)
	default:
		return errors.New("Muse local token count format is unsupported")
	}
}

func museLocalTokenCountModelSupported(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "muse-")
}

func (bridge *museProviderBridge) CountTokensLocal(
	ctx context.Context,
	request providerRequest,
) (providerResponse, error) {
	if bridge == nil || bridge.executor == nil {
		return providerResponse{}, errors.New("Muse provider bridge is unavailable")
	}
	response, err := bridge.executor.CountTokens(ctx, muse.ExecuteRequest{
		AttemptID: request.AttemptID, Model: request.Model,
		Payload: append([]byte(nil), request.Payload...), Format: request.Format,
		Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		BaseURL: request.BaseURL, ProxyURL: request.ProxyURL,
	})
	headers := response.Headers.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	headers.Set(localTokenCountHeader, "local-estimate")
	return providerResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: headers,
		AppliedReasoningEffort:       response.AppliedReasoningEffort,
		AppliedReasoningMode:         response.AppliedReasoningMode,
		AppliedReasoningBudgetTokens: response.AppliedReasoningBudgetTokens,
	}, err
}

func museRequest(request providerRequest, credentialID string) muse.ExecuteRequest {
	return muse.ExecuteRequest{
		AttemptID: request.AttemptID, Model: request.Model,
		Payload: append([]byte(nil), request.Payload...), Format: request.Format,
		Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		ContinuityKey: museContinuityScope(request.ContinuityKey, credentialID, request.Model, request.AttemptID),
		BaseURL:       request.BaseURL, ProxyURL: request.ProxyURL, ProxyFromEnvironment: request.ProxyFromEnvironment, ConfiguredHeaders: append([]string(nil), request.ConfiguredHeaders...),
	}
}

func museContinuityScope(base, credentialID, model, attemptID string) string {
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
		return strings.Join([]string{"gpt-load-muse-attempt", attemptID, credentialID, model}, "\x00")
	}
	return strings.Join([]string{"gpt-load-muse", base, credentialID, model}, "\x00")
}

func (*museProviderBridge) ClassifyError(
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
		Summary: museProviderErrorSummary(status, code),
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
	var scoped interface{ IsCredentialScoped() bool }
	if errors.As(err, &scoped) && scoped.IsCredentialScoped() {
		evidence.ScopeHint = execution.ErrorScopeCredential
	}
	return status, evidence
}

func museProviderErrorSummary(status int, code string) string {
	switch {
	case status == http.StatusUnauthorized:
		return "Muse authorization was rejected"
	case status == http.StatusForbidden:
		return "Muse access was denied"
	case status == http.StatusTooManyRequests:
		return "Muse upstream rate limit was reached"
	case status >= http.StatusInternalServerError:
		return "Muse upstream service failed"
	case status >= http.StatusBadRequest:
		return "Muse upstream request was rejected"
	default:
		return "Muse upstream request failed"
	}
}

var _ providerBridge = (*museProviderBridge)(nil)
var _ providerLocalTokenCounter = (*museProviderBridge)(nil)
