package gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"gpt-load/internal/execution"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
)

func setLiveGroupMode(handler *Handler, id uint, mode state.CodexLiveMode) {
	group := handler.manager.Current().Groups[id]
	group.CodexLiveMode = mode
	handler.manager.Current().Groups[id] = group
}

func TestCodexLiveRetryBudgetAndReplaySafety(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		count, status, attempts int
		kind                    execution.ErrorKind
		safety                  execution.ReplaySafety
	}{
		{"disabled", 0, 403, 1, execution.ErrorKindHTTP, execution.ReplaySafetyRejectedBeforeProcessing},
		{"rejected", 1, 429, 2, execution.ErrorKindHTTP, execution.ReplaySafetyRejectedBeforeProcessing},
		{"ambiguous server error", 2, 503, 1, execution.ErrorKindHTTP, execution.ReplaySafetyUnknown},
		{"unproven server error", 2, 503, 1, execution.ErrorKindHTTP, ""},
		{"transport outcome unknown", 2, 0, 1, execution.ErrorKindTransport, execution.ReplaySafetyUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &liveFakeOpener{failure: &execution.ErrorEvidence{Kind: tc.kind, StatusCode: tc.status, OriginHint: execution.ErrorOriginUpstream, ReplaySafety: tc.safety}}
			handler, engine, sink, _, _ := liveGatewayFixture(t, fake)
			handler.manager.Current().Settings.RetryCount = tc.count
			setLiveGroupMode(handler, 1, state.CodexLiveDirect)
			setLiveGroupMode(handler, 2, state.CodexLiveDirect)
			server := httptest.NewServer(engine)
			defer server.Close()
			_, offer := liveClientOffer(t)
			response := liveRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/live", "gl-client", "application/sdp", []byte(offer))
			if response.StatusCode < 400 {
				t.Fatalf("status = %d", response.StatusCode)
			}
			events := waitWebsocketLogs(t, sink, 1)
			if len(fake.attempted) != tc.attempts || len(events[0].Attempts) != tc.attempts {
				t.Fatalf("attempts = %v, log = %+v", fake.attempted, events[0])
			}
			if events[0].Attempts[tc.attempts-1].WillRetry {
				t.Fatal("last attempt claims a retry that did not occur")
			}
		})
	}
}

func TestCodexLiveMixedGroupModesAndDisabledGroup(t *testing.T) {
	fake := &liveFakeOpener{}
	handler, engine, _, _, _ := liveGatewayFixture(t, fake)
	setLiveGroupMode(handler, 1, state.CodexLiveDirect)
	server := httptest.NewServer(engine)
	defer server.Close()
	for index := 0; index < 4; index++ {
		_, offer := liveClientOffer(t)
		response := liveRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/live", "gl-client", "application/sdp", []byte(offer))
		if response.StatusCode != 201 {
			t.Fatalf("status = %d", response.StatusCode)
		}
		fake.mu.Lock()
		selected, forwarded := fake.selected[index], fake.offers[index]
		fake.mu.Unlock()
		call, ok := handler.liveSessions.lookup(strings.TrimPrefix(response.Header.Get("Location"), "/v1/live/"), 1)
		if !ok || (call.media == nil) != (selected == 1) || (forwarded == offer) != (selected == 1) {
			t.Fatalf("mode does not match selected group %d", selected)
		}
		handler.liveSessions.finishCall(call, "client_hangup")
	}
	if fake.selected[0] != 1 || fake.selected[1] != 2 || fake.selected[2] != 1 || fake.selected[3] != 2 {
		t.Fatalf("round robin = %v", fake.selected)
	}
	setLiveGroupMode(handler, 1, state.CodexLiveOff)
	_, offer := liveClientOffer(t)
	response := liveRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/live", "gl-client", "application/sdp", []byte(offer))
	if response.StatusCode != 201 || fake.selected[4] != 2 {
		t.Fatal("disabled group still participates")
	}
}

func TestCodexLiveDirectControlDeadlineAndHangupFailure(t *testing.T) {
	for _, failedHangup := range []bool{false, true} {
		t.Run(map[bool]string{false: "control never attached", true: "hangup failed"}[failedHangup], func(t *testing.T) {
			fake := &liveFakeOpener{}
			handler, engine, sink, _, _ := liveGatewayFixture(t, fake)
			setLiveGroupMode(handler, 1, state.CodexLiveDirect)
			server := httptest.NewServer(engine)
			defer server.Close()
			_, offer := liveClientOffer(t)
			response := liveRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/live", "gl-client", "application/sdp", []byte(offer))
			if response.StatusCode != 201 {
				t.Fatal(response.StatusCode)
			}
			call, _ := handler.liveSessions.lookup("rtc_1", 1)
			if failedHangup {
				fake.sessions[0].hangupErr = errors.New("upstream unavailable")
				if err := handler.liveSessions.finishCall(call, "client_hangup"); err == nil {
					t.Fatal("unconfirmed hangup returned success")
				}
				if _, found := handler.liveSessions.lookup(call.id, 1); !found {
					t.Fatal("unconfirmed session was discarded")
				}
				// 停机无法继续重试时才记录最终未完成结果。
				handler.CloseCodexLive()
			} else {
				handler.liveSessions.mu.Lock()
				if call.reconnect == nil {
					handler.liveSessions.mu.Unlock()
					t.Fatal("missing initial control deadline")
				}
				call.reconnect.Reset(0)
				handler.liveSessions.mu.Unlock()
			}
			event := waitWebsocketLogs(t, sink, 1)[0]
			want := "control_timeout"
			if failedHangup {
				want = "hangup_failed"
			}
			if event.Status != telemetry.RequestStatusIncomplete || event.ErrorCode != want {
				t.Fatalf("event = %+v", event)
			}
		})
	}
}

func TestCodexLiveDirectUpstreamSessionClosed(t *testing.T) {
	terminal := []byte(`{"type":"session.closed"}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.TextMessage, terminal); err != nil {
			return
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer upstream.Close()
	fake := &liveFakeOpener{wsURL: "ws" + strings.TrimPrefix(upstream.URL, "http")}
	handler, engine, sink, _, _ := liveGatewayFixture(t, fake)
	setLiveGroupMode(handler, 1, state.CodexLiveDirect)
	server := httptest.NewServer(engine)
	defer server.Close()
	_, offer := liveClientOffer(t)
	response := liveRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/live", "gl-client", "application/sdp", []byte(offer))
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+response.Header.Get("Location"), http.Header{"Authorization": {"Bearer gl-client"}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, body, err := conn.ReadMessage()
	if err != nil || string(body) != string(terminal) {
		t.Fatalf("terminal = %s, %v", body, err)
	}
	event := waitWebsocketLogs(t, sink, 1)[0]
	if event.Status != telemetry.RequestStatusSuccess {
		t.Fatalf("event = %+v", event)
	}
}

func TestCodexLiveRefreshesRejectedCredentialWithinRetryBudget(t *testing.T) {
	var refreshed bool
	fake := &liveFakeOpener{failureFor: func(spec execution.AttemptSpec) *execution.ErrorEvidence {
		if spec.ForceCredentialRefresh {
			refreshed = true
			return nil
		}
		return &execution.ErrorEvidence{Kind: execution.ErrorKindHTTP, StatusCode: 401,
			OriginHint: execution.ErrorOriginUpstream, Hint: execution.FailureHintRefreshRequired,
			ReplaySafety: execution.ReplaySafetyRejectedBeforeProcessing}
	}}
	handler, engine, sink, _, _ := liveGatewayFixture(t, fake)
	setLiveGroupMode(handler, 1, state.CodexLiveDirect)
	handler.manager.Current().Settings.RetryCount = 1
	server := httptest.NewServer(engine)
	defer server.Close()
	_, offer := liveClientOffer(t)
	response := liveRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/live", "gl-client", "application/sdp", []byte(offer))
	if response.StatusCode != 201 || !refreshed || len(fake.attempted) != 2 || fake.attempted[0] != fake.attempted[1] {
		t.Fatalf("status=%d refresh=%t attempts=%v", response.StatusCode, refreshed, fake.attempted)
	}
	handler.CloseCodexLive()
	if attempts := waitWebsocketLogs(t, sink, 1)[0].Attempts; len(attempts) != 2 || !attempts[0].WillRetry {
		t.Fatalf("attempts=%+v", attempts)
	}
}

func TestCodexLivePreparationFailureFallsBackToAnotherAccount(t *testing.T) {
	fake := &liveFakeOpener{dispatchState: execution.DispatchNotSent, failures: map[uint]*execution.ErrorEvidence{1: {
		Kind: execution.ErrorKindProvider, Hint: execution.FailureHintReauthorizationRequired,
		OriginHint: execution.ErrorOriginUpstream, ScopeHint: execution.ErrorScopeCredential,
		Code: "refresh_outcome_unknown", Summary: "subscription credential refresh outcome is unknown",
	}}}
	handler, engine, sink, _, _ := liveGatewayFixture(t, fake)
	setLiveGroupMode(handler, 1, state.CodexLiveDirect)
	setLiveGroupMode(handler, 2, state.CodexLiveDirect)
	server := httptest.NewServer(engine)
	defer server.Close()
	_, offer := liveClientOffer(t)
	response := liveRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/realtime/calls", "gl-client", "application/sdp", []byte(offer))
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("preparation failure stopped failover: HTTP %d, attempts %v", response.StatusCode, fake.attempted)
	}
	if len(fake.attempted) != 2 || fake.attempted[0] != 1 || fake.attempted[1] != 2 {
		t.Fatalf("attempts = %v", fake.attempted)
	}
	handler.CloseCodexLive()
	event := waitWebsocketLogs(t, sink, 1)[0]
	if event.Attempts[0].DispatchState != execution.DispatchNotSent || event.Attempts[0].ResponseStarted || !event.Attempts[0].WillRetry {
		t.Fatalf("preparation log = %+v", event.Attempts[0])
	}
}
