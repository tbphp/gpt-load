package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/usage"
)

type fakeExecutionExecutor struct {
	unary  func(context.Context, execution.AttemptSpec) execution.AttemptResult
	stream func(context.Context, execution.AttemptSpec, execution.StreamSink) execution.StreamResult
}

func (fakeExecutionExecutor) Capabilities() execution.CapabilitySet {
	return execution.CapabilitySet{}
}

func (value fakeExecutionExecutor) Execute(
	ctx context.Context,
	spec execution.AttemptSpec,
) execution.AttemptResult {
	return value.unary(ctx, spec)
}

func (value fakeExecutionExecutor) ExecuteStream(
	ctx context.Context,
	spec execution.AttemptSpec,
	sink execution.StreamSink,
) execution.StreamResult {
	return value.stream(ctx, spec, sink)
}

func TestExecutionForwarderBuildsFrozenAttemptAndMapsUnaryResult(t *testing.T) {
	t.Parallel()

	wantUsage := usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 7, Output: 3}}
	executor := fakeExecutionExecutor{unary: func(
		_ context.Context,
		spec execution.AttemptSpec,
	) execution.AttemptResult {
		if spec.RequestID != "request-1" || spec.AttemptID != "attempt-1" || spec.Sequence != 2 ||
			spec.ChannelID != "openai" || spec.TargetKind != "openai" ||
			spec.RouteMode != execution.RouteNative ||
			!spec.RequiredFeatures.Has(execution.FeatureTools) ||
			spec.ClientProtocol != protocol.OpenAICompletions ||
			spec.Operation != execution.OperationChatCompletion || spec.ClientModel != "public" ||
			spec.UpstreamModel != "upstream" || spec.Method != http.MethodPost ||
			spec.Path != "/v1/chat/completions" || spec.RawQuery != "trace=kept" ||
			string(spec.Body) != `{"model":"public"}` || spec.Credential.ID != 7 {
			t.Fatalf("attempt spec = %#v", spec)
		}
		if spec.Header.Get("Authorization") != "" || spec.Header.Get("X-Remove") != "" ||
			spec.Header.Get("X-Template") != "Bearer secret" || spec.Header.Get("X-Test") != "kept" {
			t.Fatalf("attempt headers = %#v", spec.Header)
		}
		return execution.AttemptResult{
			DispatchState:     execution.DispatchMaybeSent,
			ResponseStarted:   true,
			StatusCode:        http.StatusOK,
			Header:            http.Header{"X-Request-Id": {"upstream-1"}},
			Body:              []byte(`{"ok":true}`),
			Model:             "upstream",
			UpstreamRequestID: "upstream-1",
			Usage:             &execution.UsageEvidence{Normalized: wantUsage},
		}
	}}

	result := NewExecutionForwarder(executor).Forward(context.Background(), executionForwardInput())
	if result.Err != nil || result.StatusCode != http.StatusOK ||
		string(result.Body) != `{"ok":true}` || !reflect.DeepEqual(result.Usage, wantUsage) ||
		result.DispatchState != execution.DispatchMaybeSent || !result.ResponseStarted ||
		result.UpstreamRequestID != "upstream-1" || result.UpstreamReportedModel != "upstream" {
		t.Fatalf("Forward() = %#v", result)
	}
}

func TestExecutionForwarderKeepsHTTPFailureAsUncommittedResponse(t *testing.T) {
	t.Parallel()

	evidence := execution.ErrorEvidence{
		Kind: execution.ErrorKindHTTP, StatusCode: http.StatusTooManyRequests,
		Summary: "upstream rejected request", RetryAfter: 3 * time.Second,
	}
	executor := fakeExecutionExecutor{unary: func(context.Context, execution.AttemptSpec) execution.AttemptResult {
		return execution.AttemptResult{
			DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Retry-After": {"3"}},
			Body:       []byte(`{"error":{"type":"rate_limit"}}`),
			Error:      &evidence,
		}
	}}

	result := NewExecutionForwarder(executor).Forward(context.Background(), executionForwardInput())
	if result.Err != nil || !result.HasResponse() || result.ExecutionError == nil ||
		result.ExecutionError.Kind != execution.ErrorKindHTTP || result.ErrorSummary != evidence.Summary {
		t.Fatalf("Forward() = %#v", result)
	}
	result.ExecutionError.Summary = "changed"
	if evidence.Summary != "upstream rejected request" {
		t.Fatal("Forward() retained executor-owned error evidence")
	}
}

func TestExecutionForwarderProjectsConvertedErrorsToClientProtocol(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		protocol protocol.Protocol
		dialect  dialect.Dialect
		contains []string
	}{
		{name: "OpenAI", protocol: protocol.OpenAIResponses, dialect: dialect.NewOpenAI(), contains: []string{`"error"`, `"type":"rate_limit_error"`}},
		{name: "Anthropic", protocol: protocol.Anthropic, dialect: dialect.NewAnthropic(), contains: []string{`"type":"error"`, `"type":"rate_limit_error"`}},
		{name: "Gemini", protocol: protocol.Gemini, dialect: dialect.NewGemini(), contains: []string{`"code":429`, `"status":"RESOURCE_EXHAUSTED"`}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := fakeExecutionExecutor{unary: func(context.Context, execution.AttemptSpec) execution.AttemptResult {
				return execution.AttemptResult{
					DispatchState:   execution.DispatchMaybeSent,
					ResponseStarted: true,
					StatusCode:      http.StatusTooManyRequests,
					Header:          http.Header{"Content-Type": {"application/json"}},
					Body:            []byte(`{"error":{"message":"provider-shaped"}}`),
					Error: &execution.ErrorEvidence{
						Kind: execution.ErrorKindHTTP, Hint: execution.FailureHintRateLimited,
						StatusCode: http.StatusTooManyRequests, Type: "rate_limit_error",
						Code: "RESOURCE_EXHAUSTED", Summary: "upstream returned HTTP 429",
					},
				}
			}}
			input := executionForwardInput()
			input.ClientProtocol = test.protocol
			input.Dialect = test.dialect
			input.RouteMode = execution.RouteConverted
			result := NewExecutionForwarder(executor).Forward(context.Background(), input)
			for _, want := range test.contains {
				if !strings.Contains(string(result.Body), want) {
					t.Fatalf("converted %s error body = %s, want %q", test.name, result.Body, want)
				}
			}
			if result.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("content type = %q", result.Header.Get("Content-Type"))
			}
		})
	}
}

func TestExecutionForwarderCommitsSuccessfulStreamOnlyOnFirstData(t *testing.T) {
	t.Parallel()

	readyObserved := false
	firstResponseObserved := false
	executor := fakeExecutionExecutor{stream: func(
		_ context.Context,
		_ execution.AttemptSpec,
		sink execution.StreamSink,
	) execution.StreamResult {
		if err := sink(execution.StreamEvent{
			Sequence: 1, Kind: execution.StreamEventReady, StatusCode: http.StatusOK,
			Header:            http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"stream-1"}},
			UpstreamRequestID: "stream-1",
		}); err != nil {
			t.Fatalf("ready sink: %v", err)
		}
		if readyObserved || firstResponseObserved {
			t.Fatal("ready metadata committed downstream before first data")
		}
		if err := sink(execution.StreamEvent{Sequence: 2, Kind: execution.StreamEventData, Data: []byte("data: one\n\n")}); err != nil {
			t.Fatalf("data sink: %v", err)
		}
		if !readyObserved || !firstResponseObserved {
			t.Fatal("first data did not cross commit boundary")
		}
		if err := sink(execution.StreamEvent{Sequence: 3, Kind: execution.StreamEventData, Data: []byte("data: [DONE]\n\n")}); err != nil {
			t.Fatalf("done sink: %v", err)
		}
		return execution.StreamResult{
			DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
			UpstreamRequestID: "stream-1",
		}
	}}
	input := executionForwardInput()
	input.OnStreamReady = func() { readyObserved = true }
	input.OnFirstResponse = func() { firstResponseObserved = true }
	recorder := httptest.NewRecorder()
	result := NewExecutionForwarder(executor).ForwardStream(context.Background(), input, recorder)
	if result.Err != nil || !result.Committed || result.Stream.EndReason != StreamEndCleanEOF ||
		recorder.Code != http.StatusOK || recorder.Body.String() != "data: one\n\ndata: [DONE]\n\n" {
		t.Fatalf("ForwardStream() result=%#v response=%d %q", result, recorder.Code, recorder.Body.String())
	}
}

func TestExecutionForwarderBuffersRejectedStreamWithoutCommitting(t *testing.T) {
	t.Parallel()

	evidence := execution.ErrorEvidence{
		Kind: execution.ErrorKindHTTP, StatusCode: http.StatusUnauthorized,
		Summary: "upstream rejected request",
	}
	executor := fakeExecutionExecutor{stream: func(
		_ context.Context,
		_ execution.AttemptSpec,
		sink execution.StreamSink,
	) execution.StreamResult {
		if err := sink(execution.StreamEvent{
			Sequence: 1, Kind: execution.StreamEventReady,
			StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": {"application/json"}},
		}); err != nil {
			t.Fatalf("ready sink: %v", err)
		}
		if err := sink(execution.StreamEvent{Sequence: 2, Kind: execution.StreamEventData, Data: []byte(`{"error":"invalid"}`)}); err != nil {
			t.Fatalf("data sink: %v", err)
		}
		return execution.StreamResult{
			DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": {"application/json"}},
			Error: &evidence,
		}
	}}

	recorder := httptest.NewRecorder()
	result := NewExecutionForwarder(executor).ForwardStream(
		context.Background(), executionForwardInput(), recorder,
	)
	if result.Committed || recorder.Code != http.StatusOK || recorder.Body.Len() != 0 ||
		result.StatusCode != http.StatusUnauthorized || string(result.Body) != `{"error":"invalid"}` ||
		result.ExecutionError == nil {
		t.Fatalf("ForwardStream() result=%#v response=%d %q", result, recorder.Code, recorder.Body.String())
	}
}

func TestExecutionForwarderDoesNotRetryOrCommitAfterDownstreamFailure(t *testing.T) {
	t.Parallel()

	downstreamErr := errors.New("downstream closed")
	executor := fakeExecutionExecutor{stream: func(
		_ context.Context,
		_ execution.AttemptSpec,
		sink execution.StreamSink,
	) execution.StreamResult {
		_ = sink(execution.StreamEvent{
			Sequence: 1, Kind: execution.StreamEventReady, StatusCode: http.StatusOK,
			Header: make(http.Header),
		})
		if err := sink(execution.StreamEvent{Sequence: 2, Kind: execution.StreamEventData, Data: []byte("data: one\n\n")}); err == nil {
			t.Fatal("data sink unexpectedly succeeded")
		}
		return execution.StreamResult{
			DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			StatusCode: http.StatusOK, Header: make(http.Header),
			Error: &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled, Summary: "stream sink stopped"},
		}
	}}
	writer := &failingExecutionResponseWriter{header: make(http.Header), err: downstreamErr}
	result := NewExecutionForwarder(executor).ForwardStream(
		context.Background(), executionForwardInput(), writer,
	)
	if !result.Committed || !errors.Is(result.Err, downstreamErr) || result.Stream.EndReason != StreamEndDownstreamWriteFailure {
		t.Fatalf("ForwardStream() = %#v", result)
	}
}

type failingExecutionResponseWriter struct {
	header http.Header
	err    error
}

func (writer *failingExecutionResponseWriter) Header() http.Header { return writer.header }
func (*failingExecutionResponseWriter) WriteHeader(int)            {}
func (writer *failingExecutionResponseWriter) Write([]byte) (int, error) {
	return 0, writer.err
}
func (*failingExecutionResponseWriter) FlushError() error { return nil }

func executionForwardInput() ForwardInput {
	return ForwardInput{
		Dialect:          dialect.NewOpenAI(),
		RequestID:        "request-1",
		AttemptID:        "attempt-1",
		AttemptSequence:  2,
		ClientProtocol:   protocol.OpenAICompletions,
		Operation:        execution.OperationChatCompletion,
		ChannelID:        "openai",
		TargetKind:       "openai",
		RouteMode:        execution.RouteNative,
		RequiredFeatures: gatewayFeatureSet(execution.FeatureTools),
		TargetConfig:     []byte(`{}`),
		APIKey:           "secret",
		Credential: execution.NewCredentialSnapshot(
			7, 2, 3, []byte(`{"api_key":"secret"}`),
		),
		Request: &dialect.ParsedRequest{
			Method:   http.MethodPost,
			Path:     "/v1/chat/completions",
			RawQuery: "trace=kept",
			Header: http.Header{
				"Authorization": {"Bearer downstream"},
				"X-Remove":      {"remove"},
				"X-Test":        {"kept"},
			},
			Body: []byte(`{"model":"public"}`),
		},
		ExternalModel:   "public",
		UpstreamModelID: "upstream",
		Group: state.GroupView{
			Timeouts: state.TimeoutConfig{
				FirstByte: time.Second,
				Request:   time.Second, StreamIdle: time.Second,
			},
			HeaderRules: state.HeaderRules{
				Set:    map[string]string{"X-Template": "Bearer ${API_KEY}"},
				Remove: []string{"X-Remove"},
			},
		},
	}
}

func gatewayFeatureSet(features ...execution.Feature) execution.FeatureSet {
	set, err := execution.NewFeatureSet(features...)
	if err != nil {
		panic(err)
	}
	return set
}
