package control

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/gateway"
	"gpt-load/internal/health"
	"gpt-load/internal/state"
)

func TestControlWriteLockDoesNotBlockDataPlane(t *testing.T) {
	t.Parallel()
	requestReached := make(chan bool, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		streaming := bytes.Contains(body, []byte(`"stream":true`))
		requestReached <- streaming
		if streaming {
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = writer.Write([]byte("data: {\"id\":\"chunk\"}\n\n"))
			writer.(http.Flusher).Flush()
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"response"}`))
	}))
	t.Cleanup(upstream.Close)

	fixture := newServiceFixture(t)
	const (
		groupID       = uint(10)
		upstreamKeyID = uint(20)
		accessKey     = "gl-data-plane-client"
		upstreamKey   = "sk-data-plane-upstream"
	)
	if _, err := fixture.manager.Publish(state.CompileInput{
		ChannelRegistry: fixture.channelRegistry,
		Groups: []state.GroupConfig{{ConnectionType: "api_key", ID: groupID, Name: "data-plane", ChannelID: channel.OpenAICompatible,
			Params: json.RawMessage(`{"base_url":"` + upstream.URL + `/v1"}`),
			Models: []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true,
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: fixture.encryption.Hash(accessKey),
			Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatalf("Publish(data plane) error = %v", err)
	}
	credentialData := `{"api_key":"` + upstreamKey + `"}`
	ciphertext, err := fixture.encryption.Encrypt(credentialData)
	if err != nil {
		t.Fatalf("Encrypt(upstream key) error = %v", err)
	}
	if err := fixture.registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: upstreamKeyID, GroupID: groupID, Version: 1, IdentityGeneration: 1,
		Fingerprint: fixture.encryption.Hash(credentialData), Status: state.CredentialStatusActive,
		EncryptedValue: ciphertext,
	}}); err != nil {
		t.Fatalf("ReplaceCredentials() error = %v", err)
	}

	openAI := dialect.NewOpenAI()
	handler := gateway.NewHandler(
		fixture.manager,
		fixture.registry,
		fixture.encryption,
		gateway.NewExecutionForwarder(controlHTTPExecutor{}),
		dialect.NewSet(openAI),
		health.NewStatsStore(),
		health.NewMutationCoordinator(),
		nil,
		nil,
		nil,
	)
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	registerGatewayRoutes(t, engine, handler)

	fixture.service.writeMu.Lock()
	locked := true
	defer func() {
		if locked {
			fixture.service.writeMu.Unlock()
		}
	}()

	type response struct {
		streaming bool
		recorder  *httptest.ResponseRecorder
	}
	responses := make(chan response, 2)
	for _, streaming := range []bool{false, true} {
		streaming := streaming
		go func() {
			body := `{"model":"gpt-4o"}`
			if streaming {
				body = `{"model":"gpt-4o","stream":true}`
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer "+accessKey)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			responses <- response{streaming: streaming, recorder: recorder}
		}()
	}

	for range 2 {
		select {
		case <-requestReached:
		case <-time.After(2 * time.Second):
			t.Fatal("data-plane request did not reach upstream while control writeMu was held")
		}
	}
	for range 2 {
		select {
		case got := <-responses:
			if got.recorder.Code != http.StatusOK {
				t.Fatalf("streaming=%t response = %d %s", got.streaming, got.recorder.Code, got.recorder.Body.String())
			}
			if got.streaming && !bytes.Contains(got.recorder.Body.Bytes(), []byte("data:")) {
				t.Fatalf("streaming body = %q, want SSE data", got.recorder.Body.Bytes())
			}
		case <-time.After(2 * time.Second):
			t.Fatal("data-plane request did not complete while control writeMu was held")
		}
	}
	if got, ok := fixture.registry.ActiveEncryptedCredentialDataIfMatch(state.CredentialRef{
		ID: upstreamKeyID, GroupID: groupID, Version: 1, IdentityGeneration: 1,
		Fingerprint: fixture.encryption.Hash(credentialData), EncryptedValue: ciphertext,
	}); !ok || got != ciphertext {
		t.Fatalf("Registry value = %q, %t, want active ciphertext", got, ok)
	}

	fixture.service.writeMu.Unlock()
	locked = false
}
