package gateway

import (
	"context"
	"testing"

	"gpt-load/internal/httplifecycle"
)

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
