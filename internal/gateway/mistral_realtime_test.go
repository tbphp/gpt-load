package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/health"
	"gpt-load/internal/state"
	"gpt-load/internal/testutil/encryptiontest"
)

func TestMistralRealtimeCopiesFramesWithUpstreamKey(t *testing.T) {
	t.Parallel()
	const upstreamModel = "voxtral-mini-transcribe-realtime-2602"
	var (
		mu        sync.Mutex
		seenAuth  string
		seenPath  string
		seenModel string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenAuth = r.Header.Get("Authorization")
		seenPath = r.URL.Path
		seenModel = r.URL.Query().Get("model")
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer upstream-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		kind, body, err := conn.ReadMessage()
		if err != nil {
			return
		}
		_ = conn.WriteMessage(kind, body)
	}))
	defer upstream.Close()

	service := encryptiontest.Service(t, "mistral-realtime-fixture-key")
	params, err := json.Marshal(map[string]string{"base_url": upstream.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	input := state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ID: 1, Name: "mistral", ConnectionType: "api_key", ChannelID: channel.Mistral,
			Params: params, Models: []state.ModelConfig{{ID: upstreamModel, Alias: "public"}}, Enabled: true,
		}},
		Credentials: []state.CredentialConfig{testCredentialConfig(1, 1)},
		AccessKeys:  []state.AccessKeyConfig{{ID: 1, Name: "client", KeyHash: service.Hash("gl-client"), Status: state.AccessKeyStatusActive}},
	}
	manager := state.NewManager()
	if _, err := manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	registry := state.NewCredentialRegistry()
	if err := registry.ReplaceCredentials([]state.CredentialEntry{testCredentialEntry(t, service, 1, 1, "upstream-key")}); err != nil {
		t.Fatal(err)
	}
	sink := &recordingRequestLogSink{}
	handler := NewHandler(manager, registry, service, newTestExecutionForwarder(t), dialect.NewSet(dialect.NewMistral()), health.NewStatsStore(), health.NewMutationCoordinator(), nil, sink, nil)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	gateway := httptest.NewServer(engine)
	defer gateway.Close()

	plain, err := http.NewRequest(http.MethodGet, gateway.URL+"/v1/audio/transcriptions/realtime?model=public", nil)
	if err != nil {
		t.Fatal(err)
	}
	plain.Header.Set("Authorization", "Bearer gl-client")
	plainResponse, err := http.DefaultClient.Do(plain)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(plainResponse.Body)
	_ = plainResponse.Body.Close()
	if plainResponse.StatusCode != http.StatusUpgradeRequired || !strings.Contains(string(body), "websocket_upgrade_required") {
		t.Fatalf("plain GET = %d %s", plainResponse.StatusCode, body)
	}

	missing, err := http.NewRequest(http.MethodGet, gateway.URL+"/v1/audio/transcriptions/realtime", nil)
	if err != nil {
		t.Fatal(err)
	}
	missing.Header.Set("Authorization", "Bearer gl-client")
	missing.Header.Set("Connection", "Upgrade")
	missing.Header.Set("Upgrade", "websocket")
	missing.Header.Set("Sec-WebSocket-Version", "13")
	missing.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	missingResponse, err := http.DefaultClient.Do(missing)
	if err != nil {
		t.Fatal(err)
	}
	missingBody, _ := io.ReadAll(missingResponse.Body)
	_ = missingResponse.Body.Close()
	if missingResponse.StatusCode != http.StatusBadRequest || !strings.Contains(string(missingBody), "invalid_protocol_request") {
		t.Fatalf("missing model = %d %s", missingResponse.StatusCode, missingBody)
	}

	client, response, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(gateway.URL, "http")+"/v1/audio/transcriptions/realtime?model=public",
		http.Header{"Authorization": {"Bearer gl-client"}, "Origin": {gateway.URL}},
	)
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
			payload, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			t.Fatalf("dial status=%d body=%s err=%v", status, payload, err)
		}
		t.Fatal(err)
	}
	defer client.Close()
	payload := []byte("pcm-bytes")
	if err := client.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		t.Fatal(err)
	}
	kind, echoed, err := client.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if kind != websocket.BinaryMessage || string(echoed) != string(payload) {
		t.Fatalf("echo = %d %q", kind, echoed)
	}
	mu.Lock()
	defer mu.Unlock()
	if seenAuth != "Bearer upstream-key" || seenModel != upstreamModel || seenPath != "/v1/audio/transcriptions/realtime" {
		t.Fatalf("upstream saw auth=%q model=%q path=%q", seenAuth, seenModel, seenPath)
	}
	events := waitWebsocketLogs(t, sink, 3)
	event := events[len(events)-1]
	attempt := event.Attempts
	if event.Status != "success" || event.UpstreamModel != upstreamModel || len(attempt) != 1 || attempt[0].CredentialID != 1 || attempt[0].GroupID != 1 || attempt[0].StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("log = status %s model %s attempts %+v", event.Status, event.UpstreamModel, attempt)
	}
}

func TestMistralRealtimeUpstreamURLRewritesModel(t *testing.T) {
	t.Parallel()
	got, err := mistralRealtimeUpstreamURL(
		json.RawMessage(fmt.Sprintf(`{"base_url":%q}`, "https://api.mistral.ai/v1")),
		"voxtral-mini-transcribe-realtime-2602",
		"model=public&api_key=secret",
	)
	if err != nil {
		t.Fatal(err)
	}
	want := "wss://api.mistral.ai/v1/audio/transcriptions/realtime?model=voxtral-mini-transcribe-realtime-2602"
	if got != want {
		t.Fatalf("url = %s", got)
	}
}
