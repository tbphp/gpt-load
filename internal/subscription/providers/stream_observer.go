package providers

import (
	"context"
	"io"
	"net/http"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v8/gptload-embedded/embedded"
)

// WithStreamBodyObserver 通过订阅 Provider 边界挂接上游流观测，保持 CPA 依赖封装。
func WithStreamBodyObserver(ctx context.Context, wrap func(io.ReadCloser, http.Header) io.ReadCloser) context.Context {
	return cpaembedded.WithStreamBodyObserver(ctx, wrap)
}
