package gateway

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/httplifecycle"
	"gpt-load/internal/protocol"
)

func TestSSEObservationRejectsEventAboveImagesLimit(t *testing.T) {
	limit := execution.SSEEventLimit(protocol.OpenAIImages)
	buffer := newSSEEventObservationBuffer(
		limit,
		func(dialect.StreamEvent, bool) (bool, error) { return false, nil },
	)
	_, err := buffer.push(bytes.Repeat([]byte{'x'}, limit+1))
	if !errors.Is(err, errSSEEventTooLarge) {
		t.Fatalf("push() error = %v, want %v", err, errSSEEventTooLarge)
	}
}

func TestPrioritizeStreamObservationIdentifiesServerShutdown(t *testing.T) {
	requestContext, cancel := context.WithCancelCause(context.Background())
	cancel(httplifecycle.ErrServerShutdown)

	observation := prioritizeStreamObservation(
		requestContext,
		context.Canceled,
		StreamObservation{},
	)
	if observation.EndReason != StreamEndServerShutdown ||
		streamErrorCode(observation.EndReason) != "server_shutdown" ||
		observation.ErrorSummary != fixedErrorSummary("server_shutdown") {
		t.Fatalf("shutdown observation = %#v", observation)
	}
}
