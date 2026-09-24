package cpa

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/subscription/providers/mirasim"
)

type mirasimProviderCredential struct{ value mirasim.Storage }

func (credential mirasimProviderCredential) redactionValues() []string {
	return credential.value.SecretValues()
}

type mirasimProviderBridge struct{ executor *mirasim.Executor }

func newMirasimProviderBridge() *mirasimProviderBridge {
	return &mirasimProviderBridge{executor: mirasim.NewExecutor()}
}

func (*mirasimProviderBridge) ProviderKind() channel.ProviderKind { return channel.ProviderMirasim }

func (*mirasimProviderBridge) UpstreamProtocol() protocol.Protocol {
	return protocol.OpenAIResponses
}

func mirasimUpstreamProtocol(requestPath string) protocol.Protocol {
	requestPath = strings.TrimSpace(requestPath)
	switch {
	case strings.Contains(requestPath, "/messages"):
		return protocol.Anthropic
	case strings.HasSuffix(requestPath, "/responses"):
		return protocol.OpenAIResponses
	default:
		return ""
	}
}

func (*mirasimProviderBridge) ValidateRouteCapability(route channel.RouteDescriptor) error {
	switch {
	case route.ClientProtocol == protocol.Anthropic && route.Operation == execution.OperationChatCompletion &&
		(route.RouteMode == execution.RouteNative || route.RouteMode == execution.RouteConverted):
		return nil
	case route.ClientProtocol == protocol.Anthropic && route.Operation == execution.OperationCountTokens &&
		route.RouteMode == execution.RouteNative:
		return nil
	case route.ClientProtocol == protocol.OpenAIResponses && route.Operation == execution.OperationResponsesCreate &&
		(route.RouteMode == execution.RouteNative || route.RouteMode == execution.RouteConverted):
		return nil
	case route.ClientProtocol == protocol.OpenAIResponses && route.Operation == execution.OperationResponsesInputTokens &&
		route.RouteMode == execution.RouteConverted:
		return nil
	case (route.ClientProtocol == protocol.OpenAICompletions || route.ClientProtocol == protocol.Gemini) &&
		route.Operation == execution.OperationChatCompletion && route.RouteMode == execution.RouteConverted:
		return nil
	case route.ClientProtocol == protocol.Gemini && route.Operation == execution.OperationCountTokens &&
		route.RouteMode == execution.RouteConverted:
		return nil
	default:
		return fmt.Errorf("route is not implemented by Mirasim")
	}
}

func (*mirasimProviderBridge) ParseCredential(raw []byte) (providerCredential, error) {
	credential, err := mirasim.ParseCredentialJSON(raw)
	if err != nil {
		return nil, err
	}
	return mirasimProviderCredential{value: credential}, nil
}

func (bridge *mirasimProviderBridge) Execute(
	ctx context.Context,
	_ string,
	credential providerCredential,
	request providerRequest,
) (providerResponse, error) {
	value, ok := credential.(mirasimProviderCredential)
	if !ok || bridge == nil || bridge.executor == nil {
		return providerResponse{}, errors.New("Mirasim provider bridge credential mismatch")
	}
	response, err := bridge.executor.Execute(ctx, value.value, mirasimRequest(request))
	return providerResponse{
		StatusCode: response.StatusCode, Payload: append([]byte(nil), response.Payload...),
		Headers: response.Headers.Clone(), UpstreamProtocol: mirasimUpstreamProtocol(response.UpstreamRequestPath),
	}, err
}

func (bridge *mirasimProviderBridge) ExecuteStream(
	ctx context.Context,
	_ string,
	credential providerCredential,
	request providerRequest,
) (*providerStreamResponse, error) {
	value, ok := credential.(mirasimProviderCredential)
	if !ok || bridge == nil || bridge.executor == nil {
		return nil, errors.New("Mirasim provider bridge credential mismatch")
	}
	response, body, err := bridge.executor.ExecuteStream(ctx, value.value, mirasimRequest(request))
	if err != nil || body == nil {
		return &providerStreamResponse{
			Headers: response.Headers.Clone(), UpstreamProtocol: mirasimUpstreamProtocol(response.UpstreamRequestPath),
		}, err
	}
	chunks := make(chan providerStreamChunk)
	go func() {
		defer close(chunks)
		defer body.Close()
		buffer := make([]byte, 32*1024)
		for {
			n, readErr := body.Read(buffer)
			if n > 0 {
				payload := append([]byte(nil), buffer[:n]...)
				select {
				case chunks <- providerStreamChunk{Payload: payload}:
				case <-ctx.Done():
					return
				}
			}
			if readErr != nil {
				if readErr != io.EOF {
					select {
					case chunks <- providerStreamChunk{Err: readErr}:
					case <-ctx.Done():
					}
				}
				return
			}
		}
	}()
	return &providerStreamResponse{
		Headers: response.Headers.Clone(), Chunks: chunks,
		UpstreamProtocol: mirasimUpstreamProtocol(response.UpstreamRequestPath),
	}, nil
}

func (bridge *mirasimProviderBridge) CountTokens(
	ctx context.Context,
	_ string,
	credential providerCredential,
	request providerRequest,
) (providerResponse, error) {
	value, ok := credential.(mirasimProviderCredential)
	if !ok || bridge == nil || bridge.executor == nil {
		return providerResponse{}, errors.New("Mirasim provider bridge credential mismatch")
	}
	response, err := bridge.executor.CountTokens(ctx, value.value, mirasimRequest(request))
	return providerResponse{
		StatusCode: response.StatusCode, Payload: append([]byte(nil), response.Payload...),
		Headers: response.Headers.Clone(), UpstreamProtocol: protocol.Anthropic,
	}, err
}

func (*mirasimProviderBridge) ClassifyError(
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
	switch {
	case errors.Is(err, context.DeadlineExceeded) || ctx != nil && errors.Is(context.Cause(ctx), context.DeadlineExceeded):
		kind = execution.ErrorKindTimeout
	case errors.Is(err, context.Canceled) || ctx != nil && errors.Is(context.Cause(ctx), context.Canceled):
		kind = execution.ErrorKindCanceled
	case status != 0:
		kind = execution.ErrorKindHTTP
	case func() bool { _, ok := err.(net.Error); return ok }():
		kind = execution.ErrorKindTransport
	}
	code := ""
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) && coded != nil {
		code = safeScalar(coded.ErrorCode())
	}
	evidence := &execution.ErrorEvidence{
		Kind: kind, StatusCode: status, Code: code,
		Summary: mirasimProviderErrorSummary(status),
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

func mirasimProviderErrorSummary(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return "Mirasim authorization was rejected"
	case status == http.StatusForbidden:
		return "Mirasim access was denied"
	case status == http.StatusTooManyRequests:
		return "Mirasim upstream rate limit was reached"
	case status >= http.StatusInternalServerError:
		return "Mirasim upstream service failed"
	case status >= http.StatusBadRequest:
		return "Mirasim upstream request was rejected"
	default:
		return "Mirasim upstream request failed"
	}
}

func mirasimRequest(request providerRequest) mirasim.ExecuteRequest {
	return mirasim.ExecuteRequest{
		Model: request.Model, Payload: append([]byte(nil), request.Payload...), Format: request.Format,
		Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		BaseURL: request.BaseURL,
	}
}

var _ providerBridge = (*mirasimProviderBridge)(nil)
var _ providerTokenCounter = (*mirasimProviderBridge)(nil)
