package gateway

import (
	"context"
	"testing"

	"gpt-load/internal/httplifecycle"
	"gpt-load/internal/telemetry"
)

func TestStreamAttemptObservationMapsCommittedTerminalsWithoutHealthJudge(t *testing.T) {
	tests := []struct {
		name         string
		reason       StreamEndReason
		wantCategory telemetry.FailureCategory
	}{
		{
			name:         "clean EOF",
			reason:       StreamEndCleanEOF,
			wantCategory: telemetry.FailureCategoryOK,
		},
		{
			name:         "SSE error",
			reason:       StreamEndSSEError,
			wantCategory: telemetry.FailureCategoryAmbiguous,
		},
		{
			name:         "upstream terminated",
			reason:       StreamEndUpstreamTerminated,
			wantCategory: telemetry.FailureCategoryAmbiguous,
		},
		{
			name:         "protocol error",
			reason:       StreamEndUpstreamProtocolError,
			wantCategory: telemetry.FailureCategoryAmbiguous,
		},
		{
			name:         "idle timeout",
			reason:       StreamEndIdleTimeout,
			wantCategory: telemetry.FailureCategoryAmbiguous,
		},
		{
			name:         "downstream write failure",
			reason:       StreamEndDownstreamWriteFailure,
			wantCategory: telemetry.FailureCategoryAmbiguous,
		},
		{
			name:         "client canceled",
			reason:       StreamEndClientCanceled,
			wantCategory: telemetry.FailureCategoryDownstreamCancel,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			category, action := streamAttemptObservation(UpstreamResult{
				Committed: true,
				Stream: StreamObservation{
					EndReason: test.reason,
				},
			})
			if category != test.wantCategory || action != telemetry.ActionTerminate {
				t.Fatalf(
					"streamAttemptObservation() = %q/%q, want %q/%q",
					category,
					action,
					test.wantCategory,
					telemetry.ActionTerminate,
				)
			}
		})
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
