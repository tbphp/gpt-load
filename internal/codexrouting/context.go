package codexrouting

import "context"

type contextKey struct{}

// WithDiscovery marks a Codex request as a cookie-discovery probe. The
// transport still captures Set-Cookie, but it does not inject the jar.
func WithDiscovery(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, contextKey{}, true)
}

func IsDiscovery(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(contextKey{}).(bool)
	return value
}
