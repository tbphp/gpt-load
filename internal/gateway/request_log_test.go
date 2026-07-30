package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"gpt-load/internal/dialect"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/encryption"
	platformhttp "gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
	"gpt-load/internal/ratelimit"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
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

type usageObservingStreamRetryForwarder struct {
	observed []usage.Result
}

func (*usageObservingStreamRetryForwarder) Forward(context.Context, ForwardInput) UpstreamResult {
	return UpstreamResult{Err: errors.New("unexpected non-streaming forward")}
}

func (forwarder *usageObservingStreamRetryForwarder) ForwardStream(
	_ context.Context,
	input ForwardInput,
	_ http.ResponseWriter,
) UpstreamResult {
	payloads := [][]byte{
		[]byte(`{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":9,"prompt_tokens_details":{"cached_tokens":20}}}`),
		[]byte(`{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":20}}}`),
	}
	index := len(forwarder.observed)
	if index >= len(payloads) {
		return UpstreamResult{Err: errors.New("stream script exhausted")}
	}
	capture := newUsageCaptureBoundary().newStreamForRequest(input.Dialect, true)
	capture.observeEvent(dialect.StreamEvent{Payload: payloads[index]})
	result := capture.finalize()
	forwarder.observed = append(forwarder.observed, result)
	if index == 0 {
		return UpstreamResult{
			StatusCode:                http.StatusOK,
			Header:                    http.Header{"Retry-After": {"1"}},
			ClassificationBody:        []byte(`{"error":{"type":"rate_limit_error"}}`),
			ErrorSummary:              fixedErrorSummary("upstream_sse_error"),
			RequestWritten:            true,
			ProviderErrorBeforeCommit: true,
			Usage:                     result,
		}
	}
	if input.OnStreamReady != nil {
		input.OnStreamReady()
	}
	return UpstreamResult{
		StatusCode: http.StatusOK, RequestWritten: true, Committed: true, Usage: result,
	}
}

func TestNewRequestRecorderInitializesUsageNotApplicable(t *testing.T) {
	sink := &recordingRequestLogSink{}
	recorder := newRequestRecorder(
		sink,
		"req-usage-initial",
		time.Unix(100, 0),
		9,
		protocol.OpenAIChatCompletions,
		func() time.Time { return time.Unix(101, 0) },
	)

	recorder.emit()

	if len(sink.events) != 1 {
		t.Fatalf("events = %d, want 1", len(sink.events))
	}
	event := sink.events[0]
	if event.Usage.Result.State != usage.StateNotApplicable ||
		event.Usage.Result.Tokens != (usage.Tokens{}) ||
		event.Usage.GroupID != 0 ||
		event.Usage.KeyID != 0 ||
		event.Usage.AttemptSequence != 0 {
		t.Fatalf("initial Usage = %#v", event.Usage)
	}
}

func TestHandlerRecordsProtocolOnlyResponsesResourceWithoutFabricatingModels(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode:     http.StatusOK,
		Header:         make(http.Header),
		Body:           []byte(`{"id":"resp_123","object":"response"}`),
		RequestWritten: true,
	}}}
	sink := &recordingRequestLogSink{}
	engine, handler, manager, _ := newRequestLogHandlerTestRuntime(
		t,
		forwarder,
		&recordingAccessKeyRPMLimiter{},
		sink,
		"sk-first",
	)
	if _, err := manager.Publish(state.CompileInput{
		Groups: []state.GroupConfig{{
			ID:          1,
			Name:        "responses",
			UpstreamURL: "http://upstream.invalid",
			Protocols:   []protocol.Protocol{protocol.OpenAIResponses},
			Enabled:     true,
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID:      1,
			Name:    "client",
			KeyHash: handler.encryption.Hash("gl-client"),
			Status:  state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	handler.dialects = dialect.NewSet(dialect.NewOpenAIResponses(http.DefaultClient))

	request := httptest.NewRequest(http.MethodGet, "/v1/responses/resp_123", nil)
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	events := sink.snapshot()
	if response.Code != http.StatusOK || len(events) != 1 {
		t.Fatalf("response/events = %d/%#v", response.Code, events)
	}
	event := events[0]
	if event.Protocol != protocol.OpenAIResponses ||
		event.ClientModel != "" ||
		event.UpstreamModel != "" ||
		len(event.Attempts) != 1 ||
		event.Attempts[0].UpstreamModel != "" ||
		event.Usage.Result.State != usage.StateNotApplicable {
		t.Fatalf("event = %#v", event)
	}
}

func TestRequestRecorderBindsUsageToRecordedAttempt(t *testing.T) {
	tests := []struct {
		name   string
		result usage.Result
	}{
		{name: "complete", result: usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30}}},
		{name: "partial", result: usage.Result{State: usage.StatePartial, Tokens: usage.Tokens{Output: 30}}},
		{name: "missing", result: usage.Result{State: usage.StateMissing}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sink := &recordingRequestLogSink{}
			recorder := newRequestRecorder(sink, "req-usage-bind", time.Unix(100, 0), 9, protocol.OpenAIChatCompletions, func() time.Time { return time.Unix(101, 0) })
			recorder.appendAttempt(scheduler.Selection{GroupID: 11, Group: state.GroupView{Name: "first"}, KeyID: 21}, UpstreamResult{}, telemetry.FailureCategoryOK, telemetry.ActionTerminate, "", "", time.Unix(100, 0), time.Unix(100, 0))
			second := recorder.appendAttempt(scheduler.Selection{GroupID: 12, Group: state.GroupView{Name: "second"}, KeyID: 22}, UpstreamResult{StatusCode: http.StatusOK}, telemetry.FailureCategoryOK, telemetry.ActionTerminate, "", "", time.Unix(100, 0), time.Unix(100, 0))

			recorder.completeResponse(UpstreamResult{StatusCode: http.StatusOK, Usage: test.result}, health.Result{}, "provider", second)
			recorder.emit()

			event := sink.events[0]
			if event.Usage.Result != test.result || event.Usage.GroupID != 12 || event.Usage.KeyID != 22 || event.Usage.AttemptSequence != 2 {
				t.Fatalf("Usage = %#v", event.Usage)
			}
			if event.Attempts[1].GroupID != event.Usage.GroupID || event.Attempts[1].KeyID != event.Usage.KeyID || event.Attempts[1].Sequence != event.Usage.AttemptSequence {
				t.Fatalf("attempt/Usage identity = %#v/%#v", event.Attempts[1], event.Usage)
			}
		})
	}
}

func TestRequestRecorderNon2xxKeepsAttributionButNotApplicable(t *testing.T) {
	sink := &recordingRequestLogSink{}
	recorder := newRequestRecorder(sink, "req-usage-429", time.Unix(100, 0), 9, protocol.OpenAIChatCompletions, func() time.Time { return time.Unix(101, 0) })
	index := recorder.appendAttempt(scheduler.Selection{GroupID: 12, Group: state.GroupView{Name: "second"}, KeyID: 22}, UpstreamResult{StatusCode: http.StatusTooManyRequests}, telemetry.FailureCategoryRateLimited, telemetry.ActionRetry, "", "", time.Unix(100, 0), time.Unix(100, 0))
	recorder.completeResponse(UpstreamResult{StatusCode: http.StatusTooManyRequests, Usage: usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{Output: 30}}}, health.Result{Category: health.FailureCategoryRateLimited}, "provider", index)
	recorder.emit()

	event := sink.events[0]
	if event.Usage.Result != (usage.Result{State: usage.StateNotApplicable}) || event.Usage.GroupID != 12 || event.Usage.KeyID != 22 || event.Usage.AttemptSequence != 1 {
		t.Fatalf("Usage = %#v", event.Usage)
	}
}

func TestRequestRecorderInvalidAttemptIndexDoesNotForgeUsageAttribution(t *testing.T) {
	for _, index := range []int{-1, 1} {
		t.Run(fmt.Sprintf("index_%d", index), func(t *testing.T) {
			sink := &recordingRequestLogSink{}
			recorder := newRequestRecorder(sink, "req-usage-invalid", time.Unix(100, 0), 9, protocol.OpenAIChatCompletions, func() time.Time { return time.Unix(101, 0) })
			recorder.appendAttempt(scheduler.Selection{GroupID: 12, Group: state.GroupView{Name: "second"}, KeyID: 22}, UpstreamResult{}, telemetry.FailureCategoryOK, telemetry.ActionTerminate, "", "", time.Unix(100, 0), time.Unix(100, 0))
			recorder.bindUsage(index, usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{Output: 30}}, true)
			recorder.emit()

			if got := sink.events[0].Usage; got != (telemetry.UsageObservation{Result: usage.Result{State: usage.StateNotApplicable}}) {
				t.Fatalf("Usage = %#v", got)
			}
		})
	}
}

func TestRequestRecorderDownstreamFailureKeepsBoundUsage(t *testing.T) {
	sink := &recordingRequestLogSink{}
	recorder := newRequestRecorder(sink, "req-usage-write", time.Unix(100, 0), 9, protocol.OpenAIChatCompletions, func() time.Time { return time.Unix(101, 0) })
	index := recorder.appendAttempt(scheduler.Selection{GroupID: 12, Group: state.GroupView{Name: "second"}, KeyID: 22}, UpstreamResult{StatusCode: http.StatusOK}, telemetry.FailureCategoryOK, telemetry.ActionTerminate, "", "", time.Unix(100, 0), time.Unix(100, 0))
	result := usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30}}
	recorder.completeResponse(UpstreamResult{StatusCode: http.StatusOK, Usage: result}, health.Result{}, "provider", index)
	recorder.completeDownstreamWrite(http.StatusOK)
	recorder.emit()

	event := sink.events[0]
	if event.Status != telemetry.RequestStatusIncomplete || event.Usage.Result != result || event.Usage.GroupID != 12 || event.Usage.KeyID != 22 || event.Usage.AttemptSequence != 1 {
		t.Fatalf("event = %#v", event)
	}
}

func TestHandlerPublishesUsageForActualNonStreamingResponseAttempt(t *testing.T) {
	sink := &recordingRequestLogSink{}
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{StatusCode: http.StatusTooManyRequests, Header: make(http.Header), ClassificationBody: []byte(`{"error":{"type":"rate_limit_error"}}`), Usage: usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{Output: 99}}, RequestWritten: true},
		{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), Usage: usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30}}, RequestWritten: true},
	}}
	engine, handler, _, _ := newRequestLogHandlerTestRuntime(t, forwarder, &recordingAccessKeyRPMLimiter{}, sink, "sk-first", "sk-second")
	handler.newRandom = func() *rand.Rand { return rand.New(zeroSource{}) }
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	engine.ServeHTTP(httptest.NewRecorder(), request)

	events := sink.snapshot()
	if len(events) != 1 || events[0].Usage.Result != (usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30}}) || events[0].Usage.GroupID != 1 || events[0].Usage.KeyID != 2 || events[0].Usage.AttemptSequence != 2 {
		t.Fatalf("events = %#v", events)
	}
}

func TestHandlerDiscardsPreCommitStreamUsageOnRetry(t *testing.T) {
	sink := &recordingRequestLogSink{}
	forwarder := &usageObservingStreamRetryForwarder{}
	engine, handler, _, _ := newRequestLogHandlerTestRuntime(
		t, forwarder, &recordingAccessKeyRPMLimiter{}, sink, "sk-first", "sk-second",
	)
	handler.newRandom = func() *rand.Rand { return rand.New(zeroSource{}) }
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","stream":true}`),
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	engine.ServeHTTP(httptest.NewRecorder(), request)

	first := usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 9}}
	second := usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30}}
	if len(forwarder.observed) != 2 || forwarder.observed[0] != first || forwarder.observed[1] != second {
		t.Fatalf("observed Usage = %#v, want %#v then %#v", forwarder.observed, first, second)
	}
	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %#v, want exactly one final event", events)
	}
	event := events[0]
	if event.Usage.Result != second || event.Usage.Result == first ||
		event.Usage.GroupID != 1 || event.Usage.KeyID != 2 || event.Usage.AttemptSequence != 2 {
		t.Fatalf("event Usage = %#v, want second attempt Usage", event.Usage)
	}
	if len(event.Attempts) != 2 || event.Attempts[1].GroupID != event.Usage.GroupID ||
		event.Attempts[1].KeyID != event.Usage.KeyID || event.Attempts[1].Sequence != event.Usage.AttemptSequence {
		t.Fatalf("attempts/Usage = %#v/%#v", event.Attempts, event.Usage)
	}
}

func TestHandlerFirstProviderErrorRequestLogContract(t *testing.T) {
	sink := &recordingRequestLogSink{}
	const (
		apiKey      = "sk-obviously-fake-log-contract"
		providerRaw = "provider-body-must-not-be-logged"
	)
	forwarder := &scriptedForwarder{streamResults: []UpstreamResult{
		withProviderErrorBeforeCommit(UpstreamResult{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Retry-After": {"1"}},
			ClassificationBody: []byte(
				`{"error":{"type":"rate_limit_error","message":"` + providerRaw + ` ` + apiKey + `"}}`,
			),
			ErrorSummary:   fixedErrorSummary("upstream_sse_error"),
			RequestWritten: true,
			Usage: usage.Result{
				State: usage.StateComplete,
				Tokens: usage.Tokens{
					UncachedInput: 25,
					Output:        2,
				},
			},
		}),
	}}
	engine, handler, _, _ := newRequestLogHandlerTestRuntime(
		t,
		forwarder,
		&recordingAccessKeyRPMLimiter{},
		sink,
		apiKey,
	)
	handler.newRandom = func() *rand.Rand { return rand.New(zeroSource{}) }
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","stream":true}`),
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %#v", events)
	}
	event := events[0]
	if response.Code != http.StatusBadGateway ||
		event.Status != telemetry.RequestStatusError ||
		event.StatusCode != http.StatusBadGateway ||
		event.ErrorCode != reasonUpstreamProtocol.Code ||
		event.ErrorSummary != reasonUpstreamProtocol.Message ||
		event.Usage.Result != (usage.Result{
			State: usage.StateComplete,
			Tokens: usage.Tokens{
				UncachedInput: 25,
				Output:        2,
			},
		}) ||
		event.Usage.GroupID != 1 ||
		event.Usage.KeyID != 1 ||
		event.Usage.AttemptSequence != 1 ||
		len(event.Attempts) != 1 {
		t.Fatalf("response/event = %d/%#v", response.Code, event)
	}
	attempt := event.Attempts[0]
	if attempt.StatusCode != http.StatusOK ||
		attempt.FailureCategory != telemetry.FailureCategoryRateLimited ||
		attempt.Action != telemetry.ActionCooldownKey ||
		attempt.Committed ||
		attempt.ErrorSummary != fixedErrorSummary("upstream_sse_error") {
		t.Fatalf("attempt = %#v", attempt)
	}
	serialized := fmt.Sprintf("%#v", event)
	for _, forbidden := range []string{apiKey, providerRaw, "rate_limit_error"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("request log leaked %q: %s", forbidden, serialized)
		}
	}
}

func TestHandlerRememberedLastResponseKeepsOriginalUsageAttempt(t *testing.T) {
	sink := &recordingRequestLogSink{}
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{StatusCode: http.StatusTooManyRequests, Header: make(http.Header), ClassificationBody: []byte(`{"error":{"type":"rate_limit_error"}}`), RequestWritten: true},
		{Err: errors.New("transport two"), RetryableBeforeCommit: true},
		{Err: errors.New("transport three"), RetryableBeforeCommit: true},
	}}
	engine, handler, _, _ := newRequestLogHandlerTestRuntime(t, forwarder, &recordingAccessKeyRPMLimiter{}, sink, "sk-first", "sk-second", "sk-third")
	handler.newRandom = func() *rand.Rand { return rand.New(zeroSource{}) }
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	engine.ServeHTTP(httptest.NewRecorder(), request)

	events := sink.snapshot()
	if len(events) != 1 || events[0].Usage.Result != (usage.Result{State: usage.StateNotApplicable}) || events[0].Usage.GroupID != 1 || events[0].Usage.KeyID != 1 || events[0].Usage.AttemptSequence != 1 {
		t.Fatalf("events = %#v", events)
	}
}

func TestHandlerFinalNon2xxPublishesNotApplicableWithResponseAttribution(t *testing.T) {
	sink := &recordingRequestLogSink{}
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: http.StatusTooManyRequests, Header: make(http.Header), ClassificationBody: []byte(`{"error":{"type":"rate_limit_error"}}`), Usage: usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{Output: 99}}, RequestWritten: true,
	}}}
	engine, _, _, _ := newRequestLogHandlerTestRuntime(t, forwarder, &recordingAccessKeyRPMLimiter{}, sink, "sk-first")
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	engine.ServeHTTP(httptest.NewRecorder(), request)

	events := sink.snapshot()
	if len(events) != 1 || events[0].Usage.Result != (usage.Result{State: usage.StateNotApplicable}) || events[0].Usage.GroupID != 1 || events[0].Usage.KeyID != 1 || events[0].Usage.AttemptSequence != 1 {
		t.Fatalf("events = %#v", events)
	}
}

func TestHandlerTransportAndNoCandidateUsageRemainUnattributed(t *testing.T) {
	for _, test := range []struct {
		name         string
		forwarder    *scriptedForwarder
		upstreamKeys []string
	}{
		{name: "transport", forwarder: &scriptedForwarder{results: []UpstreamResult{{Err: errors.New("transport"), RequestWritten: true}}}, upstreamKeys: []string{"sk-first"}},
		{name: "no candidate", forwarder: &scriptedForwarder{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			sink := &recordingRequestLogSink{}
			engine, _, _, _ := newRequestLogHandlerTestRuntime(t, test.forwarder, &recordingAccessKeyRPMLimiter{}, sink, test.upstreamKeys...)
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
			request.Header.Set("Authorization", "Bearer gl-client")
			engine.ServeHTTP(httptest.NewRecorder(), request)
			events := sink.snapshot()
			if len(events) != 1 || events[0].Usage != (telemetry.UsageObservation{Result: usage.Result{State: usage.StateNotApplicable}}) {
				t.Fatalf("events = %#v", events)
			}
		})
	}
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

func TestHandlerPanicFallbackEmitsFailClosedRequestLogBeforeAndAfterHeaders(t *testing.T) {
	const panicCanary = "forwarder-panic-canary-must-not-leak"
	tests := []struct {
		name       string
		body       string
		configure  func(*scriptedForwarder)
		wantStatus telemetry.RequestStatus
		wantCode   int
	}{
		{
			name: "before downstream write",
			body: `{"model":"gpt-4o"}`,
			configure: func(forwarder *scriptedForwarder) {
				forwarder.onCall = func(int) { panic(panicCanary) }
			},
			wantStatus: telemetry.RequestStatusError,
			wantCode:   http.StatusInternalServerError,
		},
		{
			name: "after accepted stream headers",
			body: `{"model":"gpt-4o","stream":true}`,
			configure: func(forwarder *scriptedForwarder) {
				forwarder.onStreamCall = func(_ int, writer http.ResponseWriter) {
					writer.WriteHeader(http.StatusAccepted)
					_, _ = writer.Write(nil)
					panic(panicCanary)
				}
			},
			wantStatus: telemetry.RequestStatusIncomplete,
			wantCode:   http.StatusAccepted,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{}
			test.configure(forwarder)
			sink := &recordingRequestLogSink{}
			engine, _, _, _ := newRequestLogHandlerTestRuntime(
				t,
				forwarder,
				&recordingAccessKeyRPMLimiter{},
				sink,
				"sk-panic-fallback",
			)
			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/chat/completions",
				strings.NewReader(test.body),
			)
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()

			var logs bytes.Buffer
			logger := logrus.StandardLogger()
			previousOutput := logger.Out
			logger.SetOutput(&logs)
			defer logger.SetOutput(previousOutput)

			var recovered any
			func() {
				defer func() { recovered = recover() }()
				engine.ServeHTTP(response, request)
			}()

			if recovered != panicCanary {
				t.Fatalf("outer recovery received %#v, want original panic %q", recovered, panicCanary)
			}
			events := sink.snapshot()
			if len(events) != 1 {
				t.Fatalf("events = %#v, want exactly one event", events)
			}
			event := events[0]
			if event.Status != test.wantStatus ||
				event.StatusCode != test.wantCode ||
				event.ErrorCode != "internal_error" ||
				event.ErrorSummary != "The request failed due to an internal error." {
				t.Fatalf("event = %#v", event)
			}
			for _, surface := range []string{
				fmt.Sprintf("%#v", event),
				response.Body.String(),
				fmt.Sprintf("%v", response.Header()),
				logs.String(),
			} {
				if strings.Contains(surface, panicCanary) {
					t.Fatalf("panic canary leaked to %q", surface)
				}
			}
		})
	}
}

func TestRequestRecorderBoundsModelsAtUTF8Boundary(t *testing.T) {
	const requestID = "00000000-0000-4000-8000-000000000206"
	clientModel := strings.Repeat("界", 85)
	upstreamModel := strings.Repeat("upstream-", 32)
	attemptModel := strings.Repeat("模", 86)
	sink := &recordingRequestLogSink{}
	startedAt := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	recorder := newRequestRecorder(
		sink,
		requestID,
		startedAt,
		1,
		protocol.OpenAIChatCompletions,
		func() time.Time { return startedAt.Add(time.Second) },
	)
	recorder.setClientModel(clientModel)
	recorder.attempts = []telemetry.Attempt{{
		Sequence:      1,
		UpstreamModel: attemptModel,
	}}
	recorder.outcome = requestOutcome{
		status:        telemetry.RequestStatusSuccess,
		statusCode:    http.StatusOK,
		upstreamModel: upstreamModel,
	}

	recorder.emit()

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	event := events[0]
	if len(clientModel) != 255 || event.ClientModel != clientModel {
		t.Fatalf(
			"client model bytes/value = %d/%q, want 255-byte model unchanged",
			len(event.ClientModel),
			event.ClientModel,
		)
	}
	if len(upstreamModel) <= 255 || event.UpstreamModel != upstreamModel {
		t.Fatalf("overall upstream model was changed before pricing: %q", event.UpstreamModel)
	}
	if len(event.Attempts) != 1 || len(attemptModel) <= 255 ||
		event.Attempts[0].UpstreamModel != attemptModel {
		t.Fatalf(
			"attempt upstream model was changed before SQLite projection: %#v",
			event.Attempts,
		)
	}
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

func (value *cancelingExtractDialect) InspectRequest(
	request *dialect.ParsedRequest,
) (dialect.RequestMetadata, error) {
	value.extractCalls++
	if value.cancel != nil {
		value.cancel()
		return dialect.RequestMetadata{}, value.err
	}
	return value.Dialect.InspectRequest(request)
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
				Protocols: []protocol.Protocol{protocol.OpenAIChatCompletions},
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
		health.NewMutationCoordinator(),
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
			wantStatus: http.StatusBadRequest, wantCode: "invalid_protocol_request",
		},
		{
			name: "model extraction error", body: io.NopCloser(strings.NewReader(`{}`)),
			wantStatus: http.StatusBadRequest, wantCode: "invalid_protocol_request",
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
				Dialect: handler.dialects[protocol.OpenAIChatCompletions],
			}
			extractDialect := &cancelingExtractDialect{
				Dialect: healthDialect,
				err:     sentinel,
			}
			if test.cancelOnExtract {
				extractDialect.cancel = cancel
			}
			handler.dialects[protocol.OpenAIChatCompletions] = extractDialect
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
				strings.Contains(response.Body.String(), "invalid_protocol_request") {
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
				Dialect: handler.dialects[protocol.OpenAIChatCompletions],
			}
			handler.dialects[protocol.OpenAIChatCompletions] = selectedDialect

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
				Dialect: handler.dialects[protocol.OpenAIChatCompletions],
			}
			handler.dialects[protocol.OpenAIChatCompletions] = selectedDialect
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
		Dialect: handler.dialects[protocol.OpenAIChatCompletions],
	}
	handler.dialects[protocol.OpenAIChatCompletions] = selectedDialect
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
		apiKey           = "opaque-provider-secret"
		authorization    = "Token " + apiKey
		apiKeyHeader     = "Secondary " + apiKey
		customRule       = "opaque  literal\tvalue"
		ordinaryEncoding = "gzip"
		disallowedBody   = "resolved-opaque-disallowed"
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		message := strings.Join(
			[]string{apiKey, authorization, apiKeyHeader, customRule, ordinaryEncoding},
			" ",
		)
		_, _ = writer.Write([]byte(
			`{"error":{"message":` + strconv.Quote(message) +
				`},"debug":"` + disallowedBody + `"}`,
		))
	}))
	defer server.Close()

	input := ForwardInput{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		Group: state.GroupView{
			UpstreamURL: server.URL,
			HeaderRules: state.HeaderRules{Set: map[string]string{
				"Authorization":   "Token ${API_KEY}",
				"Api-Key":         "Secondary ${API_KEY}",
				"X-Custom-Rule":   customRule,
				"Accept-Encoding": ordinaryEncoding,
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

	for _, secret := range []string{apiKey, authorization, apiKeyHeader} {
		if strings.Contains(string(result.ClassificationBody), secret) ||
			strings.Contains(result.ErrorSummary, secret) {
			t.Fatalf("resolved credential %q leaked: %#v", secret, result)
		}
	}
	classificationSummary := allowedErrorSummary(result.ClassificationBody)
	for _, ordinary := range []string{customRule, ordinaryEncoding} {
		if !strings.Contains(classificationSummary, ordinary) {
			t.Fatalf(
				"ordinary HeaderRule value %q was changed in ClassificationBody summary %q: %#v",
				ordinary,
				classificationSummary,
				result,
			)
		}
		if strings.Contains(result.ErrorSummary, ordinary) {
			t.Fatalf("ErrorSummary leaked HeaderRules value %q: %#v", ordinary, result)
		}
	}
	if strings.Contains(result.ErrorSummary, "opaque literal value") {
		t.Fatalf("ErrorSummary leaked normalized HeaderRules value: %#v", result)
	}
	if result.ErrorSummary == "" || !strings.Contains(result.ErrorSummary, redact.Placeholder) {
		t.Fatalf("ErrorSummary = %q, want non-empty redacted allowed-path summary", result.ErrorSummary)
	}
	if strings.Contains(result.ErrorSummary, disallowedBody) {
		t.Fatalf("ErrorSummary used disallowed JSON path: %q", result.ErrorSummary)
	}
}

func TestRequestRecorderRedactsHeaderRuleLiteralsFromNonStreamingAndSSESummaries(t *testing.T) {
	const (
		apiKey           = "fake-recorder-provider-key"
		customRule       = "opaque  literal\tvalue"
		ordinaryEncoding = "gzip"
	)
	selection := scheduler.Selection{
		GroupID: 11,
		Group: state.GroupView{
			ID: 11,
			HeaderRules: state.HeaderRules{Set: map[string]string{
				"X-Custom":        customRule,
				"Accept-Encoding": ordinaryEncoding,
			}},
		},
		KeyID: 21,
	}
	for _, test := range []struct {
		name   string
		record func(*requestRecorder, UpstreamResult) int
		finish func(*requestRecorder, UpstreamResult, int)
		result UpstreamResult
	}{
		{
			name: "non-streaming",
			result: UpstreamResult{
				StatusCode:   http.StatusBadRequest,
				ErrorSummary: "echo opaque literal value " + ordinaryEncoding,
			},
			record: func(recorder *requestRecorder, result UpstreamResult) int {
				return recorder.recordAttempt(
					selection,
					apiKey,
					result,
					health.Result{
						Category: health.FailureCategoryClientError,
						Action:   health.ActionTerminate,
					},
					time.Unix(100, 0),
					time.Unix(101, 0),
				)
			},
			finish: func(recorder *requestRecorder, result UpstreamResult, attempt int) {
				recorder.completeResponse(
					result,
					health.Result{Category: health.FailureCategoryClientError},
					"provider-model",
					attempt,
				)
			},
		},
		{
			name: "SSE",
			result: UpstreamResult{
				StatusCode: http.StatusOK,
				Committed:  true,
				Stream: StreamObservation{
					EndReason:    StreamEndSSEError,
					ErrorSummary: "echo opaque literal value " + ordinaryEncoding,
				},
			},
			record: func(recorder *requestRecorder, result UpstreamResult) int {
				return recorder.recordStreamAttempt(
					selection,
					apiKey,
					result,
					time.Unix(100, 0),
					time.Unix(101, 0),
				)
			},
			finish: func(recorder *requestRecorder, result UpstreamResult, attempt int) {
				recorder.completeStream(result, "provider-model", attempt)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			sink := &recordingRequestLogSink{}
			recorder := newRequestRecorder(
				sink,
				"req-header-rule-summary-"+test.name,
				time.Unix(100, 0),
				9,
				protocol.OpenAIChatCompletions,
				func() time.Time { return time.Unix(102, 0) },
			)
			attempt := test.record(recorder, test.result)
			test.finish(recorder, test.result, attempt)
			recorder.emit()

			if len(sink.events) != 1 || len(sink.events[0].Attempts) != 1 {
				t.Fatalf("events = %#v, want one event with one attempt", sink.events)
			}
			event := sink.events[0]
			for _, surface := range []string{
				event.ErrorSummary,
				event.Attempts[0].ErrorSummary,
			} {
				for _, forbidden := range []string{
					apiKey,
					customRule,
					"opaque literal value",
					ordinaryEncoding,
				} {
					if strings.Contains(surface, forbidden) {
						t.Fatalf("%s summary leaked %q: %q", test.name, forbidden, surface)
					}
				}
				if !strings.Contains(surface, redact.Placeholder) {
					t.Fatalf("%s summary = %q, want redaction placeholder", test.name, surface)
				}
			}
		})
	}
}

func TestRequestRecorderPreservesFixedTerminalSummariesWithShortHeaderRuleLiteral(t *testing.T) {
	const shortLiteral = "a"
	selection := scheduler.Selection{
		GroupID: 11,
		Group: state.GroupView{
			ID: 11,
			HeaderRules: state.HeaderRules{Set: map[string]string{
				"X-Short": shortLiteral,
			}},
		},
		KeyID: 21,
	}
	sink := &recordingRequestLogSink{}
	recorder := newRequestRecorder(
		sink,
		"req-fixed-terminal-summary",
		time.Unix(100, 0),
		9,
		protocol.OpenAIChatCompletions,
		func() time.Time { return time.Unix(102, 0) },
	)
	result := UpstreamResult{
		StatusCode: http.StatusOK,
		Committed:  true,
		Stream: streamTerminalObservation(
			StreamEndUpstreamTerminated,
		),
	}
	attempt := recorder.recordStreamAttempt(
		selection,
		"fake-fixed-summary-provider-key",
		result,
		time.Unix(100, 0),
		time.Unix(101, 0),
	)
	recorder.completeStream(result, "provider-model", attempt)
	recorder.emit()

	if len(sink.events) != 1 || len(sink.events[0].Attempts) != 1 {
		t.Fatalf("events = %#v, want one event with one attempt", sink.events)
	}
	want := fixedErrorSummary("upstream_stream_terminated")
	event := sink.events[0]
	if event.ErrorSummary != want || event.Attempts[0].ErrorSummary != want {
		t.Fatalf(
			"fixed summaries = outcome %q / attempt %q, want %q",
			event.ErrorSummary,
			event.Attempts[0].ErrorSummary,
			want,
		)
	}
}

func TestRequestRecorderPreservesProviderErrorFixedSummaryWithOrdinaryHeaderRules(t *testing.T) {
	const marker = "rate_limit_error"
	selection := scheduler.Selection{
		GroupID: 11,
		Group: state.GroupView{
			ID: 11,
			HeaderRules: state.HeaderRules{Set: map[string]string{
				"X-Ordinary-Marker": marker,
				"X-Short-Literal":   "a",
			}},
		},
		KeyID: 21,
	}
	sink := &recordingRequestLogSink{}
	recorder := newRequestRecorder(
		sink,
		"req-provider-fixed-summary",
		time.Unix(100, 0),
		9,
		protocol.OpenAIChatCompletions,
		func() time.Time { return time.Unix(102, 0) },
	)
	result := UpstreamResult{
		StatusCode:                http.StatusOK,
		ClassificationBody:        []byte(`{"error":{"type":"` + marker + `"}}`),
		ErrorSummary:              fixedErrorSummary("upstream_sse_error"),
		RequestWritten:            true,
		ProviderErrorBeforeCommit: true,
		Usage:                     usage.Result{State: usage.StateMissing},
	}
	decision := health.Result{
		Category: health.FailureCategoryRateLimited,
		Action:   health.ActionCooldownKey,
	}

	recorder.recordAttempt(
		selection,
		"fake-provider-fixed-summary-key",
		result,
		decision,
		time.Unix(100, 0),
		time.Unix(101, 0),
	)
	recorder.emit()

	events := sink.snapshot()
	if len(events) != 1 || len(events[0].Attempts) != 1 {
		t.Fatalf("events = %#v, want one event with one attempt", events)
	}
	want := fixedErrorSummary("upstream_sse_error")
	if got := events[0].Attempts[0].ErrorSummary; got != want {
		t.Fatalf("provider fixed attempt summary = %q, want %q", got, want)
	}
	if serialized := fmt.Sprintf("%#v", events[0]); strings.Contains(serialized, marker) {
		t.Fatalf("request log leaked ordinary Provider marker %q: %s", marker, serialized)
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
