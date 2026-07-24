package gateway

import (
	"context"
	"errors"

	"gpt-load/internal/telemetry"
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
)

type StreamObservation struct {
	EndReason    StreamEndReason
	ErrorSummary string
}

type streamEventObserver struct {
	sawErrorEvent bool
	firstSummary  string
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

func observeStreamTermination(
	ctx context.Context,
	err error,
	events *streamEventObserver,
) StreamObservation {
	observation := StreamObservation{EndReason: StreamEndCleanEOF}
	if events != nil && events.sawErrorEvent {
		observation = StreamObservation{
			EndReason:    StreamEndSSEError,
			ErrorSummary: events.firstSummary,
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
	case StreamEndCleanEOF:
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
	default:
		return "upstream_stream_terminated"
	}
}
