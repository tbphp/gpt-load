package cpa

import (
	"context"
	"errors"
	"net/http"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/outboundproxy"
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

// credentialScopedFailure reports whether a provider explicitly classified a
// failure as belonging to the selected credential. The second result is false
// when the provider did not supply this stronger scope signal.
func credentialScopedFailure(err error) (bool, bool) {
	if err == nil {
		return false, false
	}
	var scoped interface {
		IsCredentialScoped() bool
	}
	if !errors.As(err, &scoped) || scoped == nil {
		return false, false
	}
	return scoped.IsCredentialScoped(), true
}

func annotateProviderErrorEvidence(evidence *execution.ErrorEvidence, err error) {
	if evidence == nil {
		return
	}
	switch evidence.Kind {
	case execution.ErrorKindTransport, execution.ErrorKindTimeout,
		execution.ErrorKindHTTP, execution.ErrorKindProvider:
		evidence.OriginHint = execution.ErrorOriginUpstream
	case execution.ErrorKindCanceled:
		evidence.OriginHint = execution.ErrorOriginDownstream
	case execution.ErrorKindInvalidRequest:
		evidence.OriginHint = execution.ErrorOriginClient
	case execution.ErrorKindConversionUnsupported, execution.ErrorKindInternal:
		evidence.OriginHint = execution.ErrorOriginInternal
	}
	switch evidence.Hint {
	case execution.FailureHintInvalidCredential,
		execution.FailureHintRefreshRequired,
		execution.FailureHintReauthorizationRequired:
		evidence.ScopeHint = execution.ErrorScopeCredential
	case execution.FailureHintRequestRejected:
		evidence.ScopeHint = execution.ErrorScopeRequest
	case execution.FailureHintCandidateUnavailable,
		execution.FailureHintModelUnavailable:
		evidence.ScopeHint = execution.ErrorScopeModel
	case execution.FailureHintHostError:
		evidence.ScopeHint = execution.ErrorScopeGroup
	case execution.FailureHintRateLimited:
		if credentialScoped, known := credentialScopedFailure(err); known && credentialScoped {
			evidence.ScopeHint = execution.ErrorScopeCredential
		} else if requestScopedFailure(err) {
			evidence.ScopeHint = execution.ErrorScopeRequest
		}
	default:
		switch evidence.Kind {
		case execution.ErrorKindTransport, execution.ErrorKindTimeout:
			evidence.ScopeHint = execution.ErrorScopeGroup
		case execution.ErrorKindCanceled, execution.ErrorKindInvalidRequest:
			evidence.ScopeHint = execution.ErrorScopeRequest
		}
	}
}

type providerRequest struct {
	AttemptID       string
	Model           string
	Payload         []byte
	Format          string
	Headers         http.Header
	OriginalRequest []byte
	// ContinuityKey is a private, tenant-scoped key used only by providers
	// whose tool/thinking protocol needs an isolated multi-request replay lane.
	ContinuityKey        string
	ProxyURL             string
	ProxyFromEnvironment bool
}

type cpaProxySettings struct {
	URL             string
	FromEnvironment bool
}

func proxySettingsForAttempt(effective outboundproxy.Effective) (cpaProxySettings, error) {
	if effective.Config.Mode == "" {
		return cpaProxySettings{}, nil
	}
	effective, err := outboundproxy.NormalizeEffective(effective)
	if err != nil {
		return cpaProxySettings{}, err
	}
	switch effective.Config.Mode {
	case outboundproxy.ModeDirect:
		return cpaProxySettings{URL: "direct"}, nil
	case outboundproxy.ModeEnvironment:
		return cpaProxySettings{FromEnvironment: true}, nil
	case outboundproxy.ModeCustom:
		switch {
		case len(effective.Config.URL) >= len("http://") && effective.Config.URL[:len("http://")] == "http://",
			len(effective.Config.URL) >= len("socks5://") && effective.Config.URL[:len("socks5://")] == "socks5://":
			return cpaProxySettings{URL: effective.Config.URL}, nil
		default:
			return cpaProxySettings{}, outboundproxy.ErrInvalidConfig
		}
	default:
		return cpaProxySettings{}, outboundproxy.ErrInvalidConfig
	}
}

type providerResponse struct {
	Payload                []byte
	Headers                http.Header
	AppliedReasoningEffort string
	Local                  bool
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

// providerTokenCounter is implemented only by bridges with an explicit
// upstream CountTokens contract.
type providerTokenCounter interface {
	CountTokens(context.Context, string, providerCredential, providerRequest) (providerResponse, error)
}

// providerRequestValidator is an optional, provider-specific pre-dispatch
// check. It is intentionally narrow: the shared adapter only needs to reject
// inputs that a declared route cannot faithfully represent before preparing a
// subscription credential or sending anything upstream.
type providerRequestValidator interface {
	ValidateRequest(providerRequest) error
}

// providerLocalTokenCounter is a deliberately narrow contract for providers
// whose CountTokens implementation never contacts an upstream service.
type providerLocalTokenCounter interface {
	ValidateLocalTokenCount(providerRequest) error
	CountTokensLocal(context.Context, providerRequest) (providerResponse, error)
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
