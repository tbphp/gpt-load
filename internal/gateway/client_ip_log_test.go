package gateway

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/platform/utils"
	"gpt-load/internal/state"
)

func TestHandlerLogsResolvedClientIP(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`)}}}
	sink := &recordingRequestLogSink{}
	engine, _, _, _ := newRequestLogHandlerTestRuntime(t, forwarder, &recordingAccessKeyRPMLimiter{}, sink, "sk-one")
	resolver, err := utils.NewClientIPResolver("CF-Connecting-IP", []string{"192.0.2.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	request.RemoteAddr = "192.0.2.10:1234"
	request.Header.Set("Authorization", "Bearer gl-client")
	request.Header.Set("CF-Connecting-IP", "2001:db8::1")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, resolver.Apply(request))
	events := sink.snapshot()
	if response.Code != http.StatusOK || len(events) != 1 || events[0].ClientIP != "2001:db8::1" {
		t.Fatalf("response = %d, events = %+v", response.Code, events)
	}
}

func TestWebsocketUsesFrozenClientIPForAuthorizationAndLogs(t *testing.T) {
	resolver, err := utils.NewClientIPResolver("CF-Connecting-IP", nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	request.Header.Set("CF-Connecting-IP", "2001:db8::1")
	request = resolver.Apply(request)
	request.Header.Set("CF-Connecting-IP", "192.0.2.99")
	sink := &recordingRequestLogSink{}
	connection := &websocketConnection{
		request: request, keyHash: "hash", keyID: 1,
		handler: &Handler{requestLogSink: sink, requestNow: time.Now, newRequestID: func() (string, error) { return "00000000-0000-4000-8000-000000000901", nil }},
	}
	snapshot := &state.ConfigSnapshot{AccessKeysByHash: map[string]state.AccessKeyView{
		"hash": {ID: 1, Status: state.AccessKeyStatusActive, AllowedPeerCIDRs: []netip.Prefix{netip.MustParsePrefix("2001:db8::/32")}},
	}}
	if _, ok := connection.authorized(snapshot); !ok {
		t.Fatal("WebSocket did not use frozen client IP")
	}
	connection.newTurnRecorder(websocketTurn{started: time.Now()}).emit()
	events := sink.snapshot()
	if len(events) != 1 || events[0].ClientIP != "2001:db8::1" {
		t.Fatalf("events = %+v", events)
	}
}

func TestLiveLogsResolvedClientIP(t *testing.T) {
	_, engine, sink, _, _ := liveGatewayFixture(t, &liveFakeOpener{})
	resolver, err := utils.NewClientIPResolver("X-Real-IP", nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/live", strings.NewReader(""))
	request.Header.Set("Authorization", "Bearer gl-client")
	request.Header.Set("Content-Type", "application/sdp")
	request.Header.Set("X-Real-IP", "2001:db8::1")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, resolver.Apply(request))
	events := sink.snapshot()
	if response.Code != http.StatusBadRequest || len(events) != 1 || events[0].ClientIP != "2001:db8::1" {
		t.Fatalf("response = %d, events = %+v", response.Code, events)
	}
}
