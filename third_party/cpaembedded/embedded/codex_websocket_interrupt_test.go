package embedded

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestCodexWSInterruptDuringTurnAndReuse(t *testing.T) {
	t.Run("unsupported", func(t *testing.T) { testCodexWSInterrupt(t, false) })
	t.Run("accepted_before_output", func(t *testing.T) { testCodexWSInterrupt(t, true) })
}

func testCodexWSInterrupt(t *testing.T, accepted bool) {
	var connections atomic.Int32
	var controlResults atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		connections.Add(1)
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		if _, _, err = conn.ReadMessage(); err != nil {
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.created","response":{"id":"resp_interrupt","object":"response","status":"in_progress"}}`))
		var control map[string]string
		if err = conn.ReadJSON(&control); err != nil {
			t.Error(err)
			return
		}
		if control["type"] != "response.interrupt" || control["response_id"] != "resp_interrupt" || control["mode"] != "discard_partial_items" {
			t.Errorf("wrong control: %v", control)
			return
		}
		if accepted {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.interrupt.accepted","response_id":"resp_interrupt"}`))
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.incomplete","response":{"id":"resp_interrupt","object":"response","status":"incomplete","output":[],"incomplete_details":{"reason":"interrupted"},"usage":{"input_tokens":2,"output_tokens":0,"total_tokens":2}}}`))
		} else {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.interrupt.failed","response_id":"resp_interrupt","error":{"code":"interrupt_not_supported"}}`))
			_ = conn.WriteMessage(websocket.TextMessage, wsCompleted("resp_interrupt"))
		}
		if _, _, err = conn.ReadMessage(); err != nil {
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, wsCompleted("resp_next"))
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()
	session := wsTestSession(t, server.URL)
	created := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		result, err := session.ExecuteTurn(t.Context(), json.RawMessage(`{"model":"gpt-5","input":"test"}`), func(_ context.Context, body json.RawMessage) error {
			var e struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(body, &e)
			if e.Type == "response.created" {
				close(created)
			}
			if e.Type == "response.interrupt.failed" || e.Type == "response.interrupt.accepted" {
				controlResults.Add(1)
			}
			return nil
		})
		status := "completed"
		if accepted {
			status = "incomplete"
		}
		if err == nil && (result.Status != status || result.ResponseID != "resp_interrupt" || len(result.Usage) == 0) {
			t.Error("lost upstream completion/usage")
		}
		done <- err
	}()
	select {
	case <-created:
	case <-time.After(3 * time.Second):
		t.Fatal("no created event")
	}
	if err := session.Interrupt(t.Context(), "different_response"); err == nil {
		t.Fatal("accepted foreign response")
	}
	if err := session.Interrupt(t.Context(), "resp_interrupt"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("interrupt did not finish")
	}
	result, err := session.ExecuteTurn(t.Context(), json.RawMessage(`{"model":"gpt-5","input":"next","previous_response_id":"resp_interrupt"}`), nil)
	if err != nil || result.ResponseID != "resp_next" || connections.Load() != 1 || controlResults.Load() != 1 {
		t.Fatalf("session not reusable: %v connections=%d", err, connections.Load())
	}
}
