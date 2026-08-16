package cpa

import (
	"context"
	"errors"
	"net/http"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

// providerCredential is deliberately opaque to the shared CPA adapter. Each
// bridge owns its credential schema and the values required for safe redaction.
type providerCredential interface {
	redactionValues() []string
}

// requestScopedFailure recognizes provider failures that belong to the current
// request rather than to the selected credential. Provider bridges use this
// signal to preserve the stronger classification across the CPA boundary.
func requestScopedFailure(err error) bool {
	if err == nil {
		return false
	}
	var scoped interface {
		IsRequestScoped() bool
	}
	return errors.As(err, &scoped) && scoped != nil && scoped.IsRequestScoped()
}

type providerRequest struct {
	Model           string
	Payload         []byte
	Format          string
	Headers         http.Header
	OriginalRequest []byte
}

type providerResponse struct {
	Payload                []byte
	Headers                http.Header
	AppliedReasoningEffort string
}

type providerStreamResponse struct {
	Headers                http.Header
	Chunks                 <-chan providerStreamChunk
	AppliedReasoningEffort string
}

type providerStreamChunk struct {
	Payload []byte
	Err     error
}

// providerBridge is the narrow provider-specific boundary below the shared
// CPA attempt, timeout, streaming, usage, and response-conversion lifecycle.
type providerBridge interface {
	ProviderKind() channel.ProviderKind
	UpstreamProtocol() protocol.Protocol
	ValidateRouteCapability(channel.RouteDescriptor) error
	ParseCredential([]byte) (providerCredential, error)
	Execute(context.Context, string, providerCredential, providerRequest) (providerResponse, error)
	ExecuteStream(context.Context, string, providerCredential, providerRequest) (*providerStreamResponse, error)
	ClassifyError(context.Context, error, providerCredential) (int, *execution.ErrorEvidence)
}

func indexProviderBridges(bridges ...providerBridge) map[channel.ProviderKind]providerBridge {
	indexed := make(map[channel.ProviderKind]providerBridge, len(bridges))
	for _, bridge := range bridges {
		if bridge == nil || !bridge.ProviderKind().Valid() {
			continue
		}
		indexed[bridge.ProviderKind()] = bridge
	}
	return indexed
}
