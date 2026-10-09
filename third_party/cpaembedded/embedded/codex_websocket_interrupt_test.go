package embedded

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestCodexWSSessionInterruptAndContinue(t *testing.T) {
	for _, eventType := range []string{"response.incomplete", "response.done"} {
		t.Run(eventType, func(t *testing.T) { testCodexWSSessionInterruptAndContinue(t, eventType) })
	}
}

func testCodexWSSessionInterruptAndContinue(t *testing.T, eventType string) {
	interrupt := []byte(`{"type":"response.interrupt","response_id":"resp_1","mode":"discard_partial_items","extension":"keep"}`)
	var connections atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		connections.Add(1)
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, _, err = conn.ReadMessage(); err != nil {
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.created","response":{"id":"resp_1","object":"response","status":"in_progress"}}`))
		_, body, err := conn.ReadMessage()
		if err != nil || !bytes.Equal(body, interrupt) {
			t.Errorf("interrupt=%s err=%v", body, err)
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":%q,"response":{"id":"resp_1","object":"response","status":"incomplete","incomplete_details":{"reason":"interrupted"},"usage":{"input_tokens":5,"output_tokens":7},"output":[]}}`, eventType)))
		_, body, err = conn.ReadMessage()
		var request struct {
			Previous string `json:"previous_response_id"`
		}
		if err != nil || json.Unmarshal(body, &request) != nil || request.Previous != "resp_1" {
			t.Errorf("follow-up=%s err=%v", body, err)
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, wsCompleted("resp_2"))
		_, _, _ = conn.ReadMessage()
	}))
	defer upstream.Close()
	session := wsTestSession(t, upstream.URL)
	defer session.Close()
	sender, ok := any(session).(interface {
		Interrupt(context.Context, []byte) error
	})
	if !ok {
		t.Fatal("Codex session cannot interrupt an active response")
	}
	if err := sender.Interrupt(t.Context(), interrupt); err == nil {
		t.Fatal("idle interrupt was accepted")
	}
	created := make(chan struct{})
	finished := make(chan struct{})
	var first CodexWSTurnResult
	var firstErr error
	go func() {
		defer close(finished)
		first, firstErr = session.ExecuteTurn(t.Context(), json.RawMessage(`{"model":"gpt-5","input":"question","store":false}`), func(_ context.Context, body json.RawMessage) error {
			var event struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(body, &event)
			if event.Type == "response.created" {
				close(created)
			}
			return nil
		})
	}()
	select {
	case <-created:
	case <-time.After(3 * time.Second):
		t.Fatal("first response did not start")
	}
	if err := sender.Interrupt(t.Context(), []byte(`{"type":"response.interrupt","response_id":"another_response"}`)); err == nil {
		t.Fatal("interrupt for another response was accepted")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := sender.Interrupt(canceled, interrupt); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled interrupt error=%v", err)
	}
	if err := sender.Interrupt(t.Context(), interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("interrupted turn did not finish")
	}
	if firstErr != nil || first.Status != "incomplete" || first.ResponseID != "resp_1" || !json.Valid(first.Usage) {
		t.Fatalf("interrupted result=%+v err=%v", first, firstErr)
	}
	if err := sender.Interrupt(t.Context(), interrupt); err == nil {
		t.Fatal("completed turn accepted another interrupt")
	}
	second, err := session.ExecuteTurn(t.Context(), json.RawMessage(`{"model":"gpt-5","previous_response_id":"resp_1","input":"answer","store":false}`), nil)
	if err != nil || second.ResponseID != "resp_2" || connections.Load() != 1 {
		t.Fatalf("continuation=%+v err=%v connections=%d", second, err, connections.Load())
	}
}
