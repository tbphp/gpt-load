package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"hash"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/dialect"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/contentcoding"
	platformhttp "gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/usage"
)

type usageExtractorDialect struct {
	dialect.Dialect
	extract func([]byte) (usage.Result, error)
	stream  func() dialect.UsageStreamExtractor
}

type dialectWithoutUsage struct{ dialect.Dialect }

type countingUsageStreamExtractor struct {
	observeCalls  int
	finalizeCalls int
	result        usage.Result
	observeErrAt  int
	panicAt       string
}

type safePayloadUsageStreamExtractor struct {
	observeCalls      int
	rawCredentialSeen bool
	placeholderSeen   bool
	credential        string
	result            usage.Result
	finalizeCalls     int
}

type mutatingUsageStreamExtractor struct {
	panicOnObserve bool
}

type countingUsageWarningHook struct {
	calls int
}

type hashingResponseWriter struct {
	header http.Header
	status int
	bytes  int64
	hash   hash.Hash
}

func newHashingResponseWriter() *hashingResponseWriter {
	return &hashingResponseWriter{header: make(http.Header), hash: sha256.New()}
}

func (writer *hashingResponseWriter) Header() http.Header    { return writer.header }
func (writer *hashingResponseWriter) WriteHeader(status int) { writer.status = status }
func (writer *hashingResponseWriter) Write(body []byte) (int, error) {
	written, err := writer.hash.Write(body)
	writer.bytes += int64(written)
	return written, err
}
func (*hashingResponseWriter) Flush() {}

func (extractor *safePayloadUsageStreamExtractor) Observe(payload []byte) error {
	extractor.observeCalls++
	extractor.rawCredentialSeen = extractor.rawCredentialSeen || bytes.Contains(payload, []byte(extractor.credential))
	extractor.placeholderSeen = extractor.placeholderSeen || bytes.Contains(payload, []byte(redact.Placeholder))
	return nil
}

func (extractor *safePayloadUsageStreamExtractor) Finalize() (usage.Result, bool) {
	extractor.finalizeCalls++
	return extractor.result, true
}

func (extractor *countingUsageStreamExtractor) Observe([]byte) error {
	extractor.observeCalls++
	if extractor.panicAt == "observe" {
		panic("STREAM_SECRET_CANARY")
	}
	if extractor.observeCalls == extractor.observeErrAt {
		return errors.New("STREAM_SECRET_CANARY")
	}
	return nil
}

func (extractor *countingUsageStreamExtractor) Finalize() (usage.Result, bool) {
	extractor.finalizeCalls++
	if extractor.panicAt == "finalize" {
		panic("STREAM_SECRET_CANARY")
	}
	return extractor.result, true
}

func (extractor *mutatingUsageStreamExtractor) Observe(payload []byte) error {
	for index := range payload {
		payload[index] = 'x'
	}
	if extractor.panicOnObserve {
		panic("STREAM_SECRET_CANARY")
	}
	return errors.New("STREAM_SECRET_CANARY")
}

func (*mutatingUsageStreamExtractor) Finalize() (usage.Result, bool) {
	return usage.Result{State: usage.StateMissing}, true
}

func (*countingUsageWarningHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (hook *countingUsageWarningHook) Fire(*logrus.Entry) error {
	hook.calls++
	return nil
}

func (value usageExtractorDialect) ExtractUsage(body []byte) (usage.Result, error) {
	return value.extract(body)
}

func (value usageExtractorDialect) NewUsageStreamExtractor() dialect.UsageStreamExtractor {
	if value.stream == nil {
		return nil
	}
	return value.stream()
}

func (value usageExtractorDialect) RewriteRequestModel(request *dialect.ParsedRequest, upstreamModel string) (*dialect.ParsedRequest, error) {
	return value.Dialect.(dialect.ModelRewriter).RewriteRequestModel(request, upstreamModel)
}

func (value usageExtractorDialect) RewriteResponseModel(body []byte, externalModel string) ([]byte, error) {
	return value.Dialect.(dialect.ModelRewriter).RewriteResponseModel(body, externalModel)
}

func TestStreamUsageCaptureObservesEveryPayloadAndFinalizesOnce(t *testing.T) {
	want := usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{Output: 30}}
	extractor := &countingUsageStreamExtractor{result: want}
	capture := newUsageCaptureBoundary().newStreamForRequest(usageExtractorDialect{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		stream:  func() dialect.UsageStreamExtractor { return extractor },
	}, true)
	for range 3 {
		capture.observeEvent(dialect.StreamEvent{Payload: []byte(`{"event":"payload"}`)})
	}
	first := capture.finalize()
	second := capture.finalize()

	if extractor.observeCalls != 3 || extractor.finalizeCalls != 1 || first != want || second != want {
		t.Fatalf("observe/finalize/results = %d/%d/%#v/%#v, want 3/1/%#v/%#v", extractor.observeCalls, extractor.finalizeCalls, first, second, want, want)
	}
}

func TestStreamUsageCaptureContinuesAfterObserveError(t *testing.T) {
	want := usage.Result{State: usage.StatePartial, Tokens: usage.Tokens{Output: 30}}
	extractor := &countingUsageStreamExtractor{result: want, observeErrAt: 1}
	boundary := newUsageCaptureBoundary()
	capture := boundary.newStreamForRequest(usageExtractorDialect{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		stream:  func() dialect.UsageStreamExtractor { return extractor },
	}, true)
	capture.observeEvent(dialect.StreamEvent{Payload: []byte(`{"event":"first"}`)})
	capture.observeEvent(dialect.StreamEvent{Payload: []byte(`{"event":"second"}`)})

	if got := capture.finalize(); got != want || extractor.observeCalls != 2 || extractor.finalizeCalls != 1 || boundary.failureTotal.Load() != 1 {
		t.Fatalf("result/calls/failures = %#v/%d/%d/%d", got, extractor.observeCalls, extractor.finalizeCalls, boundary.failureTotal.Load())
	}
}

func TestStreamUsageCaptureDisablesAfterConstructorOrObservePanic(t *testing.T) {
	for _, test := range []struct {
		name   string
		stream func() dialect.UsageStreamExtractor
		check  func(*testing.T, *countingUsageStreamExtractor)
	}{
		{name: "constructor panic", stream: func() dialect.UsageStreamExtractor { panic("STREAM_SECRET_CANARY") }},
		{name: "nil extractor", stream: func() dialect.UsageStreamExtractor { return nil }},
		{name: "observe panic", stream: func() dialect.UsageStreamExtractor { return &countingUsageStreamExtractor{panicAt: "observe"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			boundary := newUsageCaptureBoundary()
			capture := boundary.newStreamForRequest(
				usageExtractorDialect{
					Dialect: dialect.NewOpenAI(http.DefaultClient),
					stream:  test.stream,
				},
				true,
			)
			capture.observeEvent(
				dialect.StreamEvent{Payload: []byte(`{"event":"payload"}`)},
			)
			got := capture.finalize()
			if got != (usage.Result{State: usage.StateMissing}) || boundary.failureTotal.Load() != 1 {
				t.Fatalf("result/failures = %#v/%d", got, boundary.failureTotal.Load())
			}
		})
	}
}

func TestStreamUsageCaptureFinalizeFailureIsMissing(t *testing.T) {
	for _, test := range []struct {
		name   string
		stream dialect.UsageStreamExtractor
	}{
		{name: "panic", stream: &countingUsageStreamExtractor{panicAt: "finalize"}},
		{name: "not finalized", stream: finalizeUsageStreamExtractor{result: usage.Result{State: usage.StateComplete}, finalized: false}},
		{name: "invalid result", stream: finalizeUsageStreamExtractor{result: usage.Result{State: usage.StateMissing, Tokens: usage.Tokens{Output: 1}}, finalized: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			boundary := newUsageCaptureBoundary()
			capture := boundary.newStreamForRequest(usageExtractorDialect{
				Dialect: dialect.NewOpenAI(http.DefaultClient),
				stream:  func() dialect.UsageStreamExtractor { return test.stream },
			}, true)
			if got := capture.finalize(); got != (usage.Result{State: usage.StateMissing}) || boundary.failureTotal.Load() != 1 {
				t.Fatalf("result/failures = %#v/%d", got, boundary.failureTotal.Load())
			}
		})
	}
}

func TestUsageCaptureBoundaryCopiesInjectedRequestWithoutAliasingInput(t *testing.T) {
	boundary := newUsageCaptureBoundary()
	sharedBody := []byte(`x{"stream":true}`)
	original := &dialect.ParsedRequest{
		Method: http.MethodPost,
		Path:   "/v1/chat/completions",
		Header: http.Header{"X-Original": {"preserve"}},
		Body:   sharedBody[:len(sharedBody)-1],
	}
	wantHeader := original.Header.Clone()
	wantBody := bytes.Clone(sharedBody[1:])
	selected := streamUsageInjectorDialect{
		OpenAI: dialect.NewOpenAI(http.DefaultClient),
		inject: func(request *dialect.ParsedRequest) (*dialect.ParsedRequest, error) {
			return &dialect.ParsedRequest{
				Method: request.Method,
				Path:   request.Path,
				Header: http.Header{"X-Original": request.Header["X-Original"]},
				Body:   sharedBody[1:],
			}, nil
		},
	}

	got := boundary.injectStreamUsage(selected, original)
	if got == original {
		t.Fatal("injected request aliases the original request")
	}
	if got.Header["X-Original"] == nil || &got.Header["X-Original"][0] == &original.Header["X-Original"][0] ||
		&got.Body[0] == &original.Body[0] || &got.Body[0] == &sharedBody[1] {
		t.Fatalf("injected request is not independent: %#v", got)
	}
	original.Header["X-Original"][0] = "changed-original"
	sharedBody[1] = 'X'
	if !reflect.DeepEqual(got.Header, wantHeader) || !bytes.Equal(got.Body, wantBody) {
		t.Fatalf("injected request changed after input mutation: %#v", got)
	}
	got.Header["X-Original"][0] = "changed-derived"
	got.Body[0] = 'Y'
	if original.Header.Get("X-Original") != "changed-original" || sharedBody[1] != 'X' {
		t.Fatalf("input changed after injected request mutation: header=%#v body=%q", original.Header, sharedBody)
	}
	if got := boundary.failureTotal.Load(); got != 0 {
		t.Fatalf("failure total = %d, want 0 after defensive copy", got)
	}
}

func TestUsageCaptureBoundaryNeverExposesCallerRequestToFaultyInjector(t *testing.T) {
	tests := []struct {
		name   string
		inject func(*dialect.ParsedRequest, *dialect.ParsedRequest) (*dialect.ParsedRequest, error)
	}{
		{
			name: "mutate working then error",
			inject: func(_ *dialect.ParsedRequest, working *dialect.ParsedRequest) (*dialect.ParsedRequest, error) {
				working.Method = http.MethodDelete
				working.Path = "/mutated"
				working.RawQuery = "leaked=true"
				working.Header["X-Original"][0] = "mutated"
				working.Body[0] = 'X'
				return nil, errors.New("inject failed")
			},
		},
		{
			name: "mutate working then panic",
			inject: func(_ *dialect.ParsedRequest, working *dialect.ParsedRequest) (*dialect.ParsedRequest, error) {
				working.Path = "/panic"
				working.Header.Set("X-Malicious", "present")
				working.Body = []byte(`{"malicious":true}`)
				panic("inject panic")
			},
		},
		{
			name: "return caller",
			inject: func(caller, _ *dialect.ParsedRequest) (*dialect.ParsedRequest, error) {
				return caller, nil
			},
		},
		{
			name: "return working",
			inject: func(_ *dialect.ParsedRequest, working *dialect.ParsedRequest) (*dialect.ParsedRequest, error) {
				return working, nil
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			boundary := newUsageCaptureBoundary()
			caller := &dialect.ParsedRequest{
				Method:   http.MethodPost,
				Path:     "/v1/chat/completions",
				RawQuery: "beta=true",
				Header:   http.Header{"X-Original": {"preserve"}},
				Body:     []byte(`{"model":"gpt-4o","stream":true}`),
			}
			want := &dialect.ParsedRequest{
				Method:   http.MethodPost,
				Path:     "/v1/chat/completions",
				RawQuery: "beta=true",
				Header:   http.Header{"X-Original": {"preserve"}},
				Body:     []byte(`{"model":"gpt-4o","stream":true}`),
			}
			selected := streamUsageInjectorDialect{
				OpenAI: dialect.NewOpenAI(http.DefaultClient),
				inject: func(working *dialect.ParsedRequest) (*dialect.ParsedRequest, error) {
					return test.inject(caller, working)
				},
			}

			got := boundary.injectStreamUsage(selected, caller)
			if !reflect.DeepEqual(caller, want) {
				t.Fatalf("caller-owned request changed:\n got %#v\nwant %#v", caller, want)
			}
			if got == caller || !reflect.DeepEqual(got, want) {
				t.Fatalf("fail-open request = %#v, want independent %#v", got, want)
			}
			if got := boundary.failureTotal.Load(); got != 1 {
				t.Fatalf("failure total = %d, want 1", got)
			}
		})
	}
}

func TestForwardStreamCapturesCanonicalUsage(t *testing.T) {
	tests := []struct {
		name     string
		dialect  dialect.Dialect
		fixture  string
		wantPath string
		want     usage.Tokens
	}{
		{name: "OpenAI", dialect: dialect.NewOpenAI(http.DefaultClient), fixture: "openai/stream.jsonl", wantPath: "/v1/chat/completions", want: usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30}},
		{name: "Anthropic", dialect: dialect.NewAnthropic(http.DefaultClient), fixture: "anthropic/stream.jsonl", wantPath: "/v1/messages", want: usage.Tokens{UncachedInput: 80, CacheRead: 20, CacheWrite5M: 5, CacheWrite1H: 7, Output: 30}},
		{name: "Gemini", dialect: dialect.NewGemini(http.DefaultClient), fixture: "gemini/stream.jsonl", wantPath: "/v1beta/models/gemini:streamGenerateContent", want: usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wire := usageStreamFixtureAsSSE(t, test.fixture)
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = writer.Write(wire)
			}))
			defer upstream.Close()

			input := usageForwardInput(upstream.URL, test.dialect)
			input.Group.Timeouts.StreamIdle = time.Second
			input.Request.Path = test.wantPath
			input.Request.Body = []byte(`{"stream":true}`)
			result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(context.Background(), input, newRecordingResponseWriter())
			if result.Usage.State != usage.StateComplete || result.Usage.Tokens != test.want {
				t.Fatalf("Usage = %#v, want complete with %#v", result.Usage, test.want)
			}
		})
	}
}

func TestForwardStreamUsageStateRequiresProviderFinalEvidence(t *testing.T) {
	tests := []struct {
		name    string
		dialect dialect.Dialect
		path    string
		wire    string
		want    usage.State
	}{
		{name: "OpenAI partial", dialect: dialect.NewOpenAI(http.DefaultClient), path: "/v1/chat/completions", wire: `data: {"choices":[{"delta":{}}],"usage":{"prompt_tokens":100,"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":20}}}` + "\n\n", want: usage.StatePartial},
		{
			name:    "OpenAI terminal choice",
			dialect: dialect.NewOpenAI(http.DefaultClient),
			path:    "/v1/chat/completions",
			wire: "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"你好\"},\"finish_reason\":null}]}\n\n" +
				"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\",\"native_finish_reason\":\"stop\"}],\"usage\":{\"completion_tokens\":13,\"total_tokens\":317,\"prompt_tokens\":304,\"prompt_tokens_details\":{\"cached_tokens\":0,\"cached_creation_tokens\":0},\"completion_tokens_details\":{\"reasoning_tokens\":0}}}\n\n" +
				"data: [DONE]\n\n",
			want: usage.StateComplete,
		},
		{
			name:    "OpenAI first event is terminal usage",
			dialect: dialect.NewOpenAI(http.DefaultClient),
			path:    "/v1/chat/completions",
			wire: "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"completion_tokens\":13,\"total_tokens\":317,\"prompt_tokens\":304,\"prompt_tokens_details\":{\"cached_tokens\":0}}}\n\n" +
				"data: [DONE]\n\n",
			want: usage.StateComplete,
		},
		{name: "Anthropic partial", dialect: dialect.NewAnthropic(http.DefaultClient), path: "/v1/messages", wire: `data: {"type":"message_start","message":{"usage":{"input_tokens":80,"cache_read_input_tokens":20}}}` + "\n\n", want: usage.StatePartial},
		{name: "Gemini partial", dialect: dialect.NewGemini(http.DefaultClient), path: "/v1beta/models/gemini:streamGenerateContent", wire: `data: {"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":20,"candidatesTokenCount":30}}` + "\n\n", want: usage.StatePartial},
		{name: "missing", dialect: dialect.NewOpenAI(http.DefaultClient), path: "/v1/chat/completions", wire: "data: {\"choices\":[{\"delta\":{}}]}\n\n", want: usage.StateMissing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = writer.Write([]byte(test.wire))
			}))
			defer upstream.Close()
			input := usageForwardInput(upstream.URL, test.dialect)
			input.Group.Timeouts.StreamIdle = time.Second
			input.Request.Path = test.path
			result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(context.Background(), input, newRecordingResponseWriter())
			if result.Usage.State != test.want {
				t.Fatalf("Usage state = %q, want %q", result.Usage.State, test.want)
			}
		})
	}
}

func TestForwardStreamProviderErrorFirstEventIsObservedOnceAfterSafetyBoundary(t *testing.T) {
	extractor := &countingUsageStreamExtractor{
		result: usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{Output: 1}},
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("event: error\ndata: {\"error\":{\"message\":\"quota exhausted\"}}\n\n"))
	}))
	defer upstream.Close()

	input := usageForwardInput(upstream.URL, usageExtractorDialect{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		stream:  func() dialect.UsageStreamExtractor { return extractor },
	})
	input.Group.Timeouts.StreamIdle = time.Second
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
		context.Background(),
		input,
		newRecordingResponseWriter(),
	)
	if !result.ProviderErrorBeforeCommit || extractor.observeCalls != 1 ||
		result.Usage != extractor.result {
		t.Fatalf("result/observe calls = %#v/%d, want provider error and one safely buffered observation", result, extractor.observeCalls)
	}
}

func TestUsageCaptureWarningsAreCountedThrottledAndRedacted(t *testing.T) {
	boundary := newUsageCaptureBoundary()
	var logs bytes.Buffer
	boundary.logger = logrus.New()
	boundary.logger.SetOutput(&logs)
	boundary.logger.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	now := time.Unix(100, 0)
	boundary.now = func() time.Time { return now }
	for range 3 {
		boundary.recordFailure("stream_observe", protocol.OpenAICompletions)
	}
	now = now.Add(time.Minute)
	boundary.recordFailure("stream_finalize", protocol.Anthropic)

	lines := bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte{'\n'})
	if boundary.failureTotal.Load() != 4 || len(lines) != 2 {
		t.Fatalf("failures/warnings = %d/%d, want 4/2: %q", boundary.failureTotal.Load(), len(lines), logs.String())
	}
	for _, canary := range []string{"payload-canary", "credential-canary", "error-canary", "model-canary", "url-canary", "group-canary", "key-canary", "STREAM_SECRET_CANARY"} {
		if bytes.Contains(logs.Bytes(), []byte(canary)) {
			t.Fatalf("warning leaked %q: %q", canary, logs.String())
		}
	}
	for _, line := range lines {
		for _, field := range []string{`"phase":`, `"protocol":`, `"total":`} {
			if !bytes.Contains(line, []byte(field)) {
				t.Fatalf("warning missing %s: %q", field, line)
			}
		}
	}
}

func TestNewUsageCaptureBoundaryUsesStandardLoggerConfiguration(t *testing.T) {
	standard := logrus.StandardLogger()
	originalOutput := standard.Out
	originalFormatter := standard.Formatter
	originalLevel := standard.GetLevel()
	originalHooks := standard.ReplaceHooks(make(logrus.LevelHooks))
	t.Cleanup(func() {
		standard.SetOutput(originalOutput)
		standard.SetFormatter(originalFormatter)
		standard.SetLevel(originalLevel)
		standard.ReplaceHooks(originalHooks)
	})

	var logs bytes.Buffer
	hook := &countingUsageWarningHook{}
	standard.SetOutput(&logs)
	standard.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	standard.SetLevel(logrus.WarnLevel)
	standard.AddHook(hook)

	boundary := newUsageCaptureBoundary()
	if boundary.logger != standard {
		t.Fatal("usage capture boundary does not use the standard logger")
	}
	boundary.recordFailure("stream_observe", protocol.OpenAICompletions)
	if hook.calls != 1 ||
		!bytes.Contains(logs.Bytes(), []byte(`"msg":"[DATA] Usage capture failure"`)) ||
		!bytes.Contains(logs.Bytes(), []byte(`"plane":"data"`)) ||
		!bytes.Contains(logs.Bytes(), []byte(`"level":"warning"`)) {
		t.Fatalf("standard logger output/hooks = %q/%d", logs.String(), hook.calls)
	}

	loggedBytes := logs.Len()
	standard.SetLevel(logrus.ErrorLevel)
	newUsageCaptureBoundary().recordFailure("stream_finalize", protocol.OpenAICompletions)
	if logs.Len() != loggedBytes || hook.calls != 1 {
		t.Fatalf("LOG_LEVEL was not respected: output=%q hooks=%d", logs.String(), hook.calls)
	}
}

func TestForwardStreamFinalUsageSurvivesDownstreamFailure(t *testing.T) {
	const wire = "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":30,\"prompt_tokens_details\":{\"cached_tokens\":20}}}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(wire))
	}))
	defer upstream.Close()

	downstream := newRecordingResponseWriter()
	downstream.writeErr = errors.New("downstream failure")
	input := streamForwardInput(upstream.URL)
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(context.Background(), input, downstream)
	want := usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30}}
	if result.Usage != want || result.Stream.EndReason != StreamEndDownstreamWriteFailure {
		t.Fatalf("result = %#v, want Usage %#v and downstream failure", result, want)
	}
}

func TestForwardStreamObservesKnownSecretRedactedPayload(t *testing.T) {
	const secret = "stream-secret-canary"
	extractor := &safePayloadUsageStreamExtractor{
		credential: secret,
		result:     usage.Result{State: usage.StateComplete},
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(`data: {"credential":"stream-secret-canary","choices":[]}` + "\n\n"))
	}))
	defer upstream.Close()

	input := streamForwardInput(upstream.URL)
	input.APIKey = secret
	input.Dialect = usageExtractorDialect{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		stream:  func() dialect.UsageStreamExtractor { return extractor },
	}
	downstream := newRecordingResponseWriter()
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(context.Background(), input, downstream)
	if result.Err != nil || extractor.observeCalls != 1 || extractor.rawCredentialSeen || !extractor.placeholderSeen || bytes.Contains(downstream.body.Bytes(), []byte(secret)) || !bytes.Contains(downstream.body.Bytes(), []byte(redact.Placeholder)) {
		t.Fatalf("result/extractor/downstream = %#v/%#v/%q", result, extractor, downstream.body.String())
	}
}

func TestForwardStreamRedactionFailurePreventsUsageObserve(t *testing.T) {
	const secret = "secret"
	extractor := &safePayloadUsageStreamExtractor{credential: secret, result: usage.Result{State: usage.StateComplete}}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(`data: {"secret":"leak","[REDACTED]":"safe"}` + "\n\n"))
	}))
	defer upstream.Close()

	input := streamForwardInput(upstream.URL)
	input.APIKey = secret
	input.Dialect = usageExtractorDialect{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		stream:  func() dialect.UsageStreamExtractor { return extractor },
	}
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(context.Background(), input, newRecordingResponseWriter())
	if !errors.Is(result.Err, ErrUpstreamProtocol) || extractor.observeCalls != 0 {
		t.Fatalf("result/observe calls = %#v/%d", result, extractor.observeCalls)
	}
}

func TestForwardStreamUsageExtractorMutationCannotAffectDataPlane(t *testing.T) {
	tests := []struct {
		name           string
		panicOnObserve bool
		rewriteModel   bool
		wantFailures   uint64
	}{
		{name: "error", panicOnObserve: false, wantFailures: 2},
		{name: "panic with model rewrite", panicOnObserve: true, rewriteModel: true, wantFailures: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			const providerPayload = `{"error":{"message":"provider-model failed"},"model":"provider-model"}`
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = writer.Write([]byte(
					`data: {"ready":true}` + "\n\n" +
						"event: error\ndata: " + providerPayload + "\n\n",
				))
			}))
			defer upstream.Close()

			run := func(selected dialect.Dialect) (UpstreamResult, *recordingResponseWriter, uint64) {
				input := streamForwardInput(upstream.URL)
				input.Dialect = selected
				if test.rewriteModel {
					input.ExternalModel = "public-model"
					input.UpstreamModelID = "provider-model"
					input.Request.Body = []byte(`{"model":"public-model","stream":true}`)
				}
				forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
				downstream := newRecordingResponseWriter()
				result := forwarder.ForwardStream(context.Background(), input, downstream)
				return result, downstream, forwarder.usageCapture.failureTotal.Load()
			}

			baselineResult, baselineDownstream, baselineFailures := run(dialect.NewOpenAI(http.DefaultClient))
			result, downstream, failures := run(usageExtractorDialect{
				Dialect: dialect.NewOpenAI(http.DefaultClient),
				stream: func() dialect.UsageStreamExtractor {
					return &mutatingUsageStreamExtractor{panicOnObserve: test.panicOnObserve}
				},
			})

			baselineHeader := baselineDownstream.header.Clone()
			baselineHeader.Del("Date")
			header := downstream.header.Clone()
			header.Del("Date")
			if baselineResult.Err != nil || result.Err != nil ||
				result.Committed != baselineResult.Committed ||
				result.RequestWritten != baselineResult.RequestWritten ||
				result.Stream != baselineResult.Stream ||
				result.Usage != baselineResult.Usage ||
				downstream.status != baselineDownstream.status ||
				!reflect.DeepEqual(header, baselineHeader) ||
				!bytes.Equal(downstream.body.Bytes(), baselineDownstream.body.Bytes()) ||
				baselineFailures != 0 || failures != test.wantFailures {
				t.Fatalf(
					"mutating extractor changed data plane:\n baseline result=%#v status=%d header=%#v body=%q failures=%d\n observed result=%#v status=%d header=%#v body=%q failures=%d",
					baselineResult, baselineDownstream.status, baselineHeader, baselineDownstream.body.String(), baselineFailures,
					result, downstream.status, header, downstream.body.String(), failures,
				)
			}
		})
	}
}

func TestForwardStreamSkipsNonPayloadSSEEventsForUsage(t *testing.T) {
	extractor := &safePayloadUsageStreamExtractor{result: usage.Result{State: usage.StatePartial}}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(": comment\n\ndata:\n\ndata: [DONE]\n\ndata: {\"one\":\ndata: 1}\n\n"))
	}))
	defer upstream.Close()

	input := streamForwardInput(upstream.URL)
	input.Dialect = usageExtractorDialect{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		stream:  func() dialect.UsageStreamExtractor { return extractor },
	}
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(context.Background(), input, newRecordingResponseWriter())
	if result.Err != nil || extractor.observeCalls != 1 || extractor.finalizeCalls != 1 {
		t.Fatalf("result/calls = %#v/%d/%d", result, extractor.observeCalls, extractor.finalizeCalls)
	}
}

func TestForwardStreamUsageDoesNotBufferWholeResponse(t *testing.T) {
	payload := `{"padding":"` + strings.Repeat("x", 900) + `"}`
	event := []byte("data: " + payload + "\n\n")
	events := int(maxNonStreamingResponseBodyBytes)/len(event) + 1
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher := writer.(http.Flusher)
		for index := 0; index < events; index++ {
			_, _ = writer.Write(event)
			if index%128 == 0 {
				flusher.Flush()
			}
		}
	}))
	defer upstream.Close()

	extractor := &safePayloadUsageStreamExtractor{result: usage.Result{State: usage.StateComplete}}
	input := streamForwardInput(upstream.URL)
	input.Dialect = usageExtractorDialect{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		stream:  func() dialect.UsageStreamExtractor { return extractor },
	}
	downstream := newHashingResponseWriter()
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(context.Background(), input, downstream)
	wantHash := sha256.New()
	for range events {
		_, _ = wantHash.Write(event)
	}
	if result.Err != nil || downstream.bytes != int64(events*len(event)) || !bytes.Equal(downstream.hash.Sum(nil), wantHash.Sum(nil)) || extractor.observeCalls != events || extractor.finalizeCalls != 1 || result.Usage.State != usage.StateComplete {
		t.Fatalf("result/bytes/calls/usage = %#v/%d/%d/%d/%#v", result, downstream.bytes, extractor.observeCalls, extractor.finalizeCalls, result.Usage)
	}
}

func usageStreamFixtureAsSSE(t *testing.T, name string) []byte {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	body, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "dialect", "testdata", "usage", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var wire bytes.Buffer
	for _, line := range bytes.Split(bytes.TrimSpace(body), []byte{'\n'}) {
		_, _ = wire.WriteString("data: ")
		_, _ = wire.Write(line)
		_, _ = wire.WriteString("\n\n")
	}
	return wire.Bytes()
}

type finalizeUsageStreamExtractor struct {
	result    usage.Result
	finalized bool
}

func (extractor finalizeUsageStreamExtractor) Observe([]byte) error { return nil }
func (extractor finalizeUsageStreamExtractor) Finalize() (usage.Result, bool) {
	return extractor.result, extractor.finalized
}

func usageForwardInput(upstreamURL string, selected dialect.Dialect) ForwardInput {
	return ForwardInput{
		Dialect:      selected,
		ObserveUsage: true,
		Group: state.GroupView{
			ID: 1, UpstreamURL: testUpstreamBaseURL(upstreamURL, selected.Protocol()),
			Timeouts: state.TimeoutConfig{Connect: time.Second, FirstByte: time.Second, Request: time.Second},
		},
		APIKey: "sk-test",
		Request: &dialect.ParsedRequest{
			Method: http.MethodPost, Path: "/v1/chat/completions",
			Header: make(http.Header), Body: []byte(`{"model":"gpt-4o"}`),
		},
	}
}

func TestResponsesObserveUsageGatesNonStreamingCapture(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"id":"resp_123",
			"usage":{
				"input_tokens":100,
				"input_tokens_details":{"cached_tokens":20},
				"output_tokens":30,
				"total_tokens":130
			}
		}`))
	}))
	defer upstream.Close()

	selected := dialect.NewOpenAIResponses(http.DefaultClient)
	forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
	newInput := func(observe bool, method, path, body string) ForwardInput {
		return ForwardInput{
			Dialect:      selected,
			ObserveUsage: observe,
			Group: state.GroupView{
				ID: 1, UpstreamURL: upstream.URL,
				Timeouts: state.TimeoutConfig{
					Connect: time.Second,
					Request: time.Second,
				},
			},
			APIKey: "sk-responses",
			Request: &dialect.ParsedRequest{
				Method: method,
				Path:   path,
				Header: make(http.Header),
				Body:   []byte(body),
			},
		}
	}

	create := forwarder.Forward(
		context.Background(),
		newInput(
			true,
			http.MethodPost,
			"/v1/responses",
			`{"model":"gpt-5","input":"ping"}`,
		),
	)
	want := usage.Result{
		State: usage.StateComplete,
		Tokens: usage.Tokens{
			UncachedInput: 80,
			CacheRead:     20,
			Output:        30,
		},
	}
	if create.Usage != want {
		t.Fatalf("create Usage = %#v, want %#v", create.Usage, want)
	}

	retrieve := forwarder.Forward(
		context.Background(),
		newInput(
			false,
			http.MethodGet,
			"/v1/responses/resp_123",
			"",
		),
	)
	if retrieve.Usage != (usage.Result{State: usage.StateNotApplicable}) {
		t.Fatalf("retrieve Usage = %#v, want not_applicable", retrieve.Usage)
	}
}

func assertForwardWireContract(t *testing.T, selected dialect.Dialect, got, want UpstreamResult) {
	t.Helper()
	if got.StatusCode != want.StatusCode || !reflect.DeepEqual(got.Header, want.Header) ||
		!bytes.Equal(got.Body, want.Body) || !bytes.Equal(got.ClassificationBody, want.ClassificationBody) ||
		got.Err != want.Err || got.RequestWritten != want.RequestWritten {
		t.Fatalf("wire result = %#v, baseline = %#v", got, want)
	}
	toAttempt := func(result UpstreamResult) health.Attempt {
		return health.Attempt{
			StatusCode: result.StatusCode, Body: result.ClassificationBody, Header: result.Header,
			Now: time.Unix(100, 0), Err: result.Err, RequestWritten: result.RequestWritten,
			Committed: result.Committed, RetryableBeforeCommit: result.RetryableBeforeCommit,
		}
	}
	if decision := health.Judge(selected, toAttempt(got)); decision != health.Judge(selected, toAttempt(want)) {
		t.Fatalf("health decision = %#v, baseline = %#v", decision, health.Judge(selected, toAttempt(want)))
	}
}

func forwardWithCaptureDisabled(input ForwardInput) UpstreamResult {
	forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
	forwarder.usageCapture = nil
	return forwarder.Forward(context.Background(), input)
}

func TestUsageCaptureBoundaryExtractsValidNonStreamingResult(t *testing.T) {
	want := usage.Result{
		State:  usage.StateComplete,
		Tokens: usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30},
	}
	boundary := newUsageCaptureBoundary()
	got := boundary.extractNonStreamingPlain(usageExtractorDialect{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		extract: func([]byte) (usage.Result, error) { return want, nil },
	}, []byte(`{"provider":"body"}`))

	if got != want || boundary.failureTotal.Load() != 0 {
		t.Fatalf("result/failures = %#v/%d, want %#v/0", got, boundary.failureTotal.Load(), want)
	}
}

func TestUsageCaptureBoundaryMissingCapabilityIsQuietMissing(t *testing.T) {
	boundary := newUsageCaptureBoundary()
	var logs bytes.Buffer
	boundary.logger = logrus.New()
	boundary.logger.SetOutput(&logs)
	got := boundary.extractNonStreamingPlain(dialectWithoutUsage{Dialect: dialect.NewOpenAI(http.DefaultClient)}, []byte(`{}`))

	if got != (usage.Result{State: usage.StateMissing}) || boundary.failureTotal.Load() != 0 || logs.Len() != 0 {
		t.Fatalf("result/failures/logs = %#v/%d/%q", got, boundary.failureTotal.Load(), logs.String())
	}
}

func TestUsageCaptureBoundaryRecoversExtractErrorAndPanic(t *testing.T) {
	canary := "capture-canary-must-not-log"
	for _, test := range []struct {
		name    string
		extract func([]byte) (usage.Result, error)
	}{
		{name: "error", extract: func([]byte) (usage.Result, error) { return usage.Result{}, errors.New(canary) }},
		{name: "panic", extract: func([]byte) (usage.Result, error) { panic(canary) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			boundary := newUsageCaptureBoundary()
			var logs bytes.Buffer
			boundary.logger = logrus.New()
			boundary.logger.SetOutput(&logs)
			got := boundary.extractNonStreamingPlain(usageExtractorDialect{Dialect: dialect.NewOpenAI(http.DefaultClient), extract: test.extract}, []byte(`{}`))

			if got.State != usage.StateMissing || !got.Diagnostics.Has(usage.DiagnosticInvalidPayload) || boundary.failureTotal.Load() != 1 || bytes.Contains(logs.Bytes(), []byte(canary)) {
				t.Fatalf("result/failures/logs = %#v/%d/%q", got, boundary.failureTotal.Load(), logs.String())
			}
		})
	}
}

func TestUsageCaptureBoundaryRejectsInvalidResults(t *testing.T) {
	for _, result := range []usage.Result{
		{},
		{State: usage.StateNotApplicable},
		{State: usage.State("future")},
		{State: usage.StateMissing, Tokens: usage.Tokens{Output: 1}},
		{State: usage.StateComplete, Tokens: usage.Tokens{Output: -1}},
		{State: usage.StateComplete, Tokens: usage.Tokens{CacheWriteUnknown: -1}},
		{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: math.MaxInt64, Output: 1}},
	} {
		boundary := newUsageCaptureBoundary()
		got := boundary.extractNonStreamingPlain(usageExtractorDialect{Dialect: dialect.NewOpenAI(http.DefaultClient), extract: func([]byte) (usage.Result, error) { return result, nil }}, []byte(`{}`))
		if got.State != usage.StateMissing || !got.Diagnostics.Has(usage.DiagnosticInvalidPayload) || boundary.failureTotal.Load() != 1 {
			t.Fatalf("input/result/failures = %#v/%#v/%d", result, got, boundary.failureTotal.Load())
		}
	}

	boundary := newUsageCaptureBoundary()
	zero := usage.Result{State: usage.StateComplete}
	if got := boundary.extractNonStreamingPlain(usageExtractorDialect{Dialect: dialect.NewOpenAI(http.DefaultClient), extract: func([]byte) (usage.Result, error) { return zero, nil }}, []byte(`{}`)); got != zero || boundary.failureTotal.Load() != 0 {
		t.Fatalf("zero complete/failures = %#v/%d", got, boundary.failureTotal.Load())
	}
}

func TestForwardCapturesCanonicalNonStreamingUsage(t *testing.T) {
	want := usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30}}
	tests := []struct {
		name    string
		dialect dialect.Dialect
		path    string
		body    []byte
	}{
		{name: "openai", dialect: dialect.NewOpenAI(http.DefaultClient), path: "/v1/chat/completions", body: []byte(`{"usage":{"prompt_tokens":100,"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":20}}}`)},
		{name: "anthropic", dialect: dialect.NewAnthropic(http.DefaultClient), path: "/v1/messages", body: []byte(`{"usage":{"input_tokens":80,"cache_read_input_tokens":20,"output_tokens":30}}`)},
		{name: "gemini", dialect: dialect.NewGemini(http.DefaultClient), path: "/v1beta/models/gemini:generateContent", body: []byte(`{"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":20,"candidatesTokenCount":30}}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write(test.body)
			}))
			defer upstream.Close()

			result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).Forward(context.Background(), ForwardInput{
				Dialect:      test.dialect,
				ObserveUsage: true,
				Group:        state.GroupView{ID: 1, UpstreamURL: upstream.URL, Timeouts: state.TimeoutConfig{Connect: time.Second, FirstByte: time.Second, Request: time.Second}},
				APIKey:       "sk-test",
				Request:      &dialect.ParsedRequest{Method: http.MethodPost, Path: test.path, Header: make(http.Header), Body: []byte(`{}`)},
			})
			if result.Err != nil || result.Usage != want {
				t.Fatalf("result = %#v, want Usage %#v", result, want)
			}
		})
	}
}

func TestForwarderUsesPreparedPlainBodyForUsage(t *testing.T) {
	plain := []byte(`{"usage":{"prompt_tokens":100,"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":20}}}`)
	wire := encodeContentCodingForGatewayTest(t, contentcoding.Gzip, plain)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Encoding", "gzip")
		writer.Header().Set("Content-Length", strconv.Itoa(len(wire)))
		writer.Header().Set("ETag", `"wire-v1"`)
		writer.Header().Set("Digest", "sha-256=wire-digest")
		writer.Header().Set("Date", "Mon, 02 Jan 2006 15:04:05 GMT")
		_, _ = writer.Write(wire)
	}))
	defer upstream.Close()

	selected := dialect.NewOpenAI(http.DefaultClient)
	input := usageForwardInput(upstream.URL, selected)
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).Forward(context.Background(), input)
	baseline := forwardWithCaptureDisabled(input)
	want := usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30}}
	if result.Usage != want || baseline.Usage != (usage.Result{State: usage.StateNotApplicable}) ||
		!bytes.Equal(result.Body, plain) ||
		result.Header.Get("Content-Length") != strconv.Itoa(len(plain)) ||
		len(headerFieldValues(result.Header, "Content-Encoding")) != 0 {
		t.Fatalf("result = %#v", result)
	}
	assertForwardWireContract(t, selected, result, baseline)
}

func TestForwardUsageCaptureFailureAndMissingCasesPreserveWireContract(t *testing.T) {
	for _, test := range []struct {
		name         string
		selected     dialect.Dialect
		body         []byte
		wantUsage    usage.Result
		wantFailures uint64
	}{
		{name: "missing capability", selected: dialectWithoutUsage{Dialect: dialect.NewOpenAI(http.DefaultClient)}, body: []byte(`{"id":"ok"}`), wantUsage: usage.Result{State: usage.StateMissing}},
		{name: "success without usage", selected: dialect.NewOpenAI(http.DefaultClient), body: []byte(`{"id":"ok"}`), wantUsage: missingUsage(false)},
		{name: "malformed provider payload", selected: dialect.NewOpenAI(http.DefaultClient), body: []byte(`{"usage":`), wantUsage: missingUsage(true), wantFailures: 1},
		{name: "extractor error", selected: usageExtractorDialect{Dialect: dialect.NewOpenAI(http.DefaultClient), extract: func([]byte) (usage.Result, error) { return usage.Result{}, errors.New("capture error") }}, body: []byte(`{"id":"ok"}`), wantUsage: missingUsage(true), wantFailures: 1},
		{name: "extractor panic", selected: usageExtractorDialect{Dialect: dialect.NewOpenAI(http.DefaultClient), extract: func([]byte) (usage.Result, error) { panic("capture panic") }}, body: []byte(`{"id":"ok"}`), wantUsage: missingUsage(true), wantFailures: 1},
		{name: "invalid extractor result", selected: usageExtractorDialect{Dialect: dialect.NewOpenAI(http.DefaultClient), extract: func([]byte) (usage.Result, error) {
			return usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{Output: -1}}, nil
		}}, body: []byte(`{"id":"ok"}`), wantUsage: missingUsage(true), wantFailures: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.Header().Set("Date", "Mon, 02 Jan 2006 15:04:05 GMT")
				_, _ = writer.Write(test.body)
			}))
			defer upstream.Close()

			input := usageForwardInput(upstream.URL, test.selected)
			forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
			got := forwarder.Forward(context.Background(), input)
			baseline := forwardWithCaptureDisabled(input)
			if got.Usage != test.wantUsage || baseline.Usage != (usage.Result{State: usage.StateNotApplicable}) || forwarder.usageCapture.failureTotal.Load() != test.wantFailures {
				t.Fatalf("Usage/failures = %#v/%d, want %#v/%d", got.Usage, forwarder.usageCapture.failureTotal.Load(), test.wantUsage, test.wantFailures)
			}
			assertForwardWireContract(t, test.selected, got, baseline)
		})
	}
}

func TestForwardUsageCaptureReadsProviderBodyBeforeAliasRewrite(t *testing.T) {
	const (
		upstreamModel = "provider-model"
		externalModel = "public-model"
	)
	body := []byte(`{"model":"provider-model","usage":{"prompt_tokens":100,"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":20}}}`)
	seenProviderBody := false
	selected := usageExtractorDialect{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		extract: func(captured []byte) (usage.Result, error) {
			seenProviderBody = bytes.Contains(captured, []byte(upstreamModel)) && !bytes.Contains(captured, []byte(externalModel))
			return usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30}}, nil
		},
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Date", "Mon, 02 Jan 2006 15:04:05 GMT")
		_, _ = writer.Write(body)
	}))
	defer upstream.Close()

	input := usageForwardInput(upstream.URL, selected)
	input.ExternalModel = externalModel
	input.UpstreamModelID = upstreamModel
	input.Request.Body = []byte(`{"model":"public-model"}`)
	got := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).Forward(context.Background(), input)
	baseline := forwardWithCaptureDisabled(input)
	if !seenProviderBody || got.Usage != (usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30}}) || baseline.Usage != (usage.Result{State: usage.StateNotApplicable}) || !bytes.Contains(got.Body, []byte(externalModel)) || bytes.Contains(got.Body, []byte(upstreamModel)) {
		t.Fatalf("seen/body/Usage = %t/%q/%#v", seenProviderBody, got.Body, got.Usage)
	}
	assertForwardWireContract(t, selected, got, baseline)
}
