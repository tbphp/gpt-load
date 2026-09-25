package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

type liveFakeOpener struct {
	mu       sync.Mutex
	selected []uint
	models   []string
	sessions []*liveFakeUpstream
	wsURL    string
	failure  *execution.ErrorEvidence
}

type liveFakeUpstream struct {
	peer   *webrtc.PeerConnection
	wsURL  string
	mu     sync.Mutex
	hangup int
	dials  int
	conn   *websocket.Conn
}

func (fake *liveFakeOpener) OpenLive(ctx context.Context, spec execution.AttemptSpec, offer string, sessionJSON json.RawMessage) (execution.LiveCall, *execution.ErrorEvidence) {
	if fake.failure != nil {
		return execution.LiveCall{}, fake.failure
	}
	api, err := liveMediaAPI(config.CodexLiveConfig{})
	if err != nil {
		return execution.LiveCall{}, &execution.ErrorEvidence{Summary: err.Error()}
	}
	peer, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return execution.LiveCall{}, &execution.ErrorEvidence{Summary: err.Error()}
	}
	defer func() {
		if err != nil {
			_ = peer.Close()
		}
	}()
	if err = peer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer}); err != nil {
		return execution.LiveCall{}, &execution.ErrorEvidence{Summary: err.Error()}
	}
	track, trackErr := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "audio", "fake")
	if trackErr != nil {
		return execution.LiveCall{}, &execution.ErrorEvidence{Summary: trackErr.Error()}
	}
	if _, err = peer.AddTrack(track); err != nil {
		return execution.LiveCall{}, &execution.ErrorEvidence{Summary: err.Error()}
	}
	answer, err := peer.CreateAnswer(nil)
	if err != nil {
		return execution.LiveCall{}, &execution.ErrorEvidence{Summary: err.Error()}
	}
	if err = peer.SetLocalDescription(answer); err != nil {
		return execution.LiveCall{}, &execution.ErrorEvidence{Summary: err.Error()}
	}
	if err = awaitLiveICE(ctx, peer); err != nil {
		return execution.LiveCall{}, &execution.ErrorEvidence{Summary: err.Error()}
	}
	fake.mu.Lock()
	id := len(fake.sessions) + 1
	session := &liveFakeUpstream{peer: peer, wsURL: fake.wsURL}
	fake.sessions = append(fake.sessions, session)
	fake.selected = append(fake.selected, spec.Credential.ID)
	var sessionFields struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(sessionJSON, &sessionFields); err != nil {
		fake.mu.Unlock()
		return execution.LiveCall{}, &execution.ErrorEvidence{Summary: err.Error()}
	}
	fake.models = append(fake.models, sessionFields.Model)
	fake.mu.Unlock()
	return execution.LiveCall{CallID: fmt.Sprintf("rtc_%d", id), SDP: peer.LocalDescription().SDP, Session: session}, nil
}

func (session *liveFakeUpstream) DialSideband(ctx context.Context, _ string, protocols []string) (*websocket.Conn, int, error) {
	session.mu.Lock()
	if session.conn != nil {
		session.mu.Unlock()
		return nil, 0, errors.New("live sideband is already attached")
	}
	session.mu.Unlock()
	dialer := websocket.Dialer{Subprotocols: protocols}
	connection, response, err := dialer.DialContext(ctx, session.wsURL, nil)
	status := 0
	if response != nil {
		status = response.StatusCode
	}
	if err == nil {
		session.mu.Lock()
		session.dials++
		session.conn = connection
		session.mu.Unlock()
	}
	return connection, status, err
}

func (session *liveFakeUpstream) ReleaseSideband(connection *websocket.Conn) {
	session.mu.Lock()
	if session.conn == connection {
		session.conn = nil
	}
	session.mu.Unlock()
}

func (session *liveFakeUpstream) Hangup(context.Context) error {
	session.mu.Lock()
	session.hangup++
	session.mu.Unlock()
	return nil
}

func (session *liveFakeUpstream) Close() error {
	session.mu.Lock()
	connection := session.conn
	session.mu.Unlock()
	if connection != nil {
		_ = connection.Close()
	}
	return session.peer.Close()
}

func liveGatewayFixture(t *testing.T, fake *liveFakeOpener) (*Handler, *gin.Engine, *recordingRequestLogSink, *state.CredentialRegistry, func(string)) {
	t.Helper()
	handler, manager, registry := newHandlerForTest(t, newTestExecutionForwarder(t))
	groups := make([]state.GroupConfig, 0, 2)
	configs := make([]state.CredentialConfig, 0, 2)
	entries := make([]state.CredentialEntry, 0, 2)
	for id := uint(1); id <= 2; id++ {
		groups = append(groups, state.GroupConfig{ID: id, Name: fmt.Sprintf("codex-%d", id), ChannelID: channel.Codex,
			ConnectionType: "subscription", Params: json.RawMessage(`{}`),
			Enabled: true})
		configs = append(configs, testCredentialConfig(id, id))
		credential := fmt.Sprintf(`{"type":"codex","access_token":"access-%d","refresh_token":"refresh-%d","account_id":"account-%d"}`, id, id, id)
		encrypted, err := handler.encryption.Encrypt(credential)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, state.CredentialEntry{ID: id, GroupID: id, Status: state.CredentialStatusActive,
			Version: 1, IdentityGeneration: uint64(id), Fingerprint: fmt.Sprintf("test-credential-%d", id), EncryptedValue: encrypted})
	}
	publishOwnerKey := func(key string) {
		t.Helper()
		if _, err := manager.Publish(state.CompileInput{ChannelRegistry: channel.NewRegistry(), Groups: groups, Credentials: configs,
			AccessKeys: []state.AccessKeyConfig{
				{ID: 1, Name: "owner", KeyHash: handler.encryption.Hash(key), Status: state.AccessKeyStatusActive},
				{ID: 2, Name: "other", KeyHash: handler.encryption.Hash("gl-other"), Status: state.AccessKeyStatusActive},
			}}); err != nil {
			t.Fatal(err)
		}
	}
	publishOwnerKey("gl-client")
	if err := registry.ReplaceCredentials(entries); err != nil {
		t.Fatal(err)
	}
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	handler.ConfigureCodexLive(fake, config.CodexLiveConfig{MaxSessions: 8})
	t.Cleanup(handler.CloseCodexLive)
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	return handler, engine, sink, registry, publishOwnerKey
}

func liveClientOffer(t *testing.T) (*webrtc.PeerConnection, string) {
	t.Helper()
	api, err := liveMediaAPI(config.CodexLiveConfig{})
	if err != nil {
		t.Fatal(err)
	}
	peer, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "audio", "client")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := peer.AddTrack(track); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.CreateDataChannel("oai-events", nil); err != nil {
		t.Fatal(err)
	}
	offer, err := peer.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := peer.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := awaitLiveICE(ctx, peer); err != nil {
		t.Fatal(err)
	}
	return peer, peer.LocalDescription().SDP
}

func liveRequest(t *testing.T, client *http.Client, method, endpoint, key, contentType string, body []byte) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+key)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func TestCodexLiveCreatesAcrossGroupsPinsOwnerAndLogsOnce(t *testing.T) {
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		for {
			kind, message, err := connection.ReadMessage()
			if err != nil || connection.WriteMessage(kind, message) != nil {
				return
			}
		}
	}))
	defer echo.Close()
	fake := &liveFakeOpener{wsURL: "ws" + strings.TrimPrefix(echo.URL, "http")}
	_, engine, sink, _, _ := liveGatewayFixture(t, fake)
	server := httptest.NewServer(engine)
	defer server.Close()
	models := []string{"client-live-model", channel.CodexLiveModelID, channel.CodexLiveModelID, channel.CodexLiveModelID}
	for index := 1; index <= 4; index++ {
		clientPeer, offer := liveClientOffer(t)
		body, err := json.Marshal(map[string]any{"sdp": offer, "session": map[string]any{"model": models[index-1]}})
		if err != nil {
			t.Fatal(err)
		}
		created := liveRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/realtime/calls", "gl-client", "application/json", body)
		if created.StatusCode != http.StatusCreated || created.Header.Get("Content-Type") != "application/sdp" {
			t.Fatalf("create status=%d headers=%v", created.StatusCode, created.Header)
		}
		location := created.Header.Get("Location")
		if location != fmt.Sprintf("/v1/realtime/calls/rtc_%d", index) {
			t.Fatalf("location = %q", location)
		}
		var answer bytes.Buffer
		if _, err := answer.ReadFrom(created.Body); err != nil {
			t.Fatal(err)
		}
		if err := clientPeer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.String()}); err != nil {
			t.Fatal(err)
		}
		denied := liveRequest(t, server.Client(), http.MethodPost, server.URL+location+"/hangup", "gl-other", "", nil)
		if denied.StatusCode != http.StatusNotFound {
			t.Fatalf("other owner hangup status = %d", denied.StatusCode)
		}
		if index == 1 {
			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + location
			otherHeaders := http.Header{"Authorization": {"Bearer gl-other"}}
			if connection, response, err := websocket.DefaultDialer.Dial(wsURL, otherHeaders); err == nil || response == nil || response.StatusCode != http.StatusNotFound {
				if connection != nil {
					_ = connection.Close()
				}
				t.Fatalf("other owner sideband response=%v error=%v", response, err)
			}
			ownerHeaders := http.Header{"Authorization": {"Bearer gl-client"}}
			for repeat := 0; repeat < 2; repeat++ {
				connection, _, err := websocket.DefaultDialer.Dial(wsURL, ownerHeaders)
				if err != nil {
					t.Fatal(err)
				}
				if err := connection.WriteMessage(websocket.TextMessage, []byte("voice-control")); err != nil {
					t.Fatal(err)
				}
				_, echoed, err := connection.ReadMessage()
				if err != nil || string(echoed) != "voice-control" {
					t.Fatalf("sideband echo=%q error=%v", echoed, err)
				}
				if repeat == 1 {
					if err := connection.WriteMessage(websocket.TextMessage, bytes.Repeat([]byte("x"), 256<<10+1)); err != nil {
						t.Fatal(err)
					}
					_, _, err := connection.ReadMessage()
					var closeError *websocket.CloseError
					if !errors.As(err, &closeError) || closeError.Code != websocket.CloseMessageTooBig {
						t.Fatalf("oversized sideband close = %v, want message-too-big", err)
					}
				}
				_ = connection.Close()
			}
		}
		hungup := liveRequest(t, server.Client(), http.MethodPost, server.URL+location+"/hangup", "gl-client", "", nil)
		if hungup.StatusCode != http.StatusNoContent {
			t.Fatalf("hangup status = %d", hungup.StatusCode)
		}
	}
	events := waitWebsocketLogs(t, sink, 4)
	if len(events) != 4 {
		t.Fatalf("session log count = %d", len(events))
	}
	for index, event := range events {
		if event.Protocol != protocol.CodexLive || event.Operation != execution.OperationLiveCall ||
			event.Status != telemetry.RequestStatusSuccess || event.Usage.Result.State != usage.StateMissing ||
			event.Usage.Pricing.CostState != "unpriced" || len(event.Attempts) != 1 ||
			event.ClientModel != models[index] || event.UpstreamModel != models[index] {
			t.Fatalf("incorrect live session log: %+v", event)
		}
	}
	fake.mu.Lock()
	selected := append([]uint(nil), fake.selected...)
	upstreamModels := append([]string(nil), fake.models...)
	sessions := append([]*liveFakeUpstream(nil), fake.sessions...)
	fake.mu.Unlock()
	if fmt.Sprint(upstreamModels) != fmt.Sprint(models) {
		t.Fatalf("upstream models = %v, want %v", upstreamModels, models)
	}
	if len(selected) != 4 || selected[0] == selected[1] && selected[1] == selected[2] && selected[2] == selected[3] {
		t.Fatalf("existing scheduler did not distribute live calls: %v", selected)
	}
	for _, session := range sessions {
		session.mu.Lock()
		if session.hangup != 1 {
			t.Fatalf("upstream hangup count = %d", session.hangup)
		}
		session.mu.Unlock()
	}
}

func TestCodexLiveClientMediaEndLogsSuccess(t *testing.T) {
	fake := &liveFakeOpener{}
	handler, engine, sink, _, _ := liveGatewayFixture(t, fake)
	server := httptest.NewServer(engine)
	defer server.Close()
	_, offer := liveClientOffer(t)
	body, err := json.Marshal(map[string]any{"sdp": offer})
	if err != nil {
		t.Fatal(err)
	}
	created := liveRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/realtime/calls", "gl-client", "application/json", body)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", created.StatusCode)
	}
	call, found := handler.liveSessions.lookup("rtc_1", 1)
	if !found {
		t.Fatal("created live call is missing")
	}
	call.media.clientConnected.Store(true)
	call.media.endClientMedia()
	events := waitWebsocketLogs(t, sink, 1)
	if len(events) != 1 || events[0].Status != telemetry.RequestStatusSuccess || events[0].ErrorCode != "" ||
		events[0].ClientModel != channel.CodexLiveModelID || events[0].UpstreamModel != channel.CodexLiveModelID {
		t.Fatalf("normal voice end log = %+v", events)
	}
}

func TestCodexLiveRevokesActiveCallWhenSelectedCredentialIsDisabled(t *testing.T) {
	fake := &liveFakeOpener{}
	handler, engine, sink, registry, _ := liveGatewayFixture(t, fake)
	server := httptest.NewServer(engine)
	defer server.Close()
	clientPeer, offer := liveClientOffer(t)
	body, err := json.Marshal(map[string]any{"sdp": offer, "session": map[string]any{"model": channel.CodexLiveModelID}})
	if err != nil {
		t.Fatal(err)
	}
	created := liveRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/realtime/calls", "gl-client", "application/json", body)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", created.StatusCode)
	}
	var answer bytes.Buffer
	if _, err := answer.ReadFrom(created.Body); err != nil {
		t.Fatal(err)
	}
	if err := clientPeer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.String()}); err != nil {
		t.Fatal(err)
	}
	call, found := handler.liveSessions.lookup("rtc_1", 1)
	if !found {
		t.Fatal("created call is missing")
	}
	fake.mu.Lock()
	selected := fake.selected[0]
	fake.mu.Unlock()
	if err := registry.SetCredentialStatus(selected, state.CredentialStatusDisabled); err != nil {
		t.Fatal(err)
	}
	if call.authorized() {
		t.Fatal("disabled credential still authorizes the active voice call")
	}
	select {
	case <-call.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("disabled credential did not terminate the active voice call")
	}
	deadline := time.After(10 * time.Second)
	for {
		events := sink.snapshot()
		if len(events) == 1 {
			if events[0].Status != telemetry.RequestStatusCanceled || events[0].ErrorCode != "key_revoked" {
				t.Fatalf("revoked call log = %+v", events[0])
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("revoked call log was not emitted after teardown")
		case <-time.After(20 * time.Millisecond):
		}
	}
	missing := liveRequest(t, server.Client(), http.MethodPost, server.URL+created.Header.Get("Location")+"/hangup", "gl-client", "", nil)
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("revoked call remained addressable: %d", missing.StatusCode)
	}
}

func TestCodexLiveRevokesActiveCallWhenAccessKeyRotates(t *testing.T) {
	fake := &liveFakeOpener{}
	handler, engine, sink, _, publishOwnerKey := liveGatewayFixture(t, fake)
	server := httptest.NewServer(engine)
	defer server.Close()
	clientPeer, offer := liveClientOffer(t)
	body, err := json.Marshal(map[string]any{"sdp": offer, "session": map[string]any{"model": channel.CodexLiveModelID}})
	if err != nil {
		t.Fatal(err)
	}
	created := liveRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/realtime/calls", "gl-client", "application/json", body)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", created.StatusCode)
	}
	var answer bytes.Buffer
	if _, err := answer.ReadFrom(created.Body); err != nil {
		t.Fatal(err)
	}
	if err := clientPeer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.String()}); err != nil {
		t.Fatal(err)
	}
	call, found := handler.liveSessions.lookup("rtc_1", 1)
	if !found {
		t.Fatal("created call is missing")
	}
	publishOwnerKey("gl-rotated")
	if call.authorized() {
		t.Fatal("rotated AccessKey still authorizes the old live call")
	}
	// 终止由授权轮询及时触发；请求日志要等 WebRTC 清理完成后才写出，race 模式下清理明显变慢，两段分开等待。
	select {
	case <-call.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("rotated AccessKey did not terminate the old live call")
	}
	deadline := time.After(10 * time.Second)
	for {
		events := sink.snapshot()
		if len(events) == 1 {
			if events[0].Status != telemetry.RequestStatusCanceled || events[0].ErrorCode != "key_revoked" {
				t.Fatalf("rotated call log = %+v", events[0])
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("rotated call log was not emitted after teardown")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestCodexLiveReconnectTimeoutDoesNotCloseReattachedCall(t *testing.T) {
	fake := &liveFakeOpener{}
	handler, engine, _, _, _ := liveGatewayFixture(t, fake)
	server := httptest.NewServer(engine)
	defer server.Close()
	clientPeer, offer := liveClientOffer(t)
	body, err := json.Marshal(map[string]any{"sdp": offer, "session": map[string]any{"model": channel.CodexLiveModelID}})
	if err != nil {
		t.Fatal(err)
	}
	created := liveRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/realtime/calls", "gl-client", "application/json", body)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", created.StatusCode)
	}
	var answer bytes.Buffer
	if _, err := answer.ReadFrom(created.Body); err != nil {
		t.Fatal(err)
	}
	if err := clientPeer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.String()}); err != nil {
		t.Fatal(err)
	}
	store := handler.liveSessions
	call, claimed := store.claim("rtc_1", 1)
	if !claimed {
		t.Fatal("created call cannot attach")
	}
	store.unclaim(call, nil)

	// 模拟旧超时回调已启动，但重连先取得锁并重新附着。
	store.mu.Lock()
	call.reconnect.Reset(0)
	time.Sleep(250 * time.Millisecond)
	if call.reconnect.Stop() {
		store.mu.Unlock()
		t.Fatal("reconnect timeout did not fire")
	}
	call.reconnect = nil
	call.attached = true
	store.mu.Unlock()
	select {
	case <-call.closed:
		t.Fatal("stale reconnect timeout closed a reattached call")
	case <-time.After(100 * time.Millisecond):
	}
	if _, found := store.lookup("rtc_1", 1); !found {
		t.Fatal("reattached call was removed")
	}
}

func TestCodexLiveForbiddenAccountDoesNotRetryOrCreateSession(t *testing.T) {
	fake := &liveFakeOpener{failure: &execution.ErrorEvidence{
		Kind: execution.ErrorKindHTTP, StatusCode: http.StatusForbidden,
		OriginHint: execution.ErrorOriginUpstream, ScopeHint: execution.ErrorScopeRequest,
		Summary: "Codex live request forbidden",
	}}
	handler, engine, sink, _, _ := liveGatewayFixture(t, fake)
	server := httptest.NewServer(engine)
	defer server.Close()
	_, offer := liveClientOffer(t)
	body, err := json.Marshal(map[string]any{"sdp": offer, "session": map[string]any{"model": channel.CodexLiveModelID}})
	if err != nil {
		t.Fatal(err)
	}
	response := liveRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/live", "gl-client", "application/json", body)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("forbidden status = %d", response.StatusCode)
	}
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil || payload.Code != "codex_live_upstream_forbidden" {
		t.Fatalf("forbidden response = %+v, error = %v", payload, err)
	}
	events := waitWebsocketLogs(t, sink, 1)
	if len(events) != 1 || len(events[0].Attempts) != 1 || events[0].Status != telemetry.RequestStatusError {
		t.Fatalf("forbidden logs = %+v", events)
	}
	if len(handler.liveSessions.calls) != 0 || handler.liveSessions.pending != 0 {
		t.Fatal("forbidden call left a live session")
	}
}
