package cpa

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/codex"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
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
		route.Operation == execution.OperationResponsesCreate &&
		route.RouteMode == execution.RouteNative
	if route.ClientProtocol == protocol.OpenAICompletions ||
		route.ClientProtocol == protocol.Anthropic ||
		route.ClientProtocol == protocol.Gemini {
		valid = route.Operation == execution.OperationChatCompletion &&
			route.RouteMode == execution.RouteConverted
	}
	if !valid {
		return fmt.Errorf("route is not implemented by Codex")
	}
	return nil
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
