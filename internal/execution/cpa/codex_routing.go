package cpa

import (
	"context"
	"net/http"

	"gpt-load/internal/channel"
	"gpt-load/internal/codexrouting"
	"gpt-load/internal/execution"
)

func (a *Adapter) SetCodexRouting(store *codexrouting.Store) {
	if a == nil {
		return
	}
	a.routing = store
}

func (a *Adapter) applyCodexRouting(ctx context.Context, spec execution.AttemptSpec, headers http.Header) {
	if a == nil || a.routing == nil || channel.ID(spec.ChannelID) != channel.Codex {
		return
	}
	if headers == nil {
		return
	}
	a.routing.Inject(ctx, spec.Credential.ID, spec.UpstreamModel, headers)
}

func (a *Adapter) observeCodexRouting(ctx context.Context, spec execution.AttemptSpec, headers http.Header, statusCode int) {
	if a == nil || a.routing == nil || channel.ID(spec.ChannelID) != channel.Codex || headers == nil {
		return
	}
	a.routing.Capture(ctx, spec.Credential.ID, spec.UpstreamModel, headers, statusCode)
}
