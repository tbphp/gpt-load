package cpa

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/subscription/providers/devin"
)

type devinProviderCredential struct{ value devin.Credential }

func (credential devinProviderCredential) redactionValues() []string {
	return credential.value.SecretValues()
}

type devinProviderBridge struct{ executor devin.Executor }

func newDevinProviderBridge() *devinProviderBridge {
	return &devinProviderBridge{executor: devin.NewExecutor()}
}

func (*devinProviderBridge) ProviderKind() channel.ProviderKind { return channel.ProviderDevin }

// 私有 Connect-RPC 没有公开协议标识，日志保留未知值，不冒充某个标准协议。
func (*devinProviderBridge) UpstreamProtocol() protocol.Protocol { return "" }

func (*devinProviderBridge) ValidateRouteCapability(route channel.RouteDescriptor) error {
	valid := route.RouteMode == execution.RouteConverted && ((route.ClientProtocol == protocol.OpenAIResponses && route.Operation == execution.OperationResponsesCreate) ||
		((route.ClientProtocol == protocol.OpenAICompletions || route.ClientProtocol == protocol.Anthropic || route.ClientProtocol == protocol.Gemini) && route.Operation == execution.OperationChatCompletion))
	if !valid {
		return fmt.Errorf("route is not implemented by Devin")
	}
	return nil
}

func (*devinProviderBridge) ObserveStreamBody(ctx context.Context, body io.ReadCloser, _ http.Header) io.ReadCloser {
	return devin.ObserveStreamBody(body, execution.NewFirstResponseFrameObserver(ctx))
}

func (*devinProviderBridge) ParseCredential(raw []byte) (providerCredential, error) {
	credential, err := devin.ParseCredentialJSON(raw)
	if err != nil {
		return nil, err
	}
	return devinProviderCredential{value: credential}, nil
}

func (bridge *devinProviderBridge) Execute(
	ctx context.Context,
	credentialID string,
	credential providerCredential,
	request providerRequest,
) (providerResponse, error) {
	value, ok := credential.(devinProviderCredential)
	if !ok || bridge == nil || bridge.executor == nil {
		return providerResponse{}, errors.New("Devin provider bridge credential mismatch")
	}
	response, err := bridge.executor.Execute(ctx, credentialID, value.value, devinRequest(request))
	return providerResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
		AppliedReasoningEffort:       response.AppliedReasoningEffort,
		AppliedReasoningMode:         response.AppliedReasoningMode,
		AppliedReasoningBudgetTokens: response.AppliedReasoningBudgetTokens,
	}, err
}

func (bridge *devinProviderBridge) ExecuteStream(
	ctx context.Context,
	credentialID string,
	credential providerCredential,
	request providerRequest,
) (*providerStreamResponse, error) {
	value, ok := credential.(devinProviderCredential)
	if !ok || bridge == nil || bridge.executor == nil {
		return nil, errors.New("Devin provider bridge credential mismatch")
	}
	response, err := bridge.executor.ExecuteStream(ctx, credentialID, value.value, devinRequest(request))
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

func devinRequest(request providerRequest) devin.ExecuteRequest {
	return devin.ExecuteRequest{
		AttemptID: request.AttemptID, Model: request.Model,
		Payload: append([]byte(nil), request.Payload...), Format: request.Format,
		Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		ContinuityKey: request.ContinuityKey,
		BaseURL:       request.BaseURL, ProxyURL: request.ProxyURL, ProxyFromEnvironment: request.ProxyFromEnvironment, ConfiguredHeaders: append([]string(nil), request.ConfiguredHeaders...),
	}
}

func (*devinProviderBridge) ClassifyError(
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
		Summary: devinProviderErrorSummary(status, code),
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

func devinProviderErrorSummary(status int, code string) string {
	switch {
	case status == http.StatusUnauthorized:
		return "Devin authorization was rejected"
	case status == http.StatusForbidden:
		return "Devin access was denied"
	case status == http.StatusTooManyRequests:
		return "Devin upstream rate limit was reached"
	case status >= http.StatusInternalServerError:
		return "Devin upstream service failed"
	case status >= http.StatusBadRequest:
		return "Devin upstream request was rejected"
	default:
		return "Devin upstream request failed"
	}
}

var _ providerBridge = (*devinProviderBridge)(nil)
