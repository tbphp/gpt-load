package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

type websocketInterrupt struct{ responseID, lane string }

func inspectWebsocketInterrupt(body []byte) (websocketInterrupt, error) {
	var value websocketInterrupt
	if len(body) > 8192 {
		return value, errors.New("invalid interrupt")
	}
	var fields struct {
		Type       string `json:"type"`
		ResponseID string `json:"response_id"`
		Mode       string `json:"mode"`
		StreamID   string `json:"stream_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&fields) != nil || decoder.Decode(new(json.RawMessage)) != io.EOF {
		return value, errors.New("invalid interrupt")
	}
	if fields.Type != "response.interrupt" || fields.Mode != "discard_partial_items" || fields.ResponseID == "" || len(fields.ResponseID) > 4096 || (fields.StreamID != "" && !validWebsocketLane(fields.StreamID)) {
		return value, errors.New("invalid interrupt")
	}
	value.responseID, value.lane = fields.ResponseID, fields.StreamID
	return value, nil
}

// A control frame bypasses the create queue so it can reach the active response.
// It does not start another model request or generate synthetic completion/usage.
func (s *websocketConnection) handleWebsocketInterrupt(turn websocketTurn) bool {
	var envelope struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(turn.body, &envelope) != nil || envelope.Type != "response.interrupt" {
		return false
	}
	value, err := inspectWebsocketInterrupt(turn.body)
	if err != nil {
		s.emitReason(turn.lane, reasonInvalidInterruptRequest)
		return true
	}
	snapshot := s.handler.manager.Current()
	key, authorized := s.authorized(snapshot)
	if !authorized {
		s.emitReason(value.lane, reasonInvalidAccessKey)
		s.cancel()
		return true
	}
	s.mu.Lock()
	binding := s.binding
	parent, found := s.parents[value.responseID]
	s.mu.Unlock()
	if !found || binding == nil || parent.lane != value.lane {
		s.emitReason(value.lane, reasonInterruptResponseNotFound)
		return true
	}
	current, exists := s.handler.registry.CredentialRef(binding.ref.ID)
	_, ready := s.handler.registry.ActiveEncryptedCredentialDataIfMatch(current)
	group, groupExists := snapshot.Groups[binding.ref.GroupID]
	_, groupAllowed := key.Filters.Groups[binding.ref.GroupID]
	_, protocolAllowed := key.Filters.Protocols[protocol.OpenAIResponses]
	if !exists || !ready || !sameWebsocketIdentity(binding.ref, current) || !groupExists || !group.ResponsesWebsocketEnabled || string(group.ChannelID) != binding.channel || string(group.ResolvedTarget.TargetConfig) != binding.target || (len(key.Filters.Groups) > 0 && !groupAllowed) || (len(key.Filters.Protocols) > 0 && !protocolAllowed) {
		s.emitReason(value.lane, reasonConfigurationChanged)
		s.cancel()
		return true
	}
	if parent.terminal || parent.complete || parent.interruptSent {
		return true
	}
	interrupter, ok := binding.session.(execution.WebsocketInterrupter)
	if !ok {
		s.emitReason(value.lane, reasonWebsocketInterruptUnsupported)
		return true
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	err = interrupter.Interrupt(ctx, value.responseID, value.lane)
	cancel()
	if err != nil {
		// Completion may win the race with the control write. Its real terminal
		// event already satisfies a late interrupt; never send it to a new turn.
		s.mu.Lock()
		latest := s.parents[value.responseID]
		s.mu.Unlock()
		if latest.terminal || latest.complete {
			return true
		}
		s.emitReason(value.lane, reasonUpstreamInterruptFailed)
		return true
	}
	s.mu.Lock()
	parent = s.parents[value.responseID]
	parent.interruptSent = true
	s.parents[value.responseID] = parent
	s.mu.Unlock()
	return true
}
