package wsnative

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gorilla/websocket"

	"gpt-load/internal/execution"
)

func (s *session) Interrupt(ctx context.Context, responseID, lane string) error {
	if responseID == "" || len(responseID) > 4096 {
		return errors.New("invalid interrupt response")
	}
	select {
	case s.write <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return errors.New("websocket interrupt session closed")
	}
	defer func() { <-s.write }()
	s.mu.Lock()
	active := s.turns[lane] != nil && s.failure == nil
	s.mu.Unlock()
	if !active {
		return errors.New("websocket interrupt response inactive")
	}
	fields := map[string]string{"type": "response.interrupt", "response_id": responseID, "mode": "discard_partial_items"}
	if lane != "" {
		fields["stream_id"] = lane
	}
	body, _ := json.Marshal(fields)
	deadline := time.Now().Add(5 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	defer s.conn.SetWriteDeadline(time.Time{})
	if err := s.conn.WriteMessage(websocket.TextMessage, body); err != nil {
		s.fail(failure(execution.ErrorKindTransport, "websocket_send_failed", 0))
		return err
	}
	return nil
}
