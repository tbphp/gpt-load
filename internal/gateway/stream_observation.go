package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"gpt-load/internal/dialect"
	"gpt-load/internal/usage"
)

type streamFailureKind uint8

const (
	streamFailureUpstreamRead streamFailureKind = iota + 1
	streamFailureProtocol
	streamFailureIdle
	streamFailureDownstreamWrite
	streamFailureClientCanceled
)

type streamFailure struct {
	kind streamFailureKind
	err  error
}

func (failure *streamFailure) Error() string { return failure.err.Error() }
func (failure *streamFailure) Unwrap() error { return failure.err }

type StreamEndReason uint8

const (
	StreamEndNone StreamEndReason = iota
	StreamEndCleanEOF
	StreamEndSSEError
	StreamEndUpstreamTerminated
	StreamEndUpstreamProtocolError
	StreamEndIdleTimeout
	StreamEndDownstreamWriteFailure
	StreamEndClientCanceled
	StreamEndServerShutdown
	StreamEndProviderIncomplete
)

type StreamObservation struct {
	EndReason    StreamEndReason
	ErrorSummary string
}

type streamEventObserver struct {
	classifier          dialect.StreamEventClassifier
	terminalRequired    bool
	sawTerminal         bool
	terminalForwarded   bool
	terminalDisposition dialect.StreamEventDisposition
	eventCount          int
	firstProviderError  bool
	sawErrorEvent       bool
	firstSummary        string
	firstErrorPayload   []byte
	usage               *streamUsageCapture
}

// sseEventObservationBuffer frames arbitrary executor data chunks without
// changing their wire bytes. push returns the complete, successfully observed
// wire prefix; callers that use it may mark a returned terminal boundary
// forwarded only after that prefix has been written and flushed successfully.
type sseEventObservationBuffer struct {
	pending         []byte
	pendingTerminal bool
	maxEventBytes   int
	scanner         sseRewriteBoundaryScanner
	observe         func(dialect.StreamEvent, bool) (bool, error)
}

func newSSEEventObservationBuffer(
	maxEventBytes int,
	observe func(dialect.StreamEvent, bool) (bool, error),
) *sseEventObservationBuffer {
	return &sseEventObservationBuffer{
		maxEventBytes: normalizedSSEEventLimit(maxEventBytes),
		observe:       observe,
	}
}

func (buffer *sseEventObservationBuffer) push(chunk []byte) ([]byte, bool, error) {
	if buffer == nil || buffer.observe == nil {
		return nil, false, fmt.Errorf("SSE observation callback is required")
	}
	buffer.pending = append(buffer.pending, chunk...)
	wire := buffer.pending
	consumed := 0
	terminal := false
	for {
		waitingTerminal := buffer.pendingTerminal
		optionalLF, overflow := buffer.scanner.ConsumeOptionalLineFeed(
			buffer.pending,
			false,
			buffer.maxEventBytes,
		)
		if overflow {
			return nil, false, errSSEEventTooLarge
		}
		if waitingTerminal && !buffer.scanner.optionalLineFeed {
			buffer.pendingTerminal = false
			terminal = true
		}
		if optionalLF > 0 {
			buffer.discard(optionalLF)
			consumed += optionalLF
			continue
		}

		eventEnd, complete := buffer.scanner.Find(buffer.pending)
		if !complete {
			if len(buffer.pending) > buffer.maxEventBytes {
				return nil, false, errSSEEventTooLarge
			}
			return wire[:consumed], terminal, nil
		}
		if eventEnd > buffer.maxEventBytes {
			return nil, false, errSSEEventTooLarge
		}

		terminalEvent, err := buffer.observeEvent(buffer.pending[:eventEnd])
		if err != nil {
			return nil, false, err
		}
		buffer.discard(eventEnd)
		consumed += eventEnd
		buffer.scanner.AfterEvent(eventEnd, eventEnd)
		if terminalEvent && buffer.scanner.optionalLineFeed {
			buffer.pendingTerminal = true
		} else {
			terminal = terminal || terminalEvent
		}
	}
}

func (buffer *sseEventObservationBuffer) observeEvent(event []byte) (bool, error) {
	lines := splitSSEEventLines(event)
	dataValues := make([][]byte, 0, len(lines))
	var eventName []byte
	for index := range lines {
		if name, ok := parseSSEEventName(lines[index].content); ok {
			eventName = name
		}
		if lines[index].isData {
			dataValues = append(dataValues, lines[index].data)
		}
	}
	if len(dataValues) == 0 {
		return false, nil
	}
	payload := bytes.Join(dataValues, []byte{'\n'})
	if len(payload) == 0 {
		return false, nil
	}
	return buffer.observe(
		dialect.StreamEvent{Name: string(eventName), Payload: payload},
		bytes.Equal(eventName, []byte("error")) || isSSEErrorPayload(payload),
	)
}

func (buffer *sseEventObservationBuffer) finish() error {
	if buffer == nil {
		return fmt.Errorf("SSE observation buffer is required")
	}
	optionalLF, overflow := buffer.scanner.ConsumeOptionalLineFeed(
		buffer.pending,
		true,
		buffer.maxEventBytes,
	)
	if overflow {
		return errSSEEventTooLarge
	}
	if optionalLF > 0 {
		buffer.discard(optionalLF)
	}
	if buffer.pendingTerminal && !buffer.scanner.optionalLineFeed {
		buffer.pendingTerminal = false
	}
	if len(buffer.pending) > 0 {
		return errSSEEventIncomplete
	}
	buffer.scanner.Reset()
	return nil
}

func (buffer *sseEventObservationBuffer) discard(count int) {
	buffer.pending = buffer.pending[count:]
	if len(buffer.pending) == 0 {
		buffer.pending = nil
	}
}

func newStreamEventObserver(
	selected dialect.Dialect,
	capture *streamUsageCapture,
) *streamEventObserver {
	observer := &streamEventObserver{usage: capture}
	classifier, ok := selected.(dialect.StreamEventClassifier)
	if !ok {
		return observer
	}
	observer.classifier = classifier
	observer.terminalRequired = classifier.RequiresTerminalEvent()
	return observer
}

func safeClassifyStreamEvent(
	classifier dialect.StreamEventClassifier,
	event dialect.StreamEvent,
) (result dialect.StreamEventClassification, err error, panicked bool) {
	defer func() {
		if recover() != nil {
			result = dialect.StreamEventClassification{}
			err = nil
			panicked = true
		}
	}()
	result, err = classifier.ClassifyStreamEvent(dialect.StreamEvent{
		Name:    event.Name,
		Payload: bytes.Clone(event.Payload),
	})
	return result, err, false
}

func validStreamEventDisposition(value dialect.StreamEventDisposition) bool {
	return value >= dialect.StreamEventContinue &&
		value <= dialect.StreamEventFailed
}

func (observer *streamEventObserver) classify(
	event dialect.StreamEvent,
	genericProviderError bool,
) (bool, error) {
	if observer == nil {
		return genericProviderError, nil
	}
	if observer.sawTerminal {
		if bytes.Equal(bytes.TrimSpace(event.Payload), []byte("[DONE]")) {
			return false, nil
		}
		return false, fmt.Errorf(
			"%w: SSE data event received after terminal event",
			ErrUpstreamProtocol,
		)
	}

	classification := dialect.StreamEventClassification{
		Disposition: dialect.StreamEventContinue,
	}
	if observer.classifier != nil {
		var err error
		var panicked bool
		classification, err, panicked = safeClassifyStreamEvent(
			observer.classifier,
			event,
		)
		if panicked || err != nil ||
			!validStreamEventDisposition(classification.Disposition) {
			return false, fmt.Errorf(
				"%w: classify upstream SSE event",
				ErrUpstreamProtocol,
			)
		}
	}

	if classification.IsTerminal() {
		observer.sawTerminal = true
		observer.terminalDisposition = classification.Disposition
	}
	providerError := genericProviderError ||
		classification.IsProviderError()
	observer.eventCount++
	if observer.eventCount == 1 {
		observer.firstProviderError = providerError
	}
	return providerError, nil
}

func (observer *streamEventObserver) firstEventWasProviderError() bool {
	return observer != nil && observer.firstProviderError
}

func (observer *streamEventObserver) observeError(payload []byte, summary string) {
	if observer == nil {
		return
	}
	observer.sawErrorEvent = true
	if observer.firstSummary == "" {
		observer.firstSummary = summary
		observer.firstErrorPayload = bytes.Clone(payload)
	}
}

func (observer *streamEventObserver) observeUsageEvent(
	event dialect.StreamEvent,
) {
	if bytes.Equal(bytes.TrimSpace(event.Payload), []byte("[DONE]")) {
		return
	}
	if observer != nil && observer.usage != nil {
		observer.usage.observeEvent(event)
	}
}

func (observer *streamEventObserver) finalizeUsage() usage.Result {
	if observer == nil || observer.usage == nil {
		return usage.Result{State: usage.StateMissing}
	}
	return observer.usage.finalize()
}

func (observer *streamEventObserver) markTerminalForwarded() bool {
	if observer == nil || !observer.sawTerminal {
		return false
	}
	observer.terminalForwarded = true
	return true
}

func (observer *streamEventObserver) endObservation() StreamObservation {
	if observer == nil {
		return StreamObservation{EndReason: StreamEndCleanEOF}
	}
	if observer.sawErrorEvent {
		return StreamObservation{
			EndReason:    StreamEndSSEError,
			ErrorSummary: observer.firstSummary,
		}
	}
	if observer.sawTerminal &&
		observer.terminalDisposition == dialect.StreamEventIncomplete {
		return streamTerminalObservation(StreamEndProviderIncomplete)
	}
	return StreamObservation{EndReason: StreamEndCleanEOF}
}

func (observer *streamEventObserver) validateEOF() error {
	if observer == nil || !observer.terminalRequired || observer.sawTerminal {
		return nil
	}
	return &streamFailure{
		kind: streamFailureProtocol,
		err: fmt.Errorf(
			"%w: stream ended before required terminal event",
			ErrUpstreamProtocol,
		),
	}
}

func observeStreamTermination(
	ctx context.Context,
	err error,
	events *streamEventObserver,
) StreamObservation {
	observation := events.endObservation()
	if events != nil && events.terminalForwarded {
		if errors.Is(err, ErrUpstreamProtocol) {
			return prioritizeStreamObservation(nil, err, observation)
		}
		if errors.Is(err, context.Canceled) ||
			(ctx != nil && errors.Is(ctx.Err(), context.Canceled)) {
			return observation
		}
	}
	return prioritizeStreamObservation(ctx, err, observation)
}

func prioritizeStreamObservation(
	ctx context.Context,
	err error,
	observation StreamObservation,
) StreamObservation {
	if isServerShutdown(ctx) {
		return streamTerminalObservation(StreamEndServerShutdown)
	}
	if (ctx != nil && ctx.Err() != nil) || errors.Is(err, context.Canceled) {
		return streamTerminalObservation(StreamEndClientCanceled)
	}

	var failure *streamFailure
	if errors.As(err, &failure) {
		switch failure.kind {
		case streamFailureClientCanceled:
			return streamTerminalObservation(StreamEndClientCanceled)
		case streamFailureDownstreamWrite:
			return streamTerminalObservation(StreamEndDownstreamWriteFailure)
		case streamFailureIdle:
			return streamTerminalObservation(StreamEndIdleTimeout)
		case streamFailureProtocol:
			return streamTerminalObservation(StreamEndUpstreamProtocolError)
		case streamFailureUpstreamRead:
			return streamTerminalObservation(StreamEndUpstreamTerminated)
		}
	}

	switch {
	case errors.Is(err, ErrUpstreamProtocol):
		return streamTerminalObservation(StreamEndUpstreamProtocolError)
	case errors.Is(err, errStreamIdleTimeout):
		return streamTerminalObservation(StreamEndIdleTimeout)
	case err != nil:
		return streamTerminalObservation(StreamEndUpstreamTerminated)
	case observation.EndReason != StreamEndNone:
		return observation
	default:
		return StreamObservation{EndReason: StreamEndCleanEOF}
	}
}

func streamTerminalObservation(reason StreamEndReason) StreamObservation {
	code := streamErrorCode(reason)
	return StreamObservation{
		EndReason:    reason,
		ErrorSummary: fixedErrorSummary(code),
	}
}

func streamErrorCode(reason StreamEndReason) string {
	switch reason {
	case StreamEndCleanEOF, StreamEndNone:
		return ""
	case StreamEndSSEError:
		return "upstream_sse_error"
	case StreamEndUpstreamTerminated:
		return "upstream_stream_terminated"
	case StreamEndUpstreamProtocolError:
		return "upstream_protocol_error"
	case StreamEndIdleTimeout:
		return "upstream_stream_idle_timeout"
	case StreamEndDownstreamWriteFailure:
		return "downstream_write_failed"
	case StreamEndClientCanceled:
		return "client_canceled"
	case StreamEndServerShutdown:
		return "server_shutdown"
	case StreamEndProviderIncomplete:
		return "upstream_response_incomplete"
	default:
		return "upstream_stream_terminated"
	}
}
