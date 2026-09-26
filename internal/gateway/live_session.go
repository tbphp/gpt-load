package gateway

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/pricing"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

const (
	liveSessionLifetime     = time.Hour
	liveReconnectWindow     = 30 * time.Second
	liveAuthorizationPeriod = 2 * time.Second
	liveHangupRetryDelay    = 5 * time.Second
	liveHangupMaxAttempts   = 3
)

type liveCallSession struct {
	id          string
	requestID   string
	keyID       uint
	keyHash     string
	groupID     uint
	clientModel string
	model       string
	peerAddr    string
	ref         state.CredentialRef
	upstream    execution.LiveSession
	media       *liveMediaSession
	logger      *logrus.Logger
	recorder    *requestRecorder
	closedOnce  sync.Once
	finishMu    sync.Mutex
	// 挂断重试只负责清理；本地会话结果只记录一次，由 finishMu 保护。
	hangupAttempts int
	logged         bool
	// 以下终止状态与定时器由 liveSessions.mu 保护。
	terminating         bool
	finishReason        string
	attached            bool
	timer               *time.Timer
	reconnect           *time.Timer
	reconnectGeneration uint64
	authorized          func() bool
	closed              chan struct{}
}

type liveSessions struct {
	mu      sync.Mutex
	calls   map[string]*liveCallSession
	pending int
	maximum int
	closing bool
}

func newLiveSessions() *liveSessions {
	return &liveSessions{calls: make(map[string]*liveCallSession), maximum: 32}
}

func (store *liveSessions) setMaximum(maximum int) {
	if store == nil || maximum <= 0 {
		return
	}
	store.mu.Lock()
	store.maximum = maximum
	store.mu.Unlock()
}

func (store *liveSessions) reserve() bool {
	if store == nil {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closing || len(store.calls)+store.pending >= store.maximum {
		return false
	}
	store.pending++
	return true
}

func (store *liveSessions) release() {
	store.mu.Lock()
	store.pending--
	store.mu.Unlock()
}

func (store *liveSessions) put(call *liveCallSession) bool {
	store.mu.Lock()
	if store.closing {
		store.mu.Unlock()
		return false
	}
	if _, duplicate := store.calls[call.id]; duplicate {
		store.mu.Unlock()
		return false
	}
	store.calls[call.id] = call
	call.closed = make(chan struct{})
	call.timer = time.AfterFunc(liveSessionLifetime, func() { store.finishCall(call, "expired") })
	// 两种模式都要求控制连接，防止创建后无人接入的会话占用资源。
	call.reconnectGeneration++
	generation := call.reconnectGeneration
	call.reconnect = time.AfterFunc(liveReconnectWindow, func() { store.expireReconnect(call, generation) })
	store.mu.Unlock()
	if call.media != nil {
		call.media.OnClose(func(reason string) { go store.finishCall(call, reason) })
	}
	go store.watchAuthorization(call)
	return true
}

func (store *liveSessions) watchAuthorization(call *liveCallSession) {
	ticker := time.NewTicker(liveAuthorizationPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if call.authorized != nil && !call.authorized() {
				store.finishCall(call, "key_revoked")
				return
			}
		case <-call.closed:
			return
		}
	}
}

func (store *liveSessions) claim(id string, keyID uint) (*liveCallSession, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	call := store.calls[id]
	if call == nil || call.keyID != keyID || call.attached || call.terminating {
		return nil, false
	}
	if call.reconnect != nil {
		call.reconnect.Stop()
		call.reconnect = nil
	}
	call.reconnectGeneration++
	call.attached = true
	return call, true
}

func (store *liveSessions) lookup(id string, keyID uint) (*liveCallSession, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	call := store.calls[id]
	return call, call != nil && call.keyID == keyID
}

func (store *liveSessions) unclaim(call *liveCallSession, connection *websocket.Conn) {
	call.upstream.ReleaseSideband(connection)
	store.mu.Lock()
	if store.calls[call.id] == call && !call.terminating {
		call.attached = false
		call.reconnectGeneration++
		generation := call.reconnectGeneration
		call.reconnect = time.AfterFunc(liveReconnectWindow, func() { store.expireReconnect(call, generation) })
	}
	store.mu.Unlock()
}

func (store *liveSessions) expireReconnect(call *liveCallSession, generation uint64) {
	store.mu.Lock()
	if store.calls[call.id] != call || call.attached || call.terminating || call.reconnectGeneration != generation {
		store.mu.Unlock()
		return
	}
	// 在同一把锁下锁定终止状态，过期回调不能关闭已经重新接入的会话。
	store.beginFinishLocked(call, "control_timeout")
	store.mu.Unlock()
	_ = store.attemptFinish(call)
}

func (store *liveSessions) beginFinishLocked(call *liveCallSession, reason string) {
	if !call.terminating || liveUpstreamEnded(reason) {
		call.finishReason = reason
	}
	call.terminating = true
	if call.timer != nil {
		call.timer.Stop()
		call.timer = nil
	}
	if call.reconnect != nil {
		call.reconnect.Stop()
		call.reconnect = nil
	}
}

func liveUpstreamEnded(reason string) bool {
	return reason == "upstream_session_ended" || reason == "upstream_session_error"
}

func (store *liveSessions) finishCall(call *liveCallSession, reason string) error {
	if store == nil || call == nil {
		return nil
	}
	store.mu.Lock()
	if store.calls[call.id] != call {
		store.mu.Unlock()
		return nil
	}
	// 本地关闭也会触发媒体/控制回调；已有终止流程只接受主动重试或上游结束确认。
	if call.terminating && reason != "client_hangup" && !liveUpstreamEnded(reason) {
		store.mu.Unlock()
		return nil
	}
	store.beginFinishLocked(call, reason)
	store.mu.Unlock()
	return store.attemptFinish(call)
}

// attemptFinish 串行执行同一通话的挂断；失败会话仍占用名额且不能重新接入。
func (store *liveSessions) attemptFinish(call *liveCallSession) error {
	call.finishMu.Lock()
	defer call.finishMu.Unlock()
	store.mu.Lock()
	if store.calls[call.id] != call {
		store.mu.Unlock()
		return nil
	}
	if call.timer != nil {
		call.timer.Stop()
		call.timer = nil
	}
	reason := call.finishReason
	store.mu.Unlock()
	call.stop()
	var hangupErr error
	if !liveUpstreamEnded(reason) {
		call.hangupAttempts++
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		hangupErr = call.upstream.Hangup(ctx)
		cancel()
	}
	store.mu.Lock()
	reason = call.finishReason
	// 另一个控制回调可能已确认上游结束，无需再等待 REST 挂断。
	if liveUpstreamEnded(reason) {
		hangupErr = nil
	}
	retry := hangupErr != nil && !store.closing && call.hangupAttempts < liveHangupMaxAttempts
	if retry {
		call.timer = time.AfterFunc(liveHangupRetryDelay, func() { _ = store.attemptFinish(call) })
	} else {
		delete(store.calls, call.id)
	}
	store.mu.Unlock()
	if hangupErr != nil {
		call.logHangupFailure(hangupErr, retry)
		reason = "hangup_failed"
	}
	if !call.logged {
		call.logged = true
		call.recordClose(reason)
	}
	return hangupErr
}

func (call *liveCallSession) logHangupFailure(err error, retry bool) {
	fields := liveHangupFailureFields(err)
	fields["event"] = "codex_live_hangup_failed"
	fields["retry_scheduled"] = retry
	fields["hangup_attempt"] = call.hangupAttempts
	fields["request_id"] = call.requestID
	utils.LogPlaneBestEffort(call.logger, logrus.WarnLevel, utils.LogPlaneData,
		fields, "Codex live upstream hangup was not confirmed")
}

// 仅记录固定错误类别和 HTTP 状态，不输出可能包含令牌、代理口令的原始错误。
func liveHangupFailureFields(err error) logrus.Fields {
	fields := logrus.Fields{"failure_kind": "transport"}
	var status interface{ StatusCode() int }
	var network net.Error
	switch {
	case errors.As(err, &status):
		fields["failure_kind"] = "http"
		fields["upstream_status"] = status.StatusCode()
	case errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network) && network.Timeout():
		fields["failure_kind"] = "timeout"
	case errors.Is(err, context.Canceled):
		fields["failure_kind"] = "canceled"
	}
	return fields
}

func (store *liveSessions) closeAll() {
	store.mu.Lock()
	store.closing = true
	calls := make([]*liveCallSession, 0, len(store.calls))
	for _, call := range store.calls {
		store.beginFinishLocked(call, "server_shutdown")
		calls = append(calls, call)
	}
	store.mu.Unlock()
	var wait sync.WaitGroup
	for _, call := range calls {
		wait.Add(1)
		go func() { defer wait.Done(); _ = store.attemptFinish(call) }()
	}
	wait.Wait()
}

func (call *liveCallSession) stop() {
	call.closedOnce.Do(func() {
		if call.closed != nil {
			close(call.closed)
		}
		mediaErr := call.media.Close()
		controlErr := call.upstream.Close()
		if mediaErr != nil || controlErr != nil {
			utils.LogPlaneBestEffort(call.logger, logrus.WarnLevel, utils.LogPlaneData,
				logrus.Fields{"event": "codex_live_close_failed"}, "Codex live local connection cleanup failed")
		}
	})
}

func (call *liveCallSession) recordClose(reason string) {
	if call.recorder != nil {
		call.recorder.usage = telemetry.UsageObservation{
			Result:  usage.Result{State: usage.StateMissing},
			GroupID: call.groupID, ChannelID: channel.Codex, CredentialID: call.ref.ID, AttemptSequence: len(call.recorder.attempts),
			Pricing: telemetry.PricingObservation{UpstreamModel: call.model,
				CostState: string(pricing.CostStateUnpriced), PricingCompleteness: string(pricing.CompletenessUnavailable)},
		}
		call.recorder.outcome.upstreamModel = call.model
		call.recorder.outcome.statusCode = 201
		switch reason {
		case "client_hangup", "client_media_ended", "upstream_session_ended":
			call.recorder.outcome.status = telemetry.RequestStatusSuccess
		case "server_shutdown", "key_revoked", "client_disconnected":
			call.recorder.outcome.status = telemetry.RequestStatusCanceled
			call.recorder.outcome.errorCode = reason
			call.recorder.outcome.errorSummary = "Codex live session was canceled."
		case "hangup_failed":
			call.recorder.outcome.status = telemetry.RequestStatusIncomplete
			call.recorder.outcome.errorCode = reason
			call.recorder.outcome.errorSummary = "Codex live local session ended, but upstream hangup was not confirmed."
		default:
			call.recorder.outcome.status = telemetry.RequestStatusIncomplete
			call.recorder.outcome.errorCode = reason
			call.recorder.outcome.errorSummary = "Codex live session ended before hangup."
		}
		call.recorder.emit()
	}
}
