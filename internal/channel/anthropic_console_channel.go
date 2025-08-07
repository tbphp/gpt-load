package channel

import (
	"context"
	"gpt-load/internal/models"
	"net/http"
)

func init() {
	Register("anthropic_console", newAnthropicConsoleChannel)
}

type AnthropicConsoleChannel struct {
	*AnthropicChannel
}

func newAnthropicConsoleChannel(f *Factory, group *models.Group) (ChannelProxy, error) {
	anthropicChannel, err := newAnthropicChannel(f, group)
	if err != nil {
		return nil, err
	}

	return &AnthropicConsoleChannel{
		AnthropicChannel: anthropicChannel.(*AnthropicChannel),
	}, nil
}

func (ch *AnthropicConsoleChannel) ModifyRequest(req *http.Request, apiKey *models.APIKey, group *models.Group) {
	req.Header.Set("Authorization", "Bearer "+apiKey.KeyValue)
	req.Header.Set("anthropic-version", "2023-06-01")
}

func (ch *AnthropicConsoleChannel) ValidateKey(ctx context.Context, key string) (bool, error) {
	// Wrap HTTP client to intercept and modify auth headers
	originalTransport := ch.HTTPClient.Transport
    ch.HTTPClient.Transport = &headerRewriteTransport{
        base: originalTransport,
        keyRewriter: func(req *http.Request) {
            if apiKey := req.Header.Get("x-api-key"); apiKey != "" {
                req.Header.Del("x-api-key")
                req.Header.Set("Authorization", "Bearer "+apiKey)
            }
        },
    }
    defer func() {
        ch.HTTPClient.Transport = originalTransport
    }()
    return ch.AnthropicChannel.ValidateKey(ctx, key)
}

type headerRewriteTransport struct {
    base        http.RoundTripper
    keyRewriter func(*http.Request)
}

func (t *headerRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
    t.keyRewriter(req)
    return t.base.RoundTrip(req)
}
