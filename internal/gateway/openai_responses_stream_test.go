package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gpt-load/internal/dialect"
	"gpt-load/internal/health"
	platformhttp "gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/usage"
)

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
	if !errors.Is(result.Err, ErrUpstreamProtocol) ||
		!result.Committed ||
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
