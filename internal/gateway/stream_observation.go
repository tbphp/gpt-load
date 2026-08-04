package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"gpt-load/internal/dialect"
	"gpt-load/internal/telemetry"
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
	usage               *streamUsageCapture
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

func (observer *streamEventObserver) observeError(summary string) {
	if observer == nil {
		return
	}
	observer.sawErrorEvent = true
	if observer.firstSummary == "" {
		observer.firstSummary = summary
	}
}

func (observer *streamEventObserver) observeUsageEvent(
	event dialect.StreamEvent,
) {
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

func streamAttemptObservation(
	result UpstreamResult,
) (telemetry.FailureCategory, telemetry.Action) {
	return categoryForStream(result.Stream.EndReason), telemetry.ActionTerminate
}

func categoryForStream(reason StreamEndReason) telemetry.FailureCategory {
	switch reason {
	case StreamEndCleanEOF, StreamEndProviderIncomplete:
		return telemetry.FailureCategoryOK
	case StreamEndClientCanceled:
		return telemetry.FailureCategoryDownstreamCancel
	default:
		return telemetry.FailureCategoryAmbiguous
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
	case StreamEndProviderIncomplete:
		return "upstream_response_incomplete"
	default:
		return "upstream_stream_terminated"
	}
}
