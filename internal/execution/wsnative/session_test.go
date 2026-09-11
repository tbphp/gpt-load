package wsnative

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"gpt-load/internal/execution"
	"gpt-load/internal/outboundproxy"
)

func nativeTestProxy() outboundproxy.Effective {
	return outboundproxy.Effective{Config: outboundproxy.Config{Mode: outboundproxy.ModeDirect}, Source: outboundproxy.SourceDefault}
}

func TestSessionMultiplexesTurnsOnOneConnection(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Error("upstream authentication missing")
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		connections.Add(1)
		for i := 0; i < 2; i++ {
			_, body, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req map[string]any
			if json.Unmarshal(body, &req) != nil || req["type"] != "response.create" {
				t.Error("not a native create")
			}
		}
		for _, kind := range []string{"response.created", "response.completed"} {
			for _, lane := range []string{"first", "second"} {
				body := fmt.Sprintf(`{"type":%q,"stream_id":%q,"response":{"id":%q,"object":"response","status":"completed"}}`, kind, lane, "resp_"+lane)
				if err := conn.WriteMessage(websocket.TextMessage, []byte(body)); err != nil {
					return
				}
			}
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	session, result := Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), http.Header{"Authorization": {"Bearer fixture-key"}}, nativeTestProxy())
	if result.Error != nil || session == nil {
		t.Fatalf("dial=%+v", result)
	}
	defer session.Close()
	results := make(chan execution.WebsocketResult, 2)
	for _, lane := range []string{"first", "second"} {
		go func() {
			events := 0
			result := session.ExecuteTurn(ctx, []byte(fmt.Sprintf(`{"model":"test","stream_id":%q,"input":"hello"}`, lane)), func(_ context.Context, body []byte) error {
				var event struct {
					StreamID string `json:"stream_id"`
				}
				if json.Unmarshal(body, &event) != nil || event.StreamID != lane {
					t.Error("event delivered to wrong lane")
				}
				events++
				return nil
			})
			if events != 2 {
				t.Errorf("%s received %d events", lane, events)
			}
			results <- result
		}()
	}
	for i := 0; i < 2; i++ {
		result := <-results
		if result.Error != nil || result.DispatchState != execution.DispatchMaybeSent {
			t.Fatalf("turn=%+v", result)
		}
	}
	if connections.Load() != 1 {
		t.Fatal("multiplexing opened another connection")
	}
}

func TestHandshakeFailureDoesNotSendBusinessOrFallback(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet {
			t.Error("unexpected HTTP fallback")
		}
		w.WriteHeader(http.StatusUpgradeRequired)
	}))
	defer server.Close()
	session, result := Dial(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"), nil, nativeTestProxy())
	if session != nil || result.Error == nil || result.Error.StatusCode != 426 || result.DispatchState != execution.DispatchNotSent || calls.Load() != 1 {
		t.Fatalf("handshake result=%+v calls=%d", result, calls.Load())
	}
}

func TestSessionRejectsInvalidEventBeforeDelivery(t *testing.T) {
	for _, body := range []string{
		`{"type":"response.completed","stream_id":null,"response":{"id":"resp_1","object":"response","status":"completed"}}`,
		`{"type":"response.completed","response":{"object":"response","status":"completed"}}`,
		`{"type":"response.completed","response":{"id":"resp_1","object":"response","status":"failed"}}`,
		`{"type":"response.completed","response_id":"resp_other","response":{"id":"resp_1","object":"response","status":"completed"}}`,
		"{\"type\":\"response.completed\",\"text\":\"\xff\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\"}}",
	} {
		t.Run(fmt.Sprint(len(body)), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				if _, _, err = conn.ReadMessage(); err == nil {
					_ = conn.WriteMessage(websocket.TextMessage, []byte(body))
				}
				_, _, _ = conn.ReadMessage()
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			s, result := Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil, nativeTestProxy())
			if result.Error != nil {
				t.Fatal(result.Error)
			}
			defer s.Close()
			events := 0
			result = s.ExecuteTurn(ctx, []byte(`{"model":"model","input":"hello"}`), func(context.Context, []byte) error { events++; return nil })
			if result.Error == nil || events != 0 {
				t.Fatalf("invalid event delivered: events=%d result=%+v", events, result)
			}
		})
	}
}
