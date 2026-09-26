package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/testutil/sqlitetest"
)

func TestCodexLiveHangupFailureLogsAndReleasesAfterBoundedRetries(t *testing.T) {
	for _, mode := range []state.CodexLiveMode{state.CodexLiveDirect, state.CodexLiveRelay} {
		t.Run(string(mode), func(t *testing.T) {
			fake := &liveFakeOpener{}
			handler, engine, sink, _, _ := liveGatewayFixture(t, fake)
			setLiveGroupMode(handler, 1, mode)
			server := httptest.NewServer(engine)
			defer server.Close()
			_, offer := liveClientOffer(t)
			response := liveRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/live", "gl-client", "application/sdp", []byte(offer))
			if response.StatusCode != http.StatusCreated {
				t.Fatal(response.StatusCode)
			}
			call, found := handler.liveSessions.lookup("rtc_1", 1)
			if !found {
				t.Fatal("created call missing")
			}
			fake.sessions[0].mu.Lock()
			fake.sessions[0].hangupErr = errors.New("upstream unavailable")
			fake.sessions[0].mu.Unlock()
			for attempt := 1; attempt <= 3; attempt++ {
				if err := handler.liveSessions.finishCall(call, "client_hangup"); err == nil {
					t.Fatal("unconfirmed hangup returned success")
				}
				events := sink.snapshot()
				if len(events) != 1 || events[0].Status != telemetry.RequestStatusIncomplete || events[0].ErrorCode != "hangup_failed" {
					t.Errorf("attempt %d must retain one incomplete log: %+v", attempt, events)
				}
			}
			if _, found := handler.liveSessions.lookup(call.id, 1); found {
				t.Fatal("exhausted hangup retries retained the session")
			}
			handler.liveSessions.setMaximum(1)
			if !handler.liveSessions.reserve() {
				t.Fatal("exhausted hangup retries leaked the session slot")
			}
			handler.liveSessions.release()
			assertLiveLogPersisted(t, sink.snapshot()[0])
		})
	}
}

type liveLogRetention struct{}

func (liveLogRetention) RequestLogRetentionDays() int { return 7 }

func assertLiveLogPersisted(t *testing.T, event telemetry.RequestEvent) {
	t.Helper()
	service := requestlog.NewService(sqlitetest.OpenMigrated(t), nil, liveLogRetention{})
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	service.Emit(event)
	if err := service.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	record, err := service.Get(t.Context(), event.RequestID)
	if err != nil || record.Status != event.Status || record.ErrorCode != event.ErrorCode {
		t.Fatalf("persisted live log: %+v, error: %v", record, err)
	}
}

type liveSetupFailureOpener struct {
	fake *liveFakeOpener
}

func (opener liveSetupFailureOpener) OpenLive(ctx context.Context, spec execution.AttemptSpec, offer string, session json.RawMessage) (execution.LiveCall, *execution.ErrorEvidence) {
	call, failure := opener.fake.OpenLive(ctx, spec, offer, session)
	if failure != nil {
		return call, failure
	}
	opener.fake.sessions[0].mu.Lock()
	opener.fake.sessions[0].hangupErr = errors.New("temporary hangup failure")
	opener.fake.sessions[0].mu.Unlock()
	call.SDP = ""
	return call, &execution.ErrorEvidence{Kind: execution.ErrorKindTransport, Summary: "invalid upstream answer"}
}

func TestCodexLiveFailedSetupRetainsKnownUpstreamForCleanup(t *testing.T) {
	fake := &liveFakeOpener{}
	handler, engine, sink, _, _ := liveGatewayFixture(t, fake)
	setLiveGroupMode(handler, 1, state.CodexLiveDirect)
	handler.liveOpener = liveSetupFailureOpener{fake: fake}
	server := httptest.NewServer(engine)
	defer server.Close()
	_, offer := liveClientOffer(t)
	response := liveRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/live", "gl-client", "application/sdp", []byte(offer))
	if response.StatusCode != http.StatusBadGateway {
		t.Fatal(response.StatusCode)
	}
	fake.sessions[0].mu.Lock()
	attempts := fake.sessions[0].hangup
	fake.sessions[0].mu.Unlock()
	if attempts != 1 {
		t.Fatalf("known upstream call was not cleaned up: %d hangups", attempts)
	}
	handler.liveSessions.mu.Lock()
	call := handler.liveSessions.calls["rtc_1"]
	pending := call != nil && call.terminating && call.timer != nil
	handler.liveSessions.mu.Unlock()
	if !pending {
		t.Fatal("failed setup lost its pending cleanup")
	}
	if events := sink.snapshot(); len(events) != 1 || events[0].Status != telemetry.RequestStatusError {
		t.Fatalf("setup failure logs = %+v", events)
	}
	handler.CloseCodexLive()
	if len(sink.snapshot()) != 1 {
		t.Fatal("cleanup duplicated the failed setup log")
	}
}

func TestCodexLiveRelayRequiresInitialControlConnection(t *testing.T) {
	fake := &liveFakeOpener{}
	handler, engine, sink, _, _ := liveGatewayFixture(t, fake)
	server := httptest.NewServer(engine)
	defer server.Close()
	_, offer := liveClientOffer(t)
	response := liveRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/live", "gl-client", "application/sdp", []byte(offer))
	if response.StatusCode != http.StatusCreated {
		t.Fatal(response.StatusCode)
	}
	call, _ := handler.liveSessions.lookup("rtc_1", 1)
	handler.liveSessions.mu.Lock()
	if call.reconnect == nil {
		handler.liveSessions.mu.Unlock()
		t.Fatal("relay without control has no setup deadline")
	}
	generation := call.reconnectGeneration
	handler.liveSessions.mu.Unlock()
	handler.liveSessions.expireReconnect(call, generation)
	events := sink.snapshot()
	if len(events) != 1 || events[0].ErrorCode != "control_timeout" {
		t.Fatalf("events = %+v", events)
	}
	if _, found := handler.liveSessions.lookup(call.id, 1); found {
		t.Fatal("relay without control retained its session")
	}
}

func TestCodexLiveHangupAcceptsReturnedLocation(t *testing.T) {
	for _, endpoint := range []string{"/v1/live", "/v1/realtime/calls"} {
		t.Run(endpoint, func(t *testing.T) {
			fake := &liveFakeOpener{}
			handler, engine, sink, _, _ := liveGatewayFixture(t, fake)
			setLiveGroupMode(handler, 1, state.CodexLiveDirect)
			server := httptest.NewServer(engine)
			defer server.Close()
			_, offer := liveClientOffer(t)
			created := liveRequest(t, server.Client(), http.MethodPost, server.URL+endpoint, "gl-client", "application/sdp", []byte(offer))
			if created.StatusCode != http.StatusCreated {
				t.Fatal(created.StatusCode)
			}
			hangup := liveRequest(t, server.Client(), http.MethodPost, server.URL+created.Header.Get("Location")+"/hangup", "gl-client", "", nil)
			if hangup.StatusCode != http.StatusNoContent {
				t.Fatalf("returned Location cannot be hung up: HTTP %d", hangup.StatusCode)
			}
			if events := sink.snapshot(); len(events) != 1 || events[0].Status != telemetry.RequestStatusSuccess {
				t.Fatalf("events = %+v", events)
			}
		})
	}
}

type failedLiveAnswerWriter struct{ *httptest.ResponseRecorder }

func (failedLiveAnswerWriter) Write([]byte) (int, error)       { return 0, io.ErrClosedPipe }
func (failedLiveAnswerWriter) WriteString(string) (int, error) { return 0, io.ErrClosedPipe }

func TestCodexLiveFailedAnswerDeliveryEndsSession(t *testing.T) {
	fake := &liveFakeOpener{}
	handler, engine, sink, _, _ := liveGatewayFixture(t, fake)
	setLiveGroupMode(handler, 1, state.CodexLiveDirect)
	_, offer := liveClientOffer(t)
	request := httptest.NewRequest(http.MethodPost, "/v1/live", strings.NewReader(offer))
	request.Header.Set("Authorization", "Bearer gl-client")
	request.Header.Set("Content-Type", "application/sdp")
	engine.ServeHTTP(failedLiveAnswerWriter{httptest.NewRecorder()}, request)
	if _, found := handler.liveSessions.lookup("rtc_1", 1); found {
		t.Fatal("undeliverable answer left the upstream call active")
	}
	if events := sink.snapshot(); len(events) != 1 || events[0].Status != telemetry.RequestStatusCanceled {
		t.Fatalf("undeliverable answer logs = %+v", events)
	}
}
