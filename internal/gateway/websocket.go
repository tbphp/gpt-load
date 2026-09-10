package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"gpt-load/internal/execution"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

type websocketLimits struct {
	connections, perKey, lanes, active, pending, message, input, globalInput, responses int
	firstRequest, idle                                                                  time.Duration
}

func defaultWebsocketLimits() websocketLimits {
	return websocketLimits{connections: 128, perKey: 32, lanes: 32, active: 16, pending: 32, message: 10 << 20, input: 16 << 20, globalInput: 64 << 20, responses: 1024, firstRequest: 30 * time.Second}
}

type websocketBudget struct {
	mu                 sync.Mutex
	connections, input int
	keys               map[uint]int
}

func (b *websocketBudget) connection(key uint, limits websocketLimits, delta int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if delta > 0 && (b.connections >= limits.connections || b.keys[key] >= limits.perKey) {
		return false
	}
	if b.keys == nil {
		b.keys = make(map[uint]int)
	}
	b.connections += delta
	b.keys[key] += delta
	if b.keys[key] == 0 {
		delete(b.keys, key)
	}
	return true
}

// reserveInput 计入完整消息及参数覆盖后的增长量，不重复计算同一逻辑请求的副本。
func (s *websocketConnection) reserveInput(delta int) bool {
	b := &s.handler.websocketBudget
	limits := s.handler.websocketLimits
	b.mu.Lock()
	defer b.mu.Unlock()
	if delta > 0 && (b.input+delta > limits.globalInput || s.inputBytes+delta > limits.input) {
		return false
	}
	b.input += delta
	s.inputBytes += delta
	return true
}

type websocketTurn struct {
	body    []byte
	lane    string
	model   string
	started time.Time
}
type websocketFinished struct{ lane string }
type websocketParent struct {
	lane     string
	complete bool
}
type websocketBinding struct {
	monitor      sync.Once
	session      execution.WebsocketSession
	ref          state.CredentialRef
	channel      string
	target       string
	proxy        string
	headers      string
	capabilities execution.WebsocketCapabilities
}

func (s *websocketConnection) watchBinding(binding *websocketBinding) {
	binding.monitor.Do(func() {
		s.workers.Add(1)
		go func() {
			defer s.workers.Done()
			select {
			case <-binding.session.Done():
				s.cancel()
			case <-s.ctx.Done():
			}
		}()
	})
}

type websocketConnection struct {
	inputBytes   int // 受 handler.websocketBudget.mu 保护。
	handler      *Handler
	conn         *websocket.Conn
	request      *http.Request
	keyID        uint
	keyHash      string
	ctx          context.Context
	cancel       context.CancelFunc
	write        chan struct{}
	bind         chan struct{}
	mu           sync.Mutex
	binding      *websocketBinding
	parents      map[string]websocketParent
	parentOrder  []string
	workers      sync.WaitGroup
	firstRequest *time.Timer
}

func websocketIntent(request *http.Request) bool {
	if request.Method != http.MethodGet || request.URL.Path != "/v1/responses" {
		return false
	}
	for _, field := range [][2]string{{"Upgrade", "websocket"}, {"Connection", "upgrade"}} {
		for _, value := range request.Header.Values(field[0]) {
			for _, token := range strings.Split(value, ",") {
				if strings.EqualFold(strings.TrimSpace(token), field[1]) {
					return true
				}
			}
		}
	}
	for key := range request.Header {
		if strings.HasPrefix(strings.ToLower(key), "sec-websocket-") {
			return true
		}
	}
	return false
}

func websocketOriginAllowed(request *http.Request, snapshot *state.ConfigSnapshot) bool {
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	if snapshot != nil && snapshot.Settings.CORS.Enabled {
		allowed, _ := matchCORSOrigin(origin, snapshot.Settings.CORS.AllowedOrigins)
		return allowed
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.User == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && strings.EqualFold(parsed.Host, request.Host)
}

var reasonWebsocketDisabled = reason{403, "websocket_disabled", "Responses WebSocket is disabled for the available groups."}

func websocketGroupEnabled(snapshot *state.ConfigSnapshot, key state.AccessKeyView, groupID uint) bool {
	if snapshot == nil {
		return false
	}
	group, exists := snapshot.Groups[groupID]
	if !exists || !snapshot.GroupCatalog[groupID].Enabled || !group.ResponsesWebsocketEnabled || !group.ResolvedTarget.ResponsesWebsocket.Native {
		return false
	}
	if _, allowed := key.Filters.Groups[groupID]; len(key.Filters.Groups) > 0 && !allowed {
		return false
	}
	if _, allowed := key.Filters.Protocols[protocol.OpenAIResponses]; len(key.Filters.Protocols) > 0 && !allowed {
		return false
	}
	return true
}

func hasEnabledWebsocketGroup(snapshot *state.ConfigSnapshot, key state.AccessKeyView) bool {
	if snapshot != nil {
		for groupID := range snapshot.Groups {
			if websocketGroupEnabled(snapshot, key, groupID) {
				return true
			}
		}
	}
	return false
}

func (h *Handler) handleWebsocket(c *gin.Context, requestContext *dataPlaneRequestContext) {
	if !hasEnabledWebsocketGroup(requestContext.snapshot, requestContext.accessKey) {
		_ = h.writeReason(c, reasonWebsocketDisabled)
		return
	}
	limits := h.websocketLimits
	if !h.websocketBudget.connection(requestContext.accessKey.ID, limits, 1) {
		_ = h.writeReason(c, reason{503, "websocket_connection_limit", "WebSocket connection limit reached."})
		return
	}
	defer h.websocketBudget.connection(requestContext.accessKey.ID, limits, -1)
	upgrader := websocket.Upgrader{HandshakeTimeout: h.writeTimeout, CheckOrigin: func(r *http.Request) bool { return websocketOriginAllowed(r, requestContext.snapshot) }}
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(c.Request.Context())
	s := &websocketConnection{handler: h, conn: conn, request: c.Request.Clone(ctx), ctx: ctx, cancel: cancel, keyID: requestContext.accessKey.ID, write: make(chan struct{}, 1), bind: make(chan struct{}, 1), parents: make(map[string]websocketParent)}
	for hash, key := range requestContext.snapshot.AccessKeysByHash {
		if key.ID == s.keyID {
			s.keyHash = hash
			break
		}
	}
	conn.SetReadLimit(int64(limits.message))
	stop := context.AfterFunc(ctx, func() {
		_ = conn.Close()
		s.mu.Lock()
		binding := s.binding
		s.mu.Unlock()
		if binding != nil {
			_ = binding.session.Close()
		}
	})
	defer func() {
		cancel()
		_ = conn.Close()
		s.mu.Lock()
		binding := s.binding
		s.mu.Unlock()
		if binding != nil {
			_ = binding.session.Close()
		}
		s.workers.Wait()
		stop()
	}()
	s.run()
}

func (s *websocketConnection) authorized(snapshot *state.ConfigSnapshot) (state.AccessKeyView, bool) {
	if snapshot == nil {
		return state.AccessKeyView{}, false
	}
	key, ok := snapshot.AccessKeysByHash[s.keyHash]
	if !ok || key.ID != s.keyID || key.Status != state.AccessKeyStatusActive || (key.ExpiresAtMS != nil && s.handler.now().UnixMilli() >= *key.ExpiresAtMS) || !websocketOriginAllowed(s.request, snapshot) {
		return key, false
	}
	if len(key.AllowedPeerCIDRs) > 0 {
		peer, err := utils.NormalizePeerIP(s.request.RemoteAddr)
		if err != nil || !utils.AllowedCIDRsContain(key.AllowedPeerCIDRs, peer) {
			return key, false
		}
	}
	return key, true
}

// 单个读协程只持有一条受预算约束的消息；队列与同流调度均由 run 持有。
func (s *websocketConnection) readMessages(out chan<- websocketTurn) {
	defer s.workers.Done()
	defer s.cancel()
	limits := s.handler.websocketLimits
	scratch := make([]byte, 32<<10)
	for {
		kind, reader, err := s.conn.NextReader()
		if err != nil {
			return
		}
		if kind != websocket.TextMessage {
			s.closeWith(websocket.CloseUnsupportedData, "Text JSON is required.")
			return
		}
		var body []byte
		for {
			n, readErr := reader.Read(scratch)
			if n > 0 {
				if len(body)+n > limits.message || !s.reserveInput(n) {
					s.reserveInput(-len(body))
					s.closeWith(websocket.CloseTryAgainLater, "WebSocket input limit reached.")
					return
				}
				body = append(body, scratch[:n]...)
			}
			if readErr != nil {
				if !errors.Is(readErr, io.EOF) {
					s.reserveInput(-len(body))
					return
				}
				break
			}
		}
		turn := websocketTurn{body: body, started: s.handler.requestNow()}
		var envelope map[string]json.RawMessage
		if !utf8.Valid(body) || json.Unmarshal(body, &envelope) != nil || envelope == nil {
			s.reserveInput(-len(body))
			s.closeWith(websocket.CloseInvalidFramePayloadData, "A JSON object is required.")
			return
		} else if raw, ok := envelope["stream_id"]; ok {
			if json.Unmarshal(raw, &turn.lane) != nil || !validWebsocketLane(turn.lane) {
				s.reserveInput(-len(body))
				s.closeWith(websocket.ClosePolicyViolation, "Invalid stream_id.")
				return
			}
		}
		if raw, exists := envelope["model"]; exists {
			if json.Unmarshal(raw, &turn.model) != nil || len(turn.model) > maxDataPlaneModelBytes {
				turn.model = ""
			}
		}
		select {
		case out <- turn:
		case <-s.ctx.Done():
			s.reserveInput(-len(body))
			return
		}
	}
}

func validWebsocketLane(lane string) bool {
	if len(lane) == 0 || len(lane) > 256 {
		return false
	}
	for _, c := range lane {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

func (s *websocketConnection) run() {
	limits := s.handler.websocketLimits
	messages := make(chan websocketTurn)
	finished := make(chan websocketFinished, limits.active)
	s.workers.Add(1)
	go s.readMessages(messages)
	queues := make(map[string][]websocketTurn)
	busy := make(map[string]bool)
	lanes := make([]string, 0, limits.lanes+1)
	active, pending := 0, 0
	first := time.NewTimer(limits.firstRequest)
	s.firstRequest = first
	defer first.Stop()
	idle := time.NewTimer(time.Hour)
	idle.Stop()
	defer idle.Stop()
	expiry := time.NewTimer(time.Hour)
	expiry.Stop()
	defer expiry.Stop()
	refreshConfiguration := func(snapshot *state.ConfigSnapshot) bool {
		key, ok := s.authorized(snapshot)
		if !ok {
			s.closeWith(websocket.ClosePolicyViolation, "WebSocket authorization expired.")
			return false
		}
		s.mu.Lock()
		binding := s.binding
		s.mu.Unlock()
		enabled := false
		if binding != nil {
			// 分组停用、删除或 WS 关闭时即时中止；其余路由条件在下一轮检查。
			group, exists := snapshot.Groups[binding.ref.GroupID]
			enabled = exists && group.ResponsesWebsocketEnabled
		} else {
			enabled = hasEnabledWebsocketGroup(snapshot, key)
		}
		if !enabled {
			s.closeWith(websocket.ClosePolicyViolation, reasonWebsocketDisabled.Message)
			return false
		}
		expiry.Stop()
		if key.ExpiresAtMS != nil {
			expiry.Reset(time.Until(time.UnixMilli(*key.ExpiresAtMS)))
		}
		return true
	}
	snapshot, updates := s.handler.manager.CurrentWithUpdates()
	if !refreshConfiguration(snapshot) {
		return
	}
	defer func() {
		for _, q := range queues {
			for _, turn := range q {
				recorder := s.newTurnRecorder(turn)
				recorder.completeCanceled(s.ctx, 0, -1)
				recorder.emit()
				s.reserveInput(-len(turn.body))
			}
		}
	}()
	for {
		if s.ctx.Err() != nil {
			return
		}
		for _, lane := range lanes {
			q := queues[lane]
			if busy[lane] || len(q) == 0 || active >= limits.active {
				continue
			}
			turn := q[0]
			q[0] = websocketTurn{}
			queues[lane] = q[1:]
			pending--
			active++
			busy[lane] = true
			s.workers.Add(1)
			go func() {
				defer s.workers.Done()
				defer s.reserveInput(-len(turn.body))
				s.executeTurn(turn)
				select {
				case finished <- websocketFinished{lane: turn.lane}:
				case <-s.ctx.Done():
				}
			}()
		}
		select {
		case turn := <-messages:
			idle.Stop()
			if _, exists := queues[turn.lane]; !exists {
				named := 0
				for _, name := range lanes {
					if name != "" {
						named++
					}
				}
				if turn.lane != "" && named >= limits.lanes {
					s.reserveInput(-len(turn.body))
					turn.body = nil
					// 新流尚无活动轮，拒绝可直接归属，不占用新的历史流名。
					value := reason{400, "websocket_stream_limit_reached", "WebSocket stream limit reached. Reuse an existing stream_id or open a new connection."}
					key, authorized := s.authorized(s.handler.manager.Current())
					if !authorized {
						value = reasonInvalidAccessKey
					} else if !s.handler.limiter.Allow(key.ID, key.RPMLimit).Allowed {
						value = reasonAccessKeyRateLimited
					}
					recorder := s.newTurnRecorder(turn)
					recorder.completeReason(value)
					recorder.emit()
					s.emitReason(turn.lane, value)
					if !authorized {
						s.cancel()
					}
					continue
				}
				queues[turn.lane] = nil
				lanes = append(lanes, turn.lane)
			}
			if pending >= limits.pending {
				s.reserveInput(-len(turn.body))
				s.closeWith(websocket.CloseTryAgainLater, "WebSocket queue limit reached.")
				return
			}
			queues[turn.lane] = append(queues[turn.lane], turn)
			pending++
		case done := <-finished:
			active--
			busy[done.lane] = false
			if active == 0 && pending == 0 && limits.idle > 0 {
				idle.Reset(limits.idle)
			}
		case <-updates:
			snapshot, updates = s.handler.manager.CurrentWithUpdates()
			if !refreshConfiguration(snapshot) {
				return
			}
		case <-expiry.C:
			s.closeWith(websocket.ClosePolicyViolation, "WebSocket authorization expired.")
			return
		case <-first.C:
			s.closeWith(websocket.CloseGoingAway, "First request timed out.")
			return
		case <-idle.C:
			s.closeWith(websocket.CloseGoingAway, "WebSocket conversation is idle.")
			return
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *websocketConnection) closeWith(code int, message string) {
	_ = s.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, message), time.Now().Add(s.handler.writeTimeout))
	s.cancel()
}
func (s *websocketConnection) emit(ctx context.Context, body []byte) error {
	select {
	case s.write <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
	defer func() { <-s.write }()
	deadline := time.Now().Add(s.handler.writeTimeout)
	if until, ok := ctx.Deadline(); ok && until.Before(deadline) {
		deadline = until
	}
	if err := s.conn.SetWriteDeadline(deadline); err != nil {
		s.cancel()
		return err
	}
	if err := s.conn.WriteMessage(websocket.TextMessage, body); err != nil {
		s.cancel()
		return err
	}
	return nil
}
func (s *websocketConnection) emitReason(lane string, value reason) {
	errorType := "invalid_request_error"
	switch {
	case value.Code == reasonAccessKeyCostLimitExceeded.Code:
		errorType = "usage_limit_reached"
	case value.Status == http.StatusTooManyRequests:
		errorType = "rate_limit_error"
	case value.Status == http.StatusUnauthorized:
		errorType = "authentication_error"
	case value.Status == http.StatusForbidden:
		errorType = "permission_error"
	case value.Status >= http.StatusInternalServerError:
		errorType = "server_error"
	}
	body, err := json.Marshal(struct {
		Type     string `json:"type"`
		StreamID string `json:"stream_id,omitempty"`
		Status   int    `json:"status"`
		Error    struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{Type: "error", StreamID: lane, Status: value.Status, Error: struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}{errorType, value.Code, value.Message}})
	if err == nil {
		_ = s.emit(s.ctx, body)
	} else {
		s.cancel()
	}
}
