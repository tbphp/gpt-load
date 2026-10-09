package embedded

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v8/gptload-embedded/modelcatalog"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	internalexecutor "github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/thinking"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/proxyutil"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

const (
	CodexWSNotSent            = "not_sent"
	CodexWSMaybeSent          = "maybe_sent"
	defaultCodexWSTurnTimeout = 5 * time.Minute
	defaultCodexWSMaxBytes    = 10 << 20
)

// CodexWSSessionOptions 固定调用者已选择的身份、API 代理根地址和出站代理。
// ProxyURL 传入现有代理策略选定的 direct 或代理 URL，不读取环境代理。
type CodexWSSessionOptions struct {
	CredentialID    string
	Credential      CodexCredential
	BaseURL         string
	ProxyURL        string
	TurnTimeout     time.Duration
	MaxRequestBytes int
	MaxEventBytes   int
	Headers         http.Header
	// ObserveHeaders 在事件之前交付握手头，在结束前交付失败响应头。
	// 回调须及时返回，不等待本轮结束。
	ObserveHeaders func(http.Header, time.Time)
}

// CodexWSTurnResult 只描述本轮执行，不执行健康、额度或日志记账。
type CodexWSTurnResult struct {
	AppliedReasoningEffort       string
	AppliedReasoningMode         string
	AppliedReasoningBudgetTokens *int64
	ResponseID                   string
	Status                       string
	Usage                        json.RawMessage
	Headers                      http.Header
	HeaderObservedAt             time.Time
	DispatchState                string
}

// CodexWSError 的文本不包含上游响应、凭据、地址或代理密码。
// UpstreamType、UpstreamCode、HTTPStatus 和 RetryAfter 仅提供调用者所需的错误分类证据。
type CodexWSError struct {
	Code          string
	UpstreamType  string
	UpstreamCode  string
	HTTPStatus    int
	RetryAfter    time.Duration
	DispatchState string
	cause         error
}

func (err *CodexWSError) Error() string { return "codex websocket: " + err.Code }
func (err *CodexWSError) Unwrap() error { return err.cause }

func codexWSError(code string) *CodexWSError {
	return &CodexWSError{Code: code, DispatchState: CodexWSNotSent}
}

// CodexWSSession 是由调用者独占的 Codex 上游会话。
type CodexWSSession struct {
	lastResponseID      string
	lastModel           string
	lastEffort          string
	lastOverride        bool
	activeResponseID    string
	interruptResponseID string
	auth                *cliproxyauth.Auth
	inner               *internalexecutor.CodexWebsocketsExecutor
	id                  string
	options             CodexWSSessionOptions
	resource            *codexWSResource
	mu                  sync.Mutex
	running             bool
	started             bool
	closed              bool
	bound               bool
	cancel              context.CancelFunc
	closeConnection     func() error
	pendingConnection   net.Conn
	closeDone           chan struct{}
	closeErr            error
}

// NewCodexWSSession 只创建句柄，不建连、不刷新 token，也不保留 refresh token。
func NewCodexWSSession(options CodexWSSessionOptions) (*CodexWSSession, error) {
	if strings.TrimSpace(options.CredentialID) == "" || validateCredential(options.Credential) != nil {
		return nil, codexWSError("invalid_session_options")
	}
	endpoints, err := ResolveCodexAPIEndpoints(options.BaseURL)
	if err != nil {
		return nil, codexWSError("invalid_session_options")
	}
	proxy, err := proxyutil.Parse(options.ProxyURL)
	if err != nil || (proxy.Mode != proxyutil.ModeDirect && proxy.Mode != proxyutil.ModeProxy) {
		return nil, codexWSError("invalid_proxy")
	}
	if proxy.URL != nil && (proxy.URL.RawQuery != "" || proxy.URL.Fragment != "" || (proxy.URL.Path != "" && proxy.URL.Path != "/")) {
		return nil, codexWSError("invalid_proxy")
	}
	if options.TurnTimeout < 0 || options.MaxRequestBytes < 0 || options.MaxEventBytes < 0 {
		return nil, codexWSError("invalid_session_options")
	}
	if options.TurnTimeout == 0 {
		options.TurnTimeout = defaultCodexWSTurnTimeout
	}
	if options.MaxRequestBytes == 0 {
		options.MaxRequestBytes = defaultCodexWSMaxBytes
	}
	if options.MaxEventBytes == 0 {
		options.MaxEventBytes = defaultCodexWSMaxBytes
	}
	auth := NewCodexAuth(options.CredentialID, options.Credential, endpoints.ExecutionBase)
	delete(auth.Metadata, "refresh_token")
	auth.ProxyURL = proxy.Raw
	options.Credential = CodexCredential{}
	options.Headers = options.Headers.Clone()
	session := &CodexWSSession{
		auth: auth, id: codexWSSessionIDPrefix + uuid.NewString(), options: options, closeDone: make(chan struct{}),
		inner: internalexecutor.NewCodexWebsocketsExecutor(&internalconfig.Config{
			// 不启用 SDK 的启动缓冲，确保 ExecuteStream 先返回握手，再交付原生事件。
			// 客户端标识由桥接层固定，避免 CPA 覆写为其内置旧版本。
			Codex: internalconfig.CodexConfig{ModelLevelCooling: true, StreamBootstrapBuffering: false, DisableCodexCloaking: true},
		}),
	}
	session.resource = &codexWSResource{session: session}
	return session, nil
}

// ExecuteTurn 同步执行一轮；emit 按原始上游事件顺序调用，不积累聊天历史。
// emit 可为 nil；非空回调须及时返回并响应 ctx，不能在回调中等待本轮结束。
// 事件大小检查发生在 SDK 读取之后，不是 SDK 原始帧读取的内存上限。
func (s *CodexWSSession) ExecuteTurn(ctx context.Context, payload json.RawMessage, emit func(context.Context, json.RawMessage) error) (CodexWSTurnResult, error) {
	result := CodexWSTurnResult{DispatchState: CodexWSNotSent}
	if s == nil {
		return result, codexWSError("session_closed")
	}
	model, previous, err := s.validateRequest(payload)
	if err != nil {
		return result, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return result, codexWSContextError(ctx.Err(), CodexWSNotSent)
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return result, codexWSError("session_closed")
	}
	if s.running {
		s.mu.Unlock()
		return result, codexWSError("session_busy")
	}
	if !s.started && previous != "" {
		s.mu.Unlock()
		return result, codexWSError("continuation_requires_session")
	}
	var turnCtx context.Context
	var cancel context.CancelFunc
	if _, bounded := ctx.Deadline(); bounded {
		// 网关每轮的 deadline 是业务期限；创建时的默认值不能截断后续配置上调。
		turnCtx, cancel = context.WithCancel(ctx)
	} else {
		turnCtx, cancel = context.WithTimeout(ctx, s.options.TurnTimeout)
	}
	hasReasoningOverride := false
	// 支持更新的原生 Responses 路径由 CPA 原样保留配置。
	if modelcatalog.SupportsReasoningUpdates(model) {
		result.AppliedReasoningEffort = thinking.ExtractTranslatedReasoningEffort(payload, ProviderCodex)
		result.AppliedReasoningMode, result.AppliedReasoningBudgetTokens = extractReasoningDetails(payload, ProviderCodex)
		gjson.GetBytes(payload, "input").ForEach(func(_, item gjson.Result) bool {
			if item.Get("type").String() == "configuration_update" {
				hasReasoningOverride = true
			}
			return true
		})
		if !hasReasoningOverride && s.lastOverride && previous != "" && previous == s.lastResponseID && model == s.lastModel {
			result.AppliedReasoningEffort = s.lastEffort
			hasReasoningOverride = true
		}
	}
	s.cancel, s.running = cancel, true
	s.activeResponseID, s.interruptResponseID = "", ""
	reuse := s.started
	s.mu.Unlock()
	cleanupDone := make(chan struct{})
	stop := context.AfterFunc(turnCtx, func() { s.invalidate(true); close(cleanupDone) })
	defer func() {
		if !stop() {
			<-cleanupDone
		}
		cancel()
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		// 关闭与 SDK 创建会话竞态时，仍清理本调用留下的空登记。
		if closed {
			s.inner.CloseExecutionSession(s.id)
		}
		s.mu.Lock()
		s.cancel, s.running = nil, false
		s.activeResponseID, s.interruptResponseID = "", ""
		s.mu.Unlock()
	}()
	turnCtx = cliproxyexecutor.WithUpstreamAttemptTracker(turnCtx)
	// Gorilla 的 Upgrade 读取只设置 deadline；捕获尚未绑定 lifecycle 的连接，
	// 使主动取消也能中止 TLS/WS 握手，而不必等待握手超时。
	turnCtx = httptrace.WithClientTrace(turnCtx, &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) { s.trackDialConnection(info.Conn) },
	})
	if reuse {
		turnCtx = cliproxyexecutor.WithRequiredUpstreamWebsocket(turnCtx)
	}
	observation := codexWSTurnObservation{
		result: &result, maxBytes: s.options.MaxEventBytes, emit: emit, cancel: cancel,
		failSession: func() { s.invalidate(false) },
		observeResponse: func(id, status, reason string) bool {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.activeResponseID = id
			interrupted := status == "incomplete" && reason == "interrupted" && s.interruptResponseID == id && id != ""
			if status == "completed" || status == "incomplete" || status == "failed" || status == "cancelled" {
				s.activeResponseID = ""
			}
			return interrupted
		},
	}
	headersReady := make(chan struct{})
	headers := normalizedCodexHeaders(s.options.Headers)
	headers.Set("Originator", "codex-tui")
	if helps.IsNativeCodexRequest(payload, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAIResponse, Headers: headers}) {
		// 关闭 CPA 的旧 UA 覆写后，原生请求只保留显式会话头；补回原有缓存会话语义。
		sessionID := headers.Get("Session-Id")
		if cacheKey := strings.TrimSpace(gjson.GetBytes(payload, "prompt_cache_key").String()); cacheKey != "" {
			sessionID = cacheKey
			headers.Set("Conversation_id", cacheKey)
		}
		if sessionID == "" {
			sessionID = uuid.NewString()
		}
		headers.Del("Session-Id")
		headers.Set("Session_id", sessionID)
	}
	stream, executionErr := s.inner.ExecuteStream(turnCtx, s.auth, cliproxyexecutor.Request{
		Model: model, Payload: append([]byte(nil), payload...), Format: sdktranslator.FormatOpenAIResponse,
	}, cliproxyexecutor.Options{
		Stream: true, Headers: headers, SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse,
		Metadata:           map[string]any{cliproxyexecutor.ExecutionSessionMetadataKey: s.id},
		ExecutionLifecycle: s.resource,
		WebSocketResponseObserver: func(ctx context.Context, event cliproxyexecutor.WebSocketResponseEvent) {
			// SDK 的读取协程可先于 ExecuteStream 返回运行；握手交接完成前不交付事件。
			<-headersReady
			observation.observe(ctx, event)
		},
	})
	if stream != nil {
		result.Headers = stream.Headers.Clone()
		if len(result.Headers) > 0 {
			result.HeaderObservedAt = time.Now()
			if s.options.ObserveHeaders != nil {
				s.options.ObserveHeaders(result.Headers.Clone(), result.HeaderObservedAt)
			}
		}
	}
	close(headersReady)
	if stream != nil {
		// 原生 JSON 由 observer 交付。排空 SDK 的转换输出，确保单轮收尾完成。
		for chunk := range stream.Chunks {
			if chunk.Err != nil && executionErr == nil {
				executionErr = chunk.Err
			}
		}
	}
	s.mu.Lock()
	bound := s.bound
	s.mu.Unlock()
	if bound && cliproxyexecutor.UpstreamAttempted(turnCtx) {
		result.DispatchState = CodexWSMaybeSent
	}
	if observation.err != nil {
		s.invalidate(true)
		observation.err.DispatchState = result.DispatchState
		return result, observation.err
	}
	if turnCtx.Err() != nil {
		s.invalidate(true)
		return result, codexWSContextError(turnCtx.Err(), result.DispatchState)
	}
	if executionErr != nil {
		// 连接 deadline 可能先于 context 定时器触发，仍须保留超时分类。
		var timeout net.Error
		if errors.As(executionErr, &timeout) && timeout.Timeout() {
			s.invalidate(true)
			return result, codexWSContextError(context.DeadlineExceeded, result.DispatchState)
		}
		failure := codexWSError("upstream_error")
		failure.DispatchState, failure.UpstreamCode = result.DispatchState, observation.upstreamCode
		failure.UpstreamType, failure.RetryAfter = codexWSErrorMetadata(executionErr)
		var status interface{ StatusCode() int }
		if errors.As(executionErr, &status) {
			failure.HTTPStatus = status.StatusCode()
		}
		var headers interface{ Headers() http.Header }
		if errors.As(executionErr, &headers) {
			result.Headers = headers.Headers().Clone()
			if len(result.Headers) > 0 {
				result.HeaderObservedAt = time.Now()
				if s.options.ObserveHeaders != nil {
					s.options.ObserveHeaders(result.Headers.Clone(), result.HeaderObservedAt)
				}
			}
		}
		// SDK 请求预处理失败且未接触上游时，不破坏已有连接。
		if cliproxyexecutor.UpstreamAttempted(turnCtx) || cliproxyexecutor.IsUpstreamWebsocketReplayRequired(executionErr) {
			s.invalidate(true)
		}
		return result, failure
	}
	if (result.Status != "completed" && !observation.interrupted) || result.ResponseID == "" {
		s.invalidate(true)
		failure := codexWSError("incomplete_response")
		failure.DispatchState, failure.UpstreamCode = result.DispatchState, observation.upstreamCode
		return result, failure
	}
	s.mu.Lock()
	s.started = true
	s.lastOverride = hasReasoningOverride
	s.lastResponseID, s.lastModel, s.lastEffort = result.ResponseID, model, result.AppliedReasoningEffort
	s.mu.Unlock()
	return result, nil
}

// Interrupt 复用 CPA 的控制帧接口，只允许中断当前正在生成的响应。
func (s *CodexWSSession) Interrupt(ctx context.Context, payload []byte) error {
	if s == nil {
		return codexWSError("response_not_active")
	}
	var frame struct {
		Type       string `json:"type"`
		ResponseID string `json:"response_id"`
	}
	if len(payload) > s.options.MaxRequestBytes || json.Unmarshal(payload, &frame) != nil ||
		frame.Type != "response.interrupt" || frame.ResponseID == "" {
		return codexWSError("invalid_request")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.closed || !s.running || s.activeResponseID != frame.ResponseID {
		s.mu.Unlock()
		return codexWSError("response_not_active")
	}
	s.interruptResponseID = frame.ResponseID
	s.mu.Unlock()
	if err := s.inner.InterruptExecutionSession(ctx, s.id, payload); err != nil {
		s.mu.Lock()
		if s.interruptResponseID == frame.ResponseID {
			s.interruptResponseID = ""
		}
		s.mu.Unlock()
		if errors.Is(err, cliproxyexecutor.ErrNoActiveUpstreamWebsocket) {
			return codexWSError("response_not_active")
		}
		return &CodexWSError{Code: "interrupt_failed", DispatchState: CodexWSMaybeSent, cause: err}
	}
	return nil
}

func codexWSErrorMetadata(err error) (string, time.Duration) {
	upstreamType := ""
	for current := err; current != nil; current = errors.Unwrap(current) {
		var payload struct {
			Type  string `json:"type"`
			Error struct {
				Type string `json:"type"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(current.Error()), &payload) != nil {
			continue
		}
		upstreamType = payload.Error.Type
		if upstreamType == "" {
			upstreamType = payload.Type
		}
		upstreamType = safeCodexWSCode(upstreamType)
		if upstreamType != "" {
			break
		}
	}
	var retry interface{ RetryAfter() *time.Duration }
	if errors.As(err, &retry) && retry != nil {
		if value := retry.RetryAfter(); value != nil && *value > 0 {
			return upstreamType, *value
		}
	}
	return upstreamType, 0
}

func (s *CodexWSSession) validateRequest(payload []byte) (string, string, error) {
	if len(payload) > s.options.MaxRequestBytes {
		return "", "", codexWSError("request_too_large")
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(payload, &root) != nil || root == nil {
		return "", "", codexWSError("invalid_request")
	}
	var model, previous string
	if json.Unmarshal(root["model"], &model) != nil || strings.TrimSpace(model) == "" {
		return "", "", codexWSError("invalid_request")
	}
	if raw, ok := root["previous_response_id"]; ok && json.Unmarshal(raw, &previous) != nil {
		return "", "", codexWSError("invalid_request")
	}
	// 首版仅支持原生单轮生成，不静默丢弃多流或后台执行语义。
	for _, key := range []string{"type", "stream_id", "conversation"} {
		if _, ok := root[key]; ok {
			return "", "", codexWSError("unsupported_request")
		}
	}
	if raw, ok := root["background"]; ok {
		var background bool
		if json.Unmarshal(raw, &background) != nil || background {
			return "", "", codexWSError("unsupported_request")
		}
	}
	return model, previous, nil
}

// Done 在本 Session 失效或关闭后通知调用者。
func (s *CodexWSSession) Done() <-chan struct{} {
	if s == nil || s.closeDone == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return s.closeDone
}

// Close 可从事件回调调用；它关闭连接并取消本轮，ExecuteTurn 随后退出。
func (s *CodexWSSession) Close() error {
	if s == nil {
		return nil
	}
	s.invalidate(true)
	<-s.closeDone
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeErr
}

func (s *CodexWSSession) trackDialConnection(connection net.Conn) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.recordCloseError(connection.Close())
		return
	}
	previous := s.pendingConnection
	s.pendingConnection = connection
	s.mu.Unlock()
	if previous != nil {
		s.recordCloseError(previous.Close())
	}
}

func (s *CodexWSSession) recordCloseError(err error) {
	if err == nil || errors.Is(err, net.ErrClosed) {
		return
	}
	s.mu.Lock()
	s.closeErr = codexWSError("close_failed")
	s.mu.Unlock()
}

func (s *CodexWSSession) invalidate(cancelActive bool) {
	s.mu.Lock()
	if s.closed {
		cancel := s.cancel
		s.mu.Unlock()
		if cancelActive && cancel != nil {
			cancel()
		}
		return
	}
	s.closed = true
	cancel, closeConnection := s.cancel, s.closeConnection
	pendingConnection := s.pendingConnection
	s.closeConnection = nil
	s.pendingConnection = nil
	s.mu.Unlock()
	if cancelActive && cancel != nil {
		cancel()
	}
	if pendingConnection != nil {
		s.recordCloseError(pendingConnection.Close())
	}
	if closeConnection != nil {
		s.recordCloseError(closeConnection())
	}
	s.inner.CloseExecutionSession(s.id)
	close(s.closeDone)
}

type codexWSResource struct{ session *CodexWSSession }

// Bind 只接受第一条连接；SDK 重拨后再次绑定时，在第二次业务发送前拒绝。
func (resource *codexWSResource) Bind(closeConnection func() error) error {
	s := resource.session
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.bound {
		return codexWSError("session_closed")
	}
	s.bound, s.closeConnection = true, closeConnection
	s.pendingConnection = nil
	return nil
}

func (resource *codexWSResource) End(string) { resource.session.invalidate(false) }

func codexWSContextError(err error, dispatch string) *CodexWSError {
	code := "canceled"
	if errors.Is(err, context.DeadlineExceeded) {
		code = "timeout"
	}
	return &CodexWSError{Code: code, DispatchState: dispatch, cause: err}
}

type codexWSTurnObservation struct {
	result          *CodexWSTurnResult
	maxBytes        int
	emit            func(context.Context, json.RawMessage) error
	cancel          context.CancelFunc
	err             *CodexWSError
	upstreamCode    string
	failSession     func()
	observeResponse func(string, string, string) bool
	interrupted     bool
}

func (o *codexWSTurnObservation) observe(ctx context.Context, event cliproxyexecutor.WebSocketResponseEvent) {
	if o.err != nil || ctx.Err() != nil {
		return
	}
	fail := func(code string) { o.err = codexWSError(code); o.cancel() }
	if len(event.Payload) > o.maxBytes {
		fail("event_too_large")
		return
	}
	var envelope struct {
		Type     string `json:"type"`
		Response struct {
			ID                string          `json:"id"`
			Status            string          `json:"status"`
			Usage             json.RawMessage `json:"usage"`
			IncompleteDetails struct {
				Reason string `json:"reason"`
			} `json:"incomplete_details"`
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		} `json:"response"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(event.Payload, &envelope) != nil || envelope.Type == "" {
		fail("invalid_event")
		return
	}
	if envelope.Response.ID != "" {
		if o.result.ResponseID != "" && o.result.ResponseID != envelope.Response.ID {
			fail("invalid_event")
			return
		}
		o.result.ResponseID = envelope.Response.ID
	}
	switch envelope.Type {
	case "response.completed":
		o.result.Status, o.result.Usage = "completed", envelope.Response.Usage
	case "response.done":
		o.result.Status, o.result.Usage = envelope.Response.Status, envelope.Response.Usage
		o.upstreamCode = safeCodexWSCode(envelope.Response.Error.Code)
	case "response.incomplete":
		o.result.Status, o.result.Usage = "incomplete", envelope.Response.Usage
	case "response.failed", "error":
		o.result.Status, o.result.Usage = "failed", envelope.Response.Usage
		o.upstreamCode = safeCodexWSCode(envelope.Error.Code)
		if o.upstreamCode == "" {
			o.upstreamCode = safeCodexWSCode(envelope.Response.Error.Code)
		}
	}
	if envelope.Response.ID != "" && o.observeResponse != nil {
		o.interrupted = o.observeResponse(envelope.Response.ID, o.result.Status, envelope.Response.IncompleteDetails.Reason)
	}
	if o.emit != nil {
		if err := o.emit(ctx, append(json.RawMessage(nil), event.Payload...)); err != nil {
			fail("event_consumer_failed")
		}
	}
	// 先释放已失败的会话；让 SDK 继续返回原始错误分类，而非在断连日志里输出错误正文。
	if !o.interrupted && (o.result.Status == "failed" || o.result.Status == "incomplete" || (envelope.Type == "response.done" && o.result.Status != "completed")) {
		o.failSession()
	}
}

func safeCodexWSCode(code string) string {
	if len(code) > 128 {
		return ""
	}
	for _, char := range code {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-' || char == '.') {
			return ""
		}
	}
	return code
}
