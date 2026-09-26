package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/protocol"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
	"gpt-load/internal/telemetry"
)

const (
	liveCallBodyLimit = 16 << 20
	liveSetupTimeout  = 30 * time.Second
)

var (
	reasonLiveUnavailable = reason{Status: 503, Code: "codex_live_unavailable", Message: "Codex live is unavailable."}
	reasonLiveSession     = reason{Status: 404, Code: "codex_live_session_not_found", Message: "Codex live session not found."}
)

// ConfigureCodexLive is called during dependency assembly, before HTTP traffic.
func (handler *Handler) ConfigureCodexLive(opener execution.LiveOpener, settings config.CodexLiveConfig) {
	if handler == nil {
		return
	}
	handler.liveOpener = opener
	handler.liveConfig = settings
	if handler.liveSessions == nil {
		handler.liveSessions = newLiveSessions()
	}
	handler.liveSessions.setMaximum(settings.MaxSessions)
}

func (handler *Handler) handleCodexLive(c *gin.Context, request *dataPlaneRequestContext) {
	switch request.selectedRoute.Kind {
	case endpointLiveCreate:
		handler.createCodexLive(c, request)
	case endpointLiveSideband:
		handler.connectCodexLive(c, request)
	case endpointLiveHangup:
		handler.hangupCodexLive(c, request)
	default:
		_ = handler.writeReason(c, reasonEndpointNotFound)
	}
}

func parseCodexLiveCall(request *http.Request) (string, json.RawMessage, string, error) {
	if request == nil || request.Body == nil {
		return "", nil, "", errors.New("missing Codex live body")
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, liveCallBodyLimit+1))
	if err != nil || len(body) > liveCallBodyLimit {
		return "", nil, "", errors.New("Codex live body is too large")
	}
	mediaType, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil {
		return "", nil, "", errors.New("invalid Codex live content type")
	}
	var offer string
	var session json.RawMessage
	switch mediaType {
	case "application/sdp", "text/plain":
		offer = string(body)
	case "application/json":
		var payload struct {
			SDP     string          `json:"sdp"`
			Session json.RawMessage `json:"session"`
		}
		if json.Unmarshal(body, &payload) != nil {
			return "", nil, "", errors.New("invalid Codex live JSON")
		}
		offer, session = payload.SDP, payload.Session
	case "multipart/form-data":
		boundary := params["boundary"]
		if boundary == "" {
			return "", nil, "", errors.New("missing Codex live multipart boundary")
		}
		reader := multipart.NewReader(strings.NewReader(string(body)), boundary)
		for {
			part, partErr := reader.NextPart()
			if errors.Is(partErr, io.EOF) {
				break
			}
			if partErr != nil {
				return "", nil, "", errors.New("invalid Codex live multipart")
			}
			content, readErr := io.ReadAll(io.LimitReader(part, liveCallBodyLimit+1))
			name := part.FormName()
			_ = part.Close()
			if readErr != nil || len(content) > liveCallBodyLimit {
				return "", nil, "", errors.New("invalid Codex live multipart field")
			}
			switch name {
			case "sdp":
				offer = string(content)
			case "session":
				session = content
			}
		}
	default:
		return "", nil, "", errors.New("unsupported Codex live content type")
	}
	if strings.TrimSpace(offer) == "" {
		return "", nil, "", errors.New("Codex live SDP is required")
	}
	if len(session) == 0 {
		session = json.RawMessage(`{}`)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(session, &fields) != nil || fields == nil {
		return "", nil, "", errors.New("invalid Codex live session")
	}
	model := channel.CodexLiveModelID
	if raw, ok := fields["model"]; ok {
		if json.Unmarshal(raw, &model) != nil || strings.TrimSpace(model) == "" {
			return "", nil, "", errors.New("invalid Codex live model")
		}
	}
	if len(model) > maxDataPlaneModelBytes {
		return "", nil, "", errors.New("Codex live model is too long")
	}
	return offer, session, model, nil
}

func liveSessionWithModel(raw json.RawMessage, model string) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	fields["model"] = encoded
	return json.Marshal(fields)
}

func (handler *Handler) createCodexLive(c *gin.Context, request *dataPlaneRequestContext) {
	if handler.liveOpener == nil || handler.liveSessions == nil {
		_ = handler.writeReason(c, reasonLiveUnavailable)
		return
	}
	id, idErr := handler.newRequestID()
	if idErr == nil {
		c.Header(requestIDHeader, id)
	}
	recorder := newRequestRecorder(handler.requestLogSink, id, request.requestStarted, request.accessKey.ID, protocol.CodexLive, handler.requestNow)
	recorder.setOperation(execution.OperationLiveCall)
	stored := false
	defer func() {
		if !stored {
			recorder.completeMissingOutcome(c.Writer.Written(), c.Writer.Status())
			recorder.emit()
		}
	}()
	failed := func(value reason) { handler.completeReason(c, recorder, value) }
	offer, sessionJSON, model, err := parseCodexLiveCall(c.Request)
	if err != nil {
		failed(reasonInvalidProtocolRequest)
		return
	}
	recorder.setClientModel(model)
	if handler.accessQuota != nil {
		decision, current := handler.checkAccessQuotaForSnapshot(request.snapshot, request.accessKey.ID, handler.quotaNow())
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
	if !handler.liveSessions.reserve() {
		failed(reasonLiveUnavailable)
		return
	}
	defer handler.liveSessions.release()
	query := scheduler.Query{ClientProtocol: protocol.CodexLive, Operation: execution.OperationLiveCall,
		RouteRequirement: execution.RouteRequirementNative, ExternalModel: &model, AccessKey: request.accessKey,
		AllowedCredentialRefs: make(map[uint]state.CredentialRef)}
	groups := scheduler.CandidateGroupIDsForQuery(request.snapshot, query)
	for _, ref := range handler.registry.CaptureActiveCredentialRefs(groups) {
		query.AllowedCredentialRefs[ref.ID] = ref
	}
	iterator := scheduler.New(request.snapshot, handler.registry, query)
	setupContext, cancelSetup := context.WithTimeout(c.Request.Context(), liveSetupTimeout)
	defer cancelSetup()
	failure := reasonNoCandidate
	var refreshSelection *scheduler.Selection
	refreshUsed := false
	for sequence := 1; sequence <= retryAttemptLimit(request.snapshot.Settings.RetryCount); sequence++ {
		if setupContext.Err() != nil {
			failed(reasonLiveUnavailable)
			return
		}
		var selection scheduler.Selection
		var ref state.CredentialRef
		forceRefresh := false
		if refreshSelection != nil {
			selection = *refreshSelection
			previous := query.AllowedCredentialRefs[selection.CredentialID]
			current, exists := handler.registry.CredentialRef(selection.CredentialID)
			refreshSelection = nil
			if !exists || current.GroupID != previous.GroupID || current.IdentityGeneration != previous.IdentityGeneration || current.ProxyFingerprint != previous.ProxyFingerprint || current.EncryptedProxy != previous.EncryptedProxy || !iterator.ChargeReplay(selection, current) {
				failed(reasonConfigurationChanged)
				return
			}
			ref = current
			forceRefresh = current.Version <= previous.Version
		} else {
			selection, err = iterator.Next()
			if err != nil {
				break
			}
			ref = query.AllowedCredentialRefs[selection.CredentialID]
		}
		if selection.ChannelID != channel.Codex || selection.UpstreamModelID == nil || *selection.UpstreamModelID != model {
			failed(reasonNoCandidate)
			return
		}
		if handler.manager.Current() != request.snapshot {
			failed(reasonConfigurationChanged)
			return
		}
		encrypted, exists := handler.registry.ActiveEncryptedCredentialDataIfMatch(ref)
		if !exists {
			failed(reasonConfigurationChanged)
			return
		}
		plain, err := handler.encryption.Decrypt(encrypted)
		if err != nil {
			failed(reasonConfigurationChanged)
			return
		}
		credential, err := normalizeChannelCredential(handler.channels, handler.subscriptions, selection.ChannelID, selection.Group.ConnectionType, plain)
		if err != nil {
			failed(reasonConfigurationChanged)
			return
		}
		proxy, fingerprint, err := resolveAttemptProxy(handler.encryption, selection.Group.Proxy, ref)
		if err != nil {
			failed(reasonConfigurationChanged)
			return
		}
		parsed := &dialect.ParsedRequest{Method: http.MethodPost, Path: c.Request.URL.Path,
			Header: c.Request.Header.Clone(), Body: sessionJSON}
		input := ForwardInput{Group: selection.Group, CredentialSecrets: credential.secrets, Request: parsed,
			ExternalModel: model, UpstreamModelID: *selection.UpstreamModelID,
			RequestID: id, AttemptID: id + ":" + strconv.Itoa(sequence), AttemptSequence: uint32(sequence), ForceCredentialRefresh: forceRefresh,
			ClientProtocol: protocol.CodexLive, Operation: execution.OperationLiveCall,
			RouteRequirement: execution.RouteRequirementNative, ChannelID: string(selection.ChannelID),
			RouteMode: execution.RouteNative, TargetConfig: selection.ResolvedTarget.TargetConfig,
			Credential: execution.NewCredentialSnapshot(ref.ID, ref.Version, ref.IdentityGeneration, credential.payload),
			Proxy:      proxy, ProxyFingerprint: fingerprint}
		spec, err := newExecutionAttemptSpec(input)
		if err != nil {
			failed(reasonInvalidProtocolRequest)
			return
		}
		sessionJSON, err = liveSessionWithModel(sessionJSON, *selection.UpstreamModelID)
		if err != nil {
			failed(reasonInvalidProtocolRequest)
			return
		}
		var media *liveMediaSession
		upstreamOffer := offer
		if selection.Group.CodexLiveMode == state.CodexLiveRelay {
			mediaProxy, proxyErr := liveMediaProxyURL(proxy)
			if proxyErr != nil {
				failed(reasonConfigurationChanged)
				return
			}
			media, err = newLiveMediaSession(setupContext, offer, handler.liveConfig, mediaProxy)
			if err != nil {
				failed(reasonLiveUnavailable)
				return
			}
			upstreamOffer = media.upstreamOffer
		}
		mediaStored := false
		defer func() {
			if !mediaStored {
				_ = media.Close()
			}
		}()
		started := recorder.beforeForward()
		updateDebugHeaders(c.Writer.Header(), selection.Group.Name, sequence)
		upstream, evidence := handler.liveOpener.OpenLive(setupContext, spec, upstreamOffer, sessionJSON)
		if evidence != nil {
			status := http.StatusBadGateway
			if evidence.StatusCode >= 400 && evidence.StatusCode < 600 {
				status = evidence.StatusCode
			}
			dispatch := upstream.DispatchState
			if !dispatch.Valid() {
				dispatch = execution.DispatchMaybeSent
			}
			result := UpstreamResult{StatusCode: evidence.StatusCode, DispatchState: dispatch,
				ExecutionError: evidence, ErrorSummary: evidence.Summary,
				ResponseStarted: dispatch == execution.DispatchMaybeSent && evidence.StatusCode != 0, UpstreamProtocol: protocol.CodexLive}
			decisionAt := handler.now()
			decision := judgeUpstreamResult(result, decisionAt, health.DecisionContext{
				DefaultRateLimitCooldown: subscriptionruntime.DefaultRefreshFailureCooldown,
				CredentialRefreshable:    true, Method: http.MethodPost, Operation: execution.OperationLiveCall,
			})
			if decision.Retry == health.RetryRefreshCredential && refreshUsed {
				decision.Retry = health.RetryNextCandidate
			}
			attemptIndex := recorder.recordAttempt(selection, credential.secrets, result, decision, started, handler.requestNow())
			handler.applyGroupDecisionEffect(selection.Group, ref, refreshCooldownCredentialVersion(result, ref.Version), decision, status, decisionAt, *selection.UpstreamModelID)
			failure = reason{Status: status, Code: "codex_live_upstream_failed", Message: "Codex live upstream request failed."}
			if status == http.StatusForbidden {
				failure.Code = "codex_live_upstream_forbidden"
				failure.Message = "Codex live access was denied by the upstream account."
			}
			_ = media.Close()
			if !decision.ShouldRetry() {
				failed(failure)
				return
			}
			if decision.Effect == health.EffectSkipGroup {
				iterator.SkipGroup(selection.GroupID)
			}
			if decision.Retry == health.RetryRefreshCredential && !refreshUsed {
				refreshUsed = true
				refreshSelection = &selection
			}
			recorder.retryIfAnotherForward(attemptIndex)
			continue
		}
		if upstream.Session == nil || upstream.CallID == "" {
			failed(reasonLiveUnavailable)
			return
		}
		upstreamStored := false
		defer func() {
			if !upstreamStored {
				_ = upstream.Session.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = upstream.Session.Hangup(ctx)
				cancel()
			}
		}()
		answer := upstream.SDP
		if media != nil {
			answer, err = media.AcceptAnswer(setupContext, upstream.SDP)
			if err != nil {
				failed(reasonLiveUnavailable)
				return
			}
		}
		if strings.TrimSpace(answer) == "" {
			failed(reasonLiveUnavailable)
			return
		}
		recorder.attempts = append(recorder.attempts, telemetry.Attempt{
			Sequence: sequence, CompletedAt: handler.requestNow(), GroupID: selection.GroupID, GroupName: selection.Group.Name,
			ChannelID: channel.Codex, CredentialID: selection.CredentialID, Operation: execution.OperationLiveCall,
			RouteMode: execution.RouteNative, UpstreamModel: *selection.UpstreamModelID, DispatchState: execution.DispatchMaybeSent,
			ResponseStarted: true, UpstreamProtocol: protocol.CodexLive, StatusCode: http.StatusCreated,
			DurationMs: handler.requestNow().Sub(started).Milliseconds(), FailureCategory: telemetry.FailureCategoryOK,
			RetryDirective: telemetry.RetryNone, Effect: telemetry.EffectNone, Action: telemetry.ActionTerminate, Committed: true,
		})
		keyHash := ""
		for hash, key := range request.snapshot.AccessKeysByHash {
			if key.ID == request.accessKey.ID {
				keyHash = hash
				break
			}
		}
		if keyHash == "" {
			failed(reasonConfigurationChanged)
			return
		}
		call := &liveCallSession{id: upstream.CallID, keyID: request.accessKey.ID, keyHash: keyHash, groupID: selection.GroupID,
			clientModel: model, model: *selection.UpstreamModelID, peerAddr: c.Request.RemoteAddr,
			ref: ref, upstream: upstream.Session, media: media, recorder: recorder, logger: handler.logger}
		call.authorized = func() bool { return handler.liveCallAuthorized(call.keyID, call) }
		if !handler.liveSessions.put(call) {
			failed(reasonLiveUnavailable)
			return
		}
		handler.recordCredentialSuccess(ref, handler.now())
		mediaStored, upstreamStored, stored = true, true, true
		location := "/v1/live/" + upstream.CallID
		if c.Request.URL.Path == "/v1/realtime/calls" {
			location = "/v1/realtime/calls/" + upstream.CallID
		}
		c.Header("Location", location)
		c.Header("Content-Type", "application/sdp")
		c.String(http.StatusCreated, "%s", answer)
		return
	}
	failed(failure)
}

func liveMediaProxyURL(effective outboundproxy.Effective) (string, error) {
	effective, err := outboundproxy.NormalizeEffective(effective)
	if err != nil {
		return "", err
	}
	switch effective.Config.Mode {
	case outboundproxy.ModeDirect:
		return "direct", nil
	case outboundproxy.ModeCustom:
		return effective.Config.URL, nil
	case outboundproxy.ModeEnvironment:
		target, err := url.Parse("https://api.openai.com/")
		if err != nil {
			return "", err
		}
		proxy, err := http.ProxyFromEnvironment(&http.Request{URL: target})
		if err != nil {
			return "", err
		}
		if proxy == nil {
			return "direct", nil
		}
		return proxy.String(), nil
	default:
		return "", outboundproxy.ErrInvalidConfig
	}
}

func (handler *Handler) liveCallAuthorized(keyID uint, call *liveCallSession) bool {
	snapshot := handler.manager.Current()
	key, exists := snapshot.AccessKeysByHash[call.keyHash]
	if !exists || key.ID != keyID || key.Status != state.AccessKeyStatusActive ||
		(key.ExpiresAtMS != nil && handler.requestNow().UnixMilli() >= *key.ExpiresAtMS) {
		return false
	}
	if len(key.AllowedPeerCIDRs) > 0 {
		peer, err := utils.NormalizePeerIP(call.peerAddr)
		if err != nil || !utils.AllowedCIDRsContain(key.AllowedPeerCIDRs, peer) {
			return false
		}
	}
	model := call.clientModel
	query := scheduler.Query{ClientProtocol: protocol.CodexLive, Operation: execution.OperationLiveCall,
		RouteRequirement: execution.RouteRequirementNative, ExternalModel: &model, AccessKey: key}
	for _, groupID := range scheduler.CandidateGroupIDsForQuery(snapshot, query) {
		if groupID == call.groupID {
			for _, current := range handler.registry.CaptureActiveCredentialRefs([]uint{groupID}) {
				if current.ID == call.ref.ID && current.IdentityGeneration == call.ref.IdentityGeneration {
					return true
				}
			}
			return false
		}
	}
	return false
}

func (handler *Handler) connectCodexLive(c *gin.Context, request *dataPlaneRequestContext) {
	id := c.Param("call_id")
	style := "live"
	if c.Request.URL.Path == "/v1/realtime" {
		id = c.Query("call_id")
		style = "realtime-query"
	}
	if strings.HasPrefix(c.Request.URL.Path, "/v1/realtime/calls/") {
		style = "realtime-calls"
	}
	call, claimed := handler.liveSessions.claim(id, request.accessKey.ID)
	if !claimed {
		_ = handler.writeReason(c, reasonLiveSession)
		return
	}
	if !handler.liveCallAuthorized(request.accessKey.ID, call) {
		handler.liveSessions.finishCall(call, "key_revoked")
		_ = handler.writeReason(c, reasonLiveSession)
		return
	}
	var upstreamConnection *websocket.Conn
	defer func() { handler.liveSessions.unclaim(call, upstreamConnection) }()
	connection, status, err := call.upstream.DialSideband(c.Request.Context(), style, websocket.Subprotocols(c.Request))
	if err != nil {
		if status < 400 || status >= 600 {
			status = http.StatusBadGateway
		}
		_ = handler.writeReason(c, reason{Status: status, Code: "codex_live_sideband_failed", Message: "Codex live control connection failed."})
		return
	}
	upstreamConnection = connection
	defer connection.Close()
	upgrader := websocket.Upgrader{HandshakeTimeout: handler.writeTimeout,
		Subprotocols: []string{connection.Subprotocol()},
		CheckOrigin:  func(r *http.Request) bool { return websocketOriginAllowed(r, request.snapshot) }}
	downstream, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer downstream.Close()
	connection.SetReadLimit(256 << 10)
	downstream.SetReadLimit(256 << 10)
	transfer := func(destination, source *websocket.Conn, done chan<- struct{}) {
		defer func() { done <- struct{}{} }()
		for {
			kind, content, err := source.ReadMessage()
			if err != nil {
				return
			}
			if len(content) > 256<<10 || destination.WriteMessage(kind, content) != nil {
				return
			}
			// 只接受上游控制连接的整通会话结束事件；单轮 response.done 不结束通话。
			if source == connection && kind == websocket.TextMessage {
				var event struct {
					Type  string          `json:"type"`
					Error json.RawMessage `json:"error"`
				}
				if json.Unmarshal(content, &event) == nil && event.Type == "session.closed" {
					reason := "upstream_session_ended"
					if len(event.Error) > 0 && string(event.Error) != "null" {
						reason = "upstream_session_error"
					}
					handler.liveSessions.finishCall(call, reason)
					return
				}
			}
		}
	}
	done := make(chan struct{}, 2)
	go transfer(connection, downstream, done)
	go transfer(downstream, connection, done)
	<-done
	_ = connection.Close()
	_ = downstream.Close()
	<-done
}

func (handler *Handler) hangupCodexLive(c *gin.Context, request *dataPlaneRequestContext) {
	id := c.Param("call_id")
	call, exists := handler.liveSessions.lookup(id, request.accessKey.ID)
	if !exists {
		_ = handler.writeReason(c, reasonLiveSession)
		return
	}
	if !handler.liveCallAuthorized(request.accessKey.ID, call) {
		handler.liveSessions.finishCall(call, "key_revoked")
		_ = handler.writeReason(c, reasonLiveSession)
		return
	}
	if err := handler.liveSessions.finishCall(call, "client_hangup"); err != nil {
		_ = handler.writeReason(c, reason{Status: http.StatusBadGateway, Code: "codex_live_hangup_failed", Message: "Codex live hangup was not confirmed; retry the hangup request."})
		return
	}
	c.Status(http.StatusNoContent)
}

// CloseCodexLive releases all long-lived calls during application shutdown.
func (handler *Handler) CloseCodexLive() {
	if handler != nil && handler.liveSessions != nil {
		handler.liveSessions.closeAll()
	}
}
