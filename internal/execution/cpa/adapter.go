// Package cpa adapts the execution-only CPA bridge to GPT-Load's neutral
// executor contract. GPT-Load remains the owner of selection, retry and health.
package cpa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/execution/responsealias"
	platformredact "gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
	"gpt-load/internal/reasoning"
	"gpt-load/internal/subscription"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
	"gpt-load/internal/usage"
)

var subscriptionResponseHeaderNames = [...]string{
	"Request-Id",
	"X-Request-Id",
	"Openai-Request-Id",
	"X-Oai-Request-Id",
	"Anthropic-Request-Id",
	"X-Goog-Request-Id",
	"X-Amzn-Requestid",
	"Retry-After",
}

type Adapter struct {
	credentials credentialPreparer
	channels    *channel.Registry
	providers   map[channel.ProviderKind]providerBridge
}

type credentialPreparer interface {
	Prepare(context.Context, channel.ID, execution.CredentialSnapshot, bool) (subscriptionruntime.Credential, *execution.ErrorEvidence)
}

// NewAdapter creates the shared CPA subscription execution adapter.
func NewAdapter(credentials *subscription.CredentialManager, channels *channel.Registry) *Adapter {
	return &Adapter{
		credentials: credentials,
		channels:    channels,
		providers: indexProviderBridges(
			newCodexProviderBridge(),
			newClaudeProviderBridge(),
			newAntigravityProviderBridge(),
			newGrokProviderBridge(),
		),
	}
}

// ValidateRouteCapability delegates the implementation bound to ProviderKind.
// Channel modules remain the product-level authors of the enabled subset.
func (a *Adapter) ValidateRouteCapability(
	providerKind channel.ProviderKind,
	route channel.RouteDescriptor,
) error {
	if a == nil {
		return fmt.Errorf("CPA adapter is unavailable")
	}
	provider, ok := a.providers[providerKind]
	if !ok || provider == nil {
		return fmt.Errorf("provider %q is not implemented by CPA", providerKind)
	}
	return provider.ValidateRouteCapability(route)
}

func (a *Adapter) Execute(ctx context.Context, spec execution.AttemptSpec) execution.AttemptResult {
	spec = execution.NewAttemptSpec(spec)
	provider, err := a.validateSpec(spec)
	if err != nil {
		return unaryNotSent(execution.ErrorKindInvalidRequest, "unsupported subscription request", "", err)
	}
	proxyURL, err := proxyURLForAttempt(spec.Proxy)
	if err != nil {
		return unaryNotSent(execution.ErrorKindInternal, "initialize subscription proxy", "", nil)
	}
	if spec.Proxy.Config.Mode != "" {
		ctx = subscriptionruntime.WithNetworkContext(ctx, subscriptionruntime.NetworkContext{
			Proxy: spec.Proxy, Fingerprint: spec.ProxyFingerprint,
		})
	}
	request := bridgeRequest(spec, proxyURL)
	if validator, ok := provider.(providerRequestValidator); ok {
		if err := validator.ValidateRequest(request); err != nil {
			return unaryNotSent(
				execution.ErrorKindInvalidRequest,
				"subscription request input is not supported",
				"unsupported_subscription_input",
				err,
			)
		}
	}
	if countTokensOperation(spec.Operation) {
		if local, ok := provider.(providerLocalTokenCounter); ok {
			if err := local.ValidateLocalTokenCount(request); err != nil {
				return unaryNotSent(
					execution.ErrorKindInvalidRequest,
					"local token count supports only stateless text input",
					"local_token_count_unsupported_input",
					err,
				)
			}
			execCtx, cancel := withRequestTimeout(ctx, spec.Timeouts.Request)
			defer cancel()
			response, err := local.CountTokensLocal(execCtx, request)
			if err != nil {
				return unaryNotSent(
					execution.ErrorKindInternal,
					"local token count failed",
					"local_token_count_failed",
					err,
				)
			}
			response.Local = true
			return unaryProviderSuccess(provider, spec, response)
		}
		if _, ok := provider.(providerTokenCounter); !ok {
			return unaryNotSent(
				execution.ErrorKindInvalidRequest,
				"subscription provider does not support token counting",
				"",
				nil,
			)
		}
	}
	preparedCredential, evidence := a.credentials.Prepare(ctx, channel.ID(spec.ChannelID), spec.Credential, spec.ForceCredentialRefresh)
	if evidence != nil {
		return execution.AttemptResult{DispatchState: execution.DispatchNotSent, Error: evidence}
	}
	canonical := preparedCredential.Canonical()
	credential, err := provider.ParseCredential(canonical)
	clear(canonical)
	if err != nil {
		return unaryNotSent(execution.ErrorKindInternal, "subscription credential adapter mismatch", "", err)
	}
	execCtx, cancel := withRequestTimeout(ctx, spec.Timeouts.Request)
	defer cancel()
	var response providerResponse
	if countTokensOperation(spec.Operation) {
		counter := provider.(providerTokenCounter)
		response, err = counter.CountTokens(
			execCtx,
			strconv.FormatUint(uint64(spec.Credential.ID), 10),
			credential,
			request,
		)
	} else {
		response, err = provider.Execute(
			execCtx,
			strconv.FormatUint(uint64(spec.Credential.ID), 10),
			credential,
			request,
		)
	}
	if err != nil {
		result := unaryExecutionError(execCtx, provider, err, credential)
		if result.Error != nil && execution.UpstreamCountTokensUnsupported(
			spec.Operation,
			result.Error.StatusCode,
			result.Error.Type,
			result.Error.Code,
		) {
			result.Error.Hint = execution.FailureHintRequestRejected
		}
		result.UpstreamProtocol = provider.UpstreamProtocol()
		result.AppliedReasoning = appliedReasoning(response.AppliedReasoningEffort)
		return result
	}
	return unaryProviderSuccess(provider, spec, response)
}

func unaryProviderSuccess(
	provider providerBridge,
	spec execution.AttemptSpec,
	response providerResponse,
) execution.AttemptResult {
	body := append([]byte(nil), response.Payload...)
	headers := subscriptionResponseHeaders(response.Headers, "application/json")
	if response.Local {
		headers.Set(localTokenCountHeader, "local-estimate")
		return execution.AttemptResult{
			DispatchState: execution.DispatchLocal, ResponseStarted: true,
			StatusCode: http.StatusOK, Header: headers, Body: body,
		}
	}
	return execution.AttemptResult{
		DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
		UpstreamProtocol: provider.UpstreamProtocol(), AppliedReasoning: appliedReasoning(response.AppliedReasoningEffort), StatusCode: http.StatusOK,
		Header: headers, Body: body, Model: responseModel(body, spec.UpstreamModel),
		UpstreamRequestID: upstreamRequestID(headers), Usage: responseUsage(spec, body),
	}
}

func (a *Adapter) ExecuteStream(ctx context.Context, spec execution.AttemptSpec, sink execution.StreamSink) execution.StreamResult {
	spec = execution.NewAttemptSpec(spec)
	if sink == nil {
		return streamNotSent(execution.ErrorKindInvalidRequest, "stream sink is required", "")
	}
	provider, err := a.validateSpec(spec)
	if err != nil {
		return streamNotSent(execution.ErrorKindInvalidRequest, "unsupported subscription request", "")
	}
	if countTokensOperation(spec.Operation) {
		return streamNotSent(execution.ErrorKindInvalidRequest, "count tokens does not support streaming", "")
	}
	proxyURL, err := proxyURLForAttempt(spec.Proxy)
	if err != nil {
		return streamNotSent(execution.ErrorKindInternal, "initialize subscription proxy", "")
	}
	if spec.Proxy.Config.Mode != "" {
		ctx = subscriptionruntime.WithNetworkContext(ctx, subscriptionruntime.NetworkContext{
			Proxy: spec.Proxy, Fingerprint: spec.ProxyFingerprint,
		})
	}
	request := bridgeRequest(spec, proxyURL)
	if validator, ok := provider.(providerRequestValidator); ok {
		if err := validator.ValidateRequest(request); err != nil {
			return streamNotSent(execution.ErrorKindInvalidRequest, "subscription request input is not supported", "unsupported_subscription_input")
		}
	}
	preparedCredential, evidence := a.credentials.Prepare(ctx, channel.ID(spec.ChannelID), spec.Credential, spec.ForceCredentialRefresh)
	if evidence != nil {
		return execution.StreamResult{DispatchState: execution.DispatchNotSent, Error: evidence}
	}
	canonical := preparedCredential.Canonical()
	credential, err := provider.ParseCredential(canonical)
	clear(canonical)
	if err != nil {
		return streamNotSent(execution.ErrorKindInternal, "subscription credential adapter mismatch", "")
	}
	execCtx, cancel := withRequestTimeout(ctx, spec.Timeouts.Request)
	defer cancel()
	streamCtx, cancelStream := context.WithCancelCause(execCtx)
	defer cancelStream(context.Canceled)
	firstByte := startFirstByteGate(spec.Timeouts.FirstByte, cancelStream)
	defer firstByte.stop()
	response, err := provider.ExecuteStream(streamCtx, strconv.FormatUint(uint64(spec.Credential.ID), 10), credential, request)
	if err != nil {
		result := unaryExecutionError(streamCtx, provider, err, credential)
		var applied *reasoning.Config
		if response != nil {
			applied = appliedReasoning(response.AppliedReasoningEffort)
		}
		return execution.StreamResult{
			DispatchState: result.DispatchState, ResponseStarted: result.ResponseStarted,
			UpstreamProtocol: provider.UpstreamProtocol(), AppliedReasoning: applied,
			StatusCode: result.StatusCode, Header: result.Header,
			UpstreamRequestID: result.UpstreamRequestID, Error: result.Error,
		}
	}
	applied := appliedReasoning(response.AppliedReasoningEffort)
	headers := subscriptionResponseHeaders(response.Headers, "text/event-stream")
	sequence := uint64(1)
	ready := false
	upstreamStarted := false
	openAIDone := false
	geminiTerminal := false
	var nativeResponsesAssembler *nativeResponsesSSEAssembler
	if spec.ClientProtocol == protocol.OpenAIResponses && spec.RouteMode == execution.RouteNative {
		nativeResponsesAssembler = &nativeResponsesSSEAssembler{}
	}
	emitPayloads := func(payloads [][]byte) *execution.StreamResult {
		for _, unframed := range payloads {
			payload := frameSSE(spec.ClientProtocol, unframed)
			payload, rewriteErr := rewriteStreamModelAlias(spec, payload)
			if rewriteErr != nil {
				failure := streamInternalError(provider.UpstreamProtocol(), headers, applied, "rewrite subscription response model", ready)
				return &failure
			}
			if spec.ClientProtocol == protocol.OpenAICompletions && isOpenAIDone(payload) {
				openAIDone = true
			}
			if spec.ClientProtocol == protocol.Gemini && isGeminiTerminal(payload) {
				geminiTerminal = true
			}
			if !ready {
				if err := sink(execution.StreamEvent{Kind: execution.StreamEventReady, Sequence: sequence, StatusCode: http.StatusOK, Header: headers}); err != nil {
					failure := streamConsumerStopped(provider.UpstreamProtocol(), headers, applied, false)
					return &failure
				}
				ready = true
			}
			sequence++
			if err := sink(execution.StreamEvent{Kind: execution.StreamEventData, Sequence: sequence, Data: payload}); err != nil {
				failure := streamConsumerStopped(provider.UpstreamProtocol(), headers, applied, ready)
				return &failure
			}
		}
		return nil
	}
	for {
		idleTimeout := spec.Timeouts.StreamIdle
		if !ready && !upstreamStarted {
			idleTimeout = 0
		}
		chunk, ok, idleErr := nextChunk(streamCtx, response.Chunks, idleTimeout)
		if idleErr != nil {
			return streamExecutionError(streamCtx, provider, headers, idleErr, credential, applied, ready)
		}
		if !ok {
			if err := context.Cause(streamCtx); err != nil {
				return streamExecutionError(streamCtx, provider, headers, err, credential, applied, ready)
			}
			if nativeResponsesAssembler != nil {
				payloads, finishErr := nativeResponsesAssembler.finish()
				if finishErr != nil {
					return streamInternalError(provider.UpstreamProtocol(), headers, applied, "invalid subscription SSE stream", ready)
				}
				if failure := emitPayloads(payloads); failure != nil {
					return *failure
				}
			}
			if !ready {
				return streamInternalError(provider.UpstreamProtocol(), headers, applied, "subscription upstream stream ended without data", false)
			}
			if spec.ClientProtocol == protocol.OpenAICompletions && !openAIDone {
				sequence++
				if err := sink(execution.StreamEvent{Kind: execution.StreamEventData, Sequence: sequence, Data: []byte("data: [DONE]\n\n")}); err != nil {
					return streamConsumerStopped(provider.UpstreamProtocol(), headers, applied, ready)
				}
			}
			if spec.ClientProtocol == protocol.Gemini && !geminiTerminal {
				sequence++
				if err := sink(execution.StreamEvent{Kind: execution.StreamEventData, Sequence: sequence, Data: []byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n")}); err != nil {
					return streamConsumerStopped(provider.UpstreamProtocol(), headers, applied, ready)
				}
			}
			return successfulStreamTerminal(provider.UpstreamProtocol(), spec, headers, applied)
		}
		if chunk.Err != nil {
			return streamExecutionError(streamCtx, provider, headers, chunk.Err, credential, applied, ready)
		}
		if len(chunk.Payload) == 0 && nativeResponsesAssembler == nil {
			continue
		}
		if len(chunk.Payload) > 0 {
			firstByte.stop()
			upstreamStarted = true
		}
		if err := context.Cause(streamCtx); err != nil {
			return streamExecutionError(streamCtx, provider, headers, err, credential, applied, false)
		}
		payloads := [][]byte{chunk.Payload}
		if nativeResponsesAssembler != nil {
			payloads, err = nativeResponsesAssembler.push(chunk.Payload)
			if err != nil {
				return streamInternalError(provider.UpstreamProtocol(), headers, applied, "invalid subscription SSE stream", ready)
			}
		}
		if failure := emitPayloads(payloads); failure != nil {
			return *failure
		}
	}
}

func countTokensOperation(operation execution.Operation) bool {
	return operation == execution.OperationCountTokens ||
		operation == execution.OperationResponsesInputTokens
}

func responseUsage(spec execution.AttemptSpec, body []byte) *execution.UsageEvidence {
	if countTokensOperation(spec.Operation) {
		return nil
	}
	return usageEvidence(spec.ClientProtocol, body)
}

func (a *Adapter) validateSpec(spec execution.AttemptSpec) (providerBridge, error) {
	if a == nil || a.credentials == nil || a.channels == nil || len(a.providers) == 0 {
		return nil, fmt.Errorf("subscription executor is unavailable")
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	channelID := channel.ID(spec.ChannelID)
	providerKind, ok := a.channels.ProviderKind(channelID)
	if !ok {
		return nil, fmt.Errorf("subscription target has no provider binding")
	}
	provider, ok := a.providers[providerKind]
	if !ok || provider == nil {
		return nil, fmt.Errorf("subscription target is not bound to this adapter")
	}
	target, err := a.channels.ResolveExecutionTarget(channelID, spec.TargetConfig)
	if err != nil {
		return nil, fmt.Errorf("resolve subscription target: %w", err)
	}
	if target.ProviderKind != providerKind {
		return nil, fmt.Errorf("subscription target provider binding changed")
	}
	mode, ok := target.ModeForModel(spec.ClientProtocol, spec.Operation, spec.UpstreamModel)
	if !ok || execution.RouteMode(mode) != spec.RouteMode {
		return nil, fmt.Errorf("subscription route is not declared by the channel")
	}
	if !json.Valid(spec.Body) {
		return nil, fmt.Errorf("subscription request body must be JSON")
	}
	return provider, nil
}

func bridgeRequest(spec execution.AttemptSpec, proxyURL string) providerRequest {
	return providerRequest{
		AttemptID: spec.AttemptID, Model: spec.UpstreamModel, Payload: append([]byte(nil), spec.Body...),
		Format: formatFor(spec.ClientProtocol), Headers: spec.Header.Clone(),
		OriginalRequest: append([]byte(nil), spec.Body...),
		ContinuityKey:   spec.ContinuityKey,
		ProxyURL:        proxyURL,
	}
}

func formatFor(clientProtocol protocol.Protocol) string {
	switch clientProtocol {
	case protocol.OpenAIResponses:
		return "openai-response"
	case protocol.Anthropic:
		return "claude"
	case protocol.Gemini:
		return "gemini"
	default:
		return "openai"
	}
}

func subscriptionResponseHeaders(source http.Header, contentType string) http.Header {
	headers := make(http.Header)
	for actualName, values := range source {
		allowed := false
		for _, name := range subscriptionResponseHeaderNames {
			if strings.EqualFold(actualName, name) {
				allowed = true
				break
			}
		}
		if !allowed {
			continue
		}
		for _, value := range values {
			headers.Add(actualName, value)
		}
	}
	headers.Set("Content-Type", contentType)
	if contentType == "text/event-stream" {
		headers.Set("Cache-Control", "no-cache")
	}
	if headers.Get("X-Request-Id") == "" {
		if requestID := upstreamRequestID(headers); requestID != "" {
			headers.Set("X-Request-Id", requestID)
		}
	}
	return headers
}

func unaryExecutionError(
	ctx context.Context,
	provider providerBridge,
	err error,
	credential providerCredential,
) execution.AttemptResult {
	status, evidence := provider.ClassifyError(ctx, err, credential)
	return execution.AttemptResult{
		DispatchState: execution.DispatchMaybeSent, ResponseStarted: status != 0,
		StatusCode: status, Error: evidence,
	}
}

func streamExecutionError(
	ctx context.Context,
	provider providerBridge,
	headers http.Header,
	err error,
	credential providerCredential,
	applied *reasoning.Config,
	responseStarted bool,
) execution.StreamResult {
	status, evidence := provider.ClassifyError(ctx, err, credential)
	responseStarted = responseStarted || status != 0
	if responseStarted && status == 0 {
		status = http.StatusOK
	}
	return execution.StreamResult{
		DispatchState: execution.DispatchMaybeSent, ResponseStarted: responseStarted,
		UpstreamProtocol: provider.UpstreamProtocol(), AppliedReasoning: applied, StatusCode: status,
		Header: headers, UpstreamRequestID: upstreamRequestID(headers), Error: evidence,
	}
}

func streamInternalError(
	upstreamProtocol protocol.Protocol,
	headers http.Header,
	applied *reasoning.Config,
	summary string,
	responseStarted bool,
) execution.StreamResult {
	status := 0
	if responseStarted {
		status = http.StatusOK
	}
	return execution.StreamResult{
		DispatchState: execution.DispatchMaybeSent, ResponseStarted: responseStarted,
		UpstreamProtocol: upstreamProtocol, AppliedReasoning: applied, StatusCode: status,
		Header: headers.Clone(), UpstreamRequestID: upstreamRequestID(headers),
		Error: &execution.ErrorEvidence{Kind: execution.ErrorKindInternal, Summary: summary},
	}
}

func streamConsumerStopped(
	upstreamProtocol protocol.Protocol,
	headers http.Header,
	applied *reasoning.Config,
	responseStarted bool,
) execution.StreamResult {
	status := 0
	if responseStarted {
		status = http.StatusOK
	}
	return execution.StreamResult{
		DispatchState: execution.DispatchMaybeSent, ResponseStarted: responseStarted,
		UpstreamProtocol: upstreamProtocol, AppliedReasoning: applied, StatusCode: status,
		Header: headers.Clone(), UpstreamRequestID: upstreamRequestID(headers),
		Error: &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled, Summary: "stream consumer stopped"},
	}
}

func safeErrorSummary(err error, redactionValues []string) string {
	fallback := "subscription upstream request failed"
	if err == nil {
		return fallback
	}
	summary := platformredact.ExtractErrorMessage([]byte(err.Error()))
	if summary == "" {
		summary = err.Error()
	}
	summary = platformredact.New().String(summary, redactionValues...)
	summary = strings.Join(strings.Fields(strings.ToValidUTF8(summary, "\uFFFD")), " ")
	if summary == "" {
		summary = fallback
	}
	if utf8.RuneCountInString(summary) > execution.MaxErrorSummaryLength {
		summary = string([]rune(summary)[:execution.MaxErrorSummaryLength])
	}
	return summary
}

func errorTypeCode(value string) (string, string) {
	var payload struct {
		Error struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(value), &payload) != nil {
		return "", ""
	}
	return safeScalar(payload.Error.Type), safeScalar(payload.Error.Code)
}

func safeScalar(value string) string {
	value = strings.TrimSpace(value)
	if strings.ContainsAny(value, "\r\n\x00") || len(value) > 128 {
		return ""
	}
	return value
}

func unaryNotSent(kind execution.ErrorKind, summary, code string, _ error) execution.AttemptResult {
	return execution.AttemptResult{DispatchState: execution.DispatchNotSent, Error: &execution.ErrorEvidence{Kind: kind, Code: code, Summary: summary}}
}

func streamNotSent(kind execution.ErrorKind, summary, code string) execution.StreamResult {
	return execution.StreamResult{DispatchState: execution.DispatchNotSent, Error: &execution.ErrorEvidence{Kind: kind, Code: code, Summary: summary}}
}

func successfulStreamTerminal(
	upstreamProtocol protocol.Protocol,
	spec execution.AttemptSpec,
	headers http.Header,
	applied *reasoning.Config,
) execution.StreamResult {
	return execution.StreamResult{
		DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
		UpstreamProtocol: upstreamProtocol, AppliedReasoning: applied, StatusCode: http.StatusOK,
		Header: headers, UpstreamRequestID: upstreamRequestID(headers), Model: spec.UpstreamModel,
	}
}

func appliedReasoning(effort string) *reasoning.Config {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" || len(effort) > 64 {
		return nil
	}
	for _, character := range effort {
		if (character < 'a' || character > 'z') && character != '-' && character != '_' {
			return nil
		}
	}
	return &reasoning.Config{Effort: effort}
}

func nextChunk(ctx context.Context, chunks <-chan providerStreamChunk, timeout time.Duration) (providerStreamChunk, bool, error) {
	if timeout <= 0 {
		select {
		case chunk, ok := <-chunks:
			return chunk, ok, nil
		case <-ctx.Done():
			return providerStreamChunk{}, false, context.Cause(ctx)
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case chunk, ok := <-chunks:
		return chunk, ok, nil
	case <-ctx.Done():
		return providerStreamChunk{}, false, context.Cause(ctx)
	case <-timer.C:
		return providerStreamChunk{}, false, context.DeadlineExceeded
	}
}

type firstByteGate struct {
	stopOnce sync.Once
	stopOne  chan struct{}
	done     chan struct{}
}

func startFirstByteGate(budget time.Duration, cancel context.CancelCauseFunc) *firstByteGate {
	gate := &firstByteGate{stopOne: make(chan struct{}), done: make(chan struct{})}
	if budget <= 0 {
		close(gate.done)
		return gate
	}
	go func() {
		defer close(gate.done)
		timer := time.NewTimer(budget)
		defer timer.Stop()
		select {
		case <-timer.C:
			cancel(context.DeadlineExceeded)
		case <-gate.stopOne:
		}
	}()
	return gate
}

func (g *firstByteGate) stop() {
	if g == nil {
		return
	}
	g.stopOnce.Do(func() { close(g.stopOne) })
	<-g.done
}

func frameSSE(clientProtocol protocol.Protocol, payload []byte) []byte {
	payload = bytes.TrimRight(payload, "\r\n")
	if (clientProtocol == protocol.OpenAICompletions || clientProtocol == protocol.Gemini) &&
		!bytes.HasPrefix(payload, []byte("data:")) && !bytes.HasPrefix(payload, []byte("event:")) {
		payload = append([]byte("data: "), payload...)
	}
	return append(append([]byte(nil), payload...), '\n', '\n')
}

func rewriteStreamModelAlias(spec execution.AttemptSpec, payload []byte) ([]byte, error) {
	if !responsealias.Needs(spec.ClientModel, spec.UpstreamModel) {
		return bytes.Clone(payload), nil
	}
	return responsealias.RewriteSSE(spec.ClientProtocol, payload, spec.ClientModel)
}

func isOpenAIDone(payload []byte) bool {
	payload = bytes.TrimSpace(payload)
	payload = bytes.TrimSpace(bytes.TrimPrefix(payload, []byte("data:")))
	return bytes.Equal(payload, []byte("[DONE]"))
}

func isGeminiTerminal(payload []byte) bool {
	payload = bytes.TrimSpace(payload)
	payload = bytes.TrimSpace(bytes.TrimPrefix(payload, []byte("data:")))
	var value struct {
		Candidates []struct {
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
	if json.Unmarshal(payload, &value) != nil {
		return false
	}
	if value.PromptFeedback.BlockReason != "" {
		return true
	}
	for _, candidate := range value.Candidates {
		if candidate.FinishReason != "" {
			return true
		}
	}
	return false
}

func responseModel(body []byte, fallback string) string {
	var value struct {
		Model        string `json:"model"`
		ModelVersion string `json:"modelVersion"`
		Response     struct {
			Model string `json:"model"`
		} `json:"response"`
	}
	if json.Unmarshal(body, &value) == nil {
		if strings.TrimSpace(value.Model) != "" {
			return strings.TrimSpace(value.Model)
		}
		if strings.TrimSpace(value.Response.Model) != "" {
			return strings.TrimSpace(value.Response.Model)
		}
		if strings.TrimSpace(value.ModelVersion) != "" {
			return strings.TrimSpace(value.ModelVersion)
		}
	}
	return fallback
}

func usageEvidence(clientProtocol protocol.Protocol, body []byte) *execution.UsageEvidence {
	var extractor dialect.UsageExtractor
	usageField := "usage"
	switch clientProtocol {
	case protocol.OpenAIResponses:
		extractor = dialect.NewOpenAIResponses()
	case protocol.Anthropic:
		extractor = dialect.NewAnthropic()
	case protocol.Gemini:
		extractor = dialect.NewGemini()
		usageField = "usageMetadata"
	default:
		extractor = dialect.NewOpenAI()
	}
	result, err := extractor.ExtractUsage(body)
	if err != nil || result.State == usage.StateMissing || result.State == usage.StateNotApplicable {
		return nil
	}
	var root map[string]json.RawMessage
	_ = json.Unmarshal(body, &root)
	raw := append([]byte(nil), root[usageField]...)
	return &execution.UsageEvidence{Normalized: result, Raw: raw}
}

func upstreamRequestID(headers http.Header) string {
	for _, name := range []string{
		"X-Request-Id",
		"Request-Id",
		"Openai-Request-Id",
		"X-Oai-Request-Id",
		"Anthropic-Request-Id",
		"X-Goog-Request-Id",
		"X-Amzn-Requestid",
	} {
		if value := headers.Get(name); value != "" {
			return value
		}
	}
	return ""
}

func withRequestTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

var _ execution.Executor = (*Adapter)(nil)
