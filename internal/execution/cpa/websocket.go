package cpa

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/execution/wsnative"
	"gpt-load/internal/protocol"
	"gpt-load/internal/subscription"
	"gpt-load/internal/subscription/providers/codex"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

type websocketProvider interface {
	openWebsocket(execution.AttemptSpec, providerCredential, string, string, func(http.Header, time.Time)) (execution.WebsocketSession, error)
}

// OpenWebsocket 使用既有凭据刷新和网络准备，Session 仍由单个下游连接拥有。
func (a *Adapter) OpenWebsocket(ctx context.Context, spec execution.AttemptSpec) (execution.WebsocketSession, execution.WebsocketResult) {
	result := execution.WebsocketResult{DispatchState: execution.DispatchNotSent}
	reject := func() (execution.WebsocketSession, execution.WebsocketResult) {
		result.Error = requestValidationEvidence(errors.New("unsupported native websocket request"))
		return nil, result
	}
	provider, baseURL, err := a.validateSpec(spec)
	if err != nil || spec.ClientProtocol != protocol.OpenAIResponses || spec.Operation != execution.OperationResponsesCreate || spec.RouteMode != execution.RouteNative {
		return reject()
	}
	opener, ok := provider.(websocketProvider)
	target, err := a.channels.ResolveExecutionTarget(channel.ID(spec.ChannelID), spec.TargetConfig)
	if !ok || err != nil || !target.ResponsesWebsocket.Native {
		return reject()
	}
	settings, err := proxySettingsForAttempt(spec.Proxy)
	if err != nil {
		return reject()
	}
	if settings.FromEnvironment {
		endpoint := baseURL
		if endpoint == "" {
			endpoint = "https://chatgpt.com"
		}
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if requestErr != nil {
			return reject()
		}
		proxyURL, proxyErr := http.ProxyFromEnvironment(request)
		if proxyErr != nil {
			return reject()
		}
		settings.URL = "direct"
		if proxyURL != nil {
			settings.URL = proxyURL.String()
		}
	}
	if settings.URL == "" {
		settings.URL = "direct"
	}
	ctx = subscriptionruntime.WithNetworkContext(ctx, subscriptionruntime.NetworkContext{Proxy: spec.Proxy, Fingerprint: spec.ProxyFingerprint})
	prepared, evidence := a.credentials.Prepare(ctx, channel.ID(spec.ChannelID), spec.Credential, spec.ForceCredentialRefresh)
	if evidence != nil {
		result.Error = evidence
		return nil, result
	}
	canonical := prepared.Canonical()
	credential, err := provider.ParseCredential(canonical)
	clear(canonical)
	if err != nil {
		return reject()
	}
	observationSpec := execution.AttemptSpec{Credential: execution.NewCredentialSnapshot(spec.Credential.ID, spec.Credential.Version, spec.Credential.IdentityGeneration, nil)}
	observed := &observedWebsocketSession{adapter: a, spec: observationSpec}
	s, err := opener.openWebsocket(spec, credential, baseURL, settings.URL, observed.observeHeaders)
	if err != nil {
		result.Error = codexWebsocketEvidence(ctx, err)
		return nil, result
	}
	observed.WebsocketSession = s
	return observed, result
}

type observedWebsocketSession struct {
	execution.WebsocketSession
	adapter   *Adapter
	spec      execution.AttemptSpec
	handshake subscription.PassiveQuotaSample
}

func (s *observedWebsocketSession) ExecuteTurn(ctx context.Context, payload []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
	return s.WebsocketSession.ExecuteTurn(ctx, payload, func(ctx context.Context, event []byte) error {
		observedAt := time.Now()
		windows := codex.NormalizeWebsocketQuotaWindows(event, observedAt)
		if len(windows) > 0 {
			s.adapter.credentials.RecordPassiveQuotaPair(s.spec.Credential.ID, s.spec.Credential.IdentityGeneration,
				s.handshake, subscription.PassiveQuotaSample{ObservedAtMS: observedAt.UnixMilli(), Windows: windows})
		}
		if emit != nil {
			return emit(ctx, event)
		}
		return nil
	})
}

// observeHeaders 保留响应头的原始额度样本；握手在交付本连接的事件之前记录。
func (s *observedWebsocketSession) observeHeaders(headers http.Header, observedAt time.Time) {
	if len(headers) == 0 || observedAt.IsZero() {
		return
	}
	signals := make(map[string]string, len(headers))
	for name, values := range headers {
		signals[name] = strings.Join(values, ",")
	}
	windows := codex.NormalizePassiveQuotaWindows(signals, observedAt)
	s.handshake = subscription.PassiveQuotaSample{ObservedAtMS: observedAt.UnixMilli(), Windows: windows}
	s.adapter.recordPassiveQuotaObservation(s.spec, observedAt, windows)
}

func (*codexProviderBridge) openWebsocket(spec execution.AttemptSpec, credential providerCredential, baseURL, proxyURL string, observeHeaders func(http.Header, time.Time)) (execution.WebsocketSession, error) {
	value, ok := credential.(codexProviderCredential)
	if !ok {
		return nil, errors.New("invalid websocket credential")
	}
	s, err := codex.NewWSSession(codex.WSSessionOptions{
		CredentialID: strconv.FormatUint(uint64(spec.Credential.ID), 10), Credential: value.value,
		BaseURL: baseURL, ProxyURL: proxyURL, TurnTimeout: spec.Timeouts.Request,
		Headers:         spec.Header.Clone(),
		ObserveHeaders:  observeHeaders,
		MaxRequestBytes: wsnative.MaxMessageBytes, MaxEventBytes: wsnative.MaxMessageBytes,
	})
	if err != nil {
		return nil, err
	}
	return &codexWebsocketSession{session: s}, nil
}

type codexWebsocketSession struct{ session *codex.WSSession }

func (s *codexWebsocketSession) Done() <-chan struct{} { return s.session.Done() }
func (s *codexWebsocketSession) Close() error          { return s.session.Close() }
func (s *codexWebsocketSession) ExecuteTurn(ctx context.Context, payload []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
	result, err := s.session.ExecuteTurn(ctx, payload, func(ctx context.Context, event json.RawMessage) error {
		if emit != nil {
			return emit(ctx, event)
		}
		return nil
	})
	state := execution.DispatchNotSent
	if result.DispatchState == codex.WSMaybeSent {
		state = execution.DispatchMaybeSent
	}
	return execution.WebsocketResult{DispatchState: state, Header: result.Headers, HeaderObservedAt: result.HeaderObservedAt, Error: codexWebsocketEvidence(ctx, err)}
}

func codexWebsocketEvidence(ctx context.Context, err error) *execution.ErrorEvidence {
	if err == nil {
		return nil
	}
	e := &execution.ErrorEvidence{Kind: execution.ErrorKindTransport, Code: "websocket_failed", Summary: "WebSocket upstream request failed.", OriginHint: execution.ErrorOriginUpstream, ScopeHint: execution.ErrorScopeRequest}
	var failure *codex.WSError
	if errors.As(err, &failure) {
		e.Code = failure.Code
		e.StatusCode = failure.HTTPStatus
		if failure.UpstreamCode != "" {
			e.Code = failure.UpstreamCode
			e.Kind = execution.ErrorKindProvider
			e.ScopeHint = ""
		}
		if e.StatusCode > 0 {
			e.Kind = execution.ErrorKindHTTP
			e.ScopeHint = ""
			if e.StatusCode == http.StatusUnauthorized && failure.DispatchState == codex.WSNotSent {
				e.Hint = execution.FailureHintRefreshRequired
				e.ReplaySafety = execution.ReplaySafetyRejectedBeforeProcessing
			}
		}
		if failure.DispatchState == codex.WSNotSent && e.StatusCode == 0 {
			e.Kind = execution.ErrorKindInvalidRequest
			e.OriginHint = execution.ErrorOriginInternal
		}
		if strings.EqualFold(failure.UpstreamCode, "model_at_capacity") || strings.EqualFold(failure.UpstreamCode, "model_is_at_capacity") {
			e.Hint = execution.FailureHintCandidateUnavailable
			e.ScopeHint = execution.ErrorScopeModel
			// WS 错误未携带生成阶段证据；容量不足不计为额度耗尽，也不据此重放。
			e.ReplaySafety = execution.ReplaySafetyUnknown
		}
	}
	if ctx.Err() != nil && e.StatusCode == 0 && e.Kind != execution.ErrorKindProvider {
		e.Kind = execution.ErrorKindCanceled
		e.OriginHint = execution.ErrorOriginDownstream
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			e.Kind = execution.ErrorKindTimeout
			e.OriginHint = execution.ErrorOriginUpstream
		}
	}
	return e
}
