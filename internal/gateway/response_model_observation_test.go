package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	platformhttp "gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/redact"
)

func TestForwardObservesUpstreamModelBeforeAliasRewrite(t *testing.T) {
	tests := []struct {
		name          string
		responseModel string
		wantMismatch  bool
	}{
		{name: "match", responseModel: "provider-model"},
		{name: "mismatch", responseModel: "different-provider-model", wantMismatch: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, `{"id":"chatcmpl-1","model":"`+test.responseModel+`"}`)
			}))
			defer upstream.Close()

			input := streamForwardInput(upstream.URL)
			input.Request.Body = []byte(`{"model":"public-model"}`)
			input.ExternalModel = "public-model"
			input.UpstreamModelID = "provider-model"
			result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).Forward(
				context.Background(), input,
			)

			if result.Err != nil || result.StatusCode != http.StatusOK {
				t.Fatalf("Forward() result = %#v", result)
			}
			if !result.ResponseModelObserved || result.UpstreamReportedModel != test.responseModel ||
				result.ResponseModelMismatch != test.wantMismatch {
				t.Fatalf("model observation = %#v", result)
			}
			if bytes.Contains(result.Body, []byte(test.responseModel)) ||
				!bytes.Contains(result.Body, []byte("public-model")) {
				t.Fatalf("downstream alias rewrite changed = %q", result.Body)
			}
		})
	}
}

func TestForwardMarksSuccessfulResponseWithoutModelAsUnobserved(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"id":"chatcmpl-1"}`)
	}))
	defer upstream.Close()

	input := streamForwardInput(upstream.URL)
	input.Request.Body = []byte(`{"model":"provider-model"}`)
	input.ExternalModel = "provider-model"
	input.UpstreamModelID = "provider-model"
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).Forward(
		context.Background(), input,
	)

	if result.Err != nil || result.StatusCode != http.StatusOK ||
		result.ResponseModelObserved || result.UpstreamReportedModel != "" ||
		result.ResponseModelMismatch {
		t.Fatalf("Forward() result = %#v", result)
	}
}

func TestForwardBoundsObservedModelAfterRawComparison(t *testing.T) {
	responseModel := strings.Repeat("模型", 128)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"id":"chatcmpl-1","model":"`+responseModel+`"}`)
	}))
	defer upstream.Close()

	input := streamForwardInput(upstream.URL)
	input.Request.Body = []byte(`{"model":"public-model"}`)
	input.ExternalModel = "public-model"
	input.UpstreamModelID = "provider-model"
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).Forward(
		context.Background(), input,
	)

	if result.Err != nil || result.StatusCode != http.StatusOK ||
		!result.ResponseModelObserved || !result.ResponseModelMismatch {
		t.Fatalf("Forward() result = %#v", result)
	}
	if len(result.UpstreamReportedModel) > 255 || !utf8.ValidString(result.UpstreamReportedModel) {
		t.Fatalf("bounded response model length/validity = %d/%t", len(result.UpstreamReportedModel), utf8.ValidString(result.UpstreamReportedModel))
	}
	if result.UpstreamReportedModel == responseModel {
		t.Fatal("Forward() retained the unbounded response model")
	}
}

func TestResponseModelTrackerPreservesMismatchWhenProjectionCollidesWithExpectedModel(t *testing.T) {
	const truncatedMarker = "...[truncated]"
	rawModel := strings.Repeat("x", 512)
	expected := strings.Repeat("x", 255-len(truncatedMarker)) + truncatedMarker
	tracker := newResponseModelTracker(streamForwardInput("http://example.com").Dialect, expected)

	tracker.observe([]byte(`{"model":"` + rawModel + `"}`))
	observation := tracker.observation()

	if !observation.observed || !observation.mismatch {
		t.Fatalf("model observation = %#v", observation)
	}
	if len(observation.reported) > 255 || observation.reported == expected {
		t.Fatalf("bounded collision model = %q (%d bytes), expected a distinct bounded value", observation.reported, len(observation.reported))
	}
}

func TestForwardStreamKeepsMismatchingDeclarationObservedBeforeLaterMatches(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(
			writer,
			"data: {\"id\":\"chunk-1\",\"model\":\"different-provider-model\"}\n\n"+
				"data: {\"id\":\"chunk-1\",\"model\":\"provider-model\"}\n\n",
		)
	}))
	defer upstream.Close()

	input := streamForwardInput(upstream.URL)
	input.Request.Body = []byte(`{"model":"public-model","stream":true}`)
	input.ExternalModel = "public-model"
	input.UpstreamModelID = "provider-model"
	downstream := newRecordingResponseWriter()
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
		context.Background(), input, downstream,
	)

	if result.Err != nil || !result.Committed || !result.ResponseModelObserved ||
		!result.ResponseModelMismatch || result.UpstreamReportedModel != "different-provider-model" {
		t.Fatalf("ForwardStream() result = %#v", result)
	}
	if bytes.Contains(downstream.body.Bytes(), []byte("provider-model")) ||
		!bytes.Contains(downstream.body.Bytes(), []byte("public-model")) {
		t.Fatalf("downstream stream = %q", downstream.body.String())
	}
}

func TestForwardStreamDiscardsModelObservationWhenRequestDoesNotCompleteNormally(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"model\":\"different-provider-model\"}\n\n")
	}))
	defer upstream.Close()

	input := streamForwardInput(upstream.URL)
	input.Request.Body = []byte(`{"model":"provider-model","stream":true}`)
	input.ExternalModel = "provider-model"
	input.UpstreamModelID = "provider-model"
	downstream := newRecordingResponseWriter()
	downstream.writeErr = errors.New("downstream write failed")
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
		context.Background(), input, downstream,
	)

	if result.Stream.EndReason != StreamEndDownstreamWriteFailure ||
		result.ResponseModelObserved || result.ResponseModelMismatch ||
		result.UpstreamReportedModel != "" {
		t.Fatalf("ForwardStream() result = %#v", result)
	}
}
