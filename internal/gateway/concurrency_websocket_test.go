package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
)

func TestConcurrencyWebsocketTurnsAndHotLimits(t *testing.T) {
	for _, layer := range []string{"global", "key", "group"} {
		t.Run(layer, func(t *testing.T) {
			started, finish := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(finish) })
			var calls atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				if _, _, err = conn.ReadMessage(); err != nil {
					return
				}
				n := calls.Add(1)
				if n == 1 {
					close(started)
					<-finish
				}
				_ = conn.WriteMessage(websocket.TextMessage, websocketCompleted(fmt.Sprintf("resp_%d", n), ""))
				_, _, _ = conn.ReadMessage()
			}))
			defer upstream.Close()
			h, engine, input := websocketTestHandler(t, upstream.URL, channel.OpenAI)
			sink := &recordingRequestLogSink{}
			h.requestLogSink = sink
			server := httptest.NewServer(engine)
			defer server.Close()
			first := dialGatewayWebsocket(t, server.URL)
			defer first.Close()
			second := dialGatewayWebsocket(t, server.URL)
			defer second.Close()
			defer unblock()
			if h.manager.Concurrency().Snapshot().Global != 0 {
				t.Fatal("idle socket counted as a request")
			}
			payload := []byte(`{"type":"response.create","model":"public","input":"hello"}`)
			if err := first.WriteMessage(websocket.TextMessage, payload); err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("first turn did not start")
			}
			counts := h.manager.Concurrency().Snapshot()
			if counts.Global != 1 || counts.Groups[1] != 1 {
				t.Fatalf("active turn not counted: %+v", counts)
			}
			input.SystemSettings = config.Settings{}
			switch layer {
			case "global":
				input.SystemSettings["global_concurrency_limit"] = 1
			case "key":
				input.SystemSettings["default_access_key_concurrency_limit"] = 1
			case "group":
				input.Groups[0].Settings = config.Settings{"concurrency_limit": 1}
			}
			if _, err := h.manager.Publish(input); err != nil {
				t.Fatal(err)
			}
			if err := second.WriteMessage(websocket.TextMessage, payload); err != nil {
				t.Fatal(err)
			}
			_ = second.SetReadDeadline(time.Now().Add(3 * time.Second))
			var event struct {
				Type  string
				Error struct{ Code string }
			}
			if err := second.ReadJSON(&event); err != nil {
				t.Fatal(err)
			}
			if event.Type != "error" || event.Error.Code != "concurrency_limit_exceeded" || calls.Load() != 1 {
				t.Fatalf("old connection ignored new capacity: %+v", event)
			}
			unblock()
			_ = first.SetReadDeadline(time.Now().Add(3 * time.Second))
			if _, _, err := first.ReadMessage(); err != nil {
				t.Fatal(err)
			}
			waitWebsocketLogs(t, sink, 2)
			if h.manager.Concurrency().Snapshot().Global != 0 {
				t.Fatal("finished turn retained capacity")
			}
			if err := second.WriteMessage(websocket.TextMessage, payload); err != nil {
				t.Fatal(err)
			}
			if _, _, err := second.ReadMessage(); err != nil {
				t.Fatalf("capacity rejection broke connection: %v", err)
			}
			waitWebsocketLogs(t, sink, 3)
		})
	}
}

func TestConcurrencyLiveTransferAndFailedHangup(t *testing.T) {
	fake := &liveFakeOpener{}
	h, engine, _, _, _ := liveGatewayFixture(t, fake)
	setLiveGroupMode(h, 1, state.CodexLiveDirect)
	h.manager.Current().Settings.GlobalConcurrencyLimit = 1
	server := httptest.NewServer(engine)
	defer server.Close()
	defer h.CloseCodexLive()
	_, offer := liveClientOffer(t)
	first := liveRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/live", "gl-client", "application/sdp", []byte(offer))
	if first.StatusCode != 201 {
		t.Fatalf("create returned %d", first.StatusCode)
	}
	if s := h.manager.Concurrency().Snapshot(); s.Global != 1 || s.AccessKeys[1] != 1 || s.Groups[1] != 1 {
		t.Fatalf("handler returned live capacity: %+v", s)
	}
	second := liveRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/live", "gl-client", "application/sdp", []byte(offer))
	if second.StatusCode != 429 {
		t.Fatalf("second call returned %d", second.StatusCode)
	}
	call, ok := h.liveSessions.lookup("rtc_1", 1)
	if !ok {
		t.Fatal("missing call")
	}
	fake.sessions[0].mu.Lock()
	fake.sessions[0].hangupErr = errors.New("temporary hangup failure")
	fake.sessions[0].mu.Unlock()
	for i := 0; i < 3; i++ {
		if err := h.liveSessions.finishCall(call, "client_hangup"); err == nil {
			t.Fatal("expected failed hangup")
		}
		want := int64(1)
		if i == 2 {
			want = 0
		}
		if counts := h.manager.Concurrency().Snapshot(); counts.Global != want || counts.Groups[1] != want {
			t.Fatalf("hangup %d released incorrectly: %+v", i, counts)
		}
	}
}

func TestConcurrencyLiveTerminationDuringResponseWrite(t *testing.T) {
	fake := &liveFakeOpener{}
	h, engine, _, _, _ := liveGatewayFixture(t, fake)
	setLiveGroupMode(h, 1, state.CodexLiveDirect)
	defer h.CloseCodexLive()
	_, offer := liveClientOffer(t)
	request := httptest.NewRequest(http.MethodPost, "/v1/live", strings.NewReader(offer))
	request.Header.Set("Authorization", "Bearer gl-client")
	request.Header.Set("Content-Type", "application/sdp")
	writer := &concurrencySlowErrorWriter{httptest.NewRecorder(), make(chan struct{}), make(chan struct{})}
	done := make(chan struct{})
	go func() { defer close(done); engine.ServeHTTP(writer, request) }()
	<-writer.started
	call, found := h.liveSessions.lookup("rtc_1", 1)
	if !found {
		close(writer.finish)
		<-done
		t.Fatal("response preceded session registration")
	}
	during := h.manager.Concurrency().Snapshot().Global
	err := h.liveSessions.finishCall(call, "client_hangup")
	close(writer.finish)
	<-done
	if err != nil || during != 1 {
		t.Fatalf("transfer state: current=%d error=%v", during, err)
	}
	if c := h.manager.Concurrency().Snapshot(); c.Global != 0 || len(c.Groups) != 0 || len(c.AccessKeys) != 0 {
		t.Fatalf("overlapping close leaked or double-released: %+v", c)
	}
}

func TestConcurrencyMistralRealtimeHoldsSession(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		calls.Add(1)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	h, engine, input := websocketTestHandler(t, upstream.URL+"/v1", channel.Mistral)
	input.Groups[0].Settings = config.Settings{"concurrency_limit": 1}
	if _, err := h.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	sink := &recordingRequestLogSink{}
	h.requestLogSink = sink
	server := httptest.NewServer(engine)
	defer server.Close()
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/audio/transcriptions/realtime?model=public"
	headers := http.Header{"Authorization": {"Bearer gl-client"}}
	conn, _, err := websocket.DefaultDialer.Dial(endpoint, headers)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if s := h.manager.Concurrency().Snapshot(); s.Global != 1 || s.Groups[1] != 1 {
		t.Fatalf("realtime session not counted: %+v", s)
	}
	second, response, err := websocket.DefaultDialer.Dial(endpoint, headers)
	if second != nil {
		second.Close()
	}
	if response != nil {
		defer response.Body.Close()
	}
	if err == nil || response == nil || response.StatusCode != 429 {
		t.Fatal("realtime group capacity not enforced")
	}
	conn.Close()
	waitWebsocketLogs(t, sink, 2)
	if s := h.manager.Concurrency().Snapshot(); s.Global != 0 || len(s.Groups) != 0 {
		t.Fatalf("realtime cleanup leaked: %+v", s)
	}
	if calls.Load() != 1 {
		t.Fatal("capacity rejection dialed another upstream")
	}
}

func TestConcurrencyWebsocketMultiplexRejectsOnlyNewTurn(t *testing.T) {
	started, finish := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(finish) })
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err = conn.ReadMessage(); err != nil {
			return
		}
		close(started)
		<-finish
		_ = conn.WriteMessage(websocket.TextMessage, websocketCompleted("resp_first", "first"))
		_, _, _ = conn.ReadMessage()
	}))
	defer upstream.Close()
	h, engine, input := websocketTestHandler(t, upstream.URL, channel.GPTLoad)
	input.Groups[0].Settings = config.Settings{"concurrency_limit": 1}
	if _, err := h.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	sink := &recordingRequestLogSink{}
	h.requestLogSink = sink
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	defer unblock()
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","stream_id":"first","model":"public","input":"first"}`))
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("multiplex first turn did not start")
	}
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","stream_id":"second","model":"public","input":"second"}`))
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var event struct {
		Type     string
		StreamID string `json:"stream_id"`
		Error    struct{ Code string }
	}
	if err := conn.ReadJSON(&event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "error" || event.StreamID != "second" || event.Error.Code != "concurrency_limit_exceeded" {
		t.Fatalf("wrong turn rejected: %+v", event)
	}
	waitWebsocketLogs(t, sink, 1)
	if s := h.manager.Concurrency().Snapshot(); s.Global != 1 || s.Groups[1] != 1 {
		t.Fatalf("rejection ended active turn: %+v", s)
	}
	unblock()
	if err := conn.ReadJSON(&event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "response.completed" || event.StreamID != "first" {
		t.Fatalf("active turn corrupted: %+v", event)
	}
	waitWebsocketLogs(t, sink, 2)
}
