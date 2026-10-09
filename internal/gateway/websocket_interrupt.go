package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/execution"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/protocol"
)

type websocketInterrupt struct{ responseID, lane string }

func inspectWebsocketInterrupt(body []byte) (websocketInterrupt, error) {
	var value websocketInterrupt
	if len(body) > 8192 {
		return value, errors.New("invalid interrupt")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return value, errors.New("invalid interrupt")
	}
	fields := make(map[string]string)
	for decoder.More() {
		token, err = decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok {
			return value, errors.New("invalid interrupt")
		}
		if _, duplicate := fields[name]; duplicate {
			return value, errors.New("invalid interrupt")
		}
		switch name {
		case "type", "response_id", "mode", "stream_id":
		default:
			return value, errors.New("invalid interrupt")
		}
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return value, errors.New("invalid interrupt")
		}
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return value, errors.New("invalid interrupt")
		}
		fields[name] = text
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return value, errors.New("invalid interrupt")
	}
	if decoder.Decode(new(json.RawMessage)) != io.EOF {
		return value, errors.New("invalid interrupt")
	}
	if fields["type"] != "response.interrupt" || fields["mode"] != "discard_partial_items" || fields["response_id"] == "" || len(fields["response_id"]) > 4096 {
		return value, errors.New("invalid interrupt")
	}
	if lane, exists := fields["stream_id"]; exists && !validWebsocketLane(lane) {
		return value, errors.New("invalid interrupt")
	}
	value.responseID, value.lane = fields["response_id"], fields["stream_id"]
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
		s.logWebsocketInterrupt("already_finished_or_sent")
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
			s.logWebsocketInterrupt("already_finished_or_sent")
			return true
		}
		s.logWebsocketInterrupt("send_failed")
		s.emitReason(value.lane, reasonUpstreamInterruptFailed)
		return true
	}
	s.mu.Lock()
	parent = s.parents[value.responseID]
	parent.interruptSent = true
	s.parents[value.responseID] = parent
	s.mu.Unlock()
	s.logWebsocketInterrupt("sent")
	return true
}

func (s *websocketConnection) logWebsocketInterrupt(result string) {
	utils.LogPlaneBestEffort(s.handler.logger, logrus.InfoLevel, utils.LogPlaneData,
		logrus.Fields{"event": "websocket_interrupt", "ak_id": s.keyID, "result": result}, "WebSocket interrupt control processed")
}
