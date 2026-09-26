package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	logtest "github.com/sirupsen/logrus/hooks/test"

	"gpt-load/internal/state"
	"gpt-load/internal/subscription/providers/codex"
	"gpt-load/internal/telemetry"
)

func TestCodexLiveFailedDirectHangupCanBeRetried(t *testing.T) {
	fake := &liveFakeOpener{}
	handler, engine, sink, _, _ := liveGatewayFixture(t, fake)
	setLiveGroupMode(handler, 1, state.CodexLiveDirect)
	server := httptest.NewServer(engine)
	defer server.Close()
	client, offer := liveClientOffer(t)
	created := liveRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/realtime/calls", "gl-client", "application/sdp", []byte(offer))
	if created.StatusCode != http.StatusCreated {
		t.Fatal(created.StatusCode)
	}
	body, err := io.ReadAll(created.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: string(body)}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for client.ConnectionState() != webrtc.PeerConnectionStateConnected && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if client.ConnectionState() != webrtc.PeerConnectionStateConnected {
		t.Fatal("media did not connect")
	}
	session := fake.sessions[0]
	session.mu.Lock()
	session.hangupErr = errors.New("temporary upstream failure")
	session.mu.Unlock()
	failed := liveRequest(t, server.Client(), http.MethodPost, server.URL+created.Header.Get("Location")+"/hangup", "gl-client", "", nil)
	if failed.StatusCode != http.StatusBadGateway {
		t.Fatalf("failed upstream hangup returned HTTP %d", failed.StatusCode)
	}
	call, found := handler.liveSessions.lookup("rtc_1", 1)
	if !found {
		t.Fatal("pending direct session was discarded")
	}
	if client.ConnectionState() != webrtc.PeerConnectionStateConnected {
		t.Fatal("test did not preserve direct media")
	}
	if _, claimed := handler.liveSessions.claim("rtc_1", 1); claimed {
		t.Fatal("terminating session accepted control connection")
	}
	handler.liveSessions.setMaximum(1)
	if handler.liveSessions.reserve() {
		handler.liveSessions.release()
		t.Fatal("pending hangup released its session slot")
	}
	if events := sink.snapshot(); len(events) != 1 || events[0].Status != telemetry.RequestStatusIncomplete || events[0].ErrorCode != "hangup_failed" {
		t.Fatalf("unconfirmed hangup must be visible: %+v", events)
	}
	session.mu.Lock()
	session.hangupErr = nil
	session.mu.Unlock()
	retried := liveRequest(t, server.Client(), http.MethodPost, server.URL+created.Header.Get("Location")+"/hangup", "gl-client", "", nil)
	if retried.StatusCode != http.StatusNoContent {
		t.Fatalf("retry returned %d", retried.StatusCode)
	}
	if _, found := handler.liveSessions.lookup(call.id, 1); found {
		t.Fatal("confirmed hangup still tracked")
	}
	events := waitWebsocketLogs(t, sink, 1)
	if len(events) != 1 || events[0].Status != telemetry.RequestStatusIncomplete {
		t.Fatalf("events = %+v", events)
	}
	session.mu.Lock()
	attempts := session.hangup
	session.mu.Unlock()
	if attempts != 2 {
		t.Fatalf("hangup attempts = %d", attempts)
	}
}

func TestCodexLiveHangupDiagnosticsDoNotExposeTransportSecrets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		kind   string
		status int
	}{
		{"HTTP failure", &codex.LiveHTTPError{Status: http.StatusNotFound}, "http", http.StatusNotFound},
		{"timeout", fmt.Errorf("secret-proxy-password: %w", context.DeadlineExceeded), "timeout", 0},
		{"transport", errors.New("secret-proxy-password"), "transport", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logger, hook := logtest.NewNullLogger()
			call := &liveCallSession{logger: logger, requestID: "request-diagnostic", hangupAttempts: 1}
			call.logHangupFailure(tc.err, true)
			entry := hook.LastEntry()
			if entry == nil || entry.Data["failure_kind"] != tc.kind || entry.Data["request_id"] != call.requestID {
				t.Fatalf("missing diagnostic fields: %+v", entry)
			}
			if tc.status != 0 && entry.Data["upstream_status"] != tc.status {
				t.Fatalf("missing HTTP status: %+v", entry.Data)
			}
			if _, ok := entry.Data["error"]; ok {
				t.Fatal("raw transport error leaked")
			}
		})
	}
}

func TestCodexLiveRevokedDirectSessionRetriesHangup(t *testing.T) {
	fake := &liveFakeOpener{}
	handler, engine, sink, _, publishKey := liveGatewayFixture(t, fake)
	setLiveGroupMode(handler, 1, state.CodexLiveDirect)
	server := httptest.NewServer(engine)
	defer server.Close()
	_, offer := liveClientOffer(t)
	created := liveRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/realtime/calls", "gl-client", "application/sdp", []byte(offer))
	if created.StatusCode != http.StatusCreated {
		t.Fatal(created.StatusCode)
	}
	session := fake.sessions[0]
	session.mu.Lock()
	session.hangupErr = errors.New("temporary upstream failure")
	session.mu.Unlock()
	publishKey("gl-rotated")
	deadline := time.Now().Add(5 * time.Second)
	var call *liveCallSession
	for time.Now().Before(deadline) {
		handler.liveSessions.mu.Lock()
		call = handler.liveSessions.calls["rtc_1"]
		pending := call != nil && call.terminating && call.timer != nil
		handler.liveSessions.mu.Unlock()
		if pending {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	handler.liveSessions.mu.Lock()
	pending := call != nil && call.terminating && call.timer != nil
	handler.liveSessions.mu.Unlock()
	if !pending {
		t.Fatal("revoked call did not retain its failed hangup")
	}
	session.mu.Lock()
	session.hangupErr = nil
	session.mu.Unlock()
	handler.liveSessions.mu.Lock()
	call.timer.Reset(0)
	handler.liveSessions.mu.Unlock()
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, found := handler.liveSessions.lookup("rtc_1", 1); !found {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	events := sink.snapshot()
	if len(events) != 1 || events[0].Status != telemetry.RequestStatusIncomplete || events[0].ErrorCode != "hangup_failed" {
		t.Fatalf("events = %+v", events)
	}
	if _, found := handler.liveSessions.lookup("rtc_1", 1); found {
		t.Fatal("successful automatic retry did not clear session")
	}
}
