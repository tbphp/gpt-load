package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/dialect"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/encryption"
	platformhttp "gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
	"gpt-load/internal/ratelimit"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
)

const fixedRequestID = "11111111-2222-4333-8444-555555555555"

type rpmLimiterCall struct {
	accessKeyID uint
	limit       int64
}

type recordingAccessKeyRPMLimiter struct {
	mu        sync.Mutex
	calls     []rpmLimiterCall
	decisions []ratelimit.LimitDecision
	onAllow   func(rpmLimiterCall)
}

func (limiter *recordingAccessKeyRPMLimiter) Allow(
	accessKeyID uint,
	limit int64,
) ratelimit.LimitDecision {
	limiter.mu.Lock()
	call := rpmLimiterCall{accessKeyID: accessKeyID, limit: limit}
	limiter.calls = append(limiter.calls, call)
	index := len(limiter.calls) - 1
	decision := ratelimit.LimitDecision{Allowed: true}
	if len(limiter.decisions) > 0 {
		decision = limiter.decisions[min(index, len(limiter.decisions)-1)]
	}
	onAllow := limiter.onAllow
	limiter.mu.Unlock()
	if onAllow != nil {
		onAllow(call)
	}
	return decision
}

func (limiter *recordingAccessKeyRPMLimiter) snapshot() []rpmLimiterCall {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	return append([]rpmLimiterCall(nil), limiter.calls...)
}

type recordingRequestLogSink struct {
	mu     sync.Mutex
	events []telemetry.RequestEvent
}

func (sink *recordingRequestLogSink) Emit(event telemetry.RequestEvent) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	event.Attempts = append([]telemetry.Attempt(nil), event.Attempts...)
	sink.events = append(sink.events, event)
}

func (sink *recordingRequestLogSink) snapshot() []telemetry.RequestEvent {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	events := append([]telemetry.RequestEvent(nil), sink.events...)
	for index := range events {
		events[index].Attempts = append([]telemetry.Attempt(nil), events[index].Attempts...)
	}
	return events
}

type observingReadCloser struct {
	reader io.Reader
	read   bool
}

func (reader *observingReadCloser) Read(destination []byte) (int, error) {
	reader.read = true
	return reader.reader.Read(destination)
}

func (*observingReadCloser) Close() error { return nil }

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) { return 0, errors.New("body read failed") }
func (failingReadCloser) Close() error             { return nil }

type cancelingErrorReadCloser struct {
	cancel context.CancelFunc
	err    error
}

func (reader cancelingErrorReadCloser) Read([]byte) (int, error) {
	reader.cancel()
	return 0, reader.err
}

func (cancelingErrorReadCloser) Close() error { return nil }

type countingHealthDialect struct {
	dialect.Dialect
	classifyCalls int
}

func (value *countingHealthDialect) ClassifyStatus(
	status int,
	body []byte,
) health.FailureCategory {
	value.classifyCalls++
	return value.Dialect.ClassifyStatus(status, body)
}

type cancelingExtractDialect struct {
	dialect.Dialect
	cancel       context.CancelFunc
	err          error
	extractCalls int
}

func (value *cancelingExtractDialect) ExtractModel(
	request *dialect.ParsedRequest,
) (string, bool, error) {
	value.extractCalls++
	if value.cancel != nil {
		value.cancel()
		return "", false, value.err
	}
	return value.Dialect.ExtractModel(request)
}

type cancelingHeaderDeadlineWriter struct {
	gin.ResponseWriter
	cancel context.CancelFunc
	err    error
}

func (writer *cancelingHeaderDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	if !deadline.IsZero() {
		writer.cancel()
		return writer.err
	}
	return nil
}

func newSteppingRequestClock() func() time.Time {
	var mu sync.Mutex
	next := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		current := next
		next = next.Add(10 * time.Millisecond)
		return current
	}
}

func newRequestLogHandlerTestRuntime(
	t *testing.T,
	forwarder AttemptForwarder,
	limiter AccessKeyRPMLimiter,
	sink telemetry.RequestLogSink,
	upstreamKeys ...string,
) (*gin.Engine, *Handler, *state.Manager, *state.KeyRegistry) {
	t.Helper()
	handler, manager, registry := newHandlerForTest(t, forwarder, upstreamKeys...)
	handler.limiter = limiter
	handler.requestLogSink = sink
	handler.newRequestID = func() (string, error) { return fixedRequestID, nil }
	handler.requestNow = newSteppingRequestClock()
	engine := gin.New()
	handler.RegisterRoutes(engine)
	return engine, handler, manager, registry
}

func TestHandlerRPMAdmissionOrderingAndSingleCharge(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{
			StatusCode: http.StatusUnauthorized,
			Header:     make(http.Header),
			Body:       []byte(`{"error":{"message":"invalid key"}}`),
			ClassificationBody: []byte(
				`{"error":{"message":"invalid key"}}`,
			),
			ErrorSummary:   "invalid key",
			RequestWritten: true,
		},
		{
			StatusCode:     http.StatusOK,
			Header:         make(http.Header),
			Body:           []byte(`{"ok":true}`),
			RequestWritten: true,
		},
	}}
	limiter := &recordingAccessKeyRPMLimiter{}
	sink := &recordingRequestLogSink{}
	engine, handler, _, _ := newRequestLogHandlerTestRuntime(
		t, forwarder, limiter, sink, "sk-first", "sk-second",
	)
	handler.newRandom = func() *rand.Rand { return rand.New(zeroSource{}) }
	body := &observingReadCloser{
		reader: strings.NewReader(`{"model":"gpt-4o"}`),
	}
	var recorder *httptest.ResponseRecorder
	limiter.onAllow = func(call rpmLimiterCall) {
		if body.read {
			t.Fatal("RPM admission ran after inference body read")
		}
		if got := recorder.Header().Get(requestIDHeader); got != fixedRequestID {
			t.Fatalf("request ID at admission = %q, want %q", got, fixedRequestID)
		}
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	request.Body = body
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	calls := limiter.snapshot()
	if len(calls) != 1 || calls[0].accessKeyID != 1 {
		t.Fatalf("limiter calls = %#v, want one charge for AccessKey 1", calls)
	}
	if len(forwarder.inputs) != 2 {
		t.Fatalf("forward calls = %d, want two attempts after one admission", len(forwarder.inputs))
	}
}

func TestHandlerUsesFrozenRPMLimitAcrossSnapshotPublish(t *testing.T) {
	keyService, err := encryption.NewService("frozen-rpm-test-master-key")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	manager := state.NewManager()
	publish := func(limit int64) {
		t.Helper()
		if _, err := manager.Publish(state.CompileInput{
			Groups: []state.GroupConfig{{
				ID: 1, Name: "openai", UpstreamURL: "https://unused.example.com",
				Protocols: []protocol.Protocol{protocol.OpenAI},
				Models:    []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true,
			}},
			AccessKeys: []state.AccessKeyConfig{{
				ID: 1, Name: "client", KeyHash: keyService.Hash("gl-client"),
				Status: state.AccessKeyStatusActive, RPMLimit: limit,
			}},
		}); err != nil {
			t.Fatalf("Publish(limit=%d) error = %v", limit, err)
		}
	}
	publish(3)
	limiter := &recordingAccessKeyRPMLimiter{}
	limiter.onAllow = func(rpmLimiterCall) {
		if len(limiter.snapshot()) == 1 {
			publish(9)
		}
	}
	handler := NewHandler(
		manager,
		state.NewKeyRegistry(),
		keyService,
		&scriptedForwarder{},
		dialect.NewSet(),
		health.NewStatsStore(),
		limiter,
		telemetry.NoopRequestLogSink{},
	)
	handler.newRequestID = func() (string, error) { return fixedRequestID, nil }
	engine := gin.New()
	handler.RegisterRoutes(engine)

	for range 2 {
		request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		request.Header.Set("Authorization", "Bearer gl-client")
		engine.ServeHTTP(httptest.NewRecorder(), request)
	}
	calls := limiter.snapshot()
	if len(calls) != 2 || calls[0].limit != 3 || calls[1].limit != 9 {
		t.Fatalf("limiter calls = %#v, want frozen limits [3,9]", calls)
	}
}

func TestHandlerRPMLimitRejectsWithStableResponse(t *testing.T) {
	tests := []struct {
		name       string
		retryAfter time.Duration
		want       string
	}{
		{name: "ceil seconds", retryAfter: 1500 * time.Millisecond, want: "2"},
		{name: "clamp minimum", retryAfter: 0, want: "1"},
		{name: "clamp maximum", retryAfter: 2 * time.Minute, want: "60"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{}
			limiter := &recordingAccessKeyRPMLimiter{
				decisions: []ratelimit.LimitDecision{{
					Allowed: false, RetryAfter: test.retryAfter,
				}},
			}
			sink := &recordingRequestLogSink{}
			engine, _, _, _ := newRequestLogHandlerTestRuntime(
				t, forwarder, limiter, sink, "sk-one",
			)

			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/chat/completions",
				strings.NewReader(`{"model":"gpt-4o"}`),
			)
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusTooManyRequests ||
				recorder.Header().Get("Retry-After") != test.want ||
				recorder.Header().Get(requestIDHeader) != fixedRequestID {
				t.Fatalf(
					"response status/retry/request-id = %d/%q/%q",
					recorder.Code,
					recorder.Header().Get("Retry-After"),
					recorder.Header().Get(requestIDHeader),
				)
			}
			assertJSONEqual(
				t,
				recorder.Body.String(),
				`{"code":"access_key_rate_limited","message":"Access key rate limit exceeded."}`,
			)
			events := sink.snapshot()
			if len(events) != 1 || len(events[0].Attempts) != 0 ||
				events[0].Status != telemetry.RequestStatusError ||
				events[0].StatusCode != http.StatusTooManyRequests ||
				events[0].ErrorCode != "access_key_rate_limited" {
				t.Fatalf("events = %#v, want one zero-attempt RPM rejection", events)
			}
			if len(forwarder.inputs)+len(forwarder.streamInputs) != 0 {
				t.Fatal("RPM-rejected request reached upstream")
			}
		})
	}
}

func TestHandlerRequestLogScopeAndExactlyOnce(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		target        string
		accessKey     string
		body          string
		decision      ratelimit.LimitDecision
		upstreamKeys  []string
		results       []UpstreamResult
		wantLimiter   int
		wantRequestID bool
		wantEvents    int
		wantAttempts  int
	}{
		{
			name: "invalid auth", method: http.MethodPost, target: "/v1/chat/completions",
			accessKey: "wrong", body: `{"model":"gpt-4o"}`,
		},
		{
			name: "unknown endpoint", method: http.MethodPost, target: "/unknown",
			accessKey: "gl-client", body: `{}`,
		},
		{
			name: "models endpoint", method: http.MethodGet, target: "/v1/models",
			accessKey: "gl-client", decision: ratelimit.LimitDecision{Allowed: true},
			wantLimiter: 1, wantRequestID: true,
		},
		{
			name: "inference RPM 429", method: http.MethodPost, target: "/v1/chat/completions",
			accessKey: "gl-client", body: `{"model":"gpt-4o"}`,
			decision:    ratelimit.LimitDecision{Allowed: false, RetryAfter: time.Second},
			wantLimiter: 1, wantRequestID: true, wantEvents: 1,
		},
		{
			name: "body model error", method: http.MethodPost, target: "/v1/chat/completions",
			accessKey: "gl-client", body: `{}`,
			decision:    ratelimit.LimitDecision{Allowed: true},
			wantLimiter: 1, wantRequestID: true, wantEvents: 1,
		},
		{
			name: "one request with retry", method: http.MethodPost, target: "/v1/chat/completions",
			accessKey: "gl-client", body: `{"model":"gpt-4o"}`,
			decision:     ratelimit.LimitDecision{Allowed: true},
			upstreamKeys: []string{"sk-first", "sk-second"},
			results: []UpstreamResult{
				{
					StatusCode: http.StatusUnauthorized,
					Header:     make(http.Header),
					Body:       []byte(`{"error":{"message":"invalid key"}}`),
					ClassificationBody: []byte(
						`{"error":{"message":"invalid key"}}`,
					),
					ErrorSummary:   "invalid key",
					RequestWritten: true,
				},
				{
					StatusCode: http.StatusOK, Header: make(http.Header),
					Body: []byte(`{"ok":true}`), RequestWritten: true,
				},
			},
			wantLimiter: 1, wantRequestID: true, wantEvents: 1, wantAttempts: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: test.results}
			limiter := &recordingAccessKeyRPMLimiter{
				decisions: []ratelimit.LimitDecision{test.decision},
			}
			sink := &recordingRequestLogSink{}
			engine, handler, _, _ := newRequestLogHandlerTestRuntime(
				t, forwarder, limiter, sink, test.upstreamKeys...,
			)
			handler.newRandom = func() *rand.Rand { return rand.New(zeroSource{}) }
			request := httptest.NewRequest(
				test.method, test.target, strings.NewReader(test.body),
			)
			request.Header.Set("Authorization", "Bearer "+test.accessKey)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			if got := len(limiter.snapshot()); got != test.wantLimiter {
				t.Fatalf("limiter calls = %d, want %d", got, test.wantLimiter)
			}
			hasRequestID := recorder.Header().Get(requestIDHeader) != ""
			if hasRequestID != test.wantRequestID {
				t.Fatalf("has request ID = %t, want %t", hasRequestID, test.wantRequestID)
			}
			events := sink.snapshot()
			if len(events) != test.wantEvents {
				t.Fatalf("event count = %d, want %d: %#v", len(events), test.wantEvents, events)
			}
			if len(events) == 1 && len(events[0].Attempts) != test.wantAttempts {
				t.Fatalf(
					"attempt count = %d, want %d: %#v",
					len(events[0].Attempts), test.wantAttempts, events[0],
				)
			}
		})
	}
}

func TestHandlerRecordsLocalInferenceFailuresWithoutAttempts(t *testing.T) {
	tests := []struct {
		name       string
		body       io.ReadCloser
		wantStatus int
		wantCode   string
	}{
		{
			name: "body read error", body: failingReadCloser{},
			wantStatus: http.StatusBadRequest, wantCode: "cannot_extract_model",
		},
		{
			name: "model extraction error", body: io.NopCloser(strings.NewReader(`{}`)),
			wantStatus: http.StatusBadRequest, wantCode: "cannot_extract_model",
		},
		{
			name: "no candidate", body: io.NopCloser(strings.NewReader(`{"model":"gpt-4o"}`)),
			wantStatus: http.StatusServiceUnavailable, wantCode: "no_available_candidate",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{}
			limiter := &recordingAccessKeyRPMLimiter{}
			sink := &recordingRequestLogSink{}
			engine, _, _, _ := newRequestLogHandlerTestRuntime(
				t, forwarder, limiter, sink,
			)
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			request.Body = test.body
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			events := sink.snapshot()
			if recorder.Code != test.wantStatus || len(events) != 1 ||
				events[0].Status != telemetry.RequestStatusError ||
				events[0].StatusCode != test.wantStatus ||
				events[0].ErrorCode != test.wantCode ||
				len(events[0].Attempts) != 0 {
				t.Fatalf("response/event = %d/%#v", recorder.Code, events)
			}
			if events[0].ErrorSummary == "" {
				t.Fatal("local failure summary is empty")
			}
		})
	}
}

func TestHandlerPrioritizesClientCancellationOverLocalInferenceErrors(t *testing.T) {
	sentinel := errors.New("local inference failed after cancellation")
	tests := []struct {
		name            string
		body            func(context.CancelFunc) io.ReadCloser
		cancelOnExtract bool
		wantExtract     int
	}{
		{
			name: "body read error after cancellation",
			body: func(cancel context.CancelFunc) io.ReadCloser {
				return cancelingErrorReadCloser{cancel: cancel, err: sentinel}
			},
		},
		{
			name: "model extraction error after cancellation",
			body: func(context.CancelFunc) io.ReadCloser {
				return io.NopCloser(strings.NewReader(`{"model":"gpt-4o"}`))
			},
			cancelOnExtract: true,
			wantExtract:     1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestContext, cancel := context.WithCancel(context.Background())
			defer cancel()

			forwarder := &scriptedForwarder{}
			limiter := &recordingAccessKeyRPMLimiter{}
			sink := &recordingRequestLogSink{}
			engine, handler, _, registry := newRequestLogHandlerTestRuntime(
				t, forwarder, limiter, sink,
			)
			stats := health.NewStatsStore()
			handler.stats = stats
			runtimeRegistry := &recordingRuntimeRegistry{KeyRegistry: registry}
			handler.registry = runtimeRegistry
			healthDialect := &countingHealthDialect{
				Dialect: handler.dialects[protocol.OpenAI],
			}
			extractDialect := &cancelingExtractDialect{
				Dialect: healthDialect,
				err:     sentinel,
			}
			if test.cancelOnExtract {
				extractDialect.cancel = cancel
			}
			handler.dialects[protocol.OpenAI] = extractDialect
			handler.now = func() time.Time {
				t.Fatal("canceled local failure entered health timing")
				return time.Time{}
			}

			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/chat/completions",
				nil,
			).WithContext(requestContext)
			request.Body = test.body(cancel)
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)

			if calls := limiter.snapshot(); len(calls) != 1 {
				t.Fatalf("limiter calls = %#v, want one admitted request", calls)
			}
			if response.Code == http.StatusBadRequest ||
				strings.Contains(response.Body.String(), "cannot_extract_model") {
				t.Fatalf(
					"response = %d %q, canceled local failure must not write 400",
					response.Code,
					response.Body.String(),
				)
			}
			events := sink.snapshot()
			if len(events) != 1 ||
				events[0].Status != telemetry.RequestStatusCanceled ||
				events[0].StatusCode != 0 ||
				events[0].ErrorCode != "client_canceled" ||
				len(events[0].Attempts) != 0 {
				t.Fatalf(
					"events = %#v, want one zero-attempt client cancellation",
					events,
				)
			}
			if extractDialect.extractCalls != test.wantExtract {
				t.Fatalf(
					"ExtractModel calls = %d, want %d",
					extractDialect.extractCalls,
					test.wantExtract,
				)
			}
			if len(forwarder.inputs)+len(forwarder.streamInputs) != 0 {
				t.Fatal("canceled local failure reached upstream")
			}
			if healthDialect.classifyCalls != 0 {
				t.Fatalf(
					"health classifier calls = %d, want zero",
					healthDialect.classifyCalls,
				)
			}
			if runtimeRegistry.cooldownCalls != 0 ||
				runtimeRegistry.incrFailureCalls != 0 ||
				runtimeRegistry.blacklistCalls != 0 ||
				runtimeRegistry.clearCalls != 0 {
				t.Fatalf("Registry side effects = %#v", runtimeRegistry)
			}
			if got := stats.Snapshot(1, time.Now()); got != (health.KeyStats{}) {
				t.Fatalf("StatsStore side effects = %#v, want zero", got)
			}
		})
	}
}

func TestHandlerRecordsNonStreamingRetryChain(t *testing.T) {
	t.Run("actual retry marks only the previous attempt", func(t *testing.T) {
		forwarder := &scriptedForwarder{results: []UpstreamResult{
			{
				StatusCode: http.StatusUnauthorized,
				Header:     make(http.Header),
				Body:       []byte(`{"error":{"message":" first \n invalid\tkey "}}`),
				ClassificationBody: []byte(
					`{"error":{"message":" first \n invalid\tkey "}}`,
				),
				ErrorSummary:   "first invalid key",
				RequestWritten: true,
			},
			{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: []byte(`{"ok":true}`), RequestWritten: true,
			},
		}}
		limiter := &recordingAccessKeyRPMLimiter{}
		sink := &recordingRequestLogSink{}
		engine, handler, _, _ := newRequestLogHandlerTestRuntime(
			t, forwarder, limiter, sink, "sk-first", "sk-second",
		)
		handler.newRandom = func() *rand.Rand { return rand.New(zeroSource{}) }
		request := httptest.NewRequest(
			http.MethodPost,
			"/v1/chat/completions",
			strings.NewReader(`{"model":"gpt-4o"}`),
		)
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)

		events := sink.snapshot()
		if recorder.Code != http.StatusOK || len(events) != 1 {
			t.Fatalf("response/event count = %d/%d", recorder.Code, len(events))
		}
		event := events[0]
		if event.Status != telemetry.RequestStatusSuccess || event.StatusCode != http.StatusOK ||
			event.ErrorCode != "" || event.ClientModel != "gpt-4o" ||
			event.UpstreamModel != "gpt-4o" || len(event.Attempts) != 2 {
			t.Fatalf("event = %#v", event)
		}
		first, second := event.Attempts[0], event.Attempts[1]
		if first.Sequence != 1 || !first.WillRetry ||
			first.FailureCategory != telemetry.FailureCategoryInvalidKey ||
			first.Action != telemetry.ActionFailKey ||
			first.ErrorCode != "upstream_invalid_key" ||
			first.ErrorSummary != "first invalid key" ||
			first.DurationMs <= 0 {
			t.Fatalf("first attempt = %#v", first)
		}
		if second.Sequence != 2 || second.WillRetry ||
			second.FailureCategory != telemetry.FailureCategoryOK ||
			second.Action != telemetry.ActionTerminate ||
			second.ErrorCode != "" ||
			second.DurationMs <= 0 {
			t.Fatalf("second attempt = %#v", second)
		}
	})

	t.Run("candidate exhaustion does not claim a retry", func(t *testing.T) {
		forwarder := &scriptedForwarder{results: []UpstreamResult{{
			StatusCode: http.StatusUnauthorized,
			Header:     make(http.Header),
			Body:       []byte(`{"error":{"message":"invalid key"}}`),
			ClassificationBody: []byte(
				`{"error":{"message":"invalid key"}}`,
			),
			ErrorSummary:   "invalid key",
			RequestWritten: true,
		}}}
		sink := &recordingRequestLogSink{}
		engine, handler, _, _ := newRequestLogHandlerTestRuntime(
			t,
			forwarder,
			&recordingAccessKeyRPMLimiter{},
			sink,
			"sk-only",
		)
		handler.newRandom = func() *rand.Rand { return rand.New(zeroSource{}) }
		request := httptest.NewRequest(
			http.MethodPost,
			"/v1/chat/completions",
			strings.NewReader(`{"model":"gpt-4o"}`),
		)
		request.Header.Set("Authorization", "Bearer gl-client")
		engine.ServeHTTP(httptest.NewRecorder(), request)

		events := sink.snapshot()
		if len(events) != 1 || len(events[0].Attempts) != 1 ||
			events[0].Attempts[0].WillRetry {
			t.Fatalf("events = %#v, exhausted candidate must keep WillRetry=false", events)
		}
	})
}

func TestHandlerRecordsCommittedStreamTerminalMatrix(t *testing.T) {
	const downstreamBody = "data: terminal\n\n"
	type terminalCase struct {
		name           string
		observation    StreamObservation
		err            error
		cancelRequest  bool
		wantStatus     telemetry.RequestStatus
		wantCode       string
		wantSummary    string
		wantCategory   telemetry.FailureCategory
		wantHTTPStatus int
	}
	tests := []terminalCase{
		{
			name: "clean EOF",
			observation: StreamObservation{
				EndReason: StreamEndCleanEOF,
			},
			wantStatus:     telemetry.RequestStatusSuccess,
			wantCategory:   telemetry.FailureCategoryOK,
			wantHTTPStatus: http.StatusOK,
		},
		{
			name: "SSE error then clean EOF",
			observation: StreamObservation{
				EndReason:    StreamEndSSEError,
				ErrorSummary: "safe first SSE error",
			},
			wantStatus:     telemetry.RequestStatusError,
			wantCode:       "upstream_sse_error",
			wantSummary:    "safe first SSE error",
			wantCategory:   telemetry.FailureCategoryAmbiguous,
			wantHTTPStatus: http.StatusOK,
		},
		{
			name: "abrupt upstream read failure",
			observation: StreamObservation{
				EndReason: StreamEndUpstreamTerminated,
			},
			err: &streamFailure{
				kind: streamFailureUpstreamRead,
				err:  errors.New("private upstream read detail"),
			},
			wantStatus:     telemetry.RequestStatusIncomplete,
			wantCode:       "upstream_stream_terminated",
			wantSummary:    fixedErrorSummary("upstream_stream_terminated"),
			wantCategory:   telemetry.FailureCategoryAmbiguous,
			wantHTTPStatus: http.StatusOK,
		},
		{
			name: "committed protocol rewrite failure",
			observation: StreamObservation{
				EndReason: StreamEndUpstreamProtocolError,
			},
			err: &streamFailure{
				kind: streamFailureProtocol,
				err:  fmt.Errorf("%w: private rewrite detail", ErrUpstreamProtocol),
			},
			wantStatus:     telemetry.RequestStatusIncomplete,
			wantCode:       "upstream_protocol_error",
			wantSummary:    fixedErrorSummary("upstream_protocol_error"),
			wantCategory:   telemetry.FailureCategoryAmbiguous,
			wantHTTPStatus: http.StatusOK,
		},
		{
			name: "idle timeout",
			observation: StreamObservation{
				EndReason: StreamEndIdleTimeout,
			},
			err: &streamFailure{
				kind: streamFailureIdle,
				err:  errStreamIdleTimeout,
			},
			wantStatus:     telemetry.RequestStatusIncomplete,
			wantCode:       "upstream_stream_idle_timeout",
			wantSummary:    fixedErrorSummary("upstream_stream_idle_timeout"),
			wantCategory:   telemetry.FailureCategoryAmbiguous,
			wantHTTPStatus: http.StatusOK,
		},
		{
			name: "downstream write failure",
			observation: StreamObservation{
				EndReason: StreamEndDownstreamWriteFailure,
			},
			err: &streamFailure{
				kind: streamFailureDownstreamWrite,
				err:  errors.New("private downstream write detail"),
			},
			wantStatus:     telemetry.RequestStatusIncomplete,
			wantCode:       "downstream_write_failed",
			wantSummary:    fixedErrorSummary("downstream_write_failed"),
			wantCategory:   telemetry.FailureCategoryAmbiguous,
			wantHTTPStatus: http.StatusOK,
		},
		{
			name: "downstream flush failure",
			observation: StreamObservation{
				EndReason: StreamEndDownstreamWriteFailure,
			},
			err: &streamFailure{
				kind: streamFailureDownstreamWrite,
				err:  errors.New("private downstream flush detail"),
			},
			wantStatus:     telemetry.RequestStatusIncomplete,
			wantCode:       "downstream_write_failed",
			wantSummary:    fixedErrorSummary("downstream_write_failed"),
			wantCategory:   telemetry.FailureCategoryAmbiguous,
			wantHTTPStatus: http.StatusOK,
		},
		{
			name: "client cancellation",
			observation: StreamObservation{
				EndReason: StreamEndClientCanceled,
			},
			err: &streamFailure{
				kind: streamFailureClientCanceled,
				err:  context.Canceled,
			},
			cancelRequest:  true,
			wantStatus:     telemetry.RequestStatusCanceled,
			wantCode:       "client_canceled",
			wantSummary:    fixedErrorSummary("client_canceled"),
			wantCategory:   telemetry.FailureCategoryDownstreamCancel,
			wantHTTPStatus: http.StatusOK,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestContext, cancel := context.WithCancel(context.Background())
			defer cancel()
			forwarder := &scriptedForwarder{
				invokeStreamReady: true,
				streamResults: []UpstreamResult{{
					StatusCode:     http.StatusOK,
					RequestWritten: true,
					Committed:      true,
					Err:            test.err,
					Stream:         test.observation,
				}},
			}
			forwarder.onStreamCall = func(_ int, writer http.ResponseWriter) {
				writer.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(writer, downstreamBody)
				if test.cancelRequest {
					cancel()
				}
			}
			sink := &recordingRequestLogSink{}
			engine, handler, _, registry := newRequestLogHandlerTestRuntime(
				t,
				forwarder,
				&recordingAccessKeyRPMLimiter{},
				sink,
				"sk-one",
			)
			runtimeRegistry := &recordingRuntimeRegistry{KeyRegistry: registry}
			handler.registry = runtimeRegistry
			now := time.Date(2026, time.July, 24, 13, 0, 0, 0, time.UTC)
			handler.now = func() time.Time { return now }
			selectedDialect := &countingHealthDialect{
				Dialect: handler.dialects[protocol.OpenAI],
			}
			handler.dialects[protocol.OpenAI] = selectedDialect

			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/chat/completions",
				strings.NewReader(`{"model":"gpt-4o","stream":true}`),
			).WithContext(requestContext)
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)

			events := sink.snapshot()
			if len(events) != 1 || len(events[0].Attempts) != 1 {
				t.Fatalf("events = %#v, want one event with one attempt", events)
			}
			event := events[0]
			attempt := event.Attempts[0]
			if event.Status != test.wantStatus ||
				event.StatusCode != test.wantHTTPStatus ||
				event.ErrorCode != test.wantCode ||
				event.ErrorSummary != test.wantSummary ||
				event.UpstreamModel != "gpt-4o" {
				t.Fatalf("event = %#v", event)
			}
			if attempt.FailureCategory != test.wantCategory ||
				attempt.Action != telemetry.ActionTerminate ||
				attempt.WillRetry ||
				attempt.ErrorCode != test.wantCode ||
				attempt.Committed != true {
				t.Fatalf("attempt = %#v", attempt)
			}
			if test.wantCode != "" && attempt.ErrorSummary == "" {
				t.Fatalf("attempt summary is empty: %#v", attempt)
			}
			if len(forwarder.streamInputs) != 1 {
				t.Fatalf("stream attempts = %d, want 1", len(forwarder.streamInputs))
			}
			if response.Code != http.StatusOK || response.Body.String() != downstreamBody {
				t.Fatalf(
					"downstream status/body = %d/%q, want unchanged %d/%q",
					response.Code,
					response.Body.String(),
					http.StatusOK,
					downstreamBody,
				)
			}
			if selectedDialect.classifyCalls != 0 {
				t.Fatalf("health classifier calls = %d, want 0", selectedDialect.classifyCalls)
			}
			if runtimeRegistry.cooldownCalls != 0 ||
				runtimeRegistry.incrFailureCalls != 0 ||
				runtimeRegistry.blacklistCalls != 0 ||
				runtimeRegistry.clearCalls != 1 {
				t.Fatalf("Registry side effects = %#v", runtimeRegistry)
			}
			if got := handler.stats.Snapshot(1, now); got != (health.KeyStats{Success: 1}) {
				t.Fatalf("StatsStore side effects = %#v, want existing ready success only", got)
			}
		})
	}
}

func TestHandlerStreamTelemetryDoesNotChangeHealthOrRetrySideEffects(t *testing.T) {
	tests := []struct {
		name        string
		observation StreamObservation
		err         error
	}{
		{
			name: "SSE error observation",
			observation: StreamObservation{
				EndReason:    StreamEndSSEError,
				ErrorSummary: "safe SSE error",
			},
		},
		{
			name: "upstream termination observation",
			observation: StreamObservation{
				EndReason: StreamEndUpstreamTerminated,
			},
			err: &streamFailure{
				kind: streamFailureUpstreamRead,
				err:  errors.New("private upstream detail"),
			},
		},
		{
			name: "downstream failure observation",
			observation: StreamObservation{
				EndReason: StreamEndDownstreamWriteFailure,
			},
			err: &streamFailure{
				kind: streamFailureDownstreamWrite,
				err:  errors.New("private downstream detail"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{streamResults: []UpstreamResult{
				{
					StatusCode:     http.StatusOK,
					RequestWritten: true,
					Committed:      true,
					Err:            test.err,
					Stream:         test.observation,
				},
				{
					StatusCode:     http.StatusOK,
					RequestWritten: true,
					Committed:      true,
					Stream: StreamObservation{
						EndReason: StreamEndCleanEOF,
					},
				},
			}}
			sink := &recordingRequestLogSink{}
			engine, handler, _, registry := newRequestLogHandlerTestRuntime(
				t,
				forwarder,
				&recordingAccessKeyRPMLimiter{},
				sink,
				"sk-one",
				"sk-two",
			)
			runtimeRegistry := &recordingRuntimeRegistry{KeyRegistry: registry}
			handler.registry = runtimeRegistry
			selectedDialect := &countingHealthDialect{
				Dialect: handler.dialects[protocol.OpenAI],
			}
			handler.dialects[protocol.OpenAI] = selectedDialect
			now := time.Date(2026, time.July, 24, 14, 0, 0, 0, time.UTC)
			handler.now = func() time.Time { return now }

			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/chat/completions",
				strings.NewReader(`{"model":"gpt-4o","stream":true}`),
			)
			request.Header.Set("Authorization", "Bearer gl-client")
			engine.ServeHTTP(httptest.NewRecorder(), request)

			if len(forwarder.streamInputs) != 1 {
				t.Fatalf("stream attempts = %d, want observation-only terminal return", len(forwarder.streamInputs))
			}
			if selectedDialect.classifyCalls != 0 {
				t.Fatalf("health classifier calls = %d, want 0", selectedDialect.classifyCalls)
			}
			if runtimeRegistry.cooldownCalls != 0 ||
				runtimeRegistry.incrFailureCalls != 0 ||
				runtimeRegistry.blacklistCalls != 0 ||
				runtimeRegistry.clearCalls != 0 {
				t.Fatalf("Registry side effects = %#v", runtimeRegistry)
			}
			if got := handler.stats.Snapshot(1, now); got != (health.KeyStats{}) {
				t.Fatalf("StatsStore side effects = %#v, want zero", got)
			}
			events := sink.snapshot()
			if len(events) != 1 || len(events[0].Attempts) != 1 {
				t.Fatalf("events = %#v, want one observed terminal event", events)
			}
		})
	}
}

func TestHandlerRecordsDownstreamWriteFailureWithoutChangingResponse(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       []byte(`{"ok":true}`),
	}}}
	limiter := &recordingAccessKeyRPMLimiter{}
	sink := &recordingRequestLogSink{}
	_, handler, _, _ := newRequestLogHandlerTestRuntime(
		t, forwarder, limiter, sink, "sk-one",
	)
	base := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(base)
	writeFailure := errors.New("downstream disconnected")
	writes := 0
	ginContext.Writer = &deadlineGinWriter{
		ResponseWriter: ginContext.Writer,
		write: func([]byte) (int, error) {
			writes++
			return 0, writeFailure
		},
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o"}`),
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	ginContext.Request = request

	handler.Handle(ginContext)

	events := sink.snapshot()
	if base.Code != http.StatusOK || writes != 1 || base.Body.Len() != 0 {
		t.Fatalf(
			"downstream status/writes/body = %d/%d/%q, want 200/1/empty",
			base.Code, writes, base.Body.String(),
		)
	}
	if len(events) != 1 || events[0].Status != telemetry.RequestStatusIncomplete ||
		events[0].StatusCode != http.StatusOK ||
		events[0].ErrorCode != "downstream_write_failed" ||
		len(events[0].Attempts) != 1 {
		t.Fatalf("events = %#v, want one incomplete write-failure event", events)
	}
}

func TestHandlerRecordsCanceledSuccessfulResponseWithoutHealthSideEffects(t *testing.T) {
	requestContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	forwarder := &scriptedForwarder{
		results: []UpstreamResult{{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       []byte(`{"ok":true}`),
		}},
		onCall: func(int) { cancel() },
	}
	stats := health.NewStatsStore()
	handler, _, registry := newHandlerForTestWithStats(
		t, forwarder, stats, "sk-one",
	)
	runtimeRegistry := &recordingRuntimeRegistry{KeyRegistry: registry}
	handler.registry = runtimeRegistry
	handler.limiter = &recordingAccessKeyRPMLimiter{}
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	handler.newRequestID = func() (string, error) { return fixedRequestID, nil }
	handler.requestNow = newSteppingRequestClock()
	selectedDialect := &countingHealthDialect{
		Dialect: handler.dialects[protocol.OpenAI],
	}
	handler.dialects[protocol.OpenAI] = selectedDialect
	engine := gin.New()
	handler.RegisterRoutes(engine)

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o"}`),
	).WithContext(requestContext)
	request.Header.Set("Authorization", "Bearer gl-client")
	engine.ServeHTTP(httptest.NewRecorder(), request)

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one canceled event", events)
	}
	event := events[0]
	if event.Status != telemetry.RequestStatusCanceled ||
		event.StatusCode != 0 ||
		event.ErrorCode != "client_canceled" ||
		len(event.Attempts) != 1 {
		t.Fatalf("event = %#v, want uncommitted client cancellation", event)
	}
	attempt := event.Attempts[0]
	if attempt.FailureCategory != telemetry.FailureCategoryDownstreamCancel ||
		attempt.Action != telemetry.ActionTerminate ||
		attempt.WillRetry {
		t.Fatalf("attempt = %#v, want read-only downstream_cancel/terminate", attempt)
	}
	if selectedDialect.classifyCalls != 0 {
		t.Fatalf(
			"health classifier calls = %d, canceled early branch must not judge again",
			selectedDialect.classifyCalls,
		)
	}
	if runtimeRegistry.cooldownCalls != 0 ||
		runtimeRegistry.incrFailureCalls != 0 ||
		runtimeRegistry.blacklistCalls != 0 ||
		runtimeRegistry.clearCalls != 0 {
		t.Fatalf("Registry side effects = %#v", runtimeRegistry)
	}
	if got := stats.Snapshot(1, time.Now()); got != (health.KeyStats{}) {
		t.Fatalf("StatsStore side effects = %#v", got)
	}
}

func TestHandlerPrioritizesClientCancellationOverDownstreamWriteFailure(t *testing.T) {
	tests := []struct {
		name       string
		writer     func(*gin.Context, context.CancelFunc, error) gin.ResponseWriter
		wantStatus telemetry.RequestStatus
		wantCode   string
		wantHTTP   int
	}{
		{
			name: "committed body write cancellation",
			writer: func(
				ginContext *gin.Context,
				cancel context.CancelFunc,
				writeErr error,
			) gin.ResponseWriter {
				return &deadlineGinWriter{
					ResponseWriter: ginContext.Writer,
					write: func([]byte) (int, error) {
						cancel()
						return 0, writeErr
					},
				}
			},
			wantStatus: telemetry.RequestStatusCanceled,
			wantCode:   "client_canceled",
			wantHTTP:   http.StatusOK,
		},
		{
			name: "uncommitted header write cancellation",
			writer: func(
				ginContext *gin.Context,
				cancel context.CancelFunc,
				writeErr error,
			) gin.ResponseWriter {
				return &cancelingHeaderDeadlineWriter{
					ResponseWriter: ginContext.Writer,
					cancel:         cancel,
					err:            writeErr,
				}
			},
			wantStatus: telemetry.RequestStatusCanceled,
			wantCode:   "client_canceled",
			wantHTTP:   0,
		},
		{
			name: "ordinary write failure",
			writer: func(
				ginContext *gin.Context,
				_ context.CancelFunc,
				writeErr error,
			) gin.ResponseWriter {
				return &deadlineGinWriter{
					ResponseWriter: ginContext.Writer,
					write: func([]byte) (int, error) {
						return 0, writeErr
					},
				}
			},
			wantStatus: telemetry.RequestStatusIncomplete,
			wantCode:   "downstream_write_failed",
			wantHTTP:   http.StatusOK,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestContext, cancel := context.WithCancel(context.Background())
			defer cancel()
			forwarder := &scriptedForwarder{results: []UpstreamResult{{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       []byte(`{"ok":true}`),
			}}}
			sink := &recordingRequestLogSink{}
			_, handler, _, _ := newRequestLogHandlerTestRuntime(
				t,
				forwarder,
				&recordingAccessKeyRPMLimiter{},
				sink,
				"sk-one",
			)
			base := httptest.NewRecorder()
			ginContext, _ := gin.CreateTestContext(base)
			writeErr := errors.New("downstream write failed")
			ginContext.Writer = test.writer(ginContext, cancel, writeErr)
			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/chat/completions",
				strings.NewReader(`{"model":"gpt-4o"}`),
			).WithContext(requestContext)
			request.Header.Set("Authorization", "Bearer gl-client")
			ginContext.Request = request

			handler.Handle(ginContext)

			events := sink.snapshot()
			if len(events) != 1 {
				t.Fatalf("events = %#v, want one write-terminal event", events)
			}
			event := events[0]
			if event.Status != test.wantStatus ||
				event.ErrorCode != test.wantCode ||
				event.StatusCode != test.wantHTTP {
				t.Fatalf(
					"event status/code/http = %q/%q/%d, want %q/%q/%d: %#v",
					event.Status,
					event.ErrorCode,
					event.StatusCode,
					test.wantStatus,
					test.wantCode,
					test.wantHTTP,
					event,
				)
			}
		})
	}
}

func TestForwarderRedactsResolvedCredentialSecretsBeforeClassification(t *testing.T) {
	const (
		apiKey         = "opaque-provider-secret"
		authorization  = "Token resolved-opaque-auth"
		apiKeyHeader   = "resolved-opaque-api-header"
		customRule     = "resolved-opaque-custom-rule"
		disallowedBody = "resolved-opaque-disallowed"
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(
			`{"error":{"message":"` + apiKey + ` ` + authorization + ` ` +
				apiKeyHeader + ` ` + customRule +
				`"},"debug":"` + disallowedBody + `"}`,
		))
	}))
	defer server.Close()

	input := ForwardInput{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		Group: state.GroupView{
			UpstreamURL: server.URL,
			HeaderRules: state.HeaderRules{Set: map[string]string{
				"Authorization": authorization,
				"Api-Key":       apiKeyHeader,
				"X-Custom-Rule": customRule,
			}},
		},
		APIKey: apiKey,
		Request: &dialect.ParsedRequest{
			Method: http.MethodPost,
			Path:   "/v1/chat/completions",
			Header: make(http.Header),
			Body:   []byte(`{"model":"gpt-4o"}`),
		},
	}
	forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
	result := forwarder.Forward(context.Background(), input)

	for _, secret := range []string{apiKey, authorization, apiKeyHeader, customRule} {
		if strings.Contains(string(result.ClassificationBody), secret) ||
			strings.Contains(result.ErrorSummary, secret) {
			t.Fatalf("resolved credential %q leaked: %#v", secret, result)
		}
	}
	if result.ErrorSummary == "" || !strings.Contains(result.ErrorSummary, redact.Placeholder) {
		t.Fatalf("ErrorSummary = %q, want non-empty redacted allowed-path summary", result.ErrorSummary)
	}
	if strings.Contains(result.ErrorSummary, disallowedBody) {
		t.Fatalf("ErrorSummary used disallowed JSON path: %q", result.ErrorSummary)
	}
}

func TestRequestLogSummaryUsesOnlyAllowedJSONPathsAndUTF8Limit(t *testing.T) {
	redactor := redact.New()
	const fallback = "Upstream request failed."
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "error message first",
			body: `{"error":{"message":" first \n value ","detail":"second"},"message":"third"}`,
			want: "first value",
		},
		{
			name: "error detail",
			body: `{"error":{"detail":"nested detail"},"message":"top message"}`,
			want: "nested detail",
		},
		{name: "top message", body: `{"message":"top message"}`, want: "top message"},
		{name: "top detail", body: `{"detail":"top detail"}`, want: "top detail"},
		{name: "string error", body: `{"error":"string error"}`, want: "string error"},
		{
			name: "disallowed nested path",
			body: `{"debug":{"message":"must not persist"}}`,
			want: fallback,
		},
		{name: "non-string allowed path", body: `{"error":{"message":{"secret":"x"}}}`, want: fallback},
		{name: "non JSON", body: `raw upstream body`, want: fallback},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := summarizeErrorBody(redactor, []byte(test.body), fallback); got != test.want {
				t.Fatalf("summarizeErrorBody() = %q, want %q", got, test.want)
			}
		})
	}

	long := strings.Repeat("界", 400)
	summary := summarizeErrorBody(
		redactor,
		[]byte(`{"message":"`+long+`"}`),
		fallback,
	)
	if len(summary) > 1024 || !utf8.ValidString(summary) ||
		!strings.HasSuffix(summary, "...[truncated]") {
		t.Fatalf(
			"summary bytes/UTF-8/suffix = %d/%t/%q",
			len(summary), utf8.ValidString(summary), summary,
		)
	}
}
