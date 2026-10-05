package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"gpt-load/internal/channel"
	"gpt-load/internal/state"
)

func TestWebsocketPriorityRecoveryKeepsBoundCredential(t *testing.T) {
	var ids atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, websocketCompleted(fmt.Sprintf("priority-%d", ids.Add(1)), "")); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	handler, engine, input := websocketTestHandler(t, upstream.URL, channel.OpenAI)
	registry := handler.registry.(*state.CredentialRegistry)
	backup := input.Groups[0]
	backup.ID, backup.Name, backup.Priority = 2, "preferred", 100
	input.Groups = append(input.Groups, backup)
	input.Credentials = append(input.Credentials, testCredentialConfig(2, 2))
	if err := registry.ApplyCredentialImport(2, []state.CredentialEntry{testCredentialEntry(t, handler.encryption, 2, 2, "second-key")}); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour)
	registry.SetCooldown(2, until)
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	server := httptest.NewServer(engine)
	defer server.Close()
	send := func(conn *websocket.Conn, count int, want uint) {
		t.Helper()
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","input":"hello","prompt_cache_key":"priority-session"}`)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatal(err)
		}
		events := waitWebsocketLogs(t, sink, count)
		if len(events[count-1].Attempts) != 1 || events[count-1].Attempts[0].CredentialID != want {
			t.Fatalf("turn %d did not use credential %d", count, want)
		}
	}
	bound := dialGatewayWebsocket(t, server.URL)
	defer bound.Close()
	send(bound, 1, 1)
	registry.ClearCooldownIfMatch(2, until)
	send(bound, 2, 1)
	fresh := dialGatewayWebsocket(t, server.URL)
	defer fresh.Close()
	send(fresh, 3, 2)
}
