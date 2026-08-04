package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gpt-load/internal/dialect"
	"gpt-load/internal/health"
	platformhttp "gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/usage"
)

type cancelAfterFlushResponseWriter struct {
	*recordingResponseWriter
	cancel   context.CancelFunc
	cancelAt int
}

func (writer *cancelAfterFlushResponseWriter) FlushError() error {
	writer.flushes++
	if writer.flushes == writer.cancelAt {
		writer.cancel()
	}
	return nil
}

type cancelAfterFlushWaitForCloseResponseWriter struct {
	*recordingResponseWriter
	cancel   context.CancelFunc
	closed   <-chan struct{}
	cancelAt int
}

func (writer *cancelAfterFlushWaitForCloseResponseWriter) FlushError() error {
	writer.flushes++
	if writer.flushes == writer.cancelAt {
		writer.cancel()
		select {
		case <-writer.closed:
		case <-time.After(time.Second):
			return errors.New("timed out waiting for stream close")
		}
	}
	return nil
}

type closeNotifyingReadCloser struct {
	reader    io.Reader
	closed    chan struct{}
	closeOnce sync.Once
}

func (body *closeNotifyingReadCloser) Read(target []byte) (int, error) {
	return body.reader.Read(target)
}

func (body *closeNotifyingReadCloser) Close() error {
	body.closeOnce.Do(func() { close(body.closed) })
	return nil
}

type releaseAfterFlushResponseWriter struct {
	*recordingResponseWriter
	release     func()
	releaseOnce sync.Once
}

func (writer *releaseAfterFlushResponseWriter) FlushError() error {
	writer.flushes++
	writer.releaseOnce.Do(writer.release)
	return nil
}

func TestResponsesSSETerminalLifecycleAndUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		wire       string
		wantReason StreamEndReason
		wantUsage  usage.Result
		wantErr    bool
	}{
		{
			name: "completed explicit event",
			wire: "event: response.completed\n" +
				"data: {\"response\":{\"usage\":{\"input_tokens\":100,\"input_tokens_details\":{\"cached_tokens\":20},\"output_tokens\":30,\"total_tokens\":130}}}\n\n",
			wantReason: StreamEndCleanEOF,
			wantUsage: usage.Result{
				State: usage.StateComplete,
				Tokens: usage.Tokens{
					UncachedInput: 80,
					CacheRead:     20,
					Output:        30,
				},
			},
		},
		{
			name:       "incomplete terminal",
			wire:       "data: {\"type\":\"response.incomplete\",\"response\":{\"usage\":{\"input_tokens\":50,\"output_tokens\":5,\"total_tokens\":55}}}\n\n",
			wantReason: StreamEndProviderIncomplete,
			wantUsage: usage.Result{
				State:  usage.StateComplete,
				Tokens: usage.Tokens{UncachedInput: 50, Output: 5},
			},
		},
		{
			name:       "missing terminal after partial usage",
			wire:       "data: {\"type\":\"response.output_item.added\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}}\n\n",
			wantReason: StreamEndUpstreamProtocolError,
			wantUsage: usage.Result{
				State:  usage.StatePartial,
				Tokens: usage.Tokens{UncachedInput: 10, Output: 1},
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			upstream := httptest.NewServer(http.HandlerFunc(func(
				writer http.ResponseWriter,
				_ *http.Request,
			) {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(writer, test.wire)
			}))
			defer upstream.Close()

			downstream := newRecordingResponseWriter()
			result := NewForwarder(
				platformhttp.NewHTTPClientManager(),
				redact.New(),
			).ForwardStream(
				context.Background(),
				responsesStreamForwardInput(upstream.URL),
				downstream,
			)

			if result.Committed != true ||
				result.Stream.EndReason != test.wantReason ||
				result.Usage != test.wantUsage {
				t.Fatalf("ForwardStream() result = %#v", result)
			}
			if test.wantErr != errors.Is(result.Err, ErrUpstreamProtocol) {
				t.Fatalf("ForwardStream() error = %v, want protocol error %t", result.Err, test.wantErr)
			}
			if downstream.status != http.StatusOK ||
				downstream.body.String() != test.wire {
				t.Fatalf(
					"downstream status/body = %d/%q, want %d/%q",
					downstream.status,
					downstream.body.String(),
					http.StatusOK,
					test.wire,
				)
			}
		})
	}
}

func TestResponsesSSECompletedFlushWinsOverLaterClientCancellation(t *testing.T) {
	t.Parallel()

	first := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"
	terminal := "event: response.completed\n" +
		"data: {\"response\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":30,\"total_tokens\":130}}}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, first+terminal)
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
	}))
	defer upstream.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	downstream := &cancelAfterFlushResponseWriter{
		recordingResponseWriter: newRecordingResponseWriter(),
		cancel:                  cancel,
		cancelAt:                2,
	}
	result := NewForwarder(
		platformhttp.NewHTTPClientManager(),
		redact.New(),
	).ForwardStream(
		ctx,
		responsesStreamForwardInput(upstream.URL),
		downstream,
	)

	wantUsage := usage.Result{
		State:  usage.StateComplete,
		Tokens: usage.Tokens{UncachedInput: 100, Output: 30},
	}
	if !errors.Is(result.Err, context.Canceled) || !result.Committed ||
		result.Stream.EndReason != StreamEndCleanEOF ||
		result.Usage != wantUsage ||
		downstream.status != http.StatusOK ||
		downstream.body.String() != first+terminal {
		t.Fatalf("ForwardStream() result/downstream = %#v / %#v", result, downstream)
	}
}

func TestResponsesSSECompletedFlushKeepsTerminalBoundaryWhenCloseRacesAfterFlush(t *testing.T) {
	first := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"
	terminal := "event: response.completed\n" +
		"data: {\"response\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":30,\"total_tokens\":130}}}\n\n"
	closed := make(chan struct{})
	body := &closeNotifyingReadCloser{
		reader: strings.NewReader(first + terminal),
		closed: closed,
	}
	clients := platformhttp.NewHTTPClientManager()
	input := responsesStreamForwardInput("https://api.example.test")
	clients.GetClient(streamingClientConfig(input.Group.Timeouts)).Transport = forwarderRoundTripper(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": {"text/event-stream"},
			},
			Body:    body,
			Request: request,
		}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	downstream := &cancelAfterFlushWaitForCloseResponseWriter{
		recordingResponseWriter: newRecordingResponseWriter(),
		cancel:                  cancel,
		closed:                  closed,
		cancelAt:                2,
	}
	result := NewForwarder(clients, redact.New()).ForwardStream(ctx, input, downstream)

	if !errors.Is(result.Err, context.Canceled) || !result.Committed ||
		result.Stream.EndReason != StreamEndCleanEOF ||
		downstream.body.String() != first+terminal {
		t.Fatalf("ForwardStream() result/downstream = %#v / %#v", result, downstream)
	}
}

func TestResponsesSSELargeTerminalAfterPureCREventDoesNotMarkPartialTerminalForwarded(t *testing.T) {
	first := "event: response.output_text.delta\r" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\r\r"
	terminal := "event: response.completed\n" +
		"data: {\"response\":{\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"" +
		strings.Repeat("x", streamReadBufferSize*2) +
		"\"}]}],\"usage\":{\"input_tokens\":100,\"output_tokens\":30,\"total_tokens\":130}}}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, first+terminal)
	}))
	defer upstream.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	downstream := &cancelAfterFlushResponseWriter{
		recordingResponseWriter: newRecordingResponseWriter(),
		cancel:                  cancel,
		cancelAt:                2,
	}
	result := NewForwarder(
		platformhttp.NewHTTPClientManager(),
		redact.New(),
	).ForwardStream(
		ctx,
		responsesStreamForwardInput(upstream.URL),
		downstream,
	)

	if !errors.Is(result.Err, context.Canceled) ||
		result.Stream.EndReason != StreamEndClientCanceled ||
		downstream.body.Len() <= len(first) ||
		downstream.body.Len() >= len(first+terminal) {
		t.Fatalf("ForwardStream() result = %#v, want client cancellation before terminal forwarding", result)
	}
}

func TestResponsesSSEForwardsCompleteTerminalEventBeforeContinuing(t *testing.T) {
	t.Parallel()

	first := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"
	terminal := "event: response.completed\n" +
		"data: {\"response\":{\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"" +
		strings.Repeat("x", streamReadBufferSize*2) +
		"\"}]}],\"usage\":{\"input_tokens\":100,\"output_tokens\":30,\"total_tokens\":130}}}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, first+terminal)
	}))
	defer upstream.Close()

	downstream := newRecordingResponseWriter()
	result := NewForwarder(
		platformhttp.NewHTTPClientManager(),
		redact.New(),
	).ForwardStream(
		context.Background(),
		responsesStreamForwardInput(upstream.URL),
		downstream,
	)

	wantUsage := usage.Result{
		State:  usage.StateComplete,
		Tokens: usage.Tokens{UncachedInput: 100, Output: 30},
	}
	if result.Err != nil || !result.Committed ||
		result.Stream.EndReason != StreamEndCleanEOF ||
		result.Usage != wantUsage ||
		downstream.status != http.StatusOK ||
		downstream.body.String() != first+terminal {
		t.Fatalf(
			"ForwardStream() result/body length = %#v / %d, want complete terminal length %d",
			result,
			downstream.body.Len(),
			len(first+terminal),
		)
	}
}

func TestResponsesSSEPreservesSplitCRLFAfterTerminal(t *testing.T) {
	t.Parallel()

	first := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"
	terminalPrefix := "event: response.completed\r\n" +
		"data: {\"response\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":30,\"total_tokens\":130}}}\r\n" +
		"\r"
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, first+terminalPrefix)
		writer.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(writer, "\n")
		writer.(http.Flusher).Flush()
	}))
	defer upstream.Close()

	downstream := &releaseAfterFlushResponseWriter{
		recordingResponseWriter: newRecordingResponseWriter(),
		release:                 func() { close(release) },
	}
	result := NewForwarder(
		platformhttp.NewHTTPClientManager(),
		redact.New(),
	).ForwardStream(
		context.Background(),
		responsesStreamForwardInput(upstream.URL),
		downstream,
	)

	wantUsage := usage.Result{
		State:  usage.StateComplete,
		Tokens: usage.Tokens{UncachedInput: 100, Output: 30},
	}
	if result.Err != nil || !result.Committed ||
		result.Stream.EndReason != StreamEndCleanEOF ||
		result.Usage != wantUsage ||
		downstream.status != http.StatusOK ||
		downstream.body.String() != first+terminalPrefix+"\n" {
		t.Fatalf("ForwardStream() result/body = %#v / %q", result, downstream.body.String())
	}
}

func TestResponsesSSEFailedBeforeAndAfterCommit(t *testing.T) {
	t.Parallel()

	t.Run("first failed event remains pre-commit provider error", func(t *testing.T) {
		t.Parallel()
		wire := "event: response.failed\n" +
			"data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"rate_limit_error\"},\"usage\":{\"input_tokens\":25,\"output_tokens\":2,\"total_tokens\":27}}}\n\n"
		upstream := httptest.NewServer(http.HandlerFunc(func(
			writer http.ResponseWriter,
			_ *http.Request,
		) {
			writer.Header().Set("Content-Type", "text/event-stream")
			writer.Header().Set("Retry-After", "1")
			_, _ = io.WriteString(writer, wire)
		}))
		defer upstream.Close()

		input := responsesStreamForwardInput(upstream.URL)
		downstream := newRecordingResponseWriter()
		result := NewForwarder(
			platformhttp.NewHTTPClientManager(),
			redact.New(),
		).ForwardStream(context.Background(), input, downstream)

		if result.Err != nil || result.Committed ||
			!result.ProviderErrorBeforeCommit ||
			downstream.status != 0 || downstream.body.Len() != 0 {
			t.Fatalf("ForwardStream() result/downstream = %#v / %#v", result, downstream)
		}
		if want := (usage.Result{
			State:  usage.StateComplete,
			Tokens: usage.Tokens{UncachedInput: 25, Output: 2},
		}); result.Usage != want {
			t.Fatalf("ForwardStream() usage = %#v, want %#v", result.Usage, want)
		}
		decision := health.Judge(input.Dialect, health.Attempt{
			StatusCode:                result.StatusCode,
			Body:                      result.ClassificationBody,
			Header:                    result.Header,
			Now:                       time.Unix(1, 0),
			ProviderErrorBeforeCommit: true,
		})
		if decision.Category != health.FailureCategoryRateLimited ||
			decision.Action != health.ActionCooldownKey {
			t.Fatalf("health decision = %#v", decision)
		}
	})

	t.Run("failed after first event cannot cross commit boundary", func(t *testing.T) {
		t.Parallel()
		wire := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n" +
			"event: response.failed\n" +
			"data: {\"response\":{\"error\":{\"code\":\"server_error\"}}}\n\n"
		upstream := httptest.NewServer(http.HandlerFunc(func(
			writer http.ResponseWriter,
			_ *http.Request,
		) {
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(writer, wire)
		}))
		defer upstream.Close()

		downstream := newRecordingResponseWriter()
		result := NewForwarder(
			platformhttp.NewHTTPClientManager(),
			redact.New(),
		).ForwardStream(
			context.Background(),
			responsesStreamForwardInput(upstream.URL),
			downstream,
		)

		if result.Err != nil || !result.Committed ||
			result.ProviderErrorBeforeCommit ||
			result.Stream.EndReason != StreamEndSSEError ||
			downstream.status != http.StatusOK ||
			downstream.body.String() != wire {
			t.Fatalf("ForwardStream() result/downstream = %#v / %#v", result, downstream)
		}
	})
}

func TestResponsesSSEEventNameConflictIsProtocolError(t *testing.T) {
	t.Parallel()

	wire := "event: response.completed\n" +
		"data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_error\"}}}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, wire)
	}))
	defer upstream.Close()

	downstream := newRecordingResponseWriter()
	result := NewForwarder(
		platformhttp.NewHTTPClientManager(),
		redact.New(),
	).ForwardStream(
		context.Background(),
		responsesStreamForwardInput(upstream.URL),
		downstream,
	)
	if !errors.Is(result.Err, ErrUpstreamProtocol) ||
		result.Committed || result.ProviderErrorBeforeCommit ||
		downstream.status != 0 || downstream.body.Len() != 0 {
		t.Fatalf("ForwardStream() result/downstream = %#v / %#v", result, downstream)
	}
}

func TestResponsesSSERejectsEventsAfterTerminalWithoutReplacingUsage(t *testing.T) {
	t.Parallel()

	terminal := "event: response.completed\n" +
		"data: {\"response\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":30,\"total_tokens\":130}}}\n\n"
	lateEvent := "event: response.output_item.added\n" +
		"data: {\"response\":{\"usage\":{\"input_tokens\":900,\"output_tokens\":90,\"total_tokens\":990}}}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, terminal+lateEvent)
	}))
	defer upstream.Close()

	downstream := newRecordingResponseWriter()
	result := NewForwarder(
		platformhttp.NewHTTPClientManager(),
		redact.New(),
	).ForwardStream(
		context.Background(),
		responsesStreamForwardInput(upstream.URL),
		downstream,
	)
	wantUsage := usage.Result{
		State:  usage.StateComplete,
		Tokens: usage.Tokens{UncachedInput: 100, Output: 30},
	}
	if !errors.Is(result.Err, ErrUpstreamProtocol) || !result.Committed ||
		result.Stream.EndReason != StreamEndUpstreamProtocolError ||
		result.Usage != wantUsage ||
		downstream.body.String() != terminal {
		t.Fatalf("ForwardStream() result/downstream = %#v / %#v", result, downstream)
	}
}

func responsesStreamForwardInput(upstreamURL string) ForwardInput {
	input := streamForwardInput(upstreamURL)
	input.Dialect = dialect.NewOpenAIResponses(http.DefaultClient)
	input.Request.Path = "/v1/responses"
	input.Request.Body = []byte(`{"model":"gpt-5","stream":true}`)
	input.Group.InjectUsageOptions = true
	return input
}
