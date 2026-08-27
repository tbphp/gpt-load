package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

// ExecutionForwarder adapts the provider-neutral executor to the gateway's
// downstream commit boundary while the handler orchestration remains intact.
type ExecutionForwarder struct {
	executor       execution.Executor
	representation *responseProcessor
	usageCapture   *usageCaptureBoundary
	writeTimeout   time.Duration
}

func NewExecutionForwarder(executor execution.Executor) *ExecutionForwarder {
	return &ExecutionForwarder{
		executor: executor, representation: &responseProcessor{redactor: redact.New()},
		usageCapture: newUsageCaptureBoundary(),
		writeTimeout: downstreamWriteTimeout,
	}
}

func (forwarder *ExecutionForwarder) Forward(
	ctx context.Context,
	input ForwardInput,
) UpstreamResult {
	spec, err := newExecutionAttemptSpec(input)
	if err != nil || forwarder == nil || forwarder.executor == nil {
		return executionInputFailure(err)
	}
	executionResult := forwarder.executor.Execute(ctx, spec)
	if err := executionResult.Validate(); err != nil {
		return invalidExecutionAttemptResult(executionResult)
	}
	result := upstreamFromExecutionResult(ctx, input, executionResult)
	return forwarder.prepareBufferedResult(input, result)
}

func invalidExecutionAttemptResult(result execution.AttemptResult) UpstreamResult {
	dispatchState := execution.DispatchNotSent
	if result.DispatchState == execution.DispatchLocal {
		dispatchState = execution.DispatchLocal
	} else if result.DispatchState == execution.DispatchMaybeSent ||
		result.ResponseStarted || result.StatusCode != 0 || len(result.Header) > 0 || len(result.Body) > 0 {
		dispatchState = execution.DispatchMaybeSent
	}
	evidence := execution.ErrorEvidence{
		Kind:         execution.ErrorKindInternal,
		OriginHint:   execution.ErrorOriginInternal,
		ScopeHint:    execution.ErrorScopeRequest,
		Code:         "attempt_result_contract_invalid",
		Summary:      "Attempt executor returned an invalid result.",
		ReplaySafety: execution.ReplaySafetyUnknown,
	}
	return UpstreamResult{
		Err:            fmt.Errorf("%w: invalid attempt executor result", ErrUpstreamProtocol),
		RequestWritten: dispatchState == execution.DispatchMaybeSent,
		DispatchState:  dispatchState,
		ExecutionError: &evidence,
		ErrorSummary:   evidence.Summary,
		Usage:          usage.Result{State: usage.StateNotApplicable},
	}
}

func (forwarder *ExecutionForwarder) ForwardStream(
	ctx context.Context,
	input ForwardInput,
	downstream http.ResponseWriter,
) UpstreamResult {
	spec, err := newExecutionAttemptSpec(input)
	if err != nil || forwarder == nil || forwarder.executor == nil || downstream == nil {
		return executionInputFailure(err)
	}

	writeTimeout := forwarder.writeTimeout
	if writeTimeout <= 0 {
		writeTimeout = downstreamWriteTimeout
	}
	controller := newStreamWriteController(downstream, writeTimeout)
	defer func() { _ = controller.clear() }()
	usageCapture := forwarder.usageCapture
	if usageCapture == nil {
		usageCapture = newUsageCaptureBoundary()
	}
	streamEvents := newStreamEventObserver(
		input.Dialect,
		usageCapture.newStreamForRequest(input.Dialect, input.ObserveUsage),
	)
	redactor := redact.New()
	if forwarder.representation != nil && forwarder.representation.redactor != nil {
		redactor = forwarder.representation.redactor
	}
	summarySecrets := resolvedErrorSummarySecretValues(
		input.APIKey,
		input.Group.HeaderRules,
		input.CredentialSecrets...,
	)
	credentialSecrets := append([]string(nil), input.CredentialSecrets...)
	if input.APIKey != "" {
		credentialSecrets = append(credentialSecrets, input.APIKey)
	}
	streamBuffer := newSSEEventObservationBuffer(execution.SSEEventLimit(input.ClientProtocol), func(
		event dialect.StreamEvent,
		genericProviderError bool,
	) (bool, error) {
		if input.ClientProtocol == protocol.OpenAIImages &&
			imagesCredentialLiteralsRemain(event.Payload, credentialSecrets) {
			return false, fmt.Errorf("%w: credential remains in response event", ErrUpstreamProtocol)
		}
		wasTerminal := streamEvents.sawTerminal
		providerError, err := streamEvents.classify(event, genericProviderError)
		if err != nil {
			return false, err
		}
		streamEvents.observeUsageEvent(event)
		if providerError {
			streamEvents.observeError(
				event.Payload,
				summarizeErrorBody(
					redactor,
					event.Payload,
					fixedErrorSummary("upstream_sse_error"),
					summarySecrets...,
				),
			)
		}
		return !wasTerminal && streamEvents.sawTerminal, nil
	})

	var (
		ready         *execution.StreamEvent
		committed     bool
		firstResponse bool
		downstreamErr error
		errorBody     []byte
		streamUsage   *execution.UsageEvidence
	)
	sink := func(event execution.StreamEvent) error {
		if err := event.Validate(); err != nil {
			downstreamErr = fmt.Errorf("%w: invalid execution stream event", ErrUpstreamProtocol)
			return downstreamErr
		}
		switch event.Kind {
		case execution.StreamEventReady:
			copy := event.Clone()
			if copy.StatusCode >= http.StatusOK && copy.StatusCode < http.StatusMultipleChoices {
				if err := validateStreamContentEncoding(copy.Header); err != nil {
					downstreamErr = err
					return err
				}
				copy.Header = normalizeStreamResponseHeaders(
					sanitizeForwardResponseHeaders(copy.Header, input),
				)
			} else {
				copy.Header = sanitizeForwardResponseHeaders(copy.Header, input)
			}
			ready = &copy
			return nil
		case execution.StreamEventUsage:
			if event.Usage != nil {
				copy := event.Usage.Clone()
				streamUsage = &copy
			}
			return nil
		case execution.StreamEventData:
			if ready == nil {
				downstreamErr = fmt.Errorf("%w: execution data arrived before response metadata", ErrUpstreamProtocol)
				return downstreamErr
			}
			if ready.StatusCode < http.StatusOK || ready.StatusCode >= http.StatusMultipleChoices {
				errorBody = appendExecutionErrorBody(errorBody, event.Data)
				return nil
			}
			terminalInChunk, err := streamBuffer.push(event.Data)
			if err != nil {
				downstreamErr = executionStreamProtocolFailure(err)
				return downstreamErr
			}
			if !committed {
				if !firstResponse {
					firstResponse = true
					if input.OnFirstResponse != nil {
						input.OnFirstResponse()
					}
				}
				if streamEvents.firstEventWasProviderError() {
					errorBody = appendExecutionErrorBody(errorBody, event.Data)
					return nil
				}
				committed = true
				if err := commitStream(controller, ready.StatusCode, ready.Header, event.Data); err != nil {
					downstreamErr = err
					return err
				}
				if input.OnStreamReady != nil {
					input.OnStreamReady()
				}
				if terminalInChunk {
					streamEvents.markTerminalForwarded()
				}
				return nil
			}
			written, err := controller.write(event.Data)
			if err != nil {
				downstreamErr = &streamFailure{
					kind: streamFailureDownstreamWrite,
					err:  fmt.Errorf("write execution stream: %w", err),
				}
				return downstreamErr
			}
			if written != len(event.Data) {
				downstreamErr = &streamFailure{
					kind: streamFailureDownstreamWrite,
					err:  fmt.Errorf("write execution stream: %w", io.ErrShortWrite),
				}
				return downstreamErr
			}
			if err := controller.flush(); err != nil {
				downstreamErr = &streamFailure{
					kind: streamFailureDownstreamWrite,
					err:  fmt.Errorf("flush execution stream: %w", err),
				}
				return downstreamErr
			}
			if terminalInChunk {
				streamEvents.markTerminalForwarded()
			}
			return nil
		default:
			downstreamErr = fmt.Errorf("%w: unsupported execution stream event", ErrUpstreamProtocol)
			return downstreamErr
		}
	}

	terminal := forwarder.executor.ExecuteStream(ctx, spec, sink)
	if err := terminal.Validate(); err != nil {
		terminal = invalidExecutionStreamResult(terminal, ready, committed)
	}
	if downstreamErr == nil && terminal.Error == nil {
		if err := streamBuffer.finish(); err != nil {
			downstreamErr = executionStreamProtocolFailure(err)
		} else if err := streamEvents.validateEOF(); err != nil {
			downstreamErr = err
		}
	}
	capturedUsage := streamEvents.finalizeUsage()
	result := upstreamFromExecutionStreamResult(ctx, input, terminal, streamUsage)
	result.Usage = preferCapturedStreamUsage(result.Usage, capturedUsage)
	result.Committed = committed
	if len(errorBody) > 0 {
		result.Body = append([]byte(nil), errorBody...)
		result.ClassificationBody = append([]byte(nil), errorBody...)
	}
	if downstreamErr != nil {
		result.Err = downstreamErr
	}
	if committed {
		result.Stream = executionStreamObservation(ctx, terminal, downstreamErr, streamEvents)
	} else if ready != nil {
		result.StatusCode = ready.StatusCode
		result.Header = ready.Header.Clone()
		result.ResponseStarted = true
		result.UpstreamRequestID = ready.UpstreamRequestID
	}
	if !committed && streamEvents.firstEventWasProviderError() {
		summary := streamEvents.firstSummary
		if summary == "" {
			summary = fixedErrorSummary("upstream_sse_error")
		}
		result.ProviderErrorBeforeCommit = true
		result.ErrorSummary = summary
		result.ExecutionError = firstStreamErrorEvidence(
			streamEvents.firstErrorPayload,
			result.ExecutionError,
			result.StatusCode,
			result.UpstreamRequestID,
			summary,
			redactor,
			summarySecrets,
		)
	}
	if !committed && result.HasResponse() && !result.ProviderErrorBeforeCommit {
		result = forwarder.prepareBufferedResult(input, result)
	}
	return result
}

func invalidExecutionStreamResult(
	result execution.StreamResult,
	ready *execution.StreamEvent,
	committed bool,
) execution.StreamResult {
	dispatchState := execution.DispatchNotSent
	if result.DispatchState == execution.DispatchLocal && !committed && ready == nil {
		dispatchState = execution.DispatchLocal
	} else if committed || ready != nil || result.DispatchState == execution.DispatchMaybeSent ||
		result.ResponseStarted || result.StatusCode != 0 || len(result.Header) > 0 {
		dispatchState = execution.DispatchMaybeSent
	}
	normalized := execution.StreamResult{DispatchState: dispatchState}
	if ready != nil {
		normalized.ResponseStarted = true
		normalized.StatusCode = ready.StatusCode
		normalized.Header = ready.Header.Clone()
		normalized.UpstreamRequestID = ready.UpstreamRequestID
	}
	evidence := execution.ErrorEvidence{
		Kind:         execution.ErrorKindInternal,
		OriginHint:   execution.ErrorOriginInternal,
		ScopeHint:    execution.ErrorScopeRequest,
		Code:         "attempt_result_contract_invalid",
		Summary:      "Attempt executor returned an invalid result.",
		ReplaySafety: execution.ReplaySafetyUnknown,
	}
	if normalized.ResponseStarted {
		evidence.StatusCode = normalized.StatusCode
	}
	normalized.Error = &evidence
	return normalized
}

func firstStreamErrorEvidence(
	payload []byte,
	existing *execution.ErrorEvidence,
	statusCode int,
	requestID string,
	summary string,
	redactor *redact.Redactor,
	knownSecrets []string,
) *execution.ErrorEvidence {
	evidence := execution.ErrorEvidence{
		Kind:       execution.ErrorKindProvider,
		StatusCode: statusCode,
		Summary:    summary,
		RequestID:  requestID,
	}
	if existing != nil {
		evidence = existing.Clone()
		evidence.Kind = execution.ErrorKindProvider
		if evidence.StatusCode == 0 {
			evidence.StatusCode = statusCode
		}
		if evidence.Summary == "" {
			evidence.Summary = summary
		}
		if evidence.RequestID == "" {
			evidence.RequestID = requestID
		}
	}

	type errorObject struct {
		Type    string          `json:"type"`
		Code    json.RawMessage `json:"code"`
		Status  string          `json:"status"`
		Message string          `json:"message"`
	}
	var envelope struct {
		Type     string          `json:"type"`
		Code     json.RawMessage `json:"code"`
		Error    errorObject     `json:"error"`
		Response struct {
			Error errorObject `json:"error"`
		} `json:"response"`
	}
	if json.Unmarshal(payload, &envelope) != nil {
		return &evidence
	}
	providerError := envelope.Error
	if providerError.Type == "" && len(providerError.Code) == 0 &&
		providerError.Status == "" && providerError.Message == "" {
		providerError = envelope.Response.Error
	}
	typeValue := providerError.Type
	if typeValue == "" && !strings.EqualFold(envelope.Type, "error") {
		typeValue = envelope.Type
	}
	codeValue := streamErrorEvidenceScalar(providerError.Code)
	if codeValue == "" {
		codeValue = streamErrorEvidenceScalar(envelope.Code)
	}
	if codeValue == "" {
		codeValue = providerError.Status
	}
	typeValue = sanitizeStreamErrorEvidenceValue(redactor, typeValue, knownSecrets)
	codeValue = sanitizeStreamErrorEvidenceValue(redactor, codeValue, knownSecrets)
	statusValue := sanitizeStreamErrorEvidenceValue(redactor, providerError.Status, knownSecrets)
	if evidence.Type == "" {
		evidence.Type = typeValue
	}
	if evidence.Code == "" {
		evidence.Code = codeValue
	}
	if evidence.Hint == "" {
		evidence.Hint = streamErrorFailureHint(
			statusCode,
			typeValue,
			codeValue,
			statusValue,
			evidence.Summary,
		)
	}
	evidence.OriginHint = execution.ErrorOriginUpstream
	switch evidence.Hint {
	case execution.FailureHintInvalidCredential,
		execution.FailureHintRefreshRequired,
		execution.FailureHintReauthorizationRequired:
		evidence.ScopeHint = execution.ErrorScopeCredential
	case execution.FailureHintModelUnavailable,
		execution.FailureHintCandidateUnavailable:
		evidence.ScopeHint = execution.ErrorScopeModel
	case execution.FailureHintRequestRejected:
		evidence.ScopeHint = execution.ErrorScopeRequest
	case execution.FailureHintHostError:
		evidence.ScopeHint = execution.ErrorScopeGroup
	}
	return &evidence
}

func streamErrorEvidenceScalar(value json.RawMessage) string {
	value = json.RawMessage(strings.TrimSpace(string(value)))
	if len(value) == 0 || string(value) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(value, &text) == nil {
		return text
	}
	var number json.Number
	if json.Unmarshal(value, &number) == nil {
		return number.String()
	}
	return ""
}

func sanitizeStreamErrorEvidenceValue(
	redactor *redact.Redactor,
	value string,
	knownSecrets []string,
) string {
	value = sanitizeErrorSummary(redactor, value, knownSecrets...)
	const maxEvidenceRunes = 128
	runes := []rune(value)
	if len(runes) > maxEvidenceRunes {
		return string(runes[:maxEvidenceRunes])
	}
	return value
}

func streamErrorFailureHint(statusCode int, values ...string) execution.FailureHint {
	markers := strings.ToLower(strings.Join(values, " "))
	switch {
	case statusCode == http.StatusUnauthorized:
		return execution.FailureHintInvalidCredential
	case containsStreamErrorMarker(markers,
		"model_not_found", "model not found", "model_not_available",
		"model unavailable", "deployment_not_found", "unsupported_model"):
		return execution.FailureHintModelUnavailable
	case statusCode == http.StatusTooManyRequests || containsStreamErrorMarker(markers,
		"rate_limit", "rate limit", "too_many_requests", "quota_exceeded",
		"resource_exhausted", "throttl"):
		return execution.FailureHintRateLimited
	case containsStreamErrorMarker(markers,
		"invalid_api_key", "api_key_invalid", "authentication_error",
		"authentication failed",
		"invalid credential", "api key not valid"):
		return execution.FailureHintInvalidCredential
	case statusCode >= http.StatusInternalServerError && statusCode <= 599:
		return execution.FailureHintHostError
	default:
		return ""
	}
}

func containsStreamErrorMarker(value string, markers ...string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func (forwarder *ExecutionForwarder) prepareBufferedResult(
	input ForwardInput,
	result UpstreamResult,
) UpstreamResult {
	if forwarder == nil || !result.HasResponse() {
		return result
	}
	representation := forwarder.representation
	if representation == nil {
		representation = &responseProcessor{redactor: redact.New()}
	}
	secrets := append([]string(nil), input.CredentialSecrets...)
	if input.APIKey != "" {
		secrets = append(secrets, input.APIKey)
	}
	if result.StatusCode >= http.StatusOK && result.StatusCode < http.StatusMultipleChoices {
		policy := classifyResponseBody(input.Request.Method, result.StatusCode)
		if !policy.readBody {
			if err := validateStreamContentEncoding(result.Header); err != nil ||
				(result.StatusCode == http.StatusPartialContent && !hasSafeHeadContentRange(result.Header)) {
				return executionRepresentationFailure(result, fmt.Errorf("%w: invalid bodyless response representation", ErrUpstreamProtocol))
			}
			headers := sanitizeForwardResponseHeaders(result.Header, input, secrets...)
			headers, _ = normalizeBufferedResponse(input.Request.Method, result.StatusCode, headers, nil)
			result.Header = headers
			result.Body = nil
			return result
		}
		prepared, err := representation.prepareSuccessRepresentation(
			input, result.StatusCode, result.Header, result.Body, secrets,
		)
		if err != nil {
			return executionRepresentationFailure(result, err)
		}
		result.Header = prepared.headers
		result.Body = prepared.downstream
		result.ClassificationBody = prepared.inspectable
		return result
	}
	if result.ExecutionError != nil &&
		(input.RouteMode == execution.RouteConverted || len(result.Body) == 0) {
		result.Body = encodeClientErrorBody(input.ClientProtocol, result.StatusCode, *result.ExecutionError)
		result.Header = result.Header.Clone()
		if result.Header == nil {
			result.Header = make(http.Header)
		}
		deleteHeaderField(result.Header, "Content-Encoding")
		deleteHeaderField(result.Header, "Content-Length")
		invalidateRewrittenBodyHeaders(result.Header)
		result.Header.Set("Content-Type", "application/json")
	}
	prepared := representation.prepareErrorRepresentation(input, result.Header, result.Body, secrets)
	result.Header = prepared.headers
	result.Body = prepared.downstream
	result.ClassificationBody = prepared.classification
	return result
}

func encodeClientErrorBody(
	clientProtocol protocol.Protocol,
	status int,
	evidence execution.ErrorEvidence,
) []byte {
	message := strings.TrimSpace(evidence.Summary)
	if message == "" {
		message = "upstream request failed"
	}
	typeValue := strings.TrimSpace(evidence.Type)
	if typeValue == "" {
		switch evidence.Hint {
		case execution.FailureHintInvalidCredential:
			typeValue = "authentication_error"
		case execution.FailureHintRateLimited:
			typeValue = "rate_limit_error"
		case execution.FailureHintModelUnavailable:
			typeValue = "model_not_found"
		default:
			typeValue = "api_error"
		}
	}
	codeValue := strings.TrimSpace(evidence.Code)

	var value any
	switch clientProtocol {
	case protocol.Anthropic:
		payload := struct {
			Type  string `json:"type"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}{Type: "error"}
		payload.Error.Type = typeValue
		payload.Error.Message = message
		value = payload
	case protocol.Gemini:
		if status < 100 || status > 599 {
			status = http.StatusBadGateway
		}
		if codeValue == "" {
			codeValue = strings.ToUpper(typeValue)
		}
		payload := struct {
			Error struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				Status  string `json:"status"`
			} `json:"error"`
		}{}
		payload.Error.Code = status
		payload.Error.Message = message
		payload.Error.Status = codeValue
		value = payload
	default:
		payload := struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code,omitempty"`
			} `json:"error"`
		}{}
		payload.Error.Message = message
		payload.Error.Type = typeValue
		payload.Error.Code = codeValue
		value = payload
	}
	body, err := json.Marshal(value)
	if err != nil {
		return []byte(`{"error":{"message":"upstream request failed","type":"api_error"}}`)
	}
	return body
}

func executionRepresentationFailure(result UpstreamResult, err error) UpstreamResult {
	evidence := execution.ErrorEvidence{
		Kind: execution.ErrorKindInternal, OriginHint: execution.ErrorOriginInternal,
		ScopeHint: execution.ErrorScopeRequest, Code: "response_representation_invalid",
		Summary:      "Upstream response representation could not be processed.",
		ReplaySafety: execution.ReplaySafetyUnknown,
	}
	if result.ResponseStarted {
		evidence.StatusCode = result.StatusCode
	}
	return UpstreamResult{
		Err:               err,
		StatusCode:        result.StatusCode,
		RequestWritten:    result.RequestWritten,
		DispatchState:     result.DispatchState,
		ResponseStarted:   result.ResponseStarted,
		UpstreamProtocol:  result.UpstreamProtocol,
		AppliedReasoning:  result.AppliedReasoning.Clone(),
		UpstreamRequestID: result.UpstreamRequestID,
		ExecutionError:    &evidence,
		ErrorSummary:      evidence.Summary,
	}
}

func newExecutionAttemptSpec(input ForwardInput) (execution.AttemptSpec, error) {
	if input.Request == nil {
		return execution.AttemptSpec{}, fmt.Errorf("request is required")
	}
	headers := cloneEndToEndHeaders(input.Request.Header)
	removeDownstreamCredentials(headers)
	for name, value := range input.Group.HeaderRules.Set {
		headers.Set(name, strings.ReplaceAll(value, "${API_KEY}", input.APIKey))
	}
	for _, name := range input.Group.HeaderRules.Remove {
		headers.Del(name)
	}
	sanitizeUpstreamRequestHeaders(headers)
	headers.Set("Accept-Encoding", "identity")
	spec := execution.NewAttemptSpec(execution.AttemptSpec{
		RequestID:              input.RequestID,
		AttemptID:              input.AttemptID,
		Sequence:               input.AttemptSequence,
		ChannelID:              input.ChannelID,
		RouteMode:              input.RouteMode,
		ClientProtocol:         input.ClientProtocol,
		Operation:              input.Operation,
		RouteRequirement:       input.RouteRequirement,
		ClientModel:            input.ExternalModel,
		UpstreamModel:          input.UpstreamModelID,
		Method:                 input.Request.Method,
		Path:                   input.Request.Path,
		RawQuery:               input.Request.RawQuery,
		Header:                 headers,
		Body:                   input.Request.Body,
		IncludeUsage:           input.ObserveUsage && input.Group.InjectUsageOptions,
		ForceCredentialRefresh: input.ForceCredentialRefresh,
		ContinuityKey:          input.ContinuityKey,
		TargetConfig:           input.TargetConfig,
		Timeouts: execution.AttemptTimeouts{
			FirstByte:  input.Group.Timeouts.FirstByte,
			Request:    input.Group.Timeouts.Request,
			StreamIdle: input.Group.Timeouts.StreamIdle,
		},
		Credential:       input.Credential,
		Proxy:            input.Proxy,
		ProxyFingerprint: input.ProxyFingerprint,
	})
	if err := spec.Validate(); err != nil {
		return execution.AttemptSpec{}, err
	}
	return spec, nil
}

func upstreamFromExecutionResult(
	ctx context.Context,
	input ForwardInput,
	result execution.AttemptResult,
) UpstreamResult {
	upstream := baseExecutionResult(input, result.DispatchState, result.ResponseStarted,
		result.StatusCode, result.Header, result.Model, result.UpstreamRequestID,
		result.Usage, result.Error)
	if result.AppliedReasoning != nil {
		upstream.AppliedReasoning = result.AppliedReasoning.Clone()
	}
	upstream.UpstreamProtocol = result.UpstreamProtocol
	if input.ClientProtocol == protocol.OpenAIImages {
		// AttemptResult owns Body after the executor returns. The buffered Images
		// representation consumes it synchronously, so move that ownership across
		// the internal boundary instead of cloning a large base64 payload twice.
		upstream.Body = result.Body
		upstream.ClassificationBody = result.Body
	} else {
		upstream.Body = append([]byte(nil), result.Body...)
		upstream.ClassificationBody = append([]byte(nil), result.Body...)
	}
	if !result.ResponseStarted && result.Error != nil {
		upstream.Err = executionFailureError(ctx, result.Error)
	}
	return upstream
}

func upstreamFromExecutionStreamResult(
	ctx context.Context,
	input ForwardInput,
	result execution.StreamResult,
	streamUsage *execution.UsageEvidence,
) UpstreamResult {
	usageEvidence := result.Usage
	if usageEvidence == nil {
		usageEvidence = streamUsage
	}
	upstream := baseExecutionResult(input, result.DispatchState, result.ResponseStarted,
		result.StatusCode, result.Header, result.Model, result.UpstreamRequestID,
		usageEvidence, result.Error)
	if result.AppliedReasoning != nil {
		upstream.AppliedReasoning = result.AppliedReasoning.Clone()
	}
	upstream.UpstreamProtocol = result.UpstreamProtocol
	if !result.ResponseStarted && result.Error != nil {
		upstream.Err = executionFailureError(ctx, result.Error)
	}
	return upstream
}

func baseExecutionResult(
	input ForwardInput,
	dispatchState execution.DispatchState,
	responseStarted bool,
	statusCode int,
	header http.Header,
	model string,
	upstreamRequestID string,
	usageEvidence *execution.UsageEvidence,
	errorEvidence *execution.ErrorEvidence,
) UpstreamResult {
	result := UpstreamResult{
		StatusCode:            statusCode,
		Header:                cloneEndToEndHeaders(header),
		UpstreamReportedModel: model,
		ResponseModelObserved: model != "",
		RequestWritten:        dispatchState == execution.DispatchMaybeSent,
		DispatchState:         dispatchState,
		ResponseStarted:       responseStarted,
		UpstreamRequestID:     upstreamRequestID,
	}
	if usageEvidence != nil {
		result.Usage = usageEvidence.Normalized
	} else if input.ObserveUsage {
		result.Usage = usage.Result{State: usage.StateMissing}
	} else {
		result.Usage = usage.Result{State: usage.StateNotApplicable}
	}
	if errorEvidence != nil {
		copy := errorEvidence.Clone()
		result.ExecutionError = &copy
		result.ErrorSummary = copy.Summary
		result.ProviderErrorBeforeCommit = responseStarted &&
			statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices
	}
	return result
}

func executionFailureError(ctx context.Context, evidence *execution.ErrorEvidence) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if evidence == nil {
		return fmt.Errorf("upstream execution failed")
	}
	switch evidence.Kind {
	case execution.ErrorKindCanceled:
		return context.Canceled
	case execution.ErrorKindTimeout:
		return upstreamExecutionTimeoutError{}
	case execution.ErrorKindInvalidRequest, execution.ErrorKindProvider, execution.ErrorKindInternal:
		return fmt.Errorf("%w: upstream execution failed", ErrUpstreamProtocol)
	default:
		return fmt.Errorf("upstream execution failed")
	}
}

type upstreamExecutionTimeoutError struct{}

func (upstreamExecutionTimeoutError) Error() string   { return "upstream execution timed out" }
func (upstreamExecutionTimeoutError) Timeout() bool   { return true }
func (upstreamExecutionTimeoutError) Temporary() bool { return true }

func executionInputFailure(cause error) UpstreamResult {
	if cause == nil {
		cause = fmt.Errorf("execution runtime is unavailable")
	}
	evidence := execution.ErrorEvidence{
		Kind:    execution.ErrorKindInvalidRequest,
		Summary: "invalid execution attempt",
	}
	return UpstreamResult{
		Err:            fmt.Errorf("%w: %v", ErrUpstreamProtocol, cause),
		DispatchState:  execution.DispatchNotSent,
		ExecutionError: &evidence,
		ErrorSummary:   evidence.Summary,
	}
}

func appendExecutionErrorBody(current, chunk []byte) []byte {
	remaining := maxStreamingErrorBodyBytes - len(current)
	if remaining <= 0 {
		return current
	}
	if len(chunk) > remaining {
		chunk = chunk[:remaining]
	}
	return append(current, chunk...)
}

func executionStreamProtocolFailure(cause error) error {
	if cause == nil {
		cause = fmt.Errorf("invalid upstream SSE stream")
	}
	return &streamFailure{
		kind: streamFailureProtocol,
		err:  fmt.Errorf("%w: %v", ErrUpstreamProtocol, cause),
	}
}

func preferCapturedStreamUsage(
	current usage.Result,
	captured usage.Result,
) usage.Result {
	switch captured.State {
	case usage.StateComplete:
		if current.State != usage.StateComplete {
			return captured
		}
	case usage.StatePartial:
		if current.State == usage.StateMissing || current.State == "" {
			return captured
		}
	case usage.StateMissing:
		if current.State == "" {
			return captured
		}
	}
	return current
}

func executionStreamObservation(
	ctx context.Context,
	terminal execution.StreamResult,
	downstreamErr error,
	streamEvents *streamEventObserver,
) StreamObservation {
	if downstreamErr != nil {
		return observeStreamTermination(ctx, downstreamErr, streamEvents)
	}
	if terminal.Error == nil {
		return streamEvents.endObservation()
	}
	switch terminal.Error.Kind {
	case execution.ErrorKindCanceled:
		return observeStreamTermination(ctx, context.Canceled, streamEvents)
	case execution.ErrorKindTimeout:
		return streamTerminalObservation(StreamEndIdleTimeout)
	case execution.ErrorKindProvider, execution.ErrorKindHTTP:
		observation := streamEvents.endObservation()
		if observation.EndReason == StreamEndSSEError {
			return observation
		}
		return StreamObservation{
			EndReason:    StreamEndSSEError,
			ErrorSummary: terminal.Error.Summary,
		}
	case execution.ErrorKindInvalidRequest, execution.ErrorKindInternal:
		return streamTerminalObservation(StreamEndUpstreamProtocolError)
	default:
		return streamTerminalObservation(StreamEndUpstreamTerminated)
	}
}

var _ AttemptForwarder = (*ExecutionForwarder)(nil)
