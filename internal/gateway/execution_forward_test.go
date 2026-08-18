package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/protocol"
	"gpt-load/internal/reasoning"
	"gpt-load/internal/state"
	"gpt-load/internal/usage"
)

type fakeExecutionExecutor struct {
	unary  func(context.Context, execution.AttemptSpec) execution.AttemptResult
	stream func(context.Context, execution.AttemptSpec, execution.StreamSink) execution.StreamResult
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
			spec.ChannelID != "openai" ||
			spec.RouteMode != execution.RouteNative ||
			spec.RouteRequirement != execution.RouteRequirementNative ||
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

func TestExecutionForwarderKeepsExecutionObservationOnRepresentationFailure(t *testing.T) {
	t.Parallel()

	budget := int64(4096)
	executor := fakeExecutionExecutor{unary: func(context.Context, execution.AttemptSpec) execution.AttemptResult {
		return execution.AttemptResult{
			DispatchState:     execution.DispatchMaybeSent,
			ResponseStarted:   true,
			UpstreamProtocol:  protocol.Anthropic,
			AppliedReasoning:  &reasoning.Config{Mode: "enabled", BudgetTokens: &budget},
			StatusCode:        http.StatusOK,
			Header:            http.Header{"Content-Encoding": {"unsupported"}},
			Body:              []byte(`{"ok":true}`),
			UpstreamRequestID: "upstream-observation",
		}
	}}

	result := NewExecutionForwarder(executor).Forward(context.Background(), executionForwardInput())
	if !errors.Is(result.Err, ErrUpstreamProtocol) ||
		result.DispatchState != execution.DispatchMaybeSent || !result.ResponseStarted ||
		result.UpstreamProtocol != protocol.Anthropic ||
		result.UpstreamRequestID != "upstream-observation" ||
		result.AppliedReasoning.Mode != "enabled" || result.AppliedReasoning.BudgetTokens == nil ||
		*result.AppliedReasoning.BudgetTokens != budget {
		t.Fatalf("Forward() representation failure = %#v", result)
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
		if err := sink(execution.StreamEvent{Sequence: 2, Kind: execution.StreamEventData, Data: []byte("data: {\"id\":\"chat_1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"one\"},\"finish_reason\":null}]}\n\n")}); err != nil {
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
		recorder.Code != http.StatusOK || recorder.Body.String() != "data: {\"id\":\"chat_1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"one\"},\"finish_reason\":null}]}\n\ndata: [DONE]\n\n" {
		t.Fatalf("ForwardStream() result=%#v response=%d %q", result, recorder.Code, recorder.Body.String())
	}
}

func TestExecutionForwarderClassifiesOpenAIResponsesStreamLifecycle(t *testing.T) {
	t.Parallel()

	const (
		deltaEvent = "event: response.output_text.delta\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"
		completedEvent = "event: response.completed\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":8,\"input_tokens_details\":{\"cached_tokens\":3},\"output_tokens\":2,\"total_tokens\":10}}}\n\n"
		incompleteEvent = "event: response.incomplete\n" +
			"data: {\"type\":\"response.incomplete\",\"response\":{\"usage\":{\"input_tokens\":8,\"input_tokens_details\":{\"cached_tokens\":3},\"output_tokens\":2,\"total_tokens\":10}}}\n\n"
		failedEvent = "event: response.failed\n" +
			"data: {\"type\":\"response.failed\",\"error\":{\"message\":\"capacity exhausted\"},\"response\":{}}\n\n"
	)
	completedCRLF := strings.ReplaceAll(completedEvent, "\n", "\r\n")
	completedCRLFPrefix := completedCRLF[:len(completedCRLF)-1]

	tests := []struct {
		name           string
		chunks         []string
		cancel         bool
		terminalError  *execution.ErrorEvidence
		wantEnd        StreamEndReason
		wantSummary    string
		wantUsageState usage.State
		wantTokens     usage.Tokens
		providerError  bool
	}{
		{
			name: "completed terminal remains successful after client cancellation",
			chunks: []string{
				deltaEvent,
				completedEvent[:len(completedEvent)-7],
				completedEvent[len(completedEvent)-7:],
			},
			cancel: true,
			terminalError: &execution.ErrorEvidence{
				Kind: execution.ErrorKindCanceled, Summary: "request context canceled",
			},
			wantEnd:        StreamEndCleanEOF,
			wantUsageState: usage.StateComplete,
			wantTokens: usage.Tokens{
				UncachedInput: 5, CacheRead: 3, Output: 2,
			},
		},
		{
			name:   "split terminal CRLF remains canceled before final line feed is forwarded",
			chunks: []string{completedCRLFPrefix},
			cancel: true,
			terminalError: &execution.ErrorEvidence{
				Kind: execution.ErrorKindCanceled, Summary: "request context canceled",
			},
			wantEnd:        StreamEndClientCanceled,
			wantSummary:    fixedErrorSummary("client_canceled"),
			wantUsageState: usage.StateComplete,
			wantTokens: usage.Tokens{
				UncachedInput: 5, CacheRead: 3, Output: 2,
			},
		},
		{
			name:   "split terminal CRLF succeeds after final line feed is forwarded",
			chunks: []string{completedCRLFPrefix, "\n"},
			cancel: true,
			terminalError: &execution.ErrorEvidence{
				Kind: execution.ErrorKindCanceled, Summary: "request context canceled",
			},
			wantEnd:        StreamEndCleanEOF,
			wantUsageState: usage.StateComplete,
			wantTokens: usage.Tokens{
				UncachedInput: 5, CacheRead: 3, Output: 2,
			},
		},
		{
			name:   "client cancellation before terminal remains canceled",
			chunks: []string{deltaEvent},
			cancel: true,
			terminalError: &execution.ErrorEvidence{
				Kind: execution.ErrorKindCanceled, Summary: "request context canceled",
			},
			wantEnd:        StreamEndClientCanceled,
			wantSummary:    fixedErrorSummary("client_canceled"),
			wantUsageState: usage.StateMissing,
		},
		{
			name:           "clean transport EOF without required terminal is protocol error",
			chunks:         []string{deltaEvent},
			wantEnd:        StreamEndUpstreamProtocolError,
			wantSummary:    fixedErrorSummary("upstream_protocol_error"),
			wantUsageState: usage.StateMissing,
		},
		{
			name:   "incomplete terminal remains incomplete after client cancellation",
			chunks: []string{incompleteEvent},
			cancel: true,
			terminalError: &execution.ErrorEvidence{
				Kind: execution.ErrorKindCanceled, Summary: "request context canceled",
			},
			wantEnd:        StreamEndProviderIncomplete,
			wantSummary:    fixedErrorSummary("upstream_response_incomplete"),
			wantUsageState: usage.StateComplete,
			wantTokens: usage.Tokens{
				UncachedInput: 5, CacheRead: 3, Output: 2,
			},
		},
		{
			name:   "failed terminal remains provider error after client cancellation",
			chunks: []string{failedEvent},
			cancel: true,
			terminalError: &execution.ErrorEvidence{
				Kind: execution.ErrorKindCanceled, Summary: "request context canceled",
			},
			wantSummary:    "capacity exhausted",
			wantUsageState: usage.StateMissing,
			providerError:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			requestContext, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			executor := fakeExecutionExecutor{stream: func(
				_ context.Context,
				_ execution.AttemptSpec,
				sink execution.StreamSink,
			) execution.StreamResult {
				if err := sink(execution.StreamEvent{
					Sequence: 1, Kind: execution.StreamEventReady, StatusCode: http.StatusOK,
					Header: http.Header{"Content-Type": {"text/event-stream"}},
				}); err != nil {
					t.Fatalf("ready sink: %v", err)
				}
				for index, chunk := range test.chunks {
					if err := sink(execution.StreamEvent{
						Sequence: uint64(index + 2), Kind: execution.StreamEventData,
						Data: []byte(chunk),
					}); err != nil {
						t.Fatalf("data sink %d: %v", index+1, err)
					}
				}
				if test.cancel {
					cancel()
				}
				return execution.StreamResult{
					DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"text/event-stream"}},
					Error:      test.terminalError,
				}
			}}

			input := responsesExecutionForwardInput()
			recorder := httptest.NewRecorder()
			result := NewExecutionForwarder(executor).ForwardStream(requestContext, input, recorder)
			if test.providerError {
				if result.Committed || !result.ProviderErrorBeforeCommit ||
					result.ErrorSummary != test.wantSummary || recorder.Body.Len() != 0 {
					t.Fatalf("ForwardStream() result = %#v, body=%q", result, recorder.Body.String())
				}
			} else {
				if !result.Committed || result.Stream.EndReason != test.wantEnd ||
					result.Stream.ErrorSummary != test.wantSummary || result.Usage.State != test.wantUsageState ||
					result.Usage.Tokens != test.wantTokens {
					t.Fatalf("ForwardStream() result = %#v", result)
				}
				if got := recorder.Body.String(); got != strings.Join(test.chunks, "") {
					t.Fatalf("response body = %q, want %q", got, strings.Join(test.chunks, ""))
				}
			}
		})
	}
}

func TestExecutionForwarderDoesNotReportStreamReadyForFirstProviderError(t *testing.T) {
	t.Parallel()

	const failedEvent = "event: response.failed\n" +
		"data: {\"type\":\"response.failed\",\"error\":{\"message\":\"capacity exhausted\"},\"response\":{}}\n\n"
	readyObserved := false
	firstResponseObserved := false
	executor := fakeExecutionExecutor{stream: func(
		_ context.Context,
		_ execution.AttemptSpec,
		sink execution.StreamSink,
	) execution.StreamResult {
		if err := sink(execution.StreamEvent{
			Sequence: 1, Kind: execution.StreamEventReady,
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/event-stream"}},
		}); err != nil {
			t.Fatalf("ready sink: %v", err)
		}
		if err := sink(execution.StreamEvent{
			Sequence: 2, Kind: execution.StreamEventData, Data: []byte(failedEvent),
		}); err != nil {
			t.Fatalf("data sink: %v", err)
		}
		return execution.StreamResult{
			DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/event-stream"}},
		}
	}}
	input := executionForwardInput()
	input.Dialect = dialect.NewOpenAIResponses()
	input.OnStreamReady = func() { readyObserved = true }
	input.OnFirstResponse = func() { firstResponseObserved = true }
	recorder := httptest.NewRecorder()

	result := NewExecutionForwarder(executor).ForwardStream(
		context.Background(), input, recorder,
	)
	if result.Err != nil || result.Committed || !result.ProviderErrorBeforeCommit {
		t.Fatalf("ForwardStream() result = %#v", result)
	}
	if readyObserved {
		t.Fatal("provider error was reported as a ready successful stream")
	}
	if !firstResponseObserved {
		t.Fatal("provider error must still count as the first upstream response")
	}
}

func TestExecutionForwarderPreservesFirstProviderErrorEvidence(t *testing.T) {
	t.Parallel()

	const failedEvent = "event: error\n" +
		"data: {\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\",\"code\":\"rate_limit\",\"message\":\"Please try again later\"}}\n\n"
	header := http.Header{
		"Content-Type": {"text/event-stream"},
		"Retry-After":  {"3"},
	}
	executor := fakeExecutionExecutor{stream: func(
		_ context.Context,
		_ execution.AttemptSpec,
		sink execution.StreamSink,
	) execution.StreamResult {
		if err := sink(execution.StreamEvent{
			Sequence: 1, Kind: execution.StreamEventReady,
			StatusCode: http.StatusOK, Header: header,
		}); err != nil {
			t.Fatalf("ready sink: %v", err)
		}
		if err := sink(execution.StreamEvent{
			Sequence: 2, Kind: execution.StreamEventData, Data: []byte(failedEvent),
		}); err != nil {
			t.Fatalf("data sink: %v", err)
		}
		return execution.StreamResult{
			DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			StatusCode: http.StatusOK, Header: header,
		}
	}}
	input := executionForwardInput()
	input.Dialect = dialect.NewOpenAIResponses()
	result := NewExecutionForwarder(executor).ForwardStream(
		context.Background(), input, httptest.NewRecorder(),
	)

	if result.Committed || !result.ProviderErrorBeforeCommit || result.ExecutionError == nil {
		t.Fatalf("ForwardStream() result = %#v", result)
	}
	if result.ExecutionError.Type != "rate_limit_error" ||
		result.ExecutionError.Code != "rate_limit" ||
		result.ExecutionError.Hint != execution.FailureHintRateLimited {
		t.Fatalf("error evidence = %#v", result.ExecutionError)
	}
	now := time.Date(2026, time.July, 28, 10, 0, 0, 0, time.UTC)
	decision := judgeUpstreamResult(result, now)
	if decision.Category != health.FailureCategoryRateLimited ||
		decision.Action != health.ActionCooldownCredential ||
		!decision.CooldownUntil.Equal(now.Add(3*time.Second)) {
		t.Fatalf("health decision = %#v", decision)
	}
}

func TestExecutionForwarderRejectsStreamingEOFWithoutProtocolTerminal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dialect dialect.Dialect
		data    string
	}{
		{
			name: "OpenAI Chat", dialect: dialect.NewOpenAI(),
			data: "data: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n",
		},
		{
			name: "Anthropic", dialect: dialect.NewAnthropic(),
			data: "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n",
		},
		{
			name: "Gemini", dialect: dialect.NewGemini(),
			data: "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}],\"role\":\"model\"},\"index\":0}]}\n\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			executor := fakeExecutionExecutor{stream: func(
				_ context.Context,
				_ execution.AttemptSpec,
				sink execution.StreamSink,
			) execution.StreamResult {
				if err := sink(execution.StreamEvent{
					Sequence: 1, Kind: execution.StreamEventReady,
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"text/event-stream"}},
				}); err != nil {
					t.Fatalf("ready sink: %v", err)
				}
				if err := sink(execution.StreamEvent{
					Sequence: 2, Kind: execution.StreamEventData, Data: []byte(test.data),
				}); err != nil {
					t.Fatalf("data sink: %v", err)
				}
				return execution.StreamResult{
					DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"text/event-stream"}},
				}
			}}
			input := executionForwardInput()
			input.Dialect = test.dialect
			result := NewExecutionForwarder(executor).ForwardStream(
				context.Background(), input, httptest.NewRecorder(),
			)
			if !result.Committed || result.Err == nil ||
				result.Stream.EndReason != StreamEndUpstreamProtocolError {
				t.Fatalf("ForwardStream() result = %#v", result)
			}
		})
	}
}

func TestExecutionForwarderRejectsOpenAIResponsesDataAfterTerminal(t *testing.T) {
	t.Parallel()

	const completedEvent = "event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{}}\n\n"
	const extraEvent = "event: response.output_text.delta\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"late\"}\n\n"
	var extraErr error
	executor := fakeExecutionExecutor{stream: func(
		_ context.Context,
		_ execution.AttemptSpec,
		sink execution.StreamSink,
	) execution.StreamResult {
		if err := sink(execution.StreamEvent{
			Sequence: 1, Kind: execution.StreamEventReady, StatusCode: http.StatusOK,
			Header: http.Header{"Content-Type": {"text/event-stream"}},
		}); err != nil {
			t.Fatalf("ready sink: %v", err)
		}
		if err := sink(execution.StreamEvent{
			Sequence: 2, Kind: execution.StreamEventData, Data: []byte(completedEvent),
		}); err != nil {
			t.Fatalf("terminal sink: %v", err)
		}
		extraErr = sink(execution.StreamEvent{
			Sequence: 3, Kind: execution.StreamEventData, Data: []byte(extraEvent),
		})
		return execution.StreamResult{
			DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/event-stream"}},
			Error: &execution.ErrorEvidence{
				Kind: execution.ErrorKindCanceled, Summary: "stream sink stopped",
			},
		}
	}}

	recorder := httptest.NewRecorder()
	result := NewExecutionForwarder(executor).ForwardStream(
		context.Background(), responsesExecutionForwardInput(), recorder,
	)
	if !errors.Is(extraErr, ErrUpstreamProtocol) ||
		result.Stream.EndReason != StreamEndUpstreamProtocolError ||
		result.Stream.ErrorSummary != fixedErrorSummary("upstream_protocol_error") {
		t.Fatalf("extra error/result = %v / %#v", extraErr, result)
	}
	if got := recorder.Body.String(); got != completedEvent {
		t.Fatalf("response body = %q, want terminal only %q", got, completedEvent)
	}
}

func TestExecutionForwarderRequiresSuccessfulFlushBeforeAcceptingResponsesTerminal(t *testing.T) {
	t.Parallel()

	const completedEvent = "event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{}}\n\n"
	flushErr := errors.New("downstream flush failed")
	executor := fakeExecutionExecutor{stream: func(
		_ context.Context,
		_ execution.AttemptSpec,
		sink execution.StreamSink,
	) execution.StreamResult {
		if err := sink(execution.StreamEvent{
			Sequence: 1, Kind: execution.StreamEventReady, StatusCode: http.StatusOK,
			Header: http.Header{"Content-Type": {"text/event-stream"}},
		}); err != nil {
			t.Fatalf("ready sink: %v", err)
		}
		if err := sink(execution.StreamEvent{
			Sequence: 2, Kind: execution.StreamEventData, Data: []byte(completedEvent),
		}); !errors.Is(err, flushErr) {
			t.Fatalf("terminal sink error = %v, want %v", err, flushErr)
		}
		return execution.StreamResult{
			DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/event-stream"}},
			Error: &execution.ErrorEvidence{
				Kind: execution.ErrorKindCanceled, Summary: "stream sink stopped",
			},
		}
	}}

	writer := &executionFlushFailureWriter{
		header: make(http.Header),
		err:    flushErr,
	}
	result := NewExecutionForwarder(executor).ForwardStream(
		context.Background(), responsesExecutionForwardInput(), writer,
	)
	if !result.Committed || !errors.Is(result.Err, flushErr) ||
		result.Stream.EndReason != StreamEndDownstreamWriteFailure {
		t.Fatalf("ForwardStream() = %#v", result)
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
		if err := sink(execution.StreamEvent{Sequence: 2, Kind: execution.StreamEventData, Data: []byte("data: {\"id\":\"chat_1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"one\"},\"finish_reason\":null}]}\n\n")}); err == nil {
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

type executionFlushFailureWriter struct {
	header http.Header
	err    error
}

func (writer *executionFlushFailureWriter) Header() http.Header { return writer.header }
func (*executionFlushFailureWriter) WriteHeader(int)            {}
func (*executionFlushFailureWriter) Write(body []byte) (int, error) {
	return len(body), nil
}
func (writer *executionFlushFailureWriter) FlushError() error { return writer.err }

func executionForwardInput() ForwardInput {
	return ForwardInput{
		Dialect:          dialect.NewOpenAI(),
		RequestID:        "request-1",
		AttemptID:        "attempt-1",
		AttemptSequence:  2,
		ClientProtocol:   protocol.OpenAICompletions,
		Operation:        execution.OperationChatCompletion,
		ChannelID:        "openai",
		RouteMode:        execution.RouteNative,
		RouteRequirement: execution.RouteRequirementNative,
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

func TestNewExecutionAttemptSpecKeepsContinuityPrivate(t *testing.T) {
	input := executionForwardInput()
	input.ContinuityKey = "tenant-scoped-hmac"
	spec, err := newExecutionAttemptSpec(input)
	if err != nil {
		t.Fatal(err)
	}
	if spec.ContinuityKey != input.ContinuityKey {
		t.Fatalf("ContinuityKey = %q, want %q", spec.ContinuityKey, input.ContinuityKey)
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(input.ContinuityKey)) {
		t.Fatalf("AttemptSpec JSON exposed continuity key: %s", raw)
	}
}

func responsesExecutionForwardInput() ForwardInput {
	input := executionForwardInput()
	input.Dialect = dialect.NewOpenAIResponses()
	input.ObserveUsage = true
	input.ClientProtocol = protocol.OpenAIResponses
	input.Operation = execution.OperationResponsesCreate
	input.Request.Path = "/v1/responses"
	input.Request.RawQuery = ""
	input.Request.Body = []byte(`{"model":"public","stream":true}`)
	return input
}

func TestStreamErrorFailureHintDoesNotTreatGenericForbiddenAsInvalidCredential(t *testing.T) {
	tests := []struct {
		name   string
		status int
		values []string
		want   execution.FailureHint
	}{
		{
			name:   "model marker wins over forbidden",
			status: http.StatusForbidden,
			values: []string{"model_not_found"},
			want:   execution.FailureHintModelUnavailable,
		},
		{
			name:   "generic forbidden permission",
			status: http.StatusForbidden,
			values: []string{"permission_denied"},
		},
		{
			name:   "payment required",
			status: http.StatusPaymentRequired,
			values: []string{"billing disabled"},
		},
		{
			name:   "explicit invalid key",
			status: http.StatusForbidden,
			values: []string{"API key not valid"},
			want:   execution.FailureHintInvalidCredential,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := streamErrorFailureHint(test.status, test.values...); got != test.want {
				t.Fatalf("streamErrorFailureHint() = %q, want %q", got, test.want)
			}
		})
	}
}
