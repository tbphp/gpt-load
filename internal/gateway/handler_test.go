package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/encryption"
	platformhttp "gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/testutil/fakeupstream"
)

type scriptedForwarder struct {
	results       []UpstreamResult
	streamResults []UpstreamResult
	inputs        []ForwardInput
	streamInputs  []ForwardInput
	onCall        func(int)
	onStreamCall  func(int, http.ResponseWriter)
}

type mutatingRuntimeRegistry struct {
	*state.KeyRegistry
	mutate  func()
	mutated bool
}

func (registry *mutatingRuntimeRegistry) CollectCandidates(
	groupIDs []uint,
	excluded func(uint) bool,
	now time.Time,
) []state.KeyMeta {
	candidates := registry.KeyRegistry.CollectCandidates(groupIDs, excluded, now)
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

func (forwarder *scriptedForwarder) ForwardStream(
	_ context.Context,
	input ForwardInput,
	writer http.ResponseWriter,
) UpstreamResult {
	index := len(forwarder.streamInputs)
	forwarder.streamInputs = append(forwarder.streamInputs, input)
	if forwarder.onStreamCall != nil {
		forwarder.onStreamCall(index, writer)
	}
	if index >= len(forwarder.streamResults) {
		return UpstreamResult{Err: errors.New("stream script exhausted")}
	}
	return forwarder.streamResults[index]
}

func TestHandlerInitializesDebugHeadersBeforeValidation(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		accessKey string
		body      string
	}{
		{name: "invalid auth", path: "/v1/chat/completions", accessKey: "wrong", body: `{"model":"gpt-4o"}`},
		{name: "unknown endpoint", path: "/unknown", accessKey: "gl-client", body: `{}`},
		{name: "invalid model", path: "/v1/chat/completions", accessKey: "gl-client", body: `{}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{}
			engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one")
			request := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewBufferString(tt.body))
			request.Header.Set("Authorization", "Bearer "+tt.accessKey)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			assertDebugHeaders(t, recorder.Header(), "", "", "0")
			if len(forwarder.inputs)+len(forwarder.streamInputs) != 0 {
				t.Fatal("validation failure reached upstream forwarder")
			}
		})
	}
}

func TestHandlerRejectsCaseCollidingModelBeforeAttempt(t *testing.T) {
	forwarder := &scriptedForwarder{}
	engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one")
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewBufferString(`{"model":"forbidden","Model":"allowed"}`),
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest ||
		!strings.Contains(recorder.Body.String(), `"code":"cannot_extract_model"`) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	if len(forwarder.inputs)+len(forwarder.streamInputs) != 0 {
		t.Fatal("case-colliding request reached upstream")
	}
}

func TestHandlerServesLocalModelEndpoints(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		target   string
		headers  http.Header
		expected string
	}{
		{
			name: "OpenAI with Bearer", method: http.MethodGet, target: "/v1/models",
			headers:  http.Header{"Authorization": {"Bearer gl-client"}},
			expected: `{"object":"list","data":[{"id":"alpha","object":"model","created":1735689600,"owned_by":"gpt-load"},{"id":"beta","object":"model","created":1735689600,"owned_by":"gpt-load"},{"id":"zeta","object":"model","created":1735689600,"owned_by":"gpt-load"}]}`,
		},
		{
			name: "Anthropic with Bearer", method: http.MethodGet, target: "/v1/models",
			headers:  http.Header{"Authorization": {"Bearer gl-client"}, "Anthropic-Version": {"2023-06-01"}},
			expected: `{"data":[{"type":"model","id":"alpha","display_name":"alpha","created_at":"2025-01-01T00:00:00Z"},{"type":"model","id":"beta","display_name":"beta","created_at":"2025-01-01T00:00:00Z"},{"type":"model","id":"zeta","display_name":"zeta","created_at":"2025-01-01T00:00:00Z"}],"has_more":false,"first_id":"alpha","last_id":"zeta"}`,
		},
		{
			name: "x-api-key alone stays OpenAI", method: http.MethodGet, target: "/v1/models",
			headers:  http.Header{"X-Api-Key": {"gl-client"}},
			expected: `{"object":"list","data":[{"id":"alpha","object":"model","created":1735689600,"owned_by":"gpt-load"},{"id":"beta","object":"model","created":1735689600,"owned_by":"gpt-load"},{"id":"zeta","object":"model","created":1735689600,"owned_by":"gpt-load"}]}`,
		},
		{
			name: "Anthropic with x-api-key", method: http.MethodGet, target: "/v1/models",
			headers:  http.Header{"X-Api-Key": {"gl-client"}, "Anthropic-Version": {"2023-06-01"}},
			expected: `{"data":[{"type":"model","id":"alpha","display_name":"alpha","created_at":"2025-01-01T00:00:00Z"},{"type":"model","id":"beta","display_name":"beta","created_at":"2025-01-01T00:00:00Z"},{"type":"model","id":"zeta","display_name":"zeta","created_at":"2025-01-01T00:00:00Z"}],"has_more":false,"first_id":"alpha","last_id":"zeta"}`,
		},
		{
			name: "Gemini with query key", method: http.MethodGet, target: "/v1beta/models?key=gl-client",
			expected: `{"models":[{"name":"models/alpha"},{"name":"models/zeta"}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := newModelListHandlerEngine(t, state.FilterSet{})
			request := httptest.NewRequest(test.method, test.target, nil)
			request.Header = test.headers.Clone()
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
			}
			assertJSONEqual(t, recorder.Body.String(), test.expected)
			assertDebugHeaders(t, recorder.Header(), "", "", "0")
		})
	}
}

func TestHandlerModelEndpointsApplyFiltersAndKeepEmptyShape(t *testing.T) {
	t.Run("joint filters", func(t *testing.T) {
		engine := newModelListHandlerEngine(t, state.FilterSet{
			Protocols: map[protocol.Protocol]struct{}{protocol.OpenAI: {}},
			Models:    map[string]struct{}{"alpha": {}},
			Groups:    map[uint]struct{}{1: {}},
		})
		request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		assertJSONEqual(t, recorder.Body.String(), `{"object":"list","data":[{"id":"alpha","object":"model","created":1735689600,"owned_by":"gpt-load"}]}`)
		assertDebugHeaders(t, recorder.Header(), "", "", "0")
	})

	t.Run("protocol denied keeps official empty envelope", func(t *testing.T) {
		engine := newModelListHandlerEngine(t, state.FilterSet{
			Protocols: map[protocol.Protocol]struct{}{protocol.Gemini: {}},
		})
		request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		request.Header.Set("X-Api-Key", "gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		assertJSONEqual(t, recorder.Body.String(), `{"object":"list","data":[]}`)
		assertDebugHeaders(t, recorder.Header(), "", "", "0")
	})
}

func TestHandlerModelEndpointsRequireValidAccessKey(t *testing.T) {
	engine := newModelListHandlerEngine(t, state.FilterSet{})
	for _, header := range []string{"", "Bearer wrong"} {
		request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		if header != "" {
			request.Header.Set("Authorization", header)
		}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Body.String(), reasonInvalidAccessKey.Code) {
			t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
		}
		assertDebugHeaders(t, recorder.Header(), "", "", "0")
	}
}

func TestHandlerModelEndpointHasNoDataPlaneSideEffects(t *testing.T) {
	keyService, err := encryption.NewService("model-handler-test-master-key")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	manager := state.NewManager()
	if _, err := manager.Publish(state.CompileInput{
		Groups: []state.GroupConfig{{
			ID: 1, Name: "openai", UpstreamURL: "https://unused.example.com",
			Protocols: []protocol.Protocol{protocol.OpenAI},
			Models:    []state.ModelConfig{{ID: "alpha"}}, Enabled: true,
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: keyService.Hash("gl-client"), Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	spyEncryption := &decryptPanicEncryption{Service: keyService}
	handler := NewHandler(manager, state.NewKeyRegistry(), spyEncryption, panicForwarder{}, dialect.NewSet())
	handler.registry = panicRuntimeRegistry{}
	engine := gin.New()
	handler.RegisterRoutes(engine)

	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer gl-client")
	request.Body = panicReadCloser{}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	assertJSONEqual(t, recorder.Body.String(), `{"object":"list","data":[{"id":"alpha","object":"model","created":1735689600,"owned_by":"gpt-load"}]}`)
}

func TestHandlerModelListOverflowReturnsSmallStableErrorWithoutPartialJSON(t *testing.T) {
	engine := newModelListHandlerEngineWithLimit(t, state.FilterSet{}, 64)
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer gl-client")
	request.Header.Set("Anthropic-Version", "2023-06-01")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError || recorder.Body.Len() > 256 ||
		!strings.Contains(recorder.Body.String(), `"code":"model_list_too_large"`) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	for _, fragment := range []string{`"type":"model"`, `"id":"alpha"`, `"id":"beta"`, `"id":"zeta"`} {
		if strings.Contains(recorder.Body.String(), fragment) {
			t.Fatalf("overflow response contains partial model fragment %q: %s", fragment, recorder.Body.String())
		}
	}
}

func newModelListHandlerEngine(t *testing.T, filters state.FilterSet) *gin.Engine {
	return newModelListHandlerEngineWithLimit(t, filters, maxNonStreamingResponseBodyBytes)
}

func newModelListHandlerEngineWithLimit(
	t *testing.T,
	filters state.FilterSet,
	limit int64,
) *gin.Engine {
	t.Helper()
	keyService, err := encryption.NewService("model-handler-test-master-key")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	manager := state.NewManager()
	if _, err := manager.Publish(state.CompileInput{
		Groups: []state.GroupConfig{
			{
				ID: 1, Name: "multi", UpstreamURL: "https://multi.example.com",
				Protocols: []protocol.Protocol{protocol.OpenAI, protocol.Anthropic, protocol.Gemini},
				Models:    []state.ModelConfig{{ID: "zeta"}, {ID: "alpha"}}, Enabled: true,
			},
			{
				ID: 2, Name: "openai", UpstreamURL: "https://openai.example.com",
				Protocols: []protocol.Protocol{protocol.OpenAI, protocol.Anthropic},
				Models:    []state.ModelConfig{{ID: "beta"}}, Enabled: true,
			},
		},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: keyService.Hash("gl-client"),
			Status: state.AccessKeyStatusActive, Filters: filters,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	handler := NewHandler(
		manager, state.NewKeyRegistry(), keyService, &scriptedForwarder{}, dialect.NewSet(),
	)
	handler.modelListLimit = limit
	engine := gin.New()
	handler.RegisterRoutes(engine)
	return engine
}

type panicRuntimeRegistry struct{}

func (panicRuntimeRegistry) CollectCandidates([]uint, func(uint) bool, time.Time) []state.KeyMeta {
	panic("model endpoint collected upstream candidates")
}

func (panicRuntimeRegistry) ActiveEncryptedValue(uint, uint) (string, bool) {
	panic("model endpoint read an upstream key")
}

type panicForwarder struct{}

func (panicForwarder) Forward(context.Context, ForwardInput) UpstreamResult {
	panic("model endpoint called Forward")
}

func (panicForwarder) ForwardStream(context.Context, ForwardInput, http.ResponseWriter) UpstreamResult {
	panic("model endpoint called ForwardStream")
}

type decryptPanicEncryption struct {
	encryption.Service
}

func (*decryptPanicEncryption) Decrypt(string) (string, error) {
	panic("model endpoint decrypted an upstream key")
}

type panicReadCloser struct{}

func (panicReadCloser) Read([]byte) (int, error) {
	panic("model endpoint read request body")
}

func (panicReadCloser) Close() error {
	return nil
}

func TestHandlerReportsFinalAttemptInDebugHeaders(t *testing.T) {
	tests := []struct {
		name         string
		results      []UpstreamResult
		upstreamKeys []string
		wantAttempts string
	}{
		{
			name: "first attempt success",
			results: []UpstreamResult{{
				StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`),
			}},
			upstreamKeys: []string{"sk-first-success"}, wantAttempts: "1",
		},
		{
			name: "retry success",
			results: []UpstreamResult{
				{StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: []byte(`{"error":"invalid_api_key"}`), ClassificationBody: []byte(`{"error":"invalid_api_key"}`)},
				{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`)},
			},
			upstreamKeys: []string{"sk-retry-one", "sk-retry-two"}, wantAttempts: "2",
		},
		{
			name: "transport exhaustion",
			results: []UpstreamResult{
				{Err: errors.New("dial one")},
				{Err: errors.New("dial two")},
			},
			upstreamKeys: []string{"sk-dial-one", "sk-dial-two"}, wantAttempts: "2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: tt.results}
			engine, _, _ := newHandlerTestRuntime(t, forwarder, tt.upstreamKeys...)
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o"}`))
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			last := forwarder.inputs[len(forwarder.inputs)-1]
			assertDebugHeaders(t, recorder.Header(), "openai", utils.MaskAPIKey(last.APIKey), tt.wantAttempts)
			if strings.Contains(strings.Join(recorder.Header().Values("X-GPTLoad-Key"), ","), last.APIKey) {
				t.Fatal("debug header exposed plaintext key")
			}
		})
	}
}

func TestHandlerWriteUpstreamResponseChecksWriteResultAndClearsDeadline(t *testing.T) {
	writeFailure := errors.New("write failed")
	flushFailure := errors.New("flush failed")
	tests := []struct {
		name     string
		write    func([]byte) (int, error)
		flushErr error
		wantErr  error
	}{
		{
			name: "short write",
			write: func(body []byte) (int, error) {
				return len(body) - 1, nil
			},
			wantErr: io.ErrShortWrite,
		},
		{
			name: "write error",
			write: func([]byte) (int, error) {
				return 0, writeFailure
			},
			wantErr: writeFailure,
		},
		{
			name: "flush error",
			write: func(body []byte) (int, error) {
				return len(body), nil
			},
			flushErr: flushFailure,
			wantErr:  flushFailure,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ginContext, _ := gin.CreateTestContext(recorder)
			writer := &deadlineGinWriter{
				ResponseWriter: ginContext.Writer,
				write:          test.write,
				flushErr:       test.flushErr,
			}
			ginContext.Writer = writer
			handler := &Handler{writeTimeout: time.Second}

			err := handler.writeUpstreamResponse(ginContext, UpstreamResult{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       []byte(`{"ok":true}`),
			})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("writeUpstreamResponse() error = %v, want %v", err, test.wantErr)
			}
			if len(writer.deadlines) < 2 || writer.deadlines[0].IsZero() ||
				!writer.deadlines[len(writer.deadlines)-1].IsZero() {
				t.Fatalf("deadlines = %#v, want armed operations followed by clear", writer.deadlines)
			}
		})
	}
}

func TestHandlerWriteEmptyResponseCommitsStatusBeforeFlush(t *testing.T) {
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	writer := &deadlineGinWriter{
		ResponseWriter: ginContext.Writer,
		write: func(body []byte) (int, error) {
			return len(body), nil
		},
	}
	ginContext.Writer = writer
	handler := &Handler{writeTimeout: time.Second}

	err := handler.writeUpstreamResponse(ginContext, UpstreamResult{
		StatusCode: http.StatusNoContent,
		Header:     make(http.Header),
	})
	if err != nil || recorder.Code != http.StatusNoContent || writer.flushes != 1 {
		t.Fatalf(
			"writeUpstreamResponse() error/status/flushes = %v/%d/%d",
			err, recorder.Code, writer.flushes,
		)
	}
}

type deadlineGinWriter struct {
	gin.ResponseWriter
	write     func([]byte) (int, error)
	flushErr  error
	flushes   int
	deadlines []time.Time
}

func (writer *deadlineGinWriter) Write(body []byte) (int, error) {
	return writer.write(body)
}

func (writer *deadlineGinWriter) SetWriteDeadline(deadline time.Time) error {
	writer.deadlines = append(writer.deadlines, deadline)
	return nil
}

func (writer *deadlineGinWriter) FlushError() error {
	writer.flushes++
	return writer.flushErr
}

func TestHandlerRejectsSpoofedDebugHeaders(t *testing.T) {
	spoofed := http.Header{
		"X-GPTLoad-Group":    {"spoofed-group"},
		"X-GPTLoad-Key":      {"spoofed-key"},
		"X-GPTLoad-Attempts": {"999"},
	}
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: http.StatusOK, Header: spoofed, Body: []byte(`{"ok":true}`),
	}}}
	engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-real-key")
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	assertDebugHeaders(t, recorder.Header(), "openai", utils.MaskAPIKey("sk-real-key"), "1")
}

func assertDebugHeaders(t *testing.T, headers http.Header, group, key, attempts string) {
	t.Helper()
	want := map[string]string{
		"X-GPTLoad-Group": group, "X-GPTLoad-Key": key, "X-GPTLoad-Attempts": attempts,
	}
	for name, value := range want {
		values, exists := headers[http.CanonicalHeaderKey(name)]
		if !exists || len(values) != 1 || values[0] != value {
			t.Errorf("%s = %#v (exists=%t), want exactly [%q]", name, values, exists, value)
		}
	}
}

func TestReadRequestBodyHonorsLimit(t *testing.T) {
	t.Run("exact limit is accepted", func(t *testing.T) {
		body, err := readRequestBody(strings.NewReader("1234"), 4)
		if err != nil || string(body) != "1234" {
			t.Fatalf("readRequestBody() = %q, %v", body, err)
		}
	})

	t.Run("limit plus one is rejected without draining", func(t *testing.T) {
		reader := &boundedCountingReader{remaining: 100}
		body, err := readRequestBody(reader, 4)
		if !errors.Is(err, errRequestTooLarge) || body != nil {
			t.Fatalf("readRequestBody() = %q, %v, want request too large", body, err)
		}
		if reader.read != 5 {
			t.Fatalf("reader consumed %d bytes, want limit+1 (5)", reader.read)
		}
	})
}

func TestHandlerRejectsOversizedRequestBody(t *testing.T) {
	if maxRequestBodyBytes != 32<<20 {
		t.Fatalf("maxRequestBodyBytes = %d, want %d", maxRequestBodyBytes, 32<<20)
	}
	forwarder := &scriptedForwarder{}
	engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-unused")
	reader := &boundedCountingReader{remaining: maxRequestBodyBytes + 100}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", reader)
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if recorder.Code != http.StatusRequestEntityTooLarge || body.Code != reasonRequestTooLarge.Code {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	if reader.read != maxRequestBodyBytes+1 {
		t.Fatalf("reader consumed %d bytes, want %d", reader.read, maxRequestBodyBytes+1)
	}
	if len(forwarder.inputs)+len(forwarder.streamInputs) != 0 {
		t.Fatal("oversized request reached upstream forwarder")
	}
}

type boundedCountingReader struct {
	remaining int64
	read      int64
}

func (reader *boundedCountingReader) Read(destination []byte) (int, error) {
	if reader.remaining == 0 {
		return 0, io.EOF
	}
	read := int64(len(destination))
	if read > reader.remaining {
		read = reader.remaining
	}
	for index := int64(0); index < read; index++ {
		destination[index] = 'x'
	}
	reader.remaining -= read
	reader.read += read
	return int(read), nil
}

func TestHandlerUsesStreamingForwarder(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantNormal  int
		wantStreams int
	}{
		{name: "stream absent", body: `{"model":"gpt-4o"}`, wantNormal: 1},
		{name: "stream false", body: `{"model":"gpt-4o","stream":false}`, wantNormal: 1},
		{name: "stream true", body: `{"model":"gpt-4o","stream":true}`, wantStreams: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{
				results: []UpstreamResult{{
					StatusCode: http.StatusOK, Header: make(http.Header),
					Body: []byte(`{"ok":true}`), RequestWritten: true,
				}},
				streamResults: []UpstreamResult{{
					StatusCode: http.StatusOK, Committed: true, RequestWritten: true,
				}},
			}
			engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one")
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(tt.body))
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			if len(forwarder.inputs) != tt.wantNormal || len(forwarder.streamInputs) != tt.wantStreams {
				t.Fatalf("normal/stream calls = %d/%d, want %d/%d", len(forwarder.inputs), len(forwarder.streamInputs), tt.wantNormal, tt.wantStreams)
			}
		})
	}
}

func TestHandlerDoesNotRetryOversizedResponse(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{Err: fmt.Errorf("%w: response too large", ErrUpstreamProtocol), RequestWritten: true},
		{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true},
	}}
	engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one", "sk-two")
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadGateway || len(forwarder.inputs) != 1 {
		t.Fatalf("response/attempts = %d/%d, body=%s", recorder.Code, len(forwarder.inputs), recorder.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Code != reasonUpstreamProtocol.Code {
		t.Fatalf("response = %s, error=%v", recorder.Body.String(), err)
	}
}

func TestHandlerRetriesStreamBeforeCommit(t *testing.T) {
	protocolFailure := fmt.Errorf("%w: gzip", ErrUpstreamProtocol)
	tests := []struct {
		name         string
		results      []UpstreamResult
		wantStatus   int
		wantCode     string
		wantAttempts int
	}{
		{
			name: "protocol error then committed success",
			results: []UpstreamResult{
				{Err: protocolFailure, RequestWritten: true, RetryableBeforeCommit: true},
				{StatusCode: http.StatusOK, RequestWritten: true, Committed: true},
			},
			wantStatus: http.StatusOK, wantAttempts: 2,
		},
		{
			name: "first-event timeout then committed success",
			results: []UpstreamResult{
				{Err: context.DeadlineExceeded, RequestWritten: true, RetryableBeforeCommit: true},
				{StatusCode: http.StatusOK, RequestWritten: true, Committed: true},
			},
			wantStatus: http.StatusOK, wantAttempts: 2,
		},
		{
			name: "protocol errors exhausted",
			results: []UpstreamResult{
				{Err: protocolFailure, RequestWritten: true, RetryableBeforeCommit: true},
				{Err: protocolFailure, RequestWritten: true, RetryableBeforeCommit: true},
			},
			wantStatus: http.StatusBadGateway, wantCode: reasonUpstreamProtocol.Code, wantAttempts: 2,
		},
		{
			name: "first-event timeouts exhausted",
			results: []UpstreamResult{
				{Err: context.DeadlineExceeded, RequestWritten: true, RetryableBeforeCommit: true},
				{Err: context.DeadlineExceeded, RequestWritten: true, RetryableBeforeCommit: true},
			},
			wantStatus: http.StatusGatewayTimeout, wantCode: reasonUpstreamTimeout.Code, wantAttempts: 2,
		},
		{
			name: "transport failures exhausted",
			results: []UpstreamResult{
				{Err: errors.New("stream disconnected"), RequestWritten: true, RetryableBeforeCommit: true},
				{Err: errors.New("stream disconnected"), RequestWritten: true, RetryableBeforeCommit: true},
			},
			wantStatus: http.StatusBadGateway, wantCode: reasonUpstreamConnect.Code, wantAttempts: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{streamResults: tt.results}
			engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one", "sk-two")
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","stream":true}`))
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			if recorder.Code != tt.wantStatus || len(forwarder.streamInputs) != tt.wantAttempts {
				t.Fatalf("status/attempts = %d/%d, want %d/%d; body=%s", recorder.Code, len(forwarder.streamInputs), tt.wantStatus, tt.wantAttempts, recorder.Body.String())
			}
			if tt.wantCode != "" {
				var body struct {
					Code string `json:"code"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Code != tt.wantCode {
					t.Fatalf("response = %s, error=%v, want code %q", recorder.Body.String(), err, tt.wantCode)
				}
			}
		})
	}
}

func TestHandlerStopsAtStreamingTerminalBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		result    UpstreamResult
		writeBody bool
	}{
		{
			name: "committed disconnect",
			result: UpstreamResult{
				Err: errors.New("upstream disconnected"), RequestWritten: true, Committed: true,
			},
			writeBody: true,
		},
		{
			name: "downstream cancellation",
			result: UpstreamResult{
				Err: context.Canceled, RequestWritten: true, RetryableBeforeCommit: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{streamResults: []UpstreamResult{
				tt.result,
				{StatusCode: http.StatusOK, Committed: true},
			}}
			if tt.writeBody {
				forwarder.onStreamCall = func(index int, writer http.ResponseWriter) {
					if index == 0 {
						writer.WriteHeader(http.StatusOK)
						_, _ = writer.Write([]byte("data: first\n\n"))
					}
				}
			}
			engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one", "sk-two")
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","stream":true}`))
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			if len(forwarder.streamInputs) != 1 {
				t.Fatalf("stream attempts = %d, want 1", len(forwarder.streamInputs))
			}
			if tt.writeBody && recorder.Body.String() != "data: first\n\n" {
				t.Fatalf("committed body = %q, want only first event", recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), `"code"`) {
				t.Fatalf("terminal stream appended reason: %s", recorder.Body.String())
			}
		})
	}
}

func TestHandlerDoesNotRetryDownstreamWriteDeadline(t *testing.T) {
	deadlineErr := errors.New("downstream stream write deadline exceeded")
	forwarder := &scriptedForwarder{streamResults: []UpstreamResult{
		{
			Err: deadlineErr, RequestWritten: true,
			Committed: true, RetryableBeforeCommit: true,
		},
		{StatusCode: http.StatusOK, RequestWritten: true, Committed: true},
	}}
	forwarder.onStreamCall = func(index int, writer http.ResponseWriter) {
		if index == 0 {
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte("data: first\n\n"))
		}
	}
	engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one", "sk-two")
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewBufferString(`{"model":"gpt-4o","stream":true}`),
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()

	engine.ServeHTTP(recorder, request)

	if len(forwarder.streamInputs) != 1 {
		t.Fatalf("stream attempts = %d, want 1 after downstream write deadline", len(forwarder.streamInputs))
	}
	if recorder.Body.String() != "data: first\n\n" {
		t.Fatalf("committed body = %q, want only first event", recorder.Body.String())
	}
}

func TestHandlerDoesNotAdvanceCandidatesAfterRequestDeadline(t *testing.T) {
	forwarder := &scriptedForwarder{streamResults: []UpstreamResult{
		{Err: context.DeadlineExceeded, RequestWritten: true, RetryableBeforeCommit: true},
		{StatusCode: http.StatusOK, RequestWritten: true, Committed: true},
	}}
	engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one", "sk-two")
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewBufferString(`{"model":"gpt-4o","stream":true}`),
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Millisecond)
	defer cancel()
	request = request.WithContext(ctx)
	forwarder.onStreamCall = func(_ int, _ http.ResponseWriter) {
		<-ctx.Done()
	}
	recorder := httptest.NewRecorder()

	engine.ServeHTTP(recorder, request)

	if len(forwarder.streamInputs) != 1 {
		t.Fatalf("stream attempts = %d, want 1 after downstream deadline", len(forwarder.streamInputs))
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("deadline path appended a response: %s", recorder.Body.String())
	}
}

func TestHandlerUsesClassifierForStreamingNonSuccess(t *testing.T) {
	t.Run("retry then committed success", func(t *testing.T) {
		forwarder := &scriptedForwarder{streamResults: []UpstreamResult{
			{StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: []byte(`{"error":"invalid_api_key"}`), ClassificationBody: []byte(`{"error":"invalid_api_key"}`), RequestWritten: true},
			{StatusCode: http.StatusOK, Committed: true, RequestWritten: true},
		}}
		engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one", "sk-two")
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","stream":true}`))
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK || len(forwarder.streamInputs) != 2 {
			t.Fatalf("status/attempts = %d/%d, want 200/2", recorder.Code, len(forwarder.streamInputs))
		}
	})

	t.Run("last retryable response is passed through", func(t *testing.T) {
		forwarder := &scriptedForwarder{streamResults: []UpstreamResult{
			{StatusCode: http.StatusInternalServerError, Header: make(http.Header), Body: []byte(`{"error":"first"}`), ClassificationBody: []byte(`{"error":"first"}`), RequestWritten: true},
			{StatusCode: http.StatusInternalServerError, Header: make(http.Header), Body: []byte(`{"error":"last"}`), ClassificationBody: []byte(`{"error":"last"}`), RequestWritten: true},
		}}
		engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one", "sk-two")
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","stream":true}`))
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusInternalServerError || recorder.Body.String() != `{"error":"last"}` {
			t.Fatalf("final response = %d %s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestHandlerUsesClassifierForNonStreamingNonSuccess(t *testing.T) {
	t.Run("client error terminates after one attempt", func(t *testing.T) {
		forwarder := &scriptedForwarder{results: []UpstreamResult{
			{StatusCode: http.StatusBadRequest, Header: make(http.Header),
				Body:               []byte(`{"error":"invalid input"}`),
				ClassificationBody: []byte(`{"error":"invalid input"}`), RequestWritten: true},
			{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`)},
		}}
		engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one", "sk-two")
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			bytes.NewBufferString(`{"model":"gpt-4o"}`))
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest || len(forwarder.inputs) != 1 {
			t.Fatalf("status/attempts = %d/%d, want 400/1", recorder.Code, len(forwarder.inputs))
		}
	})

	t.Run("rate limit advances to a second key", func(t *testing.T) {
		forwarder := &scriptedForwarder{results: []UpstreamResult{
			{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {"30"}},
				Body:               []byte(`{"error":"rate_limit"}`),
				ClassificationBody: []byte(`{"error":"rate_limit"}`), RequestWritten: true},
			{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true},
		}}
		engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one", "sk-two")
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			bytes.NewBufferString(`{"model":"gpt-4o"}`))
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || len(forwarder.inputs) != 2 {
			t.Fatalf("status/attempts = %d/%d, want 200/2", recorder.Code, len(forwarder.inputs))
		}
	})
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

func TestHandlerDoesNotExposeAliasedUpstreamModelWhenRetryBudgetIsExhausted(t *testing.T) {
	const (
		externalModel = "public-model"
		upstreamModel = "provider-model"
	)
	var attempts atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Upstream-Model", upstreamModel)
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(`{"error":"provider-model internal error"}`))
	}))
	defer upstream.Close()

	engine, _ := newDialectGatewayEngine(t, protocol.OpenAI, externalModel,
		dialect.NewSet(dialect.NewOpenAI(http.DefaultClient)),
		dialectGatewayGroup{
			id: 1, name: "openai", upstreamURL: upstream.URL,
			apiKeys: []string{"sk-one", "sk-two", "sk-three", "sk-unused"},
			models:  []state.ModelConfig{{ID: upstreamModel, Alias: externalModel}},
		},
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewBufferString(`{"model":"public-model"}`),
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()

	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError || attempts.Load() != maxAttempts {
		t.Fatalf("response/attempts = %d/%d, want 500/%d", recorder.Code, attempts.Load(), maxAttempts)
	}
	if strings.Contains(recorder.Body.String(), upstreamModel) ||
		!strings.Contains(recorder.Body.String(), externalModel) {
		t.Fatalf("final response body = %s", recorder.Body.String())
	}
	assertHeadersDoNotContain(t, recorder.Header(), upstreamModel)
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
			handler := NewHandler(manager, registry, keyService, forwarder, dialect.NewSet(openAI))
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
		dialect.NewSet(openAI),
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
	handler := NewHandler(manager, registry, keyService, forwarder, dialect.NewSet(openAI))
	handler.newRandom = func() *rand.Rand { return rand.New(rand.NewSource(1)) }
	engine := gin.New()
	handler.RegisterRoutes(engine)
	return engine, manager, registry
}
