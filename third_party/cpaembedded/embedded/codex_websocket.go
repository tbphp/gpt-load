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
	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	internalexecutor "github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

const (
	CodexWSNotSent            = "not_sent"
	CodexWSMaybeSent          = "maybe_sent"
	defaultCodexWSTurnTimeout = 5 * time.Minute
	defaultCodexWSMaxBytes    = 10 << 20
)

// CodexWSSessionOptions 固定调用者已选择的身份、API 代理根地址和出站代理。
// ProxyURL 必须是 direct 或明确的 HTTP 代理 URL，不读取环境代理。
type CodexWSSessionOptions struct {
	CredentialID    string
	Credential      CodexCredential
	BaseURL         string
	ProxyURL        string
	TurnTimeout     time.Duration
	MaxRequestBytes int
	MaxEventBytes   int
}

// CodexWSTurnResult 只描述本轮执行，不执行健康、额度或日志记账。
type CodexWSTurnResult struct {
	ResponseID    string
	Status        string
	Usage         json.RawMessage
	Headers       http.Header
	DispatchState string
}

// CodexWSError 的文本不包含上游响应、凭据、地址或代理密码。
// UpstreamCode 和 HTTPStatus 仅提供调用者所需的错误分类证据。
type CodexWSError struct {
	Code          string
	UpstreamCode  string
	HTTPStatus    int
	DispatchState string
	cause         error
}

func (err *CodexWSError) Error() string { return "codex websocket: " + err.Code }
func (err *CodexWSError) Unwrap() error { return err.cause }

func codexWSError(code string) *CodexWSError {
	return &CodexWSError{Code: code, DispatchState: CodexWSNotSent}
}

// CodexWSSession 是尚未接入数据面的独立 Codex 上游会话。
type CodexWSSession struct {
	auth              *cliproxyauth.Auth
	inner             *internalexecutor.CodexWebsocketsExecutor
	id                string
	options           CodexWSSessionOptions
	resource          *codexWSResource
	mu                sync.Mutex
	running           bool
	started           bool
	closed            bool
	bound             bool
	cancel            context.CancelFunc
	closeConnection   func() error
	pendingConnection net.Conn
	closeDone         chan struct{}
	closeErr          error
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
	// 当前 SDK 的 HTTPS 代理不可用，SOCKS5 拨号忽略 context；首版明确拒绝。
	if proxy.URL != nil && proxy.URL.Scheme != "http" {
		return nil, codexWSError("unsupported_proxy")
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
	session := &CodexWSSession{
		auth: auth, id: uuid.NewString(), options: options, closeDone: make(chan struct{}),
		inner: internalexecutor.NewCodexWebsocketsExecutor(&internalconfig.Config{}),
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
	turnCtx, cancel := context.WithTimeout(ctx, s.options.TurnTimeout)
	s.cancel, s.running = cancel, true
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
	}
	stream, executionErr := s.inner.ExecuteStream(turnCtx, s.auth, cliproxyexecutor.Request{
		Model: model, Payload: append([]byte(nil), payload...), Format: sdktranslator.FormatOpenAIResponse,
	}, cliproxyexecutor.Options{
		Stream: true, SourceFormat: sdktranslator.FormatOpenAIResponse, ResponseFormat: sdktranslator.FormatOpenAIResponse,
		Metadata:           map[string]any{cliproxyexecutor.ExecutionSessionMetadataKey: s.id},
		ExecutionLifecycle: s.resource, WebSocketResponseObserver: observation.observe,
	})
	if stream != nil {
		result.Headers = stream.Headers.Clone()
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
		failure := codexWSError("upstream_error")
		failure.DispatchState, failure.UpstreamCode = result.DispatchState, observation.upstreamCode
		var status interface{ StatusCode() int }
		if errors.As(executionErr, &status) {
			failure.HTTPStatus = status.StatusCode()
		}
		var headers interface{ Headers() http.Header }
		if errors.As(executionErr, &headers) {
			result.Headers = headers.Headers().Clone()
		}
		// SDK 请求预处理失败且未接触上游时，不破坏已有连接。
		if cliproxyexecutor.UpstreamAttempted(turnCtx) || cliproxyexecutor.IsUpstreamWebsocketReplayRequired(executionErr) {
			s.invalidate(true)
		}
		return result, failure
	}
	if result.Status != "completed" || result.ResponseID == "" {
		s.invalidate(true)
		failure := codexWSError("incomplete_response")
		failure.DispatchState = result.DispatchState
		return result, failure
	}
	s.mu.Lock()
	s.started = true
	s.mu.Unlock()
	return result, nil
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
	result       *CodexWSTurnResult
	maxBytes     int
	emit         func(context.Context, json.RawMessage) error
	cancel       context.CancelFunc
	err          *CodexWSError
	upstreamCode string
	failSession  func()
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
			ID     string          `json:"id"`
			Status string          `json:"status"`
			Usage  json.RawMessage `json:"usage"`
			Error  struct {
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
	case "response.completed", "response.done":
		o.result.Status, o.result.Usage = "completed", envelope.Response.Usage
	case "response.incomplete":
		o.result.Status, o.result.Usage = "incomplete", envelope.Response.Usage
	case "response.failed", "error":
		o.result.Status, o.result.Usage = "failed", envelope.Response.Usage
		o.upstreamCode = safeCodexWSCode(envelope.Error.Code)
		if o.upstreamCode == "" {
			o.upstreamCode = safeCodexWSCode(envelope.Response.Error.Code)
		}
	}
	if o.emit != nil {
		if err := o.emit(ctx, append(json.RawMessage(nil), event.Payload...)); err != nil {
			fail("event_consumer_failed")
		}
	}
	// 先释放已失败的会话；让 SDK 继续返回原始错误分类，而非在断连日志里输出错误正文。
	if o.result.Status == "failed" || o.result.Status == "incomplete" {
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
