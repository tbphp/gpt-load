package gateway

import (
	"context"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/pricing"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

const (
	liveSessionLifetime     = time.Hour
	liveReconnectWindow     = 30 * time.Second
	liveAuthorizationPeriod = 2 * time.Second
)

type liveCallSession struct {
	id                  string
	keyID               uint
	keyHash             string
	groupID             uint
	clientModel         string
	model               string
	peerAddr            string
	ref                 state.CredentialRef
	upstream            execution.LiveSession
	media               *liveMediaSession
	recorder            *requestRecorder
	closedOnce          sync.Once
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
	store.mu.Unlock()
	call.media.OnClose(func(reason string) { go store.finishCall(call, reason) })
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
	if call == nil || call.keyID != keyID || call.attached {
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
	if store.calls[call.id] == call {
		call.attached = false
		call.reconnectGeneration++
		generation := call.reconnectGeneration
		call.reconnect = time.AfterFunc(liveReconnectWindow, func() { store.expireReconnect(call, generation) })
	}
	store.mu.Unlock()
}

func (store *liveSessions) expireReconnect(call *liveCallSession, generation uint64) {
	store.mu.Lock()
	if store.calls[call.id] != call || call.attached || call.reconnectGeneration != generation {
		store.mu.Unlock()
		return
	}
	delete(store.calls, call.id)
	call.reconnect = nil
	store.mu.Unlock()
	call.close("control_timeout")
}

func (store *liveSessions) finishCall(call *liveCallSession, reason string) {
	if store == nil || call == nil {
		return
	}
	store.mu.Lock()
	if store.calls[call.id] != call {
		store.mu.Unlock()
		return
	}
	delete(store.calls, call.id)
	if call.reconnect != nil {
		call.reconnect.Stop()
		call.reconnect = nil
	}
	store.mu.Unlock()
	call.close(reason)
}

func (store *liveSessions) closeAll() {
	store.mu.Lock()
	store.closing = true
	calls := make([]*liveCallSession, 0, len(store.calls))
	for id, call := range store.calls {
		calls = append(calls, call)
		delete(store.calls, id)
		if call.reconnect != nil {
			call.reconnect.Stop()
			call.reconnect = nil
		}
	}
	store.mu.Unlock()
	var wait sync.WaitGroup
	for _, call := range calls {
		wait.Add(1)
		go func() {
			defer wait.Done()
			call.close("server_shutdown")
		}()
	}
	wait.Wait()
}

func (call *liveCallSession) close(reason string) {
	call.closedOnce.Do(func() {
		if call.timer != nil {
			call.timer.Stop()
		}
		if call.closed != nil {
			close(call.closed)
		}
		_ = call.media.Close()
		_ = call.upstream.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = call.upstream.Hangup(ctx)
		cancel()
		if call.recorder != nil {
			call.recorder.usage = telemetry.UsageObservation{
				Result:  usage.Result{State: usage.StateMissing},
				GroupID: call.groupID, ChannelID: channel.Codex, CredentialID: call.ref.ID, AttemptSequence: 1,
				Pricing: telemetry.PricingObservation{UpstreamModel: call.model,
					CostState: string(pricing.CostStateUnpriced), PricingCompleteness: string(pricing.CompletenessUnavailable)},
			}
			call.recorder.outcome.upstreamModel = call.model
			call.recorder.outcome.statusCode = 201
			switch reason {
			case "client_hangup", "client_media_ended":
				call.recorder.outcome.status = telemetry.RequestStatusSuccess
			case "server_shutdown", "key_revoked":
				call.recorder.outcome.status = telemetry.RequestStatusCanceled
				call.recorder.outcome.errorCode = reason
				call.recorder.outcome.errorSummary = "Codex live session was canceled."
			default:
				call.recorder.outcome.status = telemetry.RequestStatusIncomplete
				call.recorder.outcome.errorCode = reason
				call.recorder.outcome.errorSummary = "Codex live session ended before hangup."
			}
			call.recorder.emit()
		}
	})
}
