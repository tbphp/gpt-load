package gateway

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gpt-load/internal/execution"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/usage"
)

// ExecutionForwarder adapts the provider-neutral executor to the gateway's
// downstream commit boundary while the handler orchestration remains intact.
type ExecutionForwarder struct {
	executor       execution.Executor
	representation *responseProcessor
	writeTimeout   time.Duration
}

func NewExecutionForwarder(executor execution.Executor) *ExecutionForwarder {
	return &ExecutionForwarder{
		executor: executor, representation: &responseProcessor{redactor: redact.New()},
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
	result := upstreamFromExecutionResult(ctx, input, forwarder.executor.Execute(ctx, spec))
	return forwarder.prepareBufferedResult(input, result)
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

	var (
		ready         *execution.StreamEvent
		committed     bool
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
			if !committed {
				committed = true
				if err := commitStream(controller, ready.StatusCode, ready.Header, event.Data); err != nil {
					downstreamErr = err
					return err
				}
				if input.OnStreamReady != nil {
					input.OnStreamReady()
				}
				if input.OnFirstResponse != nil {
					input.OnFirstResponse()
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
			return nil
		default:
			downstreamErr = fmt.Errorf("%w: unsupported execution stream event", ErrUpstreamProtocol)
			return downstreamErr
		}
	}

	terminal := forwarder.executor.ExecuteStream(ctx, spec, sink)
	result := upstreamFromExecutionStreamResult(ctx, input, terminal, streamUsage)
	result.Committed = committed
	if len(errorBody) > 0 {
		result.Body = append([]byte(nil), errorBody...)
		result.ClassificationBody = append([]byte(nil), errorBody...)
	}
	if downstreamErr != nil {
		result.Err = downstreamErr
	}
	if committed {
		result.Stream = executionStreamObservation(ctx, terminal, downstreamErr)
	} else if ready != nil {
		result.StatusCode = ready.StatusCode
		result.Header = ready.Header.Clone()
		result.ResponseStarted = true
		result.UpstreamRequestID = ready.UpstreamRequestID
	}
	if !committed && result.HasResponse() {
		result = forwarder.prepareBufferedResult(input, result)
	}
	return result
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
	prepared := representation.prepareErrorRepresentation(input, result.Header, result.Body, secrets)
	result.Header = prepared.headers
	result.Body = prepared.downstream
	result.ClassificationBody = prepared.classification
	return result
}

func executionRepresentationFailure(result UpstreamResult, err error) UpstreamResult {
	return UpstreamResult{
		Err: err, RequestWritten: result.RequestWritten, DispatchState: result.DispatchState,
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
		RequestID:      input.RequestID,
		AttemptID:      input.AttemptID,
		Sequence:       input.AttemptSequence,
		ChannelID:      input.ChannelID,
		ClientProtocol: input.ClientProtocol,
		Operation:      input.Operation,
		ClientModel:    input.ExternalModel,
		UpstreamModel:  input.UpstreamModelID,
		Method:         input.Request.Method,
		Path:           input.Request.Path,
		RawQuery:       input.Request.RawQuery,
		Header:         headers,
		Body:           input.Request.Body,
		IncludeUsage:   input.ObserveUsage && input.Group.InjectUsageOptions,
		TargetConfig:   input.TargetConfig,
		Timeouts: execution.AttemptTimeouts{
			Connect:    input.Group.Timeouts.Connect,
			FirstByte:  input.Group.Timeouts.FirstByte,
			Request:    input.Group.Timeouts.Request,
			StreamIdle: input.Group.Timeouts.StreamIdle,
		},
		Credential: input.Credential,
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
	upstream.Body = append([]byte(nil), result.Body...)
	upstream.ClassificationBody = append([]byte(nil), result.Body...)
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
		return context.DeadlineExceeded
	case execution.ErrorKindInvalidRequest, execution.ErrorKindProvider, execution.ErrorKindInternal:
		return fmt.Errorf("%w: upstream execution failed", ErrUpstreamProtocol)
	default:
		return fmt.Errorf("upstream execution failed")
	}
}

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

func executionStreamObservation(
	ctx context.Context,
	terminal execution.StreamResult,
	downstreamErr error,
) StreamObservation {
	if downstreamErr != nil {
		return prioritizeStreamObservation(ctx, downstreamErr, StreamObservation{})
	}
	if terminal.Error == nil {
		return StreamObservation{EndReason: StreamEndCleanEOF}
	}
	switch terminal.Error.Kind {
	case execution.ErrorKindCanceled:
		return streamTerminalObservation(StreamEndClientCanceled)
	case execution.ErrorKindTimeout:
		return streamTerminalObservation(StreamEndIdleTimeout)
	case execution.ErrorKindProvider, execution.ErrorKindHTTP:
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
