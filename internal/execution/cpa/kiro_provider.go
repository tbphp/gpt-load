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
	"gpt-load/internal/subscription/providers/kiro"
)

type kiroProviderCredential struct{ value kiro.Credential }

func (credential kiroProviderCredential) redactionValues() []string {
	return credential.value.SecretValues()
}

type kiroProviderBridge struct{ executor kiro.Executor }

func newKiroProviderBridge() *kiroProviderBridge {
	return &kiroProviderBridge{executor: kiro.NewExecutor()}
}

func (*kiroProviderBridge) ProviderKind() channel.ProviderKind  { return channel.ProviderKiro }
func (*kiroProviderBridge) UpstreamProtocol() protocol.Protocol { return protocol.Anthropic }

func (*kiroProviderBridge) ValidateRouteCapability(route channel.RouteDescriptor) error {
	if route.ClientProtocol != protocol.Anthropic {
		return fmt.Errorf("route is not supported by Kiro")
	}
	valid := (route.Operation == execution.OperationChatCompletion && route.RouteMode == execution.RouteNative) ||
		(route.Operation == execution.OperationCountTokens && route.RouteMode == execution.RouteConverted)
	if !valid {
		return fmt.Errorf("route is not implemented by Kiro")
	}
	return nil
}

func (*kiroProviderBridge) ParseCredential(raw []byte) (providerCredential, error) {
	credential, err := kiro.ParseCredentialJSON(raw)
	if err != nil {
		return nil, err
	}
	return kiroProviderCredential{value: credential}, nil
}

func (bridge *kiroProviderBridge) Execute(
	ctx context.Context,
	credentialID string,
	credential providerCredential,
	request providerRequest,
) (providerResponse, error) {
	value, ok := credential.(kiroProviderCredential)
	if !ok || bridge == nil || bridge.executor == nil {
		return providerResponse{}, errors.New("Kiro provider bridge credential mismatch")
	}
	response, err := bridge.executor.Execute(ctx, credentialID, value.value, kiroRequest(request, credentialID))
	return providerResponse{
		Payload: append([]byte(nil), response.Payload...), Headers: response.Headers.Clone(),
		AppliedReasoningEffort: response.AppliedReasoningEffort,
	}, err
}

func (bridge *kiroProviderBridge) ExecuteStream(
	ctx context.Context,
	credentialID string,
	credential providerCredential,
	request providerRequest,
) (*providerStreamResponse, error) {
	value, ok := credential.(kiroProviderCredential)
	if !ok || bridge == nil || bridge.executor == nil {
		return nil, errors.New("Kiro provider bridge credential mismatch")
	}
	response, err := bridge.executor.ExecuteStream(ctx, credentialID, value.value, kiroRequest(request, credentialID))
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

func (bridge *kiroProviderBridge) ValidateLocalTokenCount(request providerRequest) error {
	if !kiroLocalTokenCountModelSupported(request.Model) {
		return errors.New("Kiro local token count tokenizer is unavailable for model")
	}
	var root map[string]any
	if err := json.Unmarshal(request.Payload, &root); err != nil || root == nil {
		return errors.New("Kiro local token count request is invalid")
	}
	switch strings.ToLower(strings.TrimSpace(request.Format)) {
	case "claude", "anthropic":
		return validateLocalClaudeTokenCount(root)
	default:
		return errors.New("Kiro local token count format is unsupported")
	}
}

func kiroLocalTokenCountModelSupported(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "claude-")
}

func (bridge *kiroProviderBridge) CountTokensLocal(
	_ context.Context,
	request providerRequest,
) (providerResponse, error) {
	if bridge == nil || bridge.executor == nil {
		return providerResponse{}, errors.New("Kiro provider bridge is unavailable")
	}
	headers := make(http.Header)
	headers.Set(localTokenCountHeader, "local-estimate")
	return providerResponse{
		Payload: kiro.CountTokensLocal(request.Payload), Headers: headers,
	}, nil
}

func kiroRequest(request providerRequest, credentialID string) kiro.ExecuteRequest {
	return kiro.ExecuteRequest{
		AttemptID: request.AttemptID, Model: request.Model,
		Payload: append([]byte(nil), request.Payload...), Format: request.Format,
		Headers: request.Headers.Clone(), OriginalRequest: append([]byte(nil), request.OriginalRequest...),
		ContinuityKey: kiroContinuityScope(request.ContinuityKey, credentialID, request.Model, request.AttemptID),
		ProxyURL:      request.ProxyURL,
	}
}

func kiroContinuityScope(base, credentialID, model, attemptID string) string {
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
		return strings.Join([]string{"gpt-load-kiro-attempt", attemptID, credentialID, model}, "\x00")
	}
	return strings.Join([]string{"gpt-load-kiro", base, credentialID, model}, "\x00")
}

func (*kiroProviderBridge) ClassifyError(
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
		Summary: kiroProviderErrorSummary(status, code),
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
	annotateProviderErrorEvidence(evidence, err)
	return status, evidence
}

func kiroProviderErrorSummary(status int, code string) string {
	switch {
	case status == http.StatusUnauthorized:
		return "Kiro authorization was rejected"
	case status == http.StatusForbidden:
		return "Kiro access was denied"
	case status == http.StatusTooManyRequests:
		return "Kiro upstream rate limit was reached"
	case status >= http.StatusInternalServerError:
		return "Kiro upstream service failed"
	case status >= http.StatusBadRequest:
		return "Kiro upstream request was rejected"
	case status == 0:
		// No HTTP status (transport / local error). Return empty so the caller
		// falls back to the redacted transport summary that preserves the real
		// upstream error rather than collapsing it into a generic message.
		return ""
	default:
		return "Kiro upstream request failed"
	}
}

var _ providerBridge = (*kiroProviderBridge)(nil)
var _ providerLocalTokenCounter = (*kiroProviderBridge)(nil)
