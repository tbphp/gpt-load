package gateway

import (
	"context"
	"net/http"

	"gpt-load/internal/concurrency"
)

var (
	reasonAccessKeyConcurrency = reason{http.StatusTooManyRequests, "access_key_concurrency_limit", "Access key concurrency limit reached."}
	reasonGlobalConcurrency    = reason{http.StatusServiceUnavailable, "global_concurrency_limit", "Gateway concurrency limit reached."}
	reasonUpstreamConcurrency  = reason{http.StatusServiceUnavailable, "upstream_concurrency_limit", "Eligible upstream targets are at their concurrency limit."}
)

func requestConcurrencyReason(blocked concurrency.Subject) reason {
	if blocked == concurrency.Global {
		return reasonGlobalConcurrency
	}
	return reasonAccessKeyConcurrency
}

// Keep the slot until the forwarder has stopped using the upstream, including
// the complete stream. The attempt scope also releases on a panic.
func (h *Handler) forwardWithConcurrency(ctx context.Context, input ForwardInput, stream bool, writer http.ResponseWriter, lease *concurrency.Lease) UpstreamResult {
	defer lease.Release()
	if stream {
		return h.forwarder.ForwardStream(ctx, input, writer)
	}
	return h.forwarder.Forward(ctx, input)
}
