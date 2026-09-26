package gateway

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"gpt-load/internal/state"
)

func TestCodexLiveSlowControlReaderDisconnects(t *testing.T) {
	upstreamClosed := make(chan struct{})
	start := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		go func() {
			defer close(upstreamClosed)
			_, _, _ = conn.ReadMessage()
		}()
		<-start
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		payload := bytes.Repeat([]byte("x"), 128<<10)
		for range 256 {
			if conn.WriteMessage(websocket.TextMessage, payload) != nil {
				return
			}
		}
		<-upstreamClosed
	}))
	defer upstream.Close()
	fake := &liveFakeOpener{wsURL: "ws" + strings.TrimPrefix(upstream.URL, "http")}
	handler, engine, _, _, _ := liveGatewayFixture(t, fake)
	setLiveGroupMode(handler, 1, state.CodexLiveDirect)
	handler.writeTimeout = 50 * time.Millisecond
	server := httptest.NewServer(engine)
	defer server.Close()
	_, offer := liveClientOffer(t)
	response := liveRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/live", "gl-client", "application/sdp", []byte(offer))
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+response.Header.Get("Location"), http.Header{"Authorization": {"Bearer gl-client"}})
	if err != nil {
		close(start)
		t.Fatal(err)
	}
	defer conn.Close()
	if tcp, ok := conn.UnderlyingConn().(*net.TCPConn); ok {
		if err := tcp.SetReadBuffer(1024); err != nil {
			t.Fatal(err)
		}
	}
	close(start)
	select {
	case <-upstreamClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("control writer stayed blocked on a client that stopped reading")
	}
}
