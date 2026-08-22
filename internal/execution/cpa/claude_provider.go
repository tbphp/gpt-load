package cpa

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/subscription/providers/claude"
)

type claudeProviderCredential struct {
	value claude.Credential
}

func (credential claudeProviderCredential) redactionValues() []string {
	return credential.value.SecretValues()
}

type claudeProviderBridge struct {
	executor claude.Executor
}

func newClaudeProviderBridge() *claudeProviderBridge {
	return &claudeProviderBridge{executor: claude.NewExecutor()}
}

func (*claudeProviderBridge) ProviderKind() channel.ProviderKind {
	return channel.ProviderClaude
}

func (*claudeProviderBridge) UpstreamProtocol() protocol.Protocol {
	return protocol.Anthropic
}

func (*claudeProviderBridge) ValidateRouteCapability(route channel.RouteDescriptor) error {
	valid := route.ClientProtocol == protocol.Anthropic &&
		route.Operation == execution.OperationChatCompletion &&
		route.RouteMode == execution.RouteNative
	if route.ClientProtocol == protocol.OpenAICompletions || route.ClientProtocol == protocol.Gemini {
		valid = route.Operation == execution.OperationChatCompletion &&
			route.RouteMode == execution.RouteConverted
	}
	if route.ClientProtocol == protocol.OpenAIResponses {
		valid = (route.Operation == execution.OperationResponsesCreate ||
			route.Operation == execution.OperationResponsesInputTokens) &&
			route.RouteMode == execution.RouteConverted
	}
	if route.ClientProtocol == protocol.Anthropic && route.Operation == execution.OperationCountTokens {
		valid = route.RouteMode == execution.RouteNative
	}
	if route.ClientProtocol == protocol.Gemini && route.Operation == execution.OperationCountTokens {
		valid = route.RouteMode == execution.RouteConverted
	}
	if !valid {
		return fmt.Errorf("route is not implemented by Claude")
	}
	return nil
}

func (bridge *claudeProviderBridge) CountTokens(
	ctx context.Context,
	credentialID string,
	credential providerCredential,
	request providerRequest,
) (providerResponse, error) {
	claudeCredential, ok := credential.(claudeProviderCredential)
	if !ok || bridge == nil || bridge.executor == nil {
		return providerResponse{}, errors.New("Claude provider bridge credential mismatch")
	}
	response, err := bridge.executor.CountTokens(ctx, credentialID, claudeCredential.value, claude.ExecuteRequest{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: request.Format,
		Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		ProxyURL: request.ProxyURL,
	})
	return providerResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
	}, err
}

func (*claudeProviderBridge) ParseCredential(raw []byte) (providerCredential, error) {
	credential, err := claude.ParseCredentialJSON(raw)
	if err != nil {
		return nil, err
	}
	return claudeProviderCredential{value: credential}, nil
}

func (bridge *claudeProviderBridge) Execute(
	ctx context.Context,
	credentialID string,
	credential providerCredential,
	request providerRequest,
) (providerResponse, error) {
	claudeCredential, ok := credential.(claudeProviderCredential)
	if !ok || bridge == nil || bridge.executor == nil {
		return providerResponse{}, errors.New("Claude provider bridge credential mismatch")
	}
	response, err := bridge.executor.Execute(ctx, credentialID, claudeCredential.value, claude.ExecuteRequest{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: request.Format,
		Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		ProxyURL: request.ProxyURL,
	})
	return providerResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
		AppliedReasoningEffort: response.AppliedReasoningEffort,
	}, err
}

func (bridge *claudeProviderBridge) ExecuteStream(
	ctx context.Context,
	credentialID string,
	credential providerCredential,
	request providerRequest,
) (*providerStreamResponse, error) {
	claudeCredential, ok := credential.(claudeProviderCredential)
	if !ok || bridge == nil || bridge.executor == nil {
		return nil, errors.New("Claude provider bridge credential mismatch")
	}
	response, err := bridge.executor.ExecuteStream(ctx, credentialID, claudeCredential.value, claude.ExecuteRequest{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: request.Format,
		Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		ProxyURL: request.ProxyURL,
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

func (*claudeProviderBridge) ClassifyError(
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
	typeValue, codeValue := claudeErrorTypeCode(err)
	evidence := &execution.ErrorEvidence{
		Kind: kind, StatusCode: status, Type: typeValue, Code: codeValue,
		Summary: safeErrorSummary(err, credential.redactionValues()),
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
	case requestScopedFailure(err):
		evidence.Hint = execution.FailureHintRequestRejected
	case status == http.StatusTooManyRequests:
		if credentialScoped, known := credentialScopedFailure(err); known && !credentialScoped {
			evidence.Hint = execution.FailureHintRequestRejected
		} else {
			evidence.Hint = execution.FailureHintRateLimited
		}
	case status >= http.StatusInternalServerError:
		evidence.Hint = execution.FailureHintHostError
	}
	return status, evidence
}

func claudeErrorTypeCode(err error) (string, string) {
	var typed interface {
		ErrorType() string
		ErrorCode() string
	}
	if errors.As(err, &typed) && typed != nil {
		return safeScalar(typed.ErrorType()), safeScalar(typed.ErrorCode())
	}
	return errorTypeCode(err.Error())
}

var _ providerBridge = (*claudeProviderBridge)(nil)
var _ providerTokenCounter = (*claudeProviderBridge)(nil)
