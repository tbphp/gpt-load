package embedded

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestCodexWSSessionInterruptAndContinue(t *testing.T) {
	for _, scenario := range []struct {
		eventType    string
		outputTokens int
	}{
		{eventType: "response.incomplete", outputTokens: 7},
		{eventType: "response.done", outputTokens: 0},
		{eventType: "response.done", outputTokens: 7},
	} {
		t.Run(fmt.Sprintf("%s/output_tokens_%d", scenario.eventType, scenario.outputTokens), func(t *testing.T) {
			testCodexWSSessionInterruptAndContinue(t, scenario.eventType, scenario.outputTokens)
		})
	}
}

func TestCodexWSSessionInterruptBlockedAfterTurnCompletes(t *testing.T) {
	interrupt, err := json.Marshal(map[string]string{
		"type": "response.interrupt", "response_id": "resp_blocked", "extension": strings.Repeat("x", 9<<20),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"caller_canceled", "caller_deadline", "turn_deadline"} {
		t.Run(scenario, func(t *testing.T) {
			interruptStarted := make(chan struct{})
			finishTurn := make(chan struct{})
			release := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				if err := conn.UnderlyingConn().(*net.TCPConn).SetReadBuffer(1024); err != nil {
					t.Error(err)
					return
				}
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.created","response":{"id":"resp_blocked","object":"response","status":"in_progress"}}`)); err != nil {
					return
				}
				_, reader, err := conn.NextReader()
				if err != nil {
					return
				}
				// 只读取控制帧的第一个字节，让发送端确实进入写入后停止消费。
				if _, err := io.ReadFull(reader, make([]byte, 1)); err != nil {
					return
				}
				close(interruptStarted)
				select {
				case <-finishTurn:
				case <-release:
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, wsCompleted("resp_blocked")); err != nil {
					return
				}
				<-release
			}))
			defer upstream.Close()
			defer close(release)
			session := wsTestSession(t, upstream.URL)
			defer session.Close()
			turnTimeout := 10 * time.Second
			if scenario == "turn_deadline" {
				turnTimeout = 3 * time.Second
			}
			turnCtx, cancelTurn := context.WithTimeout(t.Context(), turnTimeout)
			defer cancelTurn()
			turnCtx = httptrace.WithClientTrace(turnCtx, &httptrace.ClientTrace{
				GotConn: func(info httptrace.GotConnInfo) {
					if err := info.Conn.(*net.TCPConn).SetWriteBuffer(1024); err != nil {
						t.Error(err)
					}
				},
			})
			created := make(chan struct{})
			turnDone := make(chan error, 1)
			go func() {
				_, err := session.ExecuteTurn(turnCtx, json.RawMessage(`{"model":"gpt-5","input":"question"}`), func(_ context.Context, body json.RawMessage) error {
					var event struct{ Type string }
					if json.Unmarshal(body, &event) == nil && event.Type == "response.created" {
						close(created)
					}
					return nil
				})
				turnDone <- err
			}()
			select {
			case <-created:
			case <-time.After(2 * time.Second):
				t.Fatal("response did not start")
			}
			writeCtx, cancelWrite := context.WithCancel(t.Context())
			defer cancelWrite()
			if scenario == "caller_deadline" {
				var cancelDeadline context.CancelFunc
				writeCtx, cancelDeadline = context.WithTimeout(writeCtx, 3*time.Second)
				defer cancelDeadline()
			}
			interruptDone := make(chan error, 1)
			go func() { interruptDone <- session.Interrupt(writeCtx, interrupt) }()
			select {
			case <-interruptStarted:
			case err := <-interruptDone:
				t.Fatalf("interrupt returned before upstream received it: %v", err)
			case <-time.After(2 * time.Second):
				t.Fatal("interrupt did not start writing")
			}
			close(finishTurn)
			select {
			case err := <-turnDone:
				if err != nil {
					t.Fatalf("turn did not complete before the blocked interrupt: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("turn did not complete")
			}
			select {
			case err := <-interruptDone:
				t.Fatalf("interrupt was not blocked after turn completion: %v", err)
			default:
			}
			wantErr := context.DeadlineExceeded
			if scenario == "caller_canceled" {
				cancelWrite()
				wantErr = context.Canceled
			}
			select {
			case err := <-interruptDone:
				if !errors.Is(err, wantErr) {
					t.Fatalf("interrupt error = %v, want %v", err, wantErr)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("interrupt write stayed blocked after cancellation or deadline")
			}
			select {
			case <-session.Done():
			default:
				t.Fatal("blocked interrupt did not close its connection")
			}
		})
	}
}

func testCodexWSSessionInterruptAndContinue(t *testing.T, eventType string, outputTokens int) {
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
		_ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":%q,"response":{"id":"resp_1","object":"response","status":"incomplete","incomplete_details":{"reason":"interrupted"},"usage":{"input_tokens":5,"output_tokens":%d},"output":[]}}`, eventType, outputTokens)))
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
	var usage struct {
		OutputTokens *int `json:"output_tokens"`
	}
	if json.Unmarshal(first.Usage, &usage) != nil || usage.OutputTokens == nil || *usage.OutputTokens != outputTokens {
		t.Fatalf("interrupted usage=%s, want output_tokens=%d", first.Usage, outputTokens)
	}
	if err := sender.Interrupt(t.Context(), interrupt); err == nil {
		t.Fatal("completed turn accepted another interrupt")
	}
	second, err := session.ExecuteTurn(t.Context(), json.RawMessage(`{"model":"gpt-5","previous_response_id":"resp_1","input":"answer","store":false}`), nil)
	if err != nil || second.ResponseID != "resp_2" || connections.Load() != 1 {
		t.Fatalf("continuation=%+v err=%v connections=%d", second, err, connections.Load())
	}
}
