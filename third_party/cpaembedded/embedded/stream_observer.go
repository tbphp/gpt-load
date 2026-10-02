package embedded

import (
	"context"
	"io"
	"net/http"
)

type streamBodyObserverKey struct{}

// WithStreamBodyObserver observes upstream bytes before CPA protocol translation.
// The wrapper must preserve the reader's bytes, errors and close behavior.
func WithStreamBodyObserver(ctx context.Context, wrap func(io.ReadCloser, http.Header) io.ReadCloser) context.Context {
	return context.WithValue(ctx, streamBodyObserverKey{}, wrap)
}
