package gateway

import (
	"context"
	"errors"

	"gpt-load/internal/httplifecycle"
)

func isServerShutdown(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	return errors.Is(context.Cause(ctx), httplifecycle.ErrServerShutdown)
}

func cancellationErrorCode(ctx context.Context) string {
	if isServerShutdown(ctx) {
		return "server_shutdown"
	}
	return "client_canceled"
}
