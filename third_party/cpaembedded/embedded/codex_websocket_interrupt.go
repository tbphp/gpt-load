package embedded

import (
	"context"
	"encoding/json"
)

func (s *CodexWSSession) Interrupt(ctx context.Context, responseID string) error {
	if s == nil {
		return codexWSError("session_closed")
	}
	s.mu.Lock()
	valid := !s.closed && s.running && s.activeResponseID != "" && s.activeResponseID == responseID
	s.mu.Unlock()
	if !valid {
		return codexWSError("interrupt_response_inactive")
	}
	payload, _ := json.Marshal(map[string]string{"type": "response.interrupt", "response_id": responseID, "mode": "discard_partial_items"})
	if err := s.inner.InterruptExecutionSession(ctx, s.id, payload); err != nil {
		// Delivery may be uncertain; do not reconnect or replay the control frame.
		return codexWSError("interrupt_send_failed")
	}
	return nil
}
