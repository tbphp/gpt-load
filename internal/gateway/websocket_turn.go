package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"gpt-load/internal/accessquota"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/execution/responsealias"
	"gpt-load/internal/health"
	"gpt-load/internal/protocol"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
)

var reasonWebsocketCapability = reason{400, "websocket_capability_unavailable", "The selected upstream does not support this WebSocket capability."}

type websocketRequest struct {
	fields   map[string]json.RawMessage
	metadata dialect.RequestMetadata
	required execution.WebsocketCapabilities
	previous string
	lane     string
}

func inspectWebsocketRequest(body []byte) (websocketRequest, error) {
	value := websocketRequest{}
	if json.Unmarshal(body, &value.fields) != nil || value.fields == nil {
		return value, ErrUpstreamProtocol
	}
	var kind string
	if json.Unmarshal(value.fields["type"], &kind) != nil || kind != "response.create" {
		return value, ErrUpstreamProtocol
	}
	if raw, exists := value.fields["stream_id"]; exists {
		if json.Unmarshal(raw, &value.lane) != nil || !validWebsocketLane(value.lane) {
			return value, ErrUpstreamProtocol
		}
		value.required.Multiplex = true
	}
	if raw, exists := value.fields["stream"]; exists && len(raw) > 0 {
		return value, ErrUpstreamProtocol
	}
	for _, key := range []string{"store", "generate", "background"} {
		raw, exists := value.fields[key]
		if !exists {
			continue
		}
		var flag bool
		if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &flag) != nil {
			return value, ErrUpstreamProtocol
		}
		switch key {
		case "store":
			value.required.StoredResponses = flag
		case "generate":
			value.required.Prewarm = !flag
		case "background":
			if flag {
				return value, ErrUpstreamProtocol
			}
		}
	}
	if raw, exists := value.fields["previous_response_id"]; exists && !bytes.Equal(raw, []byte("null")) {
		if json.Unmarshal(raw, &value.previous) != nil || value.previous == "" || len(value.previous) > 4096 {
			return value, ErrUpstreamProtocol
		}
		value.required.Continuation = true
	}
	metadata, err := dialect.NewOpenAIResponses().InspectRequest(&dialect.ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Body: body})
	if err != nil || metadata.Model == nil || *metadata.Model == "" || len(*metadata.Model) > maxDataPlaneModelBytes {
		return value, ErrUpstreamProtocol
	}
	metadata.Stream = true
	metadata.RouteRequirement = execution.RouteRequirementNative
	metadata.ResponsesStorePreference = execution.ResponsesStorePreferenceNone
	if value.required.StoredResponses {
		metadata.ResponsesStorePreference = execution.ResponsesStorePreferenceRequireStored
	}
	value.metadata = metadata
	return value, nil
}

func (s *websocketConnection) newTurnRecorder(turn websocketTurn) *requestRecorder {
	h := s.handler
	requestID, err := h.newRequestID()
	if err != nil {
		requestID = ""
	}
	recorder := newRequestRecorder(h.requestLogSink, requestID, turn.started, s.keyID, protocol.OpenAIResponses, h.requestNow)
	recorder.setOperation(execution.OperationResponsesCreate)
	recorder.setStream(true)
	recorder.setClientModel(turn.model)
	return recorder
}

func (s *websocketConnection) executeTurn(turn websocketTurn) {
	h := s.handler
	recorder := s.newTurnRecorder(turn)
	requestID := recorder.requestID
	admission := requestAccessQuotaAdmission{accessKeyID: s.keyID}
	defer func() {
		if admission.admitted && h.accessQuota != nil {
			h.logAccessQuotaCompletionFault(s.keyID, h.accessQuota.Complete(admission.ticket, recorder.estimatedCostNanoUSD()))
		}
		recorder.emit()
	}()
	reject := func(value reason) { recorder.completeReason(value); s.emitReason(turn.lane, value) }
	if s.ctx.Err() != nil {
		recorder.completeCanceled(s.ctx, 0, -1)
		return
	}
	// 首次绑定期间其余流只等待，不提前冻结配置或取得额度 Ticket。
	select {
	case s.bind <- struct{}{}:
	case <-s.ctx.Done():
		recorder.completeCanceled(s.ctx, 0, -1)
		return
	}
	var unlockOnce sync.Once
	unlock := func() { unlockOnce.Do(func() { <-s.bind }) }
	defer unlock()
	if s.ctx.Err() != nil {
		recorder.completeCanceled(s.ctx, 0, -1)
		return
	}
	snapshot := h.manager.Current()
	key, authorized := s.authorized(snapshot)
	if !authorized {
		reject(reasonInvalidAccessKey)
		s.cancel()
		return
	}
	recorder.accessKeyMultiplier = key.PriceMultiplier
	if h.accessQuota != nil {
		decision, current := h.checkAccessQuotaForSnapshot(snapshot, key.ID, h.quotaNow())
		if !current {
			reject(reasonConfigurationChanged)
			return
		}
		if !decision.Allowed {
			reject(reasonAccessKeyCostLimitExceeded)
			return
		}
	}
	if !h.limiter.Allow(key.ID, key.RPMLimit).Allowed {
		reject(reasonAccessKeyRateLimited)
		return
	}
	original, err := inspectWebsocketRequest(turn.body)
	if err != nil {
		reject(reasonInvalidProtocolRequest)
		return
	}
	model := *original.metadata.Model
	recorder.setClientModel(model)
	s.mu.Lock()
	binding := s.binding
	parent, parentFound := s.parents[original.previous]
	s.mu.Unlock()
	if binding != nil {
		currentRef, exists := h.registry.CredentialRef(binding.ref.ID)
		_, ready := h.registry.ActiveEncryptedCredentialDataIfMatch(currentRef)
		group, groupExists := snapshot.Groups[binding.ref.GroupID]
		if groupExists && !group.ResponsesWebsocketEnabled {
			reject(reasonWebsocketDisabled)
			s.cancel()
			return
		}
		_, groupAllowed := key.Filters.Groups[binding.ref.GroupID]
		_, protocolAllowed := key.Filters.Protocols[protocol.OpenAIResponses]
		if !exists || !ready || !sameWebsocketIdentity(binding.ref, currentRef) || !groupExists ||
			string(group.ChannelID) != binding.channel ||
			string(group.ResolvedTarget.TargetConfig) != binding.target ||
			(len(key.Filters.Groups) > 0 && !groupAllowed) || (len(key.Filters.Protocols) > 0 && !protocolAllowed) {
			reject(reasonConfigurationChanged)
			s.cancel()
			return
		}
	}
	if binding != nil && !binding.capabilities.Supports(original.required) {
		reject(reasonWebsocketCapability)
		return
	}
	var requiredRef *state.CredentialRef
	if binding != nil {
		requiredRef = &binding.ref
	}
	if original.previous != "" {
		if parentFound {
			if !parent.complete {
				reject(reasonResponseBindingNotFound)
				return
			}
		} else {
			stored, found := h.responseBindings.Lookup(key.ID, original.previous)
			if !found {
				reject(reasonResponseBindingNotFound)
				return
			}
			ref := state.CredentialRef{ID: stored.CredentialID, GroupID: stored.GroupID, IdentityGeneration: stored.IdentityGeneration}
			if requiredRef != nil && !sameWebsocketIdentity(*requiredRef, ref) {
				reject(reasonResponseBindingNotFound)
				return
			}
			requiredRef = &ref
			original.required.StoredResponses = true
		}
	}
	query := scheduler.Query{ClientProtocol: protocol.OpenAIResponses, Operation: execution.OperationResponsesCreate, RouteRequirement: execution.RouteRequirementNative, ResponsesStorePreference: original.metadata.ResponsesStorePreference, ExternalModel: original.metadata.Model, AccessKey: key, AllowedCredentialIDs: make(map[uint]struct{}), AllowedCredentialRefs: make(map[uint]state.CredentialRef)}
	query.ResponsesWebsocket = &original.required
	groups := scheduler.CandidateGroupIDsForQuery(snapshot, query)
	for _, ref := range h.registry.CaptureActiveCredentialRefs(groups) {
		group := snapshot.Groups[ref.GroupID]
		if !group.ResolvedTarget.ResponsesWebsocket.Supports(original.required) || (requiredRef != nil && !sameWebsocketIdentity(*requiredRef, ref)) {
			continue
		}
		query.AllowedCredentialIDs[ref.ID] = struct{}{}
		query.AllowedCredentialRefs[ref.ID] = ref
	}
	if len(query.AllowedCredentialIDs) == 0 {
		if binding == nil {
			reject(reasonWebsocketCapability)
		} else {
			reject(reasonNoCandidate)
		}
		return
	}
	affinity := h.resolveRequestAffinity(snapshot, key.ID, protocol.OpenAIResponses, original.metadata.AffinityPrefix, query.AllowedCredentialRefs)
	if requiredRef == nil {
		query.PreferredCredentialID = affinity.preferredCredentialID
	}
	iterator := scheduler.New(snapshot, h.registry, query)
	limit := retryAttemptLimit(snapshot.Settings.RetryCount)
	var refreshSelection *scheduler.Selection
	var refreshRef state.CredentialRef
	authRefreshUsed := false
	for sequence := 1; sequence <= limit; sequence++ {
		if s.ctx.Err() != nil {
			recorder.completeCanceled(s.ctx, 0, -1)
			return
		}
		var selection scheduler.Selection
		var ref state.CredentialRef
		forceCredentialRefresh := false
		if refreshSelection != nil {
			selection = *refreshSelection
			currentRef, exists := h.registry.CredentialRef(selection.CredentialID)
			if !exists || !sameWebsocketIdentity(currentRef, refreshRef) ||
				currentRef.EncryptedProxy != refreshRef.EncryptedProxy || currentRef.ProxyFingerprint != refreshRef.ProxyFingerprint {
				reject(reasonConfigurationChanged)
				return
			}
			if !iterator.ChargeReplay(selection, currentRef) {
				reject(reasonNoCandidate)
				return
			}
			ref = currentRef
			forceCredentialRefresh = currentRef.Version <= refreshRef.Version
			authRefreshUsed = true
			refreshSelection = nil
		} else {
			selection, err = iterator.Next()
			if err != nil {
				reject(reasonNoCandidate)
				return
			}
			ref = query.AllowedCredentialRefs[selection.CredentialID]
		}
		payload, effective, err := prepareWebsocketPayload(turn.body, original, selection)
		if err != nil {
			reject(reasonParameterOverrideUnavailable)
			return
		}
		extraBytes := max(0, len(payload)-len(turn.body))
		if !s.reserveInput(extraBytes) {
			reject(reason{503, "websocket_input_limit", "WebSocket input limit reached."})
			return
		}
		var releaseOnce sync.Once
		releaseInput := func() { releaseOnce.Do(func() { s.reserveInput(-extraBytes) }) }
		defer releaseInput()
		if !selection.ResolvedTarget.ResponsesWebsocket.Supports(effective.required) {
			reject(reasonWebsocketCapability)
			return
		}
		encrypted, ok := h.registry.ActiveEncryptedCredentialDataIfMatch(ref)
		if !ok {
			reject(reasonConfigurationChanged)
			return
		}
		plain, err := h.encryption.Decrypt(encrypted)
		if err != nil {
			reject(reasonConfigurationChanged)
			return
		}
		credential, err := normalizeChannelCredential(h.channels, h.subscriptions, selection.ChannelID, selection.Group.ConnectionType, plain)
		if err != nil {
			reject(reasonConfigurationChanged)
			return
		}
		proxy, fingerprint, err := resolveAttemptProxy(h.encryption, selection.Group.Proxy, ref)
		if err != nil {
			reject(reasonConfigurationChanged)
			return
		}
		id := requestID
		if id == "" {
			id = "untracked"
		}
		parsed := &dialect.ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", RawQuery: s.request.URL.RawQuery, Header: s.request.Header.Clone(), Body: payload}
		// 来源校验属于客户端连接；显式上游 HeaderRules 随后照常应用。
		parsed.Header.Del("Origin")
		input := ForwardInput{Dialect: dialect.NewOpenAIResponses(), ObserveUsage: effective.metadata.ObserveUsage, Group: selection.Group, APIKey: credential.apiKey, CredentialSecrets: credential.secrets, Request: parsed, ExternalModel: model, UpstreamModelID: optionalModelValue(selection.UpstreamModelID), RequestID: id, AttemptID: id + ":" + strconv.Itoa(sequence), AttemptSequence: uint32(sequence), ClientProtocol: protocol.OpenAIResponses, Operation: execution.OperationResponsesCreate, RouteRequirement: execution.RouteRequirementNative, ResponsesStorePreference: original.metadata.ResponsesStorePreference, ChannelID: string(selection.ChannelID), RouteMode: execution.RouteNative, TargetConfig: selection.ResolvedTarget.TargetConfig, Credential: execution.NewCredentialSnapshot(ref.ID, ref.Version, ref.IdentityGeneration, credential.payload), Proxy: proxy, ProxyFingerprint: fingerprint}
		input.ForceCredentialRefresh = forceCredentialRefresh
		spec, err := newExecutionAttemptSpec(input)
		if err != nil {
			reject(reasonInvalidProtocolRequest)
			return
		}
		headerBytes, err := json.Marshal(spec.Header)
		if err != nil {
			reject(reasonInvalidProtocolRequest)
			return
		}
		headerHash := h.encryption.Hash(string(headerBytes))
		if binding != nil && (binding.target != string(spec.TargetConfig) || binding.proxy != fingerprint || binding.headers != headerHash || !sameWebsocketIdentity(binding.ref, ref)) {
			reject(reasonConfigurationChanged)
			s.cancel()
			return
		}
		if !admission.admitted && h.accessQuota != nil {
			var decision accessquota.Decision
			var current bool
			admission.ticket, decision, current = h.admitAccessQuotaForSnapshot(snapshot, key.ID, h.quotaNow())
			if !current {
				reject(reasonConfigurationChanged)
				return
			}
			if !decision.Allowed {
				reject(reasonAccessKeyCostLimitExceeded)
				return
			}
			admission.admitted = true
		}
		recorder.setReasoning(effective.metadata.Reasoning)
		recorder.setUsageApplicable(effective.metadata.ObserveUsage)
		recorder.setPricingMode(effective.metadata.PricingMode)
		recorder.setUsageDiagnostics(effective.metadata.UsageDiagnostics)
		recorder.freezeNextAttemptPricing(h.freezeAttemptPricing(selection, effective.metadata, true, key.PriceMultiplier))
		recorder.setAffinityHit(requiredRef != nil || selection.CredentialID == affinity.preferredCredentialID)
		started := recorder.beforeForward()
		ctx, cancel := context.WithTimeout(s.ctx, selection.Group.Timeouts.Request)
		var firstByteDeadline time.Time
		if selection.Group.Timeouts.FirstByte > 0 {
			firstByteDeadline = time.Now().Add(selection.Group.Timeouts.FirstByte)
		}
		var wsResult execution.WebsocketResult
		newBinding := binding == nil
		if binding == nil {
			opener, ok := h.forwarder.(interface {
				OpenWebsocket(context.Context, ForwardInput) (execution.WebsocketSession, execution.WebsocketResult)
			})
			if !ok {
				cancel()
				reject(reasonWebsocketCapability)
				return
			}
			var session execution.WebsocketSession
			// 准入完成后才结束首轮等待；拨号至首事件共用同一首字节期限。
			s.firstRequest.Stop()
			openCtx := ctx
			var openCancel context.CancelFunc
			if !firstByteDeadline.IsZero() {
				openCtx, openCancel = context.WithDeadline(ctx, firstByteDeadline)
			}
			session, wsResult = opener.OpenWebsocket(openCtx, input)
			if openCancel != nil {
				openCancel()
			}
			if session != nil {
				binding = &websocketBinding{session: session, ref: ref, channel: string(selection.ChannelID), target: string(spec.TargetConfig), proxy: fingerprint, headers: headerHash, capabilities: selection.ResolvedTarget.ResponsesWebsocket}
				s.mu.Lock()
				s.binding = binding
				s.mu.Unlock()
				latest := h.manager.Current()
				currentKey, authorized := s.authorized(latest)
				if !authorized || !websocketGroupEnabled(latest, currentKey, binding.ref.GroupID) {
					s.closeWith(websocket.ClosePolicyViolation, reasonWebsocketDisabled.Message)
				}
				if binding.capabilities.Multiplex {
					unlock()
				}
			} else if wsResult.Error == nil {
				wsResult.Error = &execution.ErrorEvidence{Kind: execution.ErrorKindInternal, OriginHint: execution.ErrorOriginInternal, Code: "websocket_session_missing", Summary: "Upstream WebSocket session is unavailable."}
			}
		} else {
			unlock()
		}
		result := UpstreamResult{DispatchState: wsResult.DispatchState, Header: wsResult.Header, ExecutionError: wsResult.Error, UpstreamProtocol: protocol.OpenAIResponses}
		if s.ctx.Err() != nil {
			result.Err = s.ctx.Err()
			result.ExecutionError = &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled, OriginHint: execution.ErrorOriginDownstream, Code: "websocket_canceled"}
		} else if binding != nil {
			result = s.runWebsocketAttempt(ctx, cancel, binding, turn.lane, selection, ref, input, recorder, unlock, firstByteDeadline)
		} else {
			result.Err = executionFailureError(ctx, wsResult.Error)
			result.StatusCode = wsResult.Error.StatusCode
		}
		cancel()
		releaseInput()
		for _, name := range []string{"X-Request-Id", "Request-Id", "Openai-Request-Id", "X-Oai-Request-Id"} {
			if value := result.Header.Get(name); value != "" && len(value) <= 1024 {
				result.UpstreamRequestID = recorder.redactor.String(value, credential.secrets...)
				break
			}
		}
		decision := judgeUpstreamResult(result, h.now(), health.DecisionContext{DefaultRateLimitCooldown: fixedCooldown, CredentialRefreshable: selection.Group.ConnectionType == "subscription", Method: http.MethodPost, Operation: execution.OperationResponsesCreate})
		index := recorder.recordStreamAttempt(selection, credential.secrets, result, decision, started, recorder.now())
		h.applyGroupDecisionEffect(selection.Group, ref, 0, decision, result.StatusCode, h.now(), input.UpstreamModelID)
		if result.Stream.EndReason == StreamEndCleanEOF {
			h.recordCredentialSuccess(ref, h.now())
			if original.previous == "" {
				h.recordAffinitySuccess(affinity, selection, ref)
			}
		}
		// 延迟拨号的串行 Session 若首轮确实未发送，可释放失败句柄后继续既有候选迭代。
		// 多流一旦共享连接，不能以某一轮未发送推断其他轮也未发送。
		if result.DispatchState == execution.DispatchNotSent && !result.Committed && newBinding &&
			(binding == nil || !binding.capabilities.Multiplex) && decision.Retry != health.RetryNone &&
			sequence < limit && requiredRef == nil && !authRefreshUsed && s.ctx.Err() == nil {
			if decision.Retry == health.RetryRefreshCredential {
				refreshSelection, refreshRef = &selection, ref
			}
			if binding != nil {
				_ = binding.session.Close()
				s.mu.Lock()
				s.binding = nil
				s.mu.Unlock()
				binding = nil
			}
			recorder.retryIfAnotherForward(index)
			continue
		}
		if result.Committed {
			recorder.completeStream(result, input.UpstreamModelID, index)
		} else if s.ctx.Err() != nil {
			recorder.completeCanceled(s.ctx, result.StatusCode, index)
		} else {
			value := reasonUpstreamConnect
			if result.ExecutionError != nil {
				if result.ExecutionError.Kind == execution.ErrorKindTimeout {
					value = reasonUpstreamTimeout
				} else if result.ExecutionError.StatusCode >= 400 {
					value = reason{result.ExecutionError.StatusCode, result.ExecutionError.Code, "Upstream WebSocket request failed."}
				}
			}
			recorder.completeReason(value)
			s.emitReason(turn.lane, value)
		}
		if binding != nil {
			s.watchBinding(binding)
			unlock()
			if result.DispatchState == execution.DispatchMaybeSent && result.Stream.EndReason != StreamEndCleanEOF && result.Stream.EndReason != StreamEndSSEError && result.Stream.EndReason != StreamEndProviderIncomplete {
				s.cancel()
			}
			select {
			case <-binding.session.Done():
				s.cancel()
			default:
			}
		}
		return
	}
}

func sameWebsocketIdentity(a, b state.CredentialRef) bool {
	return a.ID == b.ID && a.GroupID == b.GroupID && a.IdentityGeneration == b.IdentityGeneration
}

func prepareWebsocketPayload(body []byte, original websocketRequest, selection scheduler.Selection) ([]byte, websocketRequest, error) {
	effectiveBody, _, err := selection.Group.ParameterOverrides.Apply(protocol.OpenAIResponses, execution.OperationResponsesCreate, *original.metadata.Model, body)
	if err != nil {
		return nil, original, err
	}
	effective, err := inspectWebsocketRequest(effectiveBody)
	if err != nil || effective.previous != original.previous || effective.lane != original.lane {
		return nil, original, ErrUpstreamProtocol
	}
	for _, key := range []string{"store", "generate"} {
		if raw, exists := original.fields[key]; exists {
			var oldValue, newValue bool
			_ = json.Unmarshal(raw, &oldValue)
			if json.Unmarshal(effective.fields[key], &newValue) != nil || oldValue != newValue {
				return nil, original, ErrUpstreamProtocol
			}
		}
	}
	model, _ := json.Marshal(optionalModelValue(selection.UpstreamModelID))
	effective.fields["model"] = model
	delete(effective.fields, "type") // Session 接收 Create 参数，传输层生成原生事件封套。
	for key := range effective.fields {
		switch strings.ReplaceAll(strings.ToLower(key), "-", "_") {
		case "provider", "fallback", "fallbacks", "authorization", "proxy_authorization", "api_key", "apikey", "x_api_key", "x_goog_api_key":
			delete(effective.fields, key)
		}
	}
	payload, err := json.Marshal(effective.fields)
	if len(payload) > 10<<20 {
		return nil, original, ErrUpstreamProtocol
	}
	return payload, effective, err
}

type websocketCancelCloser struct {
	cancel   context.CancelFunc
	timedOut *atomic.Bool
}

func (c websocketCancelCloser) Close() error { c.timedOut.Store(true); c.cancel(); return nil }

func (s *websocketConnection) runWebsocketAttempt(ctx context.Context, cancel context.CancelFunc, binding *websocketBinding, lane string, selection scheduler.Selection, ref state.CredentialRef, input ForwardInput, recorder *requestRecorder, unlock func(), firstByteDeadline time.Time) UpstreamResult {
	observer := newStreamEventObserver(input.Dialect, newUsageCaptureBoundary().newStreamForRequest(input.Dialect, input.ObserveUsage))
	result := UpstreamResult{UpstreamProtocol: protocol.OpenAIResponses}
	var responseID string
	var timedOut atomic.Bool
	first := newStreamWatchdog(websocketCancelCloser{cancel, &timedOut}, time.Until(firstByteDeadline))
	if !firstByteDeadline.IsZero() {
		if time.Until(firstByteDeadline) <= 0 {
			first.interrupt(errStreamIdleTimeout)
		} else {
			first.reset()
		}
	}
	defer first.stop()
	var idle *streamWatchdog
	defer func() {
		if idle != nil {
			idle.stop()
		}
	}()
	onResponse := s.handler.responseBindingObserver(s.keyID, selection, ref, input.Request)
	wsResult := binding.session.ExecuteTurn(ctx, input.Request.Body, func(ctx context.Context, body []byte) error {
		var event struct {
			Type       string          `json:"type"`
			StreamID   json.RawMessage `json:"stream_id"`
			ResponseID string          `json:"response_id"`
			Response   json.RawMessage `json:"response"`
		}
		if len(body) > s.handler.websocketLimits.message || json.Unmarshal(body, &event) != nil || observer.sawTerminal {
			return ErrUpstreamProtocol
		}
		var eventLane string
		if len(event.StreamID) > 0 && (json.Unmarshal(event.StreamID, &eventLane) != nil || !validWebsocketLane(eventLane)) {
			return ErrUpstreamProtocol
		}
		if eventLane != lane {
			return ErrUpstreamProtocol
		}
		var response struct {
			ID     string `json:"id"`
			Object string `json:"object"`
			Model  string `json:"model"`
			Status string `json:"status"`
		}
		if len(event.Response) > 0 && json.Unmarshal(event.Response, &response) != nil {
			return ErrUpstreamProtocol
		}
		id := response.ID
		if id == "" {
			id = event.ResponseID
		}
		if len(id) > 4096 || (responseID != "" && id != "" && responseID != id) ||
			(response.ID != "" && event.ResponseID != "" && response.ID != event.ResponseID) {
			return ErrUpstreamProtocol
		}
		if id != "" {
			responseID = id
		}
		if event.Type == "response.completed" || (event.Type == "response.done" && response.Status == "completed") {
			if response.ID == "" || response.Object != "response" || (response.Status != "" && response.Status != "completed") {
				return ErrUpstreamProtocol
			}
		}
		first.stop()
		if idle == nil {
			idle = newStreamWatchdog(websocketCancelCloser{cancel, &timedOut}, selection.Group.Timeouts.StreamIdle)
		}
		if selection.Group.Timeouts.StreamIdle > 0 {
			idle.reset()
		}
		observation := dialect.StreamEvent{Name: event.Type, Payload: body}
		if event.Type == "response.done" {
			// done 按真实 status 进入既有 usage/终态解析；下游仍收到原始事件。
			observation.Name = "response.failed"
			if response.Status == "completed" {
				observation.Name = "response.completed"
			}
			if response.Status == "incomplete" {
				observation.Name = "response.incomplete"
			}
			var fields map[string]json.RawMessage
			if json.Unmarshal(body, &fields) != nil {
				return ErrUpstreamProtocol
			}
			fields["type"], _ = json.Marshal(observation.Name)
			observation.Payload, _ = json.Marshal(fields)
		}
		providerError, err := observer.classify(observation, event.Type == "error")
		if err != nil {
			return err
		}
		observer.observeUsageEvent(observation)
		if providerError {
			observer.observeError(body, "Upstream WebSocket request failed.")
			body = recorder.redactor.Bytes(body, input.CredentialSecrets...)
			var fields map[string]json.RawMessage
			if json.Unmarshal(body, &fields) != nil || fields == nil {
				return ErrUpstreamProtocol
			}
			if len(event.StreamID) > 0 {
				// 流名已与客户端本轮核对，不能把合法标识误当密钥改写。
				fields["stream_id"] = event.StreamID
				body, err = json.Marshal(fields)
				if err != nil {
					return err
				}
			}
		}
		if len(event.Response) > 0 {
			if response.ID != "" {
				if len(response.ID) > 4096 {
					return ErrUpstreamProtocol
				}
				s.mu.Lock()
				prior, exists := s.parents[response.ID]
				if exists && (prior.lane != lane || prior.complete) {
					s.mu.Unlock()
					return ErrUpstreamProtocol
				}
				if !exists {
					s.parentOrder = append(s.parentOrder, response.ID)
				}
				s.parents[response.ID] = websocketParent{lane: lane, complete: observer.sawTerminal && !providerError && observer.terminalDisposition == dialect.StreamEventCompleted}
				for len(s.parentOrder) > s.handler.websocketLimits.responses {
					delete(s.parents, s.parentOrder[0])
					s.parentOrder = s.parentOrder[1:]
				}
				s.mu.Unlock()
			}
			if response.Model != "" {
				result.UpstreamReportedModel = response.Model
				result.ResponseModelObserved = true
				result.ResponseModelMismatch = response.Model != input.UpstreamModelID
			}
			if !providerError && binding.capabilities.StoredResponses && onResponse != nil {
				if err := onResponse(event.Response); err != nil {
					return err
				}
			}
		}
		if !result.Committed {
			recorder.recordFirstResponse()
		}
		if responsealias.Needs(input.ExternalModel, input.UpstreamModelID) {
			body, err = responsealias.RewriteJSON(protocol.OpenAIResponses, body, input.ExternalModel)
			if err != nil {
				return err
			}
		}
		unlock()
		if err = s.emit(ctx, body); err != nil {
			return err
		}
		result.Committed = true
		result.ResponseStarted = true
		observer.markTerminalForwarded()
		return nil
	})
	first.stop()
	if idle != nil {
		idle.stop()
	}
	result.DispatchState = wsResult.DispatchState
	result.RequestWritten = wsResult.DispatchState == execution.DispatchMaybeSent
	result.Header = wsResult.Header
	result.ExecutionError = wsResult.Error
	result.Usage = observer.finalizeUsage()
	result.StatusCode = http.StatusOK
	if wsResult.Error != nil {
		copy := wsResult.Error.Clone()
		result.ExecutionError = &copy
		if copy.StatusCode >= 400 {
			result.StatusCode = copy.StatusCode
		}
		if copy.Hint == "" {
			result.ExecutionError.Hint = streamErrorFailureHint(copy.StatusCode, copy.Code)
		}
		result.Err = executionFailureError(ctx, &copy)
		result.ErrorSummary = copy.Summary
	}
	result.Stream = observer.endObservation()
	if !observer.sawTerminal {
		if result.DispatchState == execution.DispatchNotSent && result.ExecutionError != nil {
			// 握手/准备被明确拒绝，尚无业务流；保留原错误供健康和刷新判定。
			result.Stream = StreamObservation{}
		} else {
			result.Stream = streamTerminalObservation(StreamEndUpstreamTerminated)
			if result.Err == nil {
				result.Err = fmt.Errorf("%w: missing WebSocket terminal", ErrUpstreamProtocol)
			}
		}
	}
	knownUpstreamError := wsResult.Error != nil && (wsResult.Error.Kind == execution.ErrorKindHTTP || wsResult.Error.Kind == execution.ErrorKindProvider)
	if !observer.terminalForwarded && !knownUpstreamError && (timedOut.Load() || errors.Is(ctx.Err(), context.DeadlineExceeded)) {
		result.Stream = streamTerminalObservation(StreamEndIdleTimeout)
		result.Err = upstreamExecutionTimeoutError{}
		result.ExecutionError = &execution.ErrorEvidence{Kind: execution.ErrorKindTimeout, OriginHint: execution.ErrorOriginUpstream, ScopeHint: execution.ErrorScopeRequest, Code: "websocket_timeout", Summary: "WebSocket upstream request timed out."}
	}
	if s.ctx.Err() != nil && !observer.terminalForwarded {
		result.Stream = prioritizeStreamObservation(s.ctx, s.ctx.Err(), result.Stream)
	}
	return result
}
