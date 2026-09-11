// Package wsnative 提供单个已选上游的原生 Responses WS 传输，不选号或重放请求。
package wsnative

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
	"golang.org/x/net/proxy"

	"gpt-load/internal/execution"
	"gpt-load/internal/outboundproxy"
)

const MaxMessageBytes = 10 << 20

type delivery struct {
	payload []byte
	done    chan struct{}
}

type turnSlot struct {
	events chan delivery
	done   chan struct{}
}

type session struct {
	conn             *websocket.Conn
	write            chan struct{}
	mu               sync.Mutex
	turns            map[string]*turnSlot
	header           http.Header
	headerObservedAt time.Time
	done             chan struct{}
	once             sync.Once
	failure          *execution.ErrorEvidence
	closeErr         error
}

// Dial 只建立选定的连接，握手失败不会改发 HTTP 或跟随重定向。
func Dial(ctx context.Context, endpoint string, header http.Header, effective outboundproxy.Effective) (execution.WebsocketSession, execution.WebsocketResult) {
	result := execution.WebsocketResult{DispatchState: execution.DispatchNotSent}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || (parsed.Scheme != "ws" && parsed.Scheme != "wss") {
		result.Error = failure(execution.ErrorKindInvalidRequest, "invalid_websocket_endpoint", 0)
		return nil, result
	}
	effective, err = outboundproxy.NormalizeEffective(effective)
	if err != nil {
		result.Error = failure(execution.ErrorKindInternal, "websocket_proxy_invalid", 0)
		return nil, result
	}
	dialer := websocket.Dialer{HandshakeTimeout: 30 * time.Second, NetDialContext: (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext}
	switch effective.Config.Mode {
	case outboundproxy.ModeEnvironment:
		dialer.Proxy = http.ProxyFromEnvironment
	case outboundproxy.ModeCustom:
		proxyURL, parseErr := url.Parse(effective.Config.URL)
		if parseErr != nil {
			result.Error = failure(execution.ErrorKindInternal, "websocket_proxy_invalid", 0)
			return nil, result
		}
		if proxyURL.Scheme == "socks5" {
			d, dialErr := proxy.FromURL(proxyURL, &net.Dialer{Timeout: 30 * time.Second})
			contextDialer, ok := d.(proxy.ContextDialer)
			if dialErr != nil || !ok {
				result.Error = failure(execution.ErrorKindInternal, "websocket_proxy_invalid", 0)
				return nil, result
			}
			dialer.NetDialContext = contextDialer.DialContext
		} else {
			dialer.Proxy = http.ProxyURL(proxyURL)
		}
	}
	header = header.Clone()
	for name := range header {
		key := strings.ToLower(name)
		if strings.HasPrefix(key, "sec-websocket-") || key == "connection" || key == "upgrade" || key == "content-length" || key == "content-type" || key == "accept-encoding" || key == "host" {
			delete(header, name)
		}
	}
	conn, response, err := dialer.DialContext(ctx, endpoint, header)
	if response != nil {
		result.Header = response.Header.Clone()
		result.HeaderObservedAt = time.Now()
	}
	if err != nil {
		result.Error = failure(execution.ErrorKindTransport, "websocket_handshake_failed", 0)
		if response != nil {
			result.Error = failure(execution.ErrorKindHTTP, "websocket_handshake_failed", response.StatusCode)
			result.Error.ScopeHint = ""
			if response.Body != nil {
				body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
				_ = response.Body.Close()
				var event envelope
				if json.Unmarshal(body, &event) == nil && safeCode(event.Error.Code) != "" {
					result.Error.Code = safeCode(event.Error.Code)
				}
			}
		}
		if ctx.Err() != nil && result.Error.Kind != execution.ErrorKindHTTP {
			result.Error = contextFailure(ctx.Err())
		} else if result.Error.Kind != execution.ErrorKindHTTP {
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				result.Error = failure(execution.ErrorKindTimeout, "websocket_timeout", 0)
			}
		}
		return nil, result
	}
	conn.SetReadLimit(MaxMessageBytes)
	s := &session{conn: conn, write: make(chan struct{}, 1), turns: make(map[string]*turnSlot), done: make(chan struct{}), header: result.Header.Clone(), headerObservedAt: result.HeaderObservedAt}
	go s.readLoop()
	return s, result
}

func (s *session) Done() <-chan struct{} { return s.done }

func (s *session) Close() error {
	s.fail(failure(execution.ErrorKindCanceled, "websocket_closed", 0))
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeErr
}

func (s *session) fail(evidence *execution.ErrorEvidence) {
	s.once.Do(func() {
		s.mu.Lock()
		s.failure = evidence
		s.mu.Unlock()
		err := s.conn.Close()
		s.mu.Lock()
		if err != nil && !errors.Is(err, net.ErrClosed) {
			s.closeErr = errors.New("close upstream websocket")
		}
		s.mu.Unlock()
		close(s.done)
	})
}

type envelope struct {
	Type       string          `json:"type"`
	StreamID   json.RawMessage `json:"stream_id"`
	ResponseID string          `json:"response_id"`
	Status     int             `json:"status"`
	Response   struct {
		ID     string `json:"id"`
		Object string `json:"object"`
		Status string `json:"status"`
		Error  struct {
			Code string `json:"code"`
		} `json:"error"`
	} `json:"response"`
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

func (s *session) readLoop() {
	for {
		kind, body, err := s.conn.ReadMessage()
		if err != nil {
			s.fail(failure(execution.ErrorKindTransport, "websocket_disconnected", 0))
			return
		}
		var event envelope
		if kind != websocket.TextMessage || !utf8.Valid(body) || json.Unmarshal(body, &event) != nil || event.Type == "" {
			s.fail(failure(execution.ErrorKindProvider, "invalid_websocket_event", 0))
			return
		}
		var lane string
		if len(event.StreamID) > 0 && (json.Unmarshal(event.StreamID, &lane) != nil || lane == "" || safeCode(lane) == "") {
			s.fail(failure(execution.ErrorKindProvider, "invalid_websocket_stream", 0))
			return
		}
		s.mu.Lock()
		slot := s.turns[lane]
		s.mu.Unlock()
		if slot == nil || event.Error.Code == "websocket_connection_limit_reached" {
			code := safeCode(event.Error.Code)
			if code == "" {
				code = "unmatched_websocket_event"
			}
			s.fail(failure(execution.ErrorKindProvider, code, event.Status))
			return
		}
		item := delivery{payload: body, done: make(chan struct{})}
		select {
		case slot.events <- item:
		case <-slot.done:
			s.fail(failure(execution.ErrorKindProvider, "unmatched_websocket_event", 0))
			return
		case <-s.done:
			return
		}
		// 消费者处理完当前事件再读取下一帧，避免终态尚未交付就因紧随的 Close 丢失。
		select {
		case <-item.done:
		case <-s.done:
			return
		}
	}
}

func (s *session) ExecuteTurn(ctx context.Context, payload []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
	result := execution.WebsocketResult{DispatchState: execution.DispatchNotSent}
	var request map[string]json.RawMessage
	var lane string
	if len(payload) > MaxMessageBytes || !utf8.Valid(payload) || json.Unmarshal(payload, &request) != nil || request == nil {
		result.Error = failure(execution.ErrorKindInvalidRequest, "invalid_websocket_request", 0)
		return result
	}
	if raw, exists := request["stream_id"]; exists && (json.Unmarshal(raw, &lane) != nil || lane == "") {
		result.Error = failure(execution.ErrorKindInvalidRequest, "invalid_stream_id", 0)
		return result
	}
	if len(lane) > 256 || (lane != "" && safeCode(lane) == "") {
		result.Error = failure(execution.ErrorKindInvalidRequest, "invalid_stream_id", 0)
		return result
	}
	request["type"] = json.RawMessage(`"response.create"`)
	body, err := json.Marshal(request)
	if err != nil || len(body) > MaxMessageBytes {
		result.Error = failure(execution.ErrorKindInvalidRequest, "websocket_request_too_large", 0)
		return result
	}
	s.mu.Lock()
	if s.failure != nil {
		copy := s.failure.Clone()
		result.Error = &copy
		s.mu.Unlock()
		return result
	}
	if s.turns[lane] != nil {
		s.mu.Unlock()
		result.Error = failure(execution.ErrorKindInvalidRequest, "websocket_stream_busy", 0)
		return result
	}
	ch := make(chan delivery)
	slot := &turnSlot{events: ch, done: make(chan struct{})}
	s.turns[lane] = slot
	result.Header, s.header = s.header, nil
	result.HeaderObservedAt, s.headerObservedAt = s.headerObservedAt, time.Time{}
	s.mu.Unlock()
	unregister := func() {
		s.mu.Lock()
		if s.turns[lane] == slot {
			delete(s.turns, lane)
			close(slot.done)
		}
		s.mu.Unlock()
	}
	defer unregister()
	if ctx.Err() != nil {
		result.Error = contextFailure(ctx.Err())
		return result
	}
	select {
	case s.write <- struct{}{}:
	case <-ctx.Done():
		result.Error = contextFailure(ctx.Err())
		return result
	case <-s.done:
		result.Error = failure(execution.ErrorKindTransport, "websocket_disconnected", 0)
		return result
	}
	if ctx.Err() != nil {
		<-s.write
		result.Error = contextFailure(ctx.Err())
		return result
	}
	stop := context.AfterFunc(ctx, func() { s.fail(contextFailure(ctx.Err())) })
	defer stop()
	deadline := time.Now().Add(30 * time.Second)
	if until, ok := ctx.Deadline(); ok && until.Before(deadline) {
		deadline = until
	}
	err = s.conn.SetWriteDeadline(deadline)
	if err == nil {
		result.DispatchState = execution.DispatchMaybeSent
		err = s.conn.WriteMessage(websocket.TextMessage, body)
	}
	<-s.write
	if err != nil {
		s.fail(failure(execution.ErrorKindTransport, "websocket_send_failed", 0))
	}
	responseID := ""
	for {
		select {
		case item := <-ch:
			var event envelope
			_ = json.Unmarshal(item.payload, &event) // readLoop 已校验 JSON。
			id := event.Response.ID
			if id == "" {
				id = event.ResponseID
			}
			if (id != "" && responseID != "" && id != responseID) ||
				(event.Response.ID != "" && event.ResponseID != "" && event.Response.ID != event.ResponseID) {
				close(item.done)
				s.fail(failure(execution.ErrorKindProvider, "websocket_response_mismatch", 0))
				continue
			}
			if id != "" {
				responseID = id
			}
			if event.Type == "response.completed" || (event.Type == "response.done" && event.Response.Status == "completed") {
				if event.Response.ID == "" || event.Response.Object != "response" || (event.Response.Status != "" && event.Response.Status != "completed") {
					close(item.done)
					s.fail(failure(execution.ErrorKindProvider, "invalid_websocket_terminal", 0))
					continue
				}
			}
			if emit != nil {
				if err := emit(ctx, item.payload); err != nil {
					close(item.done)
					s.fail(failure(execution.ErrorKindCanceled, "websocket_consumer_failed", 0))
					continue
				}
			}
			terminal := event.Type == "response.completed" || event.Type == "response.done" || event.Type == "response.failed" || event.Type == "response.incomplete" || event.Type == "error"
			if terminal {
				if event.Type != "response.completed" && !(event.Type == "response.done" && event.Response.Status == "completed") {
					code := safeCode(event.Error.Code)
					if code == "" {
						code = safeCode(event.Response.Error.Code)
					}
					if code == "" {
						code = "upstream_response_failed"
					}
					result.Error = failure(execution.ErrorKindProvider, code, event.Status)
					// 原生请求错误的健康作用域由既有分类器结合错误码确定。
					result.Error.ScopeHint = ""
				}
				if responseID == "" && result.Error == nil {
					result.Error = failure(execution.ErrorKindProvider, "missing_response_id", 0)
				}
			}
			if terminal {
				unregister()
			}
			close(item.done)
			if terminal {
				return result
			}
		case <-s.done:
			s.mu.Lock()
			copy := s.failure.Clone()
			s.mu.Unlock()
			result.Error = &copy
			return result
		}
	}
}

func failure(kind execution.ErrorKind, code string, status int) *execution.ErrorEvidence {
	return &execution.ErrorEvidence{Kind: kind, Code: code, StatusCode: status,
		OriginHint: execution.ErrorOriginUpstream, ScopeHint: execution.ErrorScopeRequest,
		Summary: "WebSocket upstream request failed.", ReplaySafety: execution.ReplaySafetyUnknown}
}

func contextFailure(err error) *execution.ErrorEvidence {
	kind, code := execution.ErrorKindCanceled, "websocket_canceled"
	if errors.Is(err, context.DeadlineExceeded) {
		kind, code = execution.ErrorKindTimeout, "websocket_timeout"
	}
	e := failure(kind, code, 0)
	e.OriginHint = execution.ErrorOriginDownstream
	if kind == execution.ErrorKindTimeout {
		e.OriginHint = execution.ErrorOriginUpstream
	}
	return e
}

func safeCode(value string) string {
	if len(value) > 256 {
		return ""
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return ""
		}
	}
	return value
}
