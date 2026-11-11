package gateway

import (
	"encoding/json"
	"errors"
	"strings"

	"gpt-load/internal/execution"
)

var reasonWebsocketResponseNotActive = reason{400, "websocket_response_not_active", "The WebSocket response is not active."}

func (s *websocketConnection) handleInterrupt(turn websocketTurn, fields map[string]json.RawMessage) {
	var responseID string
	if json.Unmarshal(fields["response_id"], &responseID) != nil || responseID == "" ||
		len(responseID) > 4096 || strings.TrimSpace(responseID) != responseID {
		s.emitReason(turn.lane, reasonInvalidProtocolRequest)
		return
	}
	snapshot := s.handler.manager.Current()
	key, authorized := s.authorized(snapshot)
	if !authorized {
		s.emitReason(turn.lane, reasonInvalidAccessKey)
		s.cancel()
		return
	}
	s.mu.Lock()
	binding := s.binding
	parent, found := s.parents[responseID]
	s.mu.Unlock()
	if binding == nil || !found || (turn.lane != "" && turn.lane != parent.lane) {
		s.emitReason(turn.lane, reasonWebsocketResponseNotActive)
		return
	}
	// 已结束响应的迟到中断不应留下 error 帧污染下一轮。
	if parent.complete {
		return
	}
	if !websocketGroupEnabled(snapshot, key, binding.ref.GroupID) {
		s.emitReason(parent.lane, reasonWebsocketDisabled)
		return
	}
	ref, exists := s.handler.registry.CredentialRef(binding.ref.ID)
	_, ready := s.handler.registry.ActiveEncryptedCredentialDataIfMatch(ref)
	if !exists || !ready || !sameWebsocketIdentity(binding.ref, ref) {
		s.emitReason(parent.lane, reasonConfigurationChanged)
		return
	}
	sender, supported := binding.session.(execution.WebsocketInterrupter)
	if !supported {
		s.emitReason(parent.lane, reasonWebsocketCapability)
		return
	}
	s.mu.Lock()
	parent, found = s.parents[responseID]
	if !found {
		s.mu.Unlock()
		s.emitReason(turn.lane, reasonWebsocketResponseNotActive)
		return
	}
	if parent.complete {
		s.mu.Unlock()
		return
	}
	// 先登记意图：上游可能在控制帧写入返回前就交付中断终态。
	interrupt := &websocketInterrupt{done: make(chan struct{})}
	parent.interruptRequested = true
	parent.interrupt = interrupt
	s.parents[responseID] = parent
	s.mu.Unlock()
	err := sender.Interrupt(s.ctx, turn.body)
	interrupt.err = err
	close(interrupt.done)
	if err != nil {
		s.mu.Lock()
		if current, exists := s.parents[responseID]; exists {
			current.interruptRequested = false
			s.parents[responseID] = current
		}
		s.mu.Unlock()
		if s.ctx.Err() != nil {
			return
		}
		switch {
		case errors.Is(err, execution.ErrWebsocketInterruptUnsupported):
			s.emitReason(parent.lane, reasonWebsocketCapability)
		case errors.Is(err, execution.ErrWebsocketResponseNotActive):
			s.emitReason(parent.lane, reasonWebsocketResponseNotActive)
		default:
			s.emitReason(parent.lane, reasonUpstreamConnect)
			s.cancel()
		}
	}
}
