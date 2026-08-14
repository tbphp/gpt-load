// Package cpa adapts the execution-only CPA bridge to GPT-Load's neutral
// executor contract. GPT-Load remains the owner of selection, retry and health.
package cpa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gpt-load/internal/channel"
	"gpt-load/internal/codex"
	"gpt-load/internal/connection"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/execution/responsealias"
	platformredact "gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
	"gpt-load/internal/reasoning"
	"gpt-load/internal/subscription"
	"gpt-load/internal/usage"
)

const codexUpstreamAPI = execution.UpstreamAPIOpenAIResponses

var convertedRepresentationHeaderNames = [...]string{
	"Content-Encoding",
	"Content-Length",
	"ETag",
	"Digest",
	"Content-MD5",
	"Content-Range",
	"Content-Digest",
	"Repr-Digest",
	"Signature",
	"Signature-Input",
}

type Adapter struct {
	credentials credentialPreparer
	executor    codex.Executor
}

type credentialPreparer interface {
	Prepare(context.Context, execution.CredentialSnapshot, bool) (codex.Credential, *execution.ErrorEvidence)
}

// NewAdapter creates the Codex subscription execution adapter.
func NewAdapter(credentials *subscription.CodexCredentialManager) *Adapter {
	return &Adapter{credentials: credentials, executor: codex.NewExecutor()}
}

func (a *Adapter) Execute(ctx context.Context, spec execution.AttemptSpec) execution.AttemptResult {
	spec = execution.NewAttemptSpec(spec)
	if err := a.validateSpec(spec); err != nil {
		return unaryNotSent(execution.ErrorKindInvalidRequest, "unsupported subscription request", "", err)
	}
	credential, evidence := a.credentials.Prepare(ctx, spec.Credential, spec.ForceCredentialRefresh)
	if evidence != nil {
		return execution.AttemptResult{DispatchState: execution.DispatchNotSent, Error: evidence}
	}
	execCtx, cancel := withRequestTimeout(ctx, spec.Timeouts.Request)
	defer cancel()
	response, err := a.executor.Execute(execCtx, strconv.FormatUint(uint64(spec.Credential.ID), 10), credential, bridgeRequest(spec))
	if err != nil {
		result := unaryExecutionError(execCtx, err, credential)
		result.UpstreamAPI = codexUpstreamAPI
		result.AppliedReasoning = appliedReasoning(response.AppliedReasoningEffort)
		return result
	}
	body := append([]byte(nil), response.Payload...)
	headers := convertedResponseHeaders(response.Headers, "application/json")
	return execution.AttemptResult{
		DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
		UpstreamAPI: codexUpstreamAPI, AppliedReasoning: appliedReasoning(response.AppliedReasoningEffort), StatusCode: http.StatusOK,
		Header: headers, Body: body, Model: responseModel(body, spec.UpstreamModel),
		UpstreamRequestID: upstreamRequestID(headers), Usage: usageEvidence(spec.ClientProtocol, body),
	}
}

func (a *Adapter) ExecuteStream(ctx context.Context, spec execution.AttemptSpec, sink execution.StreamSink) execution.StreamResult {
	spec = execution.NewAttemptSpec(spec)
	if sink == nil {
		return streamNotSent(execution.ErrorKindInvalidRequest, "stream sink is required", "")
	}
	if err := a.validateSpec(spec); err != nil {
		return streamNotSent(execution.ErrorKindInvalidRequest, "unsupported subscription request", "")
	}
	credential, evidence := a.credentials.Prepare(ctx, spec.Credential, spec.ForceCredentialRefresh)
	if evidence != nil {
		return execution.StreamResult{DispatchState: execution.DispatchNotSent, Error: evidence}
	}
	execCtx, cancel := withRequestTimeout(ctx, spec.Timeouts.Request)
	defer cancel()
	streamCtx, cancelStream := context.WithCancelCause(execCtx)
	defer cancelStream(context.Canceled)
	firstByte := startFirstByteGate(spec.Timeouts.FirstByte, cancelStream)
	defer firstByte.stop()
	response, err := a.executor.ExecuteStream(streamCtx, strconv.FormatUint(uint64(spec.Credential.ID), 10), credential, bridgeRequest(spec))
	if err != nil {
		result := unaryExecutionError(streamCtx, err, credential)
		var applied *reasoning.Config
		if response != nil {
			applied = appliedReasoning(response.AppliedReasoningEffort)
		}
		return execution.StreamResult{
			DispatchState: result.DispatchState, ResponseStarted: result.ResponseStarted,
			UpstreamAPI: codexUpstreamAPI, AppliedReasoning: applied,
			StatusCode: result.StatusCode, Header: result.Header,
			UpstreamRequestID: result.UpstreamRequestID, Error: result.Error,
		}
	}
	applied := appliedReasoning(response.AppliedReasoningEffort)
	headers := convertedResponseHeaders(response.Headers, "text/event-stream")
	sequence := uint64(1)
	ready := false
	openAIDone := false
	geminiTerminal := false
	for {
		idleTimeout := spec.Timeouts.StreamIdle
		if !ready {
			idleTimeout = 0
		}
		chunk, ok, idleErr := nextChunk(streamCtx, response.Chunks, idleTimeout)
		if idleErr != nil {
			return streamExecutionError(headers, idleErr, credential, applied, ready)
		}
		if !ok {
			if err := context.Cause(streamCtx); err != nil {
				return streamExecutionError(headers, err, credential, applied, ready)
			}
			if !ready {
				return streamInternalError(headers, applied, "subscription upstream stream ended without data", false)
			}
			if spec.ClientProtocol == protocol.OpenAICompletions && !openAIDone {
				sequence++
				if err := sink(execution.StreamEvent{Kind: execution.StreamEventData, Sequence: sequence, Data: []byte("data: [DONE]\n\n")}); err != nil {
					return streamConsumerStopped(headers, applied, ready)
				}
			}
			if spec.ClientProtocol == protocol.Gemini && !geminiTerminal {
				sequence++
				if err := sink(execution.StreamEvent{Kind: execution.StreamEventData, Sequence: sequence, Data: []byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n")}); err != nil {
					return streamConsumerStopped(headers, applied, ready)
				}
			}
			return successfulStreamTerminal(spec, headers, applied)
		}
		if chunk.Err != nil {
			return streamExecutionError(headers, chunk.Err, credential, applied, ready)
		}
		if len(chunk.Payload) == 0 {
			continue
		}
		firstByte.stop()
		if err := context.Cause(streamCtx); err != nil {
			return streamExecutionError(headers, err, credential, applied, false)
		}
		payload := frameSSE(spec.ClientProtocol, chunk.Payload)
		payload, rewriteErr := rewriteStreamModelAlias(spec, payload)
		if rewriteErr != nil {
			return streamInternalError(headers, applied, "rewrite subscription response model", ready)
		}
		if spec.ClientProtocol == protocol.OpenAICompletions && isOpenAIDone(payload) {
			openAIDone = true
		}
		if spec.ClientProtocol == protocol.Gemini && isGeminiTerminal(payload) {
			geminiTerminal = true
		}
		if !ready {
			if err := sink(execution.StreamEvent{Kind: execution.StreamEventReady, Sequence: sequence, StatusCode: http.StatusOK, Header: headers}); err != nil {
				return streamConsumerStopped(headers, applied, false)
			}
			ready = true
		}
		sequence++
		if err := sink(execution.StreamEvent{Kind: execution.StreamEventData, Sequence: sequence, Data: payload}); err != nil {
			return streamConsumerStopped(headers, applied, ready)
		}
	}
}

func (a *Adapter) validateSpec(spec execution.AttemptSpec) error {
	if a == nil || a.credentials == nil || a.executor == nil {
		return fmt.Errorf("subscription executor is unavailable")
	}
	if err := spec.Validate(); err != nil {
		return err
	}
	if connection.Normalize(spec.ConnectionType) != connection.Subscription || spec.ChannelID != string(channel.Codex) {
		return fmt.Errorf("unsupported subscription target")
	}
	switch spec.ClientProtocol {
	case protocol.OpenAIResponses:
		if spec.Operation != execution.OperationResponsesCreate {
			return fmt.Errorf("unsupported subscription operation")
		}
	case protocol.OpenAICompletions, protocol.Anthropic, protocol.Gemini:
		if spec.Operation != execution.OperationChatCompletion {
			return fmt.Errorf("unsupported subscription operation")
		}
	default:
		return fmt.Errorf("unsupported subscription protocol")
	}
	if !json.Valid(spec.Body) {
		return fmt.Errorf("subscription request body must be JSON")
	}
	return nil
}

func bridgeRequest(spec execution.AttemptSpec) codex.ExecuteRequest {
	return codex.ExecuteRequest{
		Model: spec.UpstreamModel, Payload: append([]byte(nil), spec.Body...),
		Format: formatFor(spec.ClientProtocol), Headers: spec.Header.Clone(),
		OriginalRequest: append([]byte(nil), spec.Body...),
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

func convertedResponseHeaders(source http.Header, contentType string) http.Header {
	headers := source.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	for _, name := range convertedRepresentationHeaderNames {
		for key := range headers {
			if strings.EqualFold(key, name) {
				delete(headers, key)
			}
		}
	}
	headers.Set("Content-Type", contentType)
	return headers
}

func unaryExecutionError(ctx context.Context, err error, credential codex.Credential) execution.AttemptResult {
	status, evidence := executionErrorEvidence(ctx, err, credential)
	return execution.AttemptResult{
		DispatchState: execution.DispatchMaybeSent, ResponseStarted: status != 0,
		StatusCode: status, Error: evidence,
	}
}

func streamExecutionError(headers http.Header, err error, credential codex.Credential, applied *reasoning.Config, responseStarted bool) execution.StreamResult {
	status, evidence := executionErrorEvidence(context.Background(), err, credential)
	responseStarted = responseStarted || status != 0
	if responseStarted && status == 0 {
		status = http.StatusOK
	}
	return execution.StreamResult{
		DispatchState: execution.DispatchMaybeSent, ResponseStarted: responseStarted,
		UpstreamAPI: codexUpstreamAPI, AppliedReasoning: applied, StatusCode: status,
		Header: headers, UpstreamRequestID: upstreamRequestID(headers), Error: evidence,
	}
}

func streamInternalError(headers http.Header, applied *reasoning.Config, summary string, responseStarted bool) execution.StreamResult {
	status := 0
	if responseStarted {
		status = http.StatusOK
	}
	return execution.StreamResult{
		DispatchState: execution.DispatchMaybeSent, ResponseStarted: responseStarted,
		UpstreamAPI: codexUpstreamAPI, AppliedReasoning: applied, StatusCode: status,
		Header: headers.Clone(), UpstreamRequestID: upstreamRequestID(headers),
		Error: &execution.ErrorEvidence{Kind: execution.ErrorKindInternal, Summary: summary},
	}
}

func streamConsumerStopped(headers http.Header, applied *reasoning.Config, responseStarted bool) execution.StreamResult {
	status := 0
	if responseStarted {
		status = http.StatusOK
	}
	return execution.StreamResult{
		DispatchState: execution.DispatchMaybeSent, ResponseStarted: responseStarted,
		UpstreamAPI: codexUpstreamAPI, AppliedReasoning: applied, StatusCode: status,
		Header: headers.Clone(), UpstreamRequestID: upstreamRequestID(headers),
		Error: &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled, Summary: "stream consumer stopped"},
	}
}

func executionErrorEvidence(ctx context.Context, err error, credential codex.Credential) (int, *execution.ErrorEvidence) {
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
		Summary: safeErrorSummary(err, credential),
	}
	if retry, ok := err.(interface{ RetryAfter() *time.Duration }); ok && retry.RetryAfter() != nil && *retry.RetryAfter() > 0 {
		evidence.RetryAfter = *retry.RetryAfter()
	}
	switch {
	case status == http.StatusUnauthorized:
		evidence.ReplaySafety = execution.ReplaySafetyUnknown
		if explicitAccessTokenExpiration(codeValue) {
			evidence.Hint = execution.FailureHintRefreshRequired
			evidence.ReplaySafety = execution.ReplaySafetyRejectedBeforeProcessing
		}
	case status == http.StatusTooManyRequests:
		evidence.Hint = execution.FailureHintRateLimited
	default:
		if status >= 500 {
			evidence.Hint = execution.FailureHintHostError
		}
	}
	return status, evidence
}

func explicitAccessTokenExpiration(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "token_expired", "access_token_expired", "expired_token":
		return true
	default:
		return false
	}
}

func safeErrorSummary(err error, credential codex.Credential) string {
	fallback := "subscription upstream request failed"
	if err == nil {
		return fallback
	}
	summary := platformredact.ExtractErrorMessage([]byte(err.Error()))
	if summary == "" {
		summary = err.Error()
	}
	summary = platformredact.New().String(
		summary,
		credential.AccessToken,
		credential.RefreshToken,
		credential.IDToken,
		credential.AccountID,
		credential.Email,
	)
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

func successfulStreamTerminal(spec execution.AttemptSpec, headers http.Header, applied *reasoning.Config) execution.StreamResult {
	return execution.StreamResult{
		DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
		UpstreamAPI: codexUpstreamAPI, AppliedReasoning: applied, StatusCode: http.StatusOK,
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

func nextChunk(ctx context.Context, chunks <-chan codex.ExecuteStreamChunk, timeout time.Duration) (codex.ExecuteStreamChunk, bool, error) {
	if timeout <= 0 {
		select {
		case chunk, ok := <-chunks:
			return chunk, ok, nil
		case <-ctx.Done():
			return codex.ExecuteStreamChunk{}, false, context.Cause(ctx)
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case chunk, ok := <-chunks:
		return chunk, ok, nil
	case <-ctx.Done():
		return codex.ExecuteStreamChunk{}, false, context.Cause(ctx)
	case <-timer.C:
		return codex.ExecuteStreamChunk{}, false, context.DeadlineExceeded
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
	if value := headers.Get("X-Request-Id"); value != "" {
		return value
	}
	return headers.Get("Request-Id")
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
