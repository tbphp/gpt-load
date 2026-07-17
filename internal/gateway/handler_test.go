package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/encryption"
	platformhttp "gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/testutil/fakeupstream"
)

type scriptedForwarder struct {
	results []UpstreamResult
	inputs  []ForwardInput
	onCall  func(int)
}

type mutatingRuntimeRegistry struct {
	*state.KeyRegistry
	mutate  func()
	mutated bool
}

func (registry *mutatingRuntimeRegistry) CollectCandidates(
	groupIDs []uint,
	excluded func(uint) bool,
) []state.KeyMeta {
	candidates := registry.KeyRegistry.CollectCandidates(groupIDs, excluded)
	if !registry.mutated && len(candidates) > 0 {
		registry.mutated = true
		registry.mutate()
	}
	return candidates
}

func (forwarder *scriptedForwarder) Forward(_ context.Context, input ForwardInput) UpstreamResult {
	index := len(forwarder.inputs)
	forwarder.inputs = append(forwarder.inputs, input)
	if forwarder.onCall != nil {
		forwarder.onCall(index)
	}
	if index >= len(forwarder.results) {
		return UpstreamResult{Err: errors.New("script exhausted")}
	}
	return forwarder.results[index]
}

func TestHandlerReturnsStableTerminalReasons(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		accessKey    string
		body         string
		upstreamKeys []string
		results      []UpstreamResult
		wantStatus   int
		wantCode     string
	}{
		{name: "invalid access key", path: "/v1/chat/completions", accessKey: "wrong", body: `{"model":"gpt-4o"}`, wantStatus: http.StatusUnauthorized, wantCode: "invalid_access_key"},
		{name: "unknown endpoint after auth", path: "/unknown", accessKey: "gl-client", body: `{}`, wantStatus: http.StatusNotFound, wantCode: "protocol_endpoint_not_found"},
		{name: "cannot extract model", path: "/v1/chat/completions", accessKey: "gl-client", body: `{}`, wantStatus: http.StatusBadRequest, wantCode: "cannot_extract_model"},
		{name: "no candidate", path: "/v1/chat/completions", accessKey: "gl-client", body: `{"model":"gpt-4o"}`, wantStatus: http.StatusServiceUnavailable, wantCode: "no_available_candidate"},
		{
			name: "post-write timeout",
			path: "/v1/chat/completions", accessKey: "gl-client", body: `{"model":"gpt-4o"}`,
			upstreamKeys: []string{"sk-one"},
			results:      []UpstreamResult{{Err: context.DeadlineExceeded, RequestWritten: true}},
			wantStatus:   http.StatusGatewayTimeout, wantCode: "upstream_timeout",
		},
		{
			name: "connection attempts exhausted",
			path: "/v1/chat/completions", accessKey: "gl-client", body: `{"model":"gpt-4o"}`,
			upstreamKeys: []string{"sk-one", "sk-two"},
			results: []UpstreamResult{
				{Err: errors.New("dial failed")},
				{Err: errors.New("dial failed")},
			},
			wantStatus: http.StatusBadGateway, wantCode: "upstream_connect_failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: tt.results}
			engine, _, _ := newHandlerTestRuntime(t, forwarder, tt.upstreamKeys...)
			request := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewBufferString(tt.body))
			request.Header.Set("Authorization", "Bearer "+tt.accessKey)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			var body struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode reason body: %v", err)
			}
			if body.Code != tt.wantCode {
				t.Fatalf("code = %q, want %q", body.Code, tt.wantCode)
			}
		})
	}
}

func TestHandlerStripsDownstreamQueryCredentialBeforeForwarding(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true,
	}}}
	engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one")
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions?key=gl-client&trace=true", bytes.NewBufferString(`{"model":"gpt-4o"}`))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || len(forwarder.inputs) != 1 {
		t.Fatalf("response/input count = %d/%d", recorder.Code, len(forwarder.inputs))
	}
	if got := forwarder.inputs[0].Request.RawQuery; got != "trace=true" {
		t.Fatalf("forward RawQuery = %q, want trace=true", got)
	}
}

func TestHandlerPreservesRawQueryBytesAfterStrippingCredential(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true,
	}}}
	engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one")
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o"}`))
	request.URL.RawQuery = "trace=first&key=gl-client&filter=%ZZ&sig=a%2Fb&z=last"
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || len(forwarder.inputs) != 1 {
		t.Fatalf("response/input count = %d/%d", recorder.Code, len(forwarder.inputs))
	}
	const want = "trace=first&filter=%ZZ&sig=a%2Fb&z=last"
	if got := forwarder.inputs[0].Request.RawQuery; got != want {
		t.Fatalf("forward RawQuery = %q, want %q", got, want)
	}
}

func TestHandlerRetries401WithAnotherKeyThenReturnsSuccess(t *testing.T) {
	upstream := fakeupstream.New(
		fakeupstream.Step{Status: http.StatusUnauthorized, Fixture: "openai/401.json"},
		fakeupstream.Step{Status: http.StatusOK, Fixture: "openai/success.json"},
	)
	defer upstream.Close()

	engine := newRealGatewayEngine(t, upstream.URL, "sk-first", "sk-second")
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte("chatcmpl-test")) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	requests := upstream.Requests()
	if len(requests) != 2 {
		t.Fatalf("upstream requests = %d, want 2", len(requests))
	}
	first := requests[0].Headers.Get("Authorization")
	second := requests[1].Headers.Get("Authorization")
	if first == second || first == "" || second == "" {
		t.Fatalf("upstream credentials = %q then %q, want two distinct keys", first, second)
	}
	for _, credential := range []string{first, second} {
		if strings.Contains(credential, "gl-client") {
			t.Fatalf("downstream access key reached upstream: %q", credential)
		}
	}
}

func TestHandlerReturnsLastUpstreamResponseWhenBudgetIsExhausted(t *testing.T) {
	upstream := fakeupstream.New(
		fakeupstream.Step{Status: http.StatusUnauthorized, Fixture: "openai/401.json"},
		fakeupstream.Step{Status: http.StatusTooManyRequests, Fixture: "openai/429.json"},
		fakeupstream.Step{Status: http.StatusInternalServerError, Fixture: "openai/500.json"},
	)
	defer upstream.Close()

	engine := newRealGatewayEngine(t, upstream.URL, "sk-one", "sk-two", "sk-three", "sk-unused")
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o"}`))
	request.Header.Set("X-Api-Key", "gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError || len(upstream.Requests()) != maxAttempts {
		t.Fatalf("response/attempts = %d/%d, want 500/%d", recorder.Code, len(upstream.Requests()), maxAttempts)
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte("internal_error")) {
		t.Fatalf("body = %s, want final upstream fixture", recorder.Body.String())
	}
}

func TestHandlerKeepsFrozenSnapshotAcrossRetry(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: []byte(`{"error":"invalid_api_key"}`), ClassificationBody: []byte(`{"error":"invalid_api_key"}`), RequestWritten: true},
		{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true},
	}}
	engine, manager, _ := newHandlerTestRuntime(t, forwarder, "sk-one", "sk-two")
	keyService, err := encryption.NewService("handler-test-master-key")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	forwarder.onCall = func(index int) {
		if index != 0 {
			return
		}
		if _, err := manager.Publish(state.CompileInput{AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: keyService.Hash("gl-client"), Status: state.AccessKeyStatusActive,
		}}}); err != nil {
			t.Fatalf("Publish() during request error = %v", err)
		}
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || len(forwarder.inputs) != 2 {
		t.Fatalf("response/attempts = %d/%d, want 200/2", recorder.Code, len(forwarder.inputs))
	}
	if current := manager.Current(); current == nil || len(current.Groups) != 0 {
		t.Fatalf("current snapshot = %#v, want newly published empty groups", current)
	}
}

func TestHandlerSkipsCandidateChangedAfterCollection(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *state.KeyRegistry, encryption.Service)
	}{
		{
			name: "key moved to another group",
			mutate: func(t *testing.T, registry *state.KeyRegistry, keyService encryption.Service) {
				t.Helper()
				encrypted, err := keyService.Encrypt("sk-group-two")
				if err != nil {
					t.Fatalf("Encrypt(group two key) error = %v", err)
				}
				if err := registry.Replace([]state.KeyEntry{{
					ID: 1, GroupID: 2, Status: state.KeyStatusActive, EncryptedValue: encrypted,
				}}); err != nil {
					t.Fatalf("Replace(moved key) error = %v", err)
				}
			},
		},
		{
			name: "key disabled",
			mutate: func(t *testing.T, registry *state.KeyRegistry, _ encryption.Service) {
				t.Helper()
				if err := registry.SetKeyStatus(1, state.KeyStatusDisabled); err != nil {
					t.Fatalf("SetKeyStatus(disabled) error = %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: []UpstreamResult{{
				StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true,
			}}}
			_, manager, registry := newHandlerTestRuntime(t, forwarder, "sk-group-one")
			keyService, err := encryption.NewService("handler-test-master-key")
			if err != nil {
				t.Fatalf("NewService() error = %v", err)
			}
			runtimeRegistry := &mutatingRuntimeRegistry{
				KeyRegistry: registry,
				mutate:      func() { tt.mutate(t, registry, keyService) },
			}
			openAI := dialect.NewOpenAI(http.DefaultClient)
			handler := NewHandler(manager, registry, keyService, forwarder, NewDialectSet(openAI))
			handler.registry = runtimeRegistry
			handler.newRandom = func() *rand.Rand { return rand.New(rand.NewSource(1)) }
			engine := gin.New()
			handler.RegisterRoutes(engine)

			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o"}`))
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusServiceUnavailable || len(forwarder.inputs) != 0 {
				t.Fatalf("response/attempts = %d/%d, want 503/0; body=%s", recorder.Code, len(forwarder.inputs), recorder.Body.String())
			}
			var body struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Code != reasonNoCandidate.Code {
				t.Fatalf("response code = %q, want %q", body.Code, reasonNoCandidate.Code)
			}
		})
	}
}

func newRealGatewayEngine(t *testing.T, upstreamURL string, upstreamKeys ...string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	keyService, err := encryption.NewService("handler-test-master-key")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	manager := state.NewManager()
	if _, err := manager.Publish(state.CompileInput{
		Groups: []state.GroupConfig{{
			ID: 1, Name: "openai", UpstreamURL: upstreamURL,
			Protocols: []protocol.Protocol{protocol.OpenAI},
			Models:    []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true,
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: keyService.Hash("gl-client"),
			Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	registry := state.NewKeyRegistry()
	entries := make([]state.KeyEntry, 0, len(upstreamKeys))
	for index, plaintext := range upstreamKeys {
		encrypted, err := keyService.Encrypt(plaintext)
		if err != nil {
			t.Fatalf("Encrypt() error = %v", err)
		}
		entries = append(entries, state.KeyEntry{
			ID: uint(index + 1), GroupID: 1, Status: state.KeyStatusActive, EncryptedValue: encrypted,
		})
	}
	if err := registry.Replace(entries); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}

	clients := platformhttp.NewHTTPClientManager()
	openAI := dialect.NewOpenAI(clients.GetClient(testDialectClientConfig()))
	handler := NewHandler(
		manager,
		registry,
		keyService,
		NewForwarder(clients, redact.New()),
		NewDialectSet(openAI),
	)
	handler.newRandom = func() *rand.Rand { return rand.New(rand.NewSource(1)) }
	engine := gin.New()
	handler.RegisterRoutes(engine)
	return engine
}

func testDialectClientConfig() *platformhttp.Config {
	return &platformhttp.Config{
		ConnectTimeout: time.Second, ResponseHeaderTimeout: time.Second,
		IdleConnTimeout: time.Second, MaxIdleConns: 10, MaxIdleConnsPerHost: 10,
		DisableCompression: true, ForceAttemptHTTP2: true,
		TLSHandshakeTimeout: time.Second, ExpectContinueTimeout: time.Second,
	}
}

func newHandlerTestRuntime(
	t *testing.T,
	forwarder AttemptForwarder,
	upstreamKeys ...string,
) (*gin.Engine, *state.Manager, *state.KeyRegistry) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	keyService, err := encryption.NewService("handler-test-master-key")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	manager := state.NewManager()
	if _, err := manager.Publish(state.CompileInput{
		Groups: []state.GroupConfig{{
			ID: 1, Name: "openai", UpstreamURL: "http://upstream.invalid",
			Protocols: []protocol.Protocol{protocol.OpenAI},
			Models:    []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true,
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: keyService.Hash("gl-client"),
			Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	registry := state.NewKeyRegistry()
	entries := make([]state.KeyEntry, 0, len(upstreamKeys))
	for index, plaintext := range upstreamKeys {
		encrypted, err := keyService.Encrypt(plaintext)
		if err != nil {
			t.Fatalf("Encrypt() error = %v", err)
		}
		entries = append(entries, state.KeyEntry{
			ID: uint(index + 1), GroupID: 1, Status: state.KeyStatusActive, EncryptedValue: encrypted,
		})
	}
	if err := registry.Replace(entries); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}

	openAI := dialect.NewOpenAI(http.DefaultClient)
	handler := NewHandler(manager, registry, keyService, forwarder, NewDialectSet(openAI))
	handler.newRandom = func() *rand.Rand { return rand.New(rand.NewSource(1)) }
	engine := gin.New()
	handler.RegisterRoutes(engine)
	return engine, manager, registry
}
