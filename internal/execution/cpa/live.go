package cpa

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/subscription/providers/codex"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

func (a *Adapter) OpenLive(ctx context.Context, spec execution.AttemptSpec, offer string, session json.RawMessage) (execution.LiveCall, *execution.ErrorEvidence) {
	provider, baseURL, err := a.validateSpec(spec)
	if err != nil || spec.ChannelID != string(channel.Codex) || spec.ClientProtocol != protocol.CodexLive ||
		spec.Operation != execution.OperationLiveCall || spec.RouteMode != execution.RouteNative || provider.ProviderKind() != channel.ProviderCodex {
		return execution.LiveCall{DispatchState: execution.DispatchNotSent}, requestValidationEvidence(errors.New("unsupported codex live request"))
	}
	settings, err := proxySettingsForAttempt(spec.Proxy)
	if err != nil {
		return execution.LiveCall{DispatchState: execution.DispatchNotSent}, requestValidationEvidence(err)
	}
	if settings.URL == "" && !settings.FromEnvironment {
		settings.URL = "direct"
	}
	ctx = subscriptionruntime.WithNetworkContext(ctx, subscriptionruntime.NetworkContext{Proxy: spec.Proxy, Fingerprint: spec.ProxyFingerprint})
	prepared, evidence := a.credentials.Prepare(ctx, channel.Codex, spec.Credential, spec.ForceCredentialRefresh)
	if evidence != nil {
		return execution.LiveCall{DispatchState: execution.DispatchNotSent}, modelAttemptPreparationEvidence(evidence)
	}
	canonical := prepared.Canonical()
	parsed, err := provider.ParseCredential(canonical)
	clear(canonical)
	if err != nil {
		return execution.LiveCall{DispatchState: execution.DispatchNotSent}, requestValidationEvidence(err)
	}
	credential, ok := parsed.(codexProviderCredential)
	if !ok {
		return execution.LiveCall{DispatchState: execution.DispatchNotSent}, requestValidationEvidence(errors.New("invalid codex live credential"))
	}
	body, err := json.Marshal(struct {
		SDP     string          `json:"sdp"`
		Session json.RawMessage `json:"session"`
	}{SDP: offer, Session: session})
	if err != nil {
		return execution.LiveCall{DispatchState: execution.DispatchNotSent}, requestValidationEvidence(err)
	}
	call, err := codex.StartLive(ctx, codex.LiveRequest{
		CredentialID: strconv.FormatUint(uint64(spec.Credential.ID), 10),
		Credential:   credential.value, BaseURL: baseURL, ProxyURL: settings.URL,
		ProxyFromEnvironment: settings.FromEnvironment, Headers: spec.Header.Clone(), Body: body,
	})
	result := execution.LiveCall{DispatchState: execution.DispatchMaybeSent, CallID: call.CallID, SDP: call.SDP, Header: call.Header}
	if call.Session != nil {
		result.Session = call.Session
	}
	if err != nil {
		return result, codexLiveFailure(err)
	}
	return result, nil
}

func codexLiveFailure(err error) *execution.ErrorEvidence {
	failure := &execution.ErrorEvidence{
		Kind: execution.ErrorKindTransport, Code: "codex_live_failed",
		Summary: "Codex live upstream request failed.", OriginHint: execution.ErrorOriginUpstream,
		ScopeHint: execution.ErrorScopeRequest,
	}
	var upstream *codex.LiveHTTPError
	if errors.As(err, &upstream) {
		failure.Kind = execution.ErrorKindHTTP
		failure.StatusCode = upstream.Status
		// 只有明确的鉴权、准入或限流拒绝能证明未创建通话。
		switch upstream.Status {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
			failure.ReplaySafety = execution.ReplaySafetyRejectedBeforeProcessing
		}
		if upstream.Status == http.StatusUnauthorized {
			failure.Hint = execution.FailureHintRefreshRequired
		}
	}
	return failure
}
