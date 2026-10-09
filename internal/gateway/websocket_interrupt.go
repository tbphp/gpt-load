package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

type websocketInterrupt struct {
	Type       string `json:"type"`
	ResponseID string `json:"response_id"`
	Mode       string `json:"mode"`
	Lane       string `json:"stream_id"`
}

func inspectWebsocketInterrupt(body []byte) (websocketInterrupt, error) {
	var value websocketInterrupt
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	// readMessages already validates JSON framing, message size and stream_id.
	if value.Type != "response.interrupt" || value.Mode != "discard_partial_items" || value.ResponseID == "" || len(value.ResponseID) > 4096 {
		return value, errors.New("invalid interrupt")
	}
	return value, nil
}

// A control frame bypasses the create queue so it can reach the active response.
// It does not start another model request or generate synthetic completion/usage.
func (s *websocketConnection) handleWebsocketInterrupt(turn websocketTurn) bool {
	value, err := inspectWebsocketInterrupt(turn.body)
	if value.Type != "response.interrupt" {
		return false
	}
	if err != nil {
		s.emitReason(turn.lane, reasonInvalidInterruptRequest)
		return true
	}
	snapshot := s.handler.manager.Current()
	key, authorized := s.authorized(snapshot)
	if !authorized {
		s.emitReason(value.Lane, reasonInvalidAccessKey)
		s.cancel()
		return true
	}
	s.mu.Lock()
	binding := s.binding
	parent, found := s.parents[value.ResponseID]
	s.mu.Unlock()
	if !found || binding == nil || parent.lane != value.Lane {
		s.emitReason(value.Lane, reasonInterruptResponseNotFound)
		return true
	}
	current, exists := s.handler.registry.CredentialRef(binding.ref.ID)
	_, ready := s.handler.registry.ActiveEncryptedCredentialDataIfMatch(current)
	group, groupExists := snapshot.Groups[binding.ref.GroupID]
	_, groupAllowed := key.Filters.Groups[binding.ref.GroupID]
	_, protocolAllowed := key.Filters.Protocols[protocol.OpenAIResponses]
	if !exists || !ready || !sameWebsocketIdentity(binding.ref, current) || !groupExists || !group.ResponsesWebsocketEnabled || string(group.ChannelID) != binding.channel || string(group.ResolvedTarget.TargetConfig) != binding.target || (len(key.Filters.Groups) > 0 && !groupAllowed) || (len(key.Filters.Protocols) > 0 && !protocolAllowed) {
		s.emitReason(value.Lane, reasonConfigurationChanged)
		s.cancel()
		return true
	}
	if parent.terminal || parent.interruptSent {
		return true
	}
	interrupter, ok := binding.session.(execution.WebsocketInterrupter)
	if !ok {
		s.emitReason(value.Lane, reasonWebsocketInterruptUnsupported)
		return true
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	err = interrupter.Interrupt(ctx, value.ResponseID, value.Lane)
	cancel()
	if err != nil {
		// Completion may win the race with the control write. Its real terminal
		// event already satisfies a late interrupt; never send it to a new turn.
		s.mu.Lock()
		latest := s.parents[value.ResponseID]
		s.mu.Unlock()
		if latest.terminal {
			return true
		}
		s.emitReason(value.Lane, reasonUpstreamInterruptFailed)
		return true
	}
	s.mu.Lock()
	parent = s.parents[value.ResponseID]
	parent.interruptSent = true
	s.parents[value.ResponseID] = parent
	s.mu.Unlock()
	return true
}
