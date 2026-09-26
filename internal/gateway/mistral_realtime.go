package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"golang.org/x/net/proxy"

	"gpt-load/internal/accessquota"
	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/protocol"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
	"gpt-load/internal/telemetry"
)

const mistralRealtimeReadLimit = 10 << 20

var reasonMistralRealtimeUpgrade = reason{
	Status:  http.StatusUpgradeRequired,
	Code:    "websocket_upgrade_required",
	Message: "WebSocket upgrade is required.",
}

var reasonMistralRealtimeOrigin = reason{
	Status:  http.StatusForbidden,
	Code:    "websocket_origin_rejected",
	Message: "WebSocket origin is not allowed.",
}

// handleMistralRealtime 只服务 Python SDK 的一次 WebSocket 握手。
// Bifrost 会解析 Responses 事件，不能拿来原样抄音频帧，所以拨号和转发留在网关。
// 同一次连接选定一把上游凭证，握手失败才按健康决策换下一把。
func (handler *Handler) handleMistralRealtime(c *gin.Context, request *dataPlaneRequestContext) {
	id, idErr := handler.newRequestID()
	if idErr == nil {
		c.Header(requestIDHeader, id)
	}
	recorder := newRequestRecorder(handler.requestLogSink, id, request.requestStarted, request.accessKey.ID, request.selectedRoute.Protocol, handler.requestNow)
	recorder.setOperation(execution.OperationMistralRealtimeTranscription)
	recorder.setUsageApplicable(false)
	connected := false
	defer func() {
		if !connected {
			recorder.completeMissingOutcome(c.Writer.Written(), c.Writer.Status())
		}
		recorder.emit()
	}()
	failed := func(value reason) { handler.completeReason(c, recorder, value) }
	if !websocket.IsWebSocketUpgrade(c.Request) {
		failed(reasonMistralRealtimeUpgrade)
		return
	}
	model := c.Request.URL.Query().Get("model")
	if model == "" || strings.TrimSpace(model) != model || len(model) > maxDataPlaneModelBytes {
		failed(reasonInvalidProtocolRequest)
		return
	}
	recorder.setClientModel(model)
	var ticket accessquota.Ticket
	if handler.accessQuota != nil {
		var decision accessquota.Decision
		var current bool
		ticket, decision, current = handler.admitAccessQuotaForSnapshot(request.snapshot, request.accessKey.ID, handler.quotaNow())
		if !current {
			failed(reasonConfigurationChanged)
			return
		}
		if !decision.Allowed {
			handler.completeAccessQuotaReason(c, recorder, decision)
			return
		}
	}
	limit := handler.limiter.Allow(request.accessKey.ID, request.accessKey.RPMLimit)
	if !limit.Allowed {
		c.Header("Retry-After", strconv.Itoa(retryAfterSeconds(limit.RetryAfter)))
		failed(reasonAccessKeyRateLimited)
		return
	}
	// Reject a browser origin before spending an upstream handshake. SDK
	// clients send no Origin and are unaffected.
	if !websocketOriginAllowed(c.Request, request.snapshot) {
		failed(reasonMistralRealtimeOrigin)
		return
	}
	upstream, upstreamModel, errReason := handler.dialMistralRealtime(c.Request.Context(), c, request, recorder, model, c.Request.URL.RawQuery)
	if errReason != nil {
		failed(*errReason)
		return
	}
	defer upstream.Close()
	upgrader := websocket.Upgrader{
		HandshakeTimeout: handler.writeTimeout,
		CheckOrigin:      func(r *http.Request) bool { return websocketOriginAllowed(r, request.snapshot) },
	}
	client, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer client.Close()
	connected = true
	recorder.outcome = requestOutcome{
		status:        telemetry.RequestStatusSuccess,
		statusCode:    http.StatusSwitchingProtocols,
		upstreamModel: upstreamModel,
	}
	if handler.accessQuota != nil {
		defer func() {
			completion := handler.accessQuota.Complete(ticket, 0)
			handler.logAccessQuotaCompletionFault(request.accessKey.ID, completion)
		}()
	}
	proxyMistralRealtime(c.Request.Context(), client, upstream)
}

// dialMistralRealtime selects one upstream credential and completes its
// WebSocket handshake. A failed handshake is judged before the client is
// upgraded, and only the final 429 receives Retry-After.
func (handler *Handler) dialMistralRealtime(ctx context.Context, c *gin.Context, request *dataPlaneRequestContext, recorder *requestRecorder, model, rawQuery string) (*websocket.Conn, string, *reason) {
	query := scheduler.Query{
		ClientProtocol:        request.selectedRoute.Protocol,
		Operation:             execution.OperationMistralRealtimeTranscription,
		RouteRequirement:      execution.RouteRequirementNative,
		ExternalModel:         &model,
		AccessKey:             request.accessKey,
		AllowedCredentialRefs: map[uint]state.CredentialRef{},
	}
	groups := scheduler.CandidateGroupIDsForQuery(request.snapshot, query)
	for _, ref := range handler.registry.CaptureActiveCredentialRefs(groups) {
		query.AllowedCredentialRefs[ref.ID] = ref
	}
	iterator := scheduler.New(request.snapshot, handler.registry, query)
	failure := reasonNoCandidate
	var cooldownUntil time.Time
	for sequence := 1; sequence <= retryAttemptLimit(request.snapshot.Settings.RetryCount); sequence++ {
		if ctx.Err() != nil {
			value := reason{Status: http.StatusGatewayTimeout, Code: "mistral_realtime_timeout", Message: "Realtime transcription timed out."}
			return nil, "", &value
		}
		selection, err := iterator.Next()
		if err != nil {
			break
		}
		if selection.ChannelID != channel.Mistral || selection.UpstreamModelID == nil || *selection.UpstreamModelID == "" {
			value := reasonNoCandidate
			return nil, "", &value
		}
		if handler.manager.Current() != request.snapshot {
			value := reasonConfigurationChanged
			return nil, "", &value
		}
		ref := query.AllowedCredentialRefs[selection.CredentialID]
		conn, dialFailure, decision := handler.dialMistralRealtimeSelection(ctx, recorder, selection, ref, rawQuery, sequence)
		if dialFailure == nil {
			return conn, *selection.UpstreamModelID, nil
		}
		failure = *dialFailure
		if !decision.CooldownUntil.IsZero() {
			cooldownUntil = decision.CooldownUntil
		}
		if decision.Effect == health.EffectSkipGroup {
			iterator.SkipGroup(selection.GroupID)
		}
		if !decision.ShouldRetry() {
			handler.setMistralRealtimeRetryAfter(c, failure, cooldownUntil)
			return nil, "", &failure
		}
	}
	handler.setMistralRealtimeRetryAfter(c, failure, cooldownUntil)
	return nil, "", &failure
}

// setMistralRealtimeRetryAfter adds Retry-After only for the final upstream
// 429. Earlier 429s stay internal so the next credential can be tried. The
// original status is unchanged for health and logs.
func (handler *Handler) setMistralRealtimeRetryAfter(c *gin.Context, failure reason, until time.Time) {
	if failure.Status != http.StatusTooManyRequests || until.IsZero() {
		return
	}
	setCooldownRetryAfter(c, until, handler.now())
}

// dialMistralRealtimeSelection dials one selected credential. The upstream
// status is kept for the client, health decision, and request log.
func (handler *Handler) dialMistralRealtimeSelection(ctx context.Context, recorder *requestRecorder, selection scheduler.Selection, ref state.CredentialRef, rawQuery string, sequence int) (*websocket.Conn, *reason, health.Decision) {
	encrypted, exists := handler.registry.ActiveEncryptedCredentialDataIfMatch(ref)
	if !exists {
		value := reasonConfigurationChanged
		return nil, &value, health.Decision{}
	}
	plain, err := handler.encryption.Decrypt(encrypted)
	if err != nil {
		value := reasonConfigurationChanged
		return nil, &value, health.Decision{}
	}
	credential, err := normalizeChannelCredential(handler.channels, handler.subscriptions, selection.ChannelID, selection.Group.ConnectionType, plain)
	if err != nil || credential.apiKey == "" {
		value := reasonConfigurationChanged
		return nil, &value, health.Decision{}
	}
	endpoint, err := mistralRealtimeUpstreamURL(selection.ResolvedTarget.TargetConfig, *selection.UpstreamModelID, rawQuery)
	if err != nil {
		value := reasonInvalidProtocolRequest
		return nil, &value, health.Decision{}
	}
	effective, _, err := resolveAttemptProxy(handler.encryption, selection.Group.Proxy, ref)
	if err != nil {
		value := reasonConfigurationChanged
		return nil, &value, health.Decision{}
	}
	dialer, err := mistralRealtimeDialer(effective)
	if err != nil {
		value := reasonConfigurationChanged
		return nil, &value, health.Decision{}
	}
	header := http.Header{"Authorization": {"Bearer " + credential.apiKey}}
	started := recorder.beforeForward()
	conn, response, dialErr := dialer.DialContext(ctx, endpoint, header)
	completed := handler.requestNow()
	if dialErr == nil {
		recorder.attempts = append(recorder.attempts, telemetry.Attempt{
			Sequence: sequence, CompletedAt: completed, GroupID: selection.GroupID, GroupName: selection.Group.Name,
			ChannelID: selection.ChannelID, CredentialID: selection.CredentialID, Operation: execution.OperationMistralRealtimeTranscription,
			RouteMode: execution.RouteNative, UpstreamModel: *selection.UpstreamModelID, DispatchState: execution.DispatchMaybeSent,
			ResponseStarted: true, UpstreamProtocol: protocol.Mistral, StatusCode: http.StatusSwitchingProtocols,
			DurationMs: completed.Sub(started).Milliseconds(), FailureCategory: telemetry.FailureCategoryOK,
			RetryDirective: telemetry.RetryNone, Effect: telemetry.EffectNone, Action: telemetry.ActionTerminate, Committed: true,
		})
		handler.recordCredentialSuccess(ref, handler.now())
		return conn, nil, health.Decision{}
	}
	status := http.StatusBadGateway
	dispatch := execution.DispatchNotSent
	responseHeader := http.Header{}
	if response != nil {
		dispatch = execution.DispatchMaybeSent
		if response.StatusCode != 0 {
			status = response.StatusCode
		}
		if response.Header != nil {
			responseHeader = response.Header.Clone()
		}
		if response.Body != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
			_ = response.Body.Close()
		}
	}
	value := reason{Status: status, Code: "mistral_realtime_upstream_failed", Message: "Realtime transcription upstream handshake failed."}
	evidence := &execution.ErrorEvidence{
		Kind: execution.ErrorKindHTTP, Code: value.Code, StatusCode: status,
		OriginHint: execution.ErrorOriginUpstream, Summary: value.Message,
	}
	if status == http.StatusTooManyRequests {
		evidence.Hint = execution.FailureHintRateLimited
		evidence.ScopeHint = execution.ErrorScopeModel
		evidence.ReplaySafety = execution.ReplaySafetyRejectedBeforeProcessing
	}
	result := UpstreamResult{
		StatusCode: status, Header: responseHeader, DispatchState: dispatch, ResponseStarted: response != nil,
		ErrorSummary: value.Message, UpstreamProtocol: protocol.Mistral, ExecutionError: evidence,
	}
	decision := judgeUpstreamResult(result, handler.now(), health.DecisionContext{
		DefaultRateLimitCooldown: subscriptionruntime.DefaultRefreshFailureCooldown,
		Method:                   http.MethodGet, Operation: execution.OperationMistralRealtimeTranscription,
	})
	recorder.recordAttempt(selection, credential.secrets, result, decision, started, completed)
	handler.applyGroupDecisionEffect(selection.Group, ref, ref.Version, decision, status, handler.now(), *selection.UpstreamModelID)
	return nil, &value, decision
}

// mistralRealtimeDialer returns the WebSocket dialer for the resolved proxy.
func mistralRealtimeDialer(effective outboundproxy.Effective) (*websocket.Dialer, error) {
	effective, err := outboundproxy.NormalizeEffective(effective)
	if err != nil {
		return nil, err
	}
	dialer := &websocket.Dialer{
		HandshakeTimeout: 30 * time.Second,
		NetDialContext:   (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	}
	switch effective.Config.Mode {
	case outboundproxy.ModeDirect, outboundproxy.ModeInherit:
		return dialer, nil
	case outboundproxy.ModeEnvironment:
		dialer.Proxy = http.ProxyFromEnvironment
		return dialer, nil
	case outboundproxy.ModeCustom:
		proxyURL, parseErr := url.Parse(effective.Config.URL)
		if parseErr != nil || proxyURL.Host == "" {
			return nil, fmt.Errorf("invalid proxy url")
		}
		if proxyURL.Scheme == "socks5" {
			base, dialErr := proxy.FromURL(proxyURL, &net.Dialer{Timeout: 30 * time.Second})
			contextDialer, ok := base.(proxy.ContextDialer)
			if dialErr != nil || !ok {
				return nil, fmt.Errorf("invalid socks5 proxy")
			}
			dialer.NetDialContext = contextDialer.DialContext
			return dialer, nil
		}
		dialer.Proxy = http.ProxyURL(proxyURL)
		return dialer, nil
	default:
		return nil, fmt.Errorf("unsupported proxy mode")
	}
}

// mistralRealtimeUpstreamURL builds the upstream WebSocket URL and replaces
// the client model query with the upstream model id.
func mistralRealtimeUpstreamURL(targetConfig json.RawMessage, upstreamModel, rawQuery string) (string, error) {
	var target struct {
		BaseURL string `json:"base_url"`
	}
	if err := json.Unmarshal(targetConfig, &target); err != nil {
		return "", err
	}
	base, path, err := dialect.MistralUpstreamTarget(target.BaseURL, "/v1/audio/transcriptions/realtime")
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return "", fmt.Errorf("invalid mistral base url")
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported mistral base url")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", err
	}
	values.Set("model", upstreamModel)
	values.Del("api_key")
	values.Del("key")
	parsed.RawQuery = values.Encode()
	parsed.Fragment = ""
	return parsed.String(), nil
}

// proxyMistralRealtime copies frames both ways until either side closes.
// It does not parse audio or transcription events.
func proxyMistralRealtime(ctx context.Context, client, upstream *websocket.Conn) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			_ = client.Close()
			_ = upstream.Close()
			cancel()
		})
	}
	var group sync.WaitGroup
	group.Add(2)
	copyFrames := func(dst, src *websocket.Conn) {
		defer group.Done()
		defer closeBoth()
		src.SetReadLimit(mistralRealtimeReadLimit)
		for {
			if ctx.Err() != nil {
				return
			}
			_ = src.SetReadDeadline(time.Now().Add(5 * time.Minute))
			kind, body, err := src.ReadMessage()
			if err != nil || len(body) > mistralRealtimeReadLimit {
				return
			}
			_ = dst.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if err := dst.WriteMessage(kind, body); err != nil {
				return
			}
		}
	}
	go copyFrames(upstream, client)
	go copyFrames(client, upstream)
	group.Wait()
}
