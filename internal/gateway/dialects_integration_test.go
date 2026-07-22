package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/dialect"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/encryption"
	platformhttp "gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/testutil/fakeupstream"
)

type dialectGatewayGroup struct {
	id          uint
	name        string
	upstreamURL string
	apiKeys     []string
	models      []state.ModelConfig
	headerRules state.HeaderRules
	firstByte   time.Duration
}

func newDialectGatewayEngine(
	t *testing.T,
	selectedProtocol protocol.Protocol,
	model string,
	dialects dialect.Set,
	groups ...dialectGatewayGroup,
) (*gin.Engine, *state.KeyRegistry) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	keyService, err := encryption.NewService("dialect-gateway-test-master-key")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	configs := make([]state.GroupConfig, 0, len(groups))
	entries := make([]state.KeyEntry, 0)
	keyID := uint(1)
	for _, group := range groups {
		models := group.models
		if len(models) == 0 {
			models = []state.ModelConfig{{ID: model}}
		}
		configs = append(configs, state.GroupConfig{
			ID: group.id, Name: group.name, UpstreamURL: group.upstreamURL,
			Protocols: []protocol.Protocol{selectedProtocol},
			Models:    models, Enabled: true,
		})
		for _, apiKey := range group.apiKeys {
			ciphertext, encryptErr := keyService.Encrypt(apiKey)
			if encryptErr != nil {
				t.Fatalf("Encrypt(group %d key) error = %v", group.id, encryptErr)
			}
			entries = append(entries, state.KeyEntry{
				ID: keyID, GroupID: group.id, Status: state.KeyStatusActive,
				EncryptedValue: ciphertext,
			})
			keyID++
		}
	}

	manager := state.NewManager()
	snapshot, err := manager.Publish(state.CompileInput{
		Groups: configs,
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: keyService.Hash("gl-client"),
			Status: state.AccessKeyStatusActive,
		}},
	})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	for _, group := range groups {
		view := snapshot.Groups[group.id]
		view.HeaderRules = group.headerRules
		if group.firstByte > 0 {
			view.Timeouts.FirstByte = group.firstByte
		}
		snapshot.Groups[group.id] = view
	}

	registry := state.NewKeyRegistry()
	if err := registry.Replace(entries); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	handler := NewHandler(
		manager,
		registry,
		keyService,
		NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()),
		dialects,
		health.NewStatsStore(),
	)
	handler.newRandom = func() *rand.Rand { return rand.New(zeroSource{}) }
	engine := gin.New()
	handler.RegisterRoutes(engine)
	return engine, registry
}

func TestGatewayRewritesEachAttemptFromOriginal(t *testing.T) {
	first := fakeupstream.New(fakeupstream.Step{Status: http.StatusUnauthorized, Fixture: "401.json"})
	defer first.Close()
	second := fakeupstream.New(fakeupstream.Step{Status: http.StatusOK, Fixture: "success.json"})
	defer second.Close()

	engine, _ := newDialectGatewayEngine(t, protocol.OpenAI, "public",
		dialect.NewSet(dialect.NewOpenAI(http.DefaultClient)),
		dialectGatewayGroup{
			id: 1, name: "first", upstreamURL: first.URL, apiKeys: []string{"sk-first"},
			models: []state.ModelConfig{{ID: "provider-one", Alias: "public"}},
		},
		dialectGatewayGroup{
			id: 2, name: "second", upstreamURL: second.URL, apiKeys: []string{"sk-second"},
			models: []state.ModelConfig{{ID: "provider-two", Alias: "public"}},
		},
	)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"public"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || recorder.Header().Get(debugHeaderAttempts) != "2" {
		t.Fatalf("response = %d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	requests := append(first.Requests(), second.Requests()...)
	if len(requests) != 2 {
		t.Fatalf("upstream requests = %d, want 2", len(requests))
	}
	wantModels := []string{"provider-one", "provider-two"}
	for index, received := range requests {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(received.Body, &body); err != nil {
			t.Fatalf("decode upstream request %d: %v", index+1, err)
		}
		if body.Model != wantModels[index] {
			t.Fatalf("upstream request %d model = %q, want %q; body=%s", index+1, body.Model, wantModels[index], received.Body)
		}
	}
}

func TestHandlerHostFailureSkipsGroupForCurrentRequestOnly(t *testing.T) {
	primary := fakeupstream.New(
		fakeupstream.Step{Status: http.StatusInternalServerError, Fixture: "openai/500.json"},
		fakeupstream.Step{Status: http.StatusInternalServerError, Fixture: "openai/500.json"},
		fakeupstream.Step{Status: http.StatusInternalServerError, Fixture: "openai/500.json"},
		fakeupstream.Step{Status: http.StatusInternalServerError, Fixture: "openai/500.json"},
	)
	defer primary.Close()
	backup := fakeupstream.New(
		fakeupstream.Step{Status: http.StatusOK, Fixture: "openai/success.json"},
		fakeupstream.Step{Status: http.StatusOK, Fixture: "openai/success.json"},
	)
	defer backup.Close()

	engine, _ := newDialectGatewayEngine(t, protocol.OpenAI, "gpt-4o",
		dialect.NewSet(dialect.NewOpenAI(http.DefaultClient)),
		dialectGatewayGroup{id: 1, name: "primary", upstreamURL: primary.URL,
			apiKeys: []string{"sk-primary-one", "sk-primary-two"}},
		dialectGatewayGroup{id: 2, name: "backup", upstreamURL: backup.URL,
			apiKeys: []string{"sk-backup"}},
	)
	for range 2 {
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			bytes.NewBufferString(`{"model":"gpt-4o"}`))
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || recorder.Header().Get(debugHeaderAttempts) != "2" {
			t.Fatalf("response = %d attempts=%s body=%s",
				recorder.Code, recorder.Header().Get(debugHeaderAttempts), recorder.Body.String())
		}
	}
	if got := len(primary.Requests()); got != 2 {
		t.Fatalf("primary requests = %d, want one per downstream request", got)
	}
	if got := len(backup.Requests()); got != 2 {
		t.Fatalf("backup requests = %d, want one per downstream request", got)
	}
}

func TestHandlerReturnsLastHostErrorWhenSkippedGroupHasNoBackup(t *testing.T) {
	upstream := fakeupstream.New(
		fakeupstream.Step{Status: http.StatusInternalServerError, Fixture: "openai/500.json"},
		fakeupstream.Step{Status: http.StatusOK, Fixture: "openai/success.json"},
	)
	defer upstream.Close()

	engine, _ := newDialectGatewayEngine(t, protocol.OpenAI, "gpt-4o",
		dialect.NewSet(dialect.NewOpenAI(http.DefaultClient)),
		dialectGatewayGroup{id: 1, name: "only", upstreamURL: upstream.URL,
			apiKeys: []string{"sk-one", "sk-two"}},
	)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewBufferString(`{"model":"gpt-4o"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError || len(upstream.Requests()) != 1 ||
		!bytes.Contains(recorder.Body.Bytes(), []byte("internal_error")) {
		t.Fatalf("response/attempts = %d/%d body=%s, want safe 500/1",
			recorder.Code, len(upstream.Requests()), recorder.Body.String())
	}
}

func TestForwarderRewritesAliasedNonStreamingResponses(t *testing.T) {
	tests := []struct {
		name             string
		value            protocol.Protocol
		dialects         dialect.Set
		path             string
		requestBody      string
		upstreamResponse string
		responseField    string
	}{
		{
			name: "OpenAI", value: protocol.OpenAI,
			dialects: dialect.NewSet(dialect.NewOpenAI(http.DefaultClient)),
			path:     "/v1/chat/completions", requestBody: `{"model":"public-model"}`,
			upstreamResponse: `{"id":"chatcmpl-1","model":"provider-response"}`,
			responseField:    "model",
		},
		{
			name: "Anthropic", value: protocol.Anthropic,
			dialects: dialect.NewSet(dialect.NewAnthropic(http.DefaultClient)),
			path:     "/v1/messages", requestBody: `{"model":"public-model"}`,
			upstreamResponse: `{"type":"message","model":"provider-response"}`,
			responseField:    "model",
		},
		{
			name: "Gemini", value: protocol.Gemini,
			dialects: dialect.NewSet(dialect.NewGemini(http.DefaultClient)),
			path:     "/v1beta/models/public-model:generateContent", requestBody: `{}`,
			upstreamResponse: `{"modelVersion":"provider-response","candidates":[]}`,
			responseField:    "modelVersion",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var receivedPath string
			var receivedHeader http.Header
			var receivedBody []byte
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				receivedPath = request.URL.Path
				receivedHeader = request.Header.Clone()
				receivedBody, _ = io.ReadAll(request.Body)
				writer.Header().Set("Content-Type", "application/json")
				writer.Header().Set("Content-Length", strconv.Itoa(len(test.upstreamResponse)))
				setRepresentationMetadata(writer.Header())
				_, _ = writer.Write([]byte(test.upstreamResponse))
			}))
			defer upstream.Close()

			engine, _ := newDialectGatewayEngine(t, test.value, "public-model", test.dialects,
				dialectGatewayGroup{
					id: 1, name: test.name, upstreamURL: upstream.URL, apiKeys: []string{"provider-key"},
					models: []state.ModelConfig{{ID: "provider-model", Alias: "public-model"}},
				},
			)
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.requestBody))
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("response = %d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
			}
			if receivedHeader.Get("Accept-Encoding") != "identity" {
				t.Fatalf("upstream Accept-Encoding = %q, want identity", receivedHeader.Get("Accept-Encoding"))
			}
			if test.value == protocol.Gemini {
				if receivedPath != "/v1beta/models/provider-model:generateContent" {
					t.Fatalf("upstream path = %q", receivedPath)
				}
			} else {
				var upstreamBody struct {
					Model string `json:"model"`
				}
				if err := json.Unmarshal(receivedBody, &upstreamBody); err != nil || upstreamBody.Model != "provider-model" {
					t.Fatalf("upstream request body/model = %s / %q / %v", receivedBody, upstreamBody.Model, err)
				}
			}
			var downstreamBody map[string]json.RawMessage
			if err := json.Unmarshal(recorder.Body.Bytes(), &downstreamBody); err != nil {
				t.Fatalf("decode downstream response: %v", err)
			}
			var responseModel string
			if err := json.Unmarshal(downstreamBody[test.responseField], &responseModel); err != nil || responseModel != "public-model" {
				t.Fatalf("downstream model = %q, %v; body=%s", responseModel, err, recorder.Body.String())
			}
			if got := recorder.Header().Get("Content-Length"); got != strconv.Itoa(recorder.Body.Len()) {
				t.Fatalf("Content-Length = %q, want %d", got, recorder.Body.Len())
			}
			assertRepresentationMetadata(t, recorder.Header(), false)
		})
	}
}

func TestTransparentModelRoutePreservesWire(t *testing.T) {
	rawResponse := []byte("{\n  \"model\" : \"same-model\", \"n\": 9007199254740993\n}\n")
	var receivedHeader http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		receivedHeader = request.Header.Clone()
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Content-Length", strconv.Itoa(len(rawResponse)))
		setRepresentationMetadata(writer.Header())
		_, _ = writer.Write(rawResponse)
	}))
	defer upstream.Close()

	engine, _ := newDialectGatewayEngine(t, protocol.OpenAI, "same-model",
		dialect.NewSet(dialect.NewOpenAI(http.DefaultClient)),
		dialectGatewayGroup{
			id: 1, name: "transparent", upstreamURL: upstream.URL, apiKeys: []string{"provider-key"},
			models:      []state.ModelConfig{{ID: "same-model"}},
			headerRules: state.HeaderRules{Set: map[string]string{"Accept-Encoding": "gzip"}},
		},
	)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"same-model"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), rawResponse) {
		t.Fatalf("transparent response = %d %q, want %q", recorder.Code, recorder.Body.Bytes(), rawResponse)
	}
	if receivedHeader.Get("Accept-Encoding") != "gzip" {
		t.Fatalf("upstream Accept-Encoding = %q, want HeaderRule gzip", receivedHeader.Get("Accept-Encoding"))
	}
	assertRepresentationMetadata(t, recorder.Header(), true)
}

func TestGatewayRewritesAliasedStreams(t *testing.T) {
	tests := []struct {
		name        string
		value       protocol.Protocol
		dialects    dialect.Set
		path        string
		requestBody string
		streamBody  string
		want        string
		unchanged   string
	}{
		{
			name: "OpenAI", value: protocol.OpenAI,
			dialects: dialect.NewSet(dialect.NewOpenAI(http.DefaultClient)),
			path:     "/v1/chat/completions", requestBody: `{"model":"public-model","stream":true}`,
			streamBody: "data: {\"id\":\"1\",\"model\":\"provider-model\",\"choices\":[]}\n\ndata: [DONE]\n\n",
			want:       `"model":"public-model"`, unchanged: "data: [DONE]\n\n",
		},
		{
			name: "Anthropic", value: protocol.Anthropic,
			dialects: dialect.NewSet(dialect.NewAnthropic(http.DefaultClient)),
			path:     "/v1/messages", requestBody: `{"model":"public-model","stream":true}`,
			streamBody: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"provider-model\"}}\n\n" +
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"unchanged\"}}\n\n",
			want: `"model":"public-model"`, unchanged: "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"unchanged\"}}\n\n",
		},
		{
			name: "Gemini", value: protocol.Gemini,
			dialects: dialect.NewSet(dialect.NewGemini(http.DefaultClient)),
			path:     "/v1beta/models/public-model:streamGenerateContent", requestBody: `{}`,
			streamBody: "data: {\"modelVersion\":\"provider-model\",\"candidates\":[]}\n\n",
			want:       `"modelVersion":"public-model"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var receivedPath string
			var receivedHeader http.Header
			var receivedBody []byte
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				receivedPath = request.URL.Path
				receivedHeader = request.Header.Clone()
				receivedBody, _ = io.ReadAll(request.Body)
				writer.Header().Set("Content-Type", "text/event-stream")
				writer.Header().Set("Content-Length", strconv.Itoa(len(test.streamBody)))
				setRepresentationMetadata(writer.Header())
				_, _ = writer.Write([]byte(test.streamBody))
				writer.(http.Flusher).Flush()
			}))
			defer upstream.Close()

			engine, _ := newDialectGatewayEngine(t, test.value, "public-model", test.dialects,
				dialectGatewayGroup{
					id: 1, name: test.name, upstreamURL: upstream.URL, apiKeys: []string{"provider-key"},
					models: []state.ModelConfig{{ID: "provider-model", Alias: "public-model"}},
				},
			)
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.requestBody))
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), test.want) {
				t.Fatalf("stream response = %d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
			}
			if test.unchanged != "" && !strings.Contains(recorder.Body.String(), test.unchanged) {
				t.Fatalf("stream lost unchanged event: %q", recorder.Body.String())
			}
			if receivedHeader.Get("Accept-Encoding") != "identity" {
				t.Fatalf("upstream Accept-Encoding = %q", receivedHeader.Get("Accept-Encoding"))
			}
			if test.value == protocol.Gemini {
				if receivedPath != "/v1beta/models/provider-model:streamGenerateContent" {
					t.Fatalf("upstream path = %q", receivedPath)
				}
			} else {
				var upstreamBody struct {
					Model string `json:"model"`
				}
				if err := json.Unmarshal(receivedBody, &upstreamBody); err != nil || upstreamBody.Model != "provider-model" {
					t.Fatalf("upstream request model/body = %q / %s / %v", upstreamBody.Model, receivedBody, err)
				}
			}
			if recorder.Header().Get("Content-Length") != "" {
				t.Fatalf("stream Content-Length = %q, want removed", recorder.Header().Get("Content-Length"))
			}
			assertRepresentationMetadata(t, recorder.Header(), false)
		})
	}
}

func TestAnthropicGatewayNonStreamAuthAndForwarding(t *testing.T) {
	for _, test := range []struct {
		name        string
		authHeader  string
		apiKey      string
		version     string
		wantVersion string
	}{
		{name: "Bearer remains Anthropic", authHeader: "Bearer gl-client", wantVersion: anthropicDefaultVersionForTest},
		{name: "x-api-key carrier", apiKey: "gl-client", version: "2024-01-01", wantVersion: "2024-01-01"},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := fakeupstream.New(fakeupstream.Step{Status: http.StatusOK, Fixture: "success.json"})
			defer upstream.Close()
			engine, _ := newDialectGatewayEngine(t, protocol.Anthropic, "claude-3-5-sonnet",
				dialect.NewSet(dialect.NewAnthropic(http.DefaultClient)),
				dialectGatewayGroup{id: 1, name: "anthropic", upstreamURL: upstream.URL, apiKeys: []string{"sk-anthropic-upstream"}},
			)
			body := `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"ping"}]}`
			request := httptest.NewRequest(http.MethodPost, "/v1/messages?trace=true", strings.NewReader(body))
			if test.authHeader != "" {
				request.Header.Set("Authorization", test.authHeader)
			}
			if test.apiKey != "" {
				request.Header.Set("X-Api-Key", test.apiKey)
			}
			if test.version != "" {
				request.Header.Set("Anthropic-Version", test.version)
			}
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK || recorder.Header().Get(debugHeaderAttempts) != "1" ||
				!strings.Contains(recorder.Body.String(), `"type":"message"`) {
				t.Fatalf("response = %d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
			}
			requests := upstream.Requests()
			if len(requests) != 1 {
				t.Fatalf("upstream requests = %d", len(requests))
			}
			got := requests[0]
			if got.Headers.Get("X-Api-Key") != "sk-anthropic-upstream" ||
				got.Headers.Get("Authorization") != "" ||
				got.Headers.Get("Anthropic-Version") != test.wantVersion ||
				got.RawQuery != "trace=true" || string(got.Body) != body {
				t.Fatalf("upstream request = %#v", got)
			}
		})
	}
}

const anthropicDefaultVersionForTest = "2023-06-01"

func TestAnthropicGatewayFailover(t *testing.T) {
	upstream := fakeupstream.New(
		fakeupstream.Step{Status: http.StatusUnauthorized, Fixture: "401.json"},
		fakeupstream.Step{Status: http.StatusOK, Fixture: "success.json"},
	)
	defer upstream.Close()
	engine, _ := newDialectGatewayEngine(t, protocol.Anthropic, "claude-3-5-sonnet",
		dialect.NewSet(dialect.NewAnthropic(http.DefaultClient)),
		dialectGatewayGroup{id: 1, name: "anthropic", upstreamURL: upstream.URL, apiKeys: []string{"sk-anthropic-one", "sk-anthropic-two"}},
	)
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-3-5-sonnet"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get(debugHeaderAttempts) != "2" {
		t.Fatalf("response = %d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	requests := upstream.Requests()
	if len(requests) != 2 || requests[0].Headers.Get("X-Api-Key") != "sk-anthropic-one" ||
		requests[1].Headers.Get("X-Api-Key") != "sk-anthropic-two" {
		t.Fatalf("upstream requests = %#v", requests)
	}
	for _, secret := range []string{"sk-anthropic-one", "sk-anthropic-two"} {
		if strings.Contains(recorder.Body.String(), secret) || strings.Contains(recorder.Header().Get(debugHeaderKey), secret) {
			t.Fatalf("response exposes %q", secret)
		}
	}

	var clientErrorRequests atomic.Int64
	clientError := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		clientErrorRequests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"function calling is not supported with this model"}}`))
	}))
	defer clientError.Close()
	engine, _ = newDialectGatewayEngine(t, protocol.Anthropic, "claude-3-5-sonnet",
		dialect.NewSet(dialect.NewAnthropic(http.DefaultClient)),
		dialectGatewayGroup{id: 1, name: "anthropic", upstreamURL: clientError.URL, apiKeys: []string{"sk-one", "sk-two"}},
	)
	request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-3-5-sonnet"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || clientErrorRequests.Load() != 1 || recorder.Header().Get(debugHeaderAttempts) != "1" {
		t.Fatalf("client error response = %d attempts=%s requests=%d body=%s", recorder.Code, recorder.Header().Get(debugHeaderAttempts), clientErrorRequests.Load(), recorder.Body.String())
	}
}

func TestAnthropicGatewayStream(t *testing.T) {
	firstEventSent := make(chan struct{})
	release := make(chan struct{})
	requestHeaders := make(chan http.Header, 1)
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	primary := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		requestHeaders <- request.Header.Clone()
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\"}\n\n"))
		writer.(http.Flusher).Flush()
		close(firstEventSent)
		<-release
		_, _ = writer.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
		writer.(http.Flusher).Flush()
	}))
	defer primary.Close()
	backup := fakeupstream.New(fakeupstream.Step{Status: http.StatusOK, Fixture: "stream.sse", Stream: true})
	defer backup.Close()

	engine, _ := newDialectGatewayEngine(t, protocol.Anthropic, "claude-3-5-sonnet",
		dialect.NewSet(dialect.NewAnthropic(http.DefaultClient)),
		dialectGatewayGroup{
			id: 1, name: "primary", upstreamURL: primary.URL, apiKeys: []string{"sk-primary"},
			headerRules: state.HeaderRules{Set: map[string]string{"Accept-Encoding": "gzip"}},
		},
		dialectGatewayGroup{id: 2, name: "backup", upstreamURL: backup.URL, apiKeys: []string{"sk-backup"}},
	)
	gatewayServer := httptest.NewServer(engine)
	defer gatewayServer.Close()

	request, err := http.NewRequest(http.MethodPost, gatewayServer.URL+"/v1/messages", strings.NewReader(`{"model":"claude-3-5-sonnet","stream":true}`))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer gl-client")
	response, err := gatewayServer.Client().Do(request)
	if err != nil {
		t.Fatalf("stream request: %v", err)
	}
	defer response.Body.Close()
	select {
	case <-firstEventSent:
	case <-time.After(time.Second):
		t.Fatal("first upstream event was not sent")
	}
	reader := bufio.NewReader(response.Body)
	firstLine, err := reader.ReadString('\n')
	if err != nil || firstLine != "event: message_start\n" {
		t.Fatalf("first streamed line = %q, %v", firstLine, err)
	}
	headers := <-requestHeaders
	if headers.Get("Accept-Encoding") != "identity" || headers.Get("X-Api-Key") != "sk-primary" {
		t.Fatalf("upstream headers = %#v", headers)
	}
	if len(backup.Requests()) != 0 || response.Header.Get(debugHeaderAttempts) != "1" {
		t.Fatalf("backup requests=%d headers=%v", len(backup.Requests()), response.Header)
	}
	releaseOnce.Do(func() { close(release) })
	rest, err := io.ReadAll(reader)
	if err != nil || !strings.Contains(string(rest), "message_stop") || strings.Contains(string(rest), `"code":`) {
		t.Fatalf("remaining stream = %q, %v", rest, err)
	}
	if response.Header.Get(debugHeaderKey) != utils.MaskAPIKey("sk-primary") {
		t.Fatalf("debug key = %q", response.Header.Get(debugHeaderKey))
	}
}

func TestGeminiGatewayNonStreamAuthAndQuery(t *testing.T) {
	for _, test := range []struct {
		name       string
		target     string
		authHeader string
		googKey    string
		body       string
	}{
		{
			name:   "query key authenticates without forwarding",
			target: "/v1beta/models/gemini-2.5-pro:generateContent?key=gl-client&alt=json&trace=true",
			body:   "{",
		},
		{
			name:    "x-goog carrier remains Gemini",
			target:  "/v1beta/models/gemini-2.5-pro:generateContent?alt=json&trace=true",
			googKey: "gl-client",
			body:    `{"model":"different-model"}`,
		},
		{
			name:       "Bearer carrier remains Gemini",
			target:     "/v1beta/models/gemini-2.5-pro:generateContent?alt=json&trace=true",
			authHeader: "Bearer gl-client",
			body:       `{}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := fakeupstream.New(fakeupstream.Step{Status: http.StatusOK, Fixture: "success.json"})
			defer upstream.Close()
			engine, _ := newDialectGatewayEngine(t, protocol.Gemini, "gemini-2.5-pro",
				dialect.NewSet(dialect.NewGemini(http.DefaultClient)),
				dialectGatewayGroup{
					id: 1, name: "gemini", upstreamURL: upstream.URL + "?tenant=base&alt=proto",
					apiKeys: []string{"gemini-upstream-key"},
				},
			)
			request := httptest.NewRequest(http.MethodPost, test.target, strings.NewReader(test.body))
			if test.authHeader != "" {
				request.Header.Set("Authorization", test.authHeader)
			}
			if test.googKey != "" {
				request.Header.Set("X-Goog-Api-Key", test.googKey)
			}
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK || recorder.Header().Get(debugHeaderAttempts) != "1" {
				t.Fatalf("response = %d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
			}
			requests := upstream.Requests()
			if len(requests) != 1 {
				t.Fatalf("upstream requests = %d", len(requests))
			}
			got := requests[0]
			query := gotQuery(t, got.RawQuery)
			if got.Path != "/v1beta/models/gemini-2.5-pro:generateContent" ||
				got.Headers.Get("X-Goog-Api-Key") != "gemini-upstream-key" ||
				got.Headers.Get("Authorization") != "" ||
				query.Get("key") != "" || query.Get("alt") != "" ||
				query.Get("tenant") != "base" || query.Get("trace") != "true" ||
				string(got.Body) != test.body {
				t.Fatalf("upstream request = %#v query=%v", got, query)
			}
		})
	}
}

func TestGeminiGatewayFailover(t *testing.T) {
	upstream := fakeupstream.New(
		fakeupstream.Step{Status: http.StatusUnauthorized, Fixture: "401.json"},
		fakeupstream.Step{Status: http.StatusOK, Fixture: "success.json"},
	)
	defer upstream.Close()
	engine, _ := newDialectGatewayEngine(t, protocol.Gemini, "gemini-2.5-pro",
		dialect.NewSet(dialect.NewGemini(http.DefaultClient)),
		dialectGatewayGroup{id: 1, name: "gemini", upstreamURL: upstream.URL, apiKeys: []string{"gemini-key-one", "gemini-key-two"}},
	)
	request := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-pro:generateContent?trace=true", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get(debugHeaderAttempts) != "2" {
		t.Fatalf("response = %d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	requests := upstream.Requests()
	if len(requests) != 2 || requests[0].Headers.Get("X-Goog-Api-Key") != "gemini-key-one" ||
		requests[1].Headers.Get("X-Goog-Api-Key") != "gemini-key-two" ||
		requests[0].Path != requests[1].Path || requests[0].RawQuery != requests[1].RawQuery {
		t.Fatalf("upstream requests = %#v", requests)
	}
}

func TestGeminiGatewayStream(t *testing.T) {
	upstream := fakeupstream.New(fakeupstream.Step{Status: http.StatusOK, Fixture: "stream.sse", Stream: true})
	defer upstream.Close()
	engine, _ := newDialectGatewayEngine(t, protocol.Gemini, "gemini-2.5-pro",
		dialect.NewSet(dialect.NewGemini(http.DefaultClient)),
		dialectGatewayGroup{
			id: 1, name: "gemini-stream", upstreamURL: upstream.URL + "?tenant=base&alt=json",
			apiKeys:     []string{"gemini-stream-key"},
			headerRules: state.HeaderRules{Set: map[string]string{"Accept-Encoding": "gzip"}},
		},
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1beta/models/gemini-2.5-pro:streamGenerateContent?key=gl-client&alt=xml&trace=true",
		strings.NewReader(`{}`),
	)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get(debugHeaderAttempts) != "1" ||
		!strings.Contains(recorder.Body.String(), "data:") || strings.Contains(recorder.Body.String(), `"code":`) {
		t.Fatalf("response = %d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	requests := upstream.Requests()
	if len(requests) != 1 {
		t.Fatalf("upstream requests = %d", len(requests))
	}
	query := gotQuery(t, requests[0].RawQuery)
	if got := query["alt"]; len(got) != 1 || got[0] != "sse" {
		t.Fatalf("alt = %#v", got)
	}
	if query.Get("key") != "" || query.Get("trace") != "true" || query.Get("tenant") != "base" ||
		requests[0].Headers.Get("Accept-Encoding") != "identity" ||
		requests[0].Headers.Get("X-Goog-Api-Key") != "gemini-stream-key" {
		t.Fatalf("upstream request = %#v query=%v", requests[0], query)
	}
}

func TestGeminiGatewayCompressed(t *testing.T) {
	t.Run("bad candidate fails over before commit", func(t *testing.T) {
		compressed := fakeupstream.New(fakeupstream.Step{
			Status: http.StatusOK, Fixture: "stream.sse", Stream: true,
			Headers: http.Header{"Content-Encoding": {"gzip"}},
		})
		defer compressed.Close()
		backup := fakeupstream.New(fakeupstream.Step{Status: http.StatusOK, Fixture: "stream.sse", Stream: true})
		defer backup.Close()
		engine, _ := newDialectGatewayEngine(t, protocol.Gemini, "gemini-2.5-pro",
			dialect.NewSet(dialect.NewGemini(http.DefaultClient)),
			dialectGatewayGroup{id: 1, name: "compressed", upstreamURL: compressed.URL, apiKeys: []string{"bad-key"}},
			dialectGatewayGroup{id: 2, name: "backup", upstreamURL: backup.URL, apiKeys: []string{"good-key"}},
		)
		request := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-pro:streamGenerateContent", strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || recorder.Header().Get(debugHeaderAttempts) != "2" ||
			len(compressed.Requests()) != 1 || len(backup.Requests()) != 1 {
			t.Fatalf("response = %d headers=%v compressed=%d backup=%d body=%s", recorder.Code, recorder.Header(), len(compressed.Requests()), len(backup.Requests()), recorder.Body.String())
		}
	})

	t.Run("all bad candidates return fixed protocol error", func(t *testing.T) {
		first := fakeupstream.New(fakeupstream.Step{
			Status: http.StatusOK, Fixture: "stream.sse", Stream: true,
			Headers: http.Header{"Content-Encoding": {"gzip"}},
		})
		defer first.Close()
		second := fakeupstream.New(fakeupstream.Step{
			Status: http.StatusOK, Fixture: "stream.sse", Stream: true,
			Headers: http.Header{"Content-Encoding": {"br"}},
		})
		defer second.Close()
		engine, registry := newDialectGatewayEngine(t, protocol.Gemini, "gemini-2.5-pro",
			dialect.NewSet(dialect.NewGemini(http.DefaultClient)),
			dialectGatewayGroup{id: 1, name: "first", upstreamURL: first.URL, apiKeys: []string{"secret-one"}},
			dialectGatewayGroup{id: 2, name: "second", upstreamURL: second.URL, apiKeys: []string{"secret-two"}},
		)
		request := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-pro:streamGenerateContent", strings.NewReader(`{}`))
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadGateway || recorder.Header().Get(debugHeaderAttempts) != "2" ||
			!strings.Contains(recorder.Body.String(), reasonUpstreamProtocol.Code) ||
			len(registry.CollectCandidates([]uint{1, 2}, nil, time.Time{})) != 2 {
			t.Fatalf("response = %d headers=%v candidates=%d body=%s", recorder.Code, recorder.Header(), len(registry.CollectCandidates([]uint{1, 2}, nil, time.Time{})), recorder.Body.String())
		}
		for _, forbidden := range []string{"secret-one", "secret-two", "data:"} {
			if strings.Contains(recorder.Body.String(), forbidden) {
				t.Fatalf("response exposes %q: %s", forbidden, recorder.Body.String())
			}
		}
	})
}

func gotQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	values, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("parse query %q: %v", raw, err)
	}
	return values
}
