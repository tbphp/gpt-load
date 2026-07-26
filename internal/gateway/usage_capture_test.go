package gateway

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/dialect"
	platformhttp "gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/state"
	"gpt-load/internal/usage"
)

type usageExtractorDialect struct {
	dialect.Dialect
	extract func([]byte) (usage.Result, error)
	stream  func() dialect.UsageStreamExtractor
}

type dialectWithoutUsage struct{ dialect.Dialect }

func (value usageExtractorDialect) ExtractUsage(body []byte) (usage.Result, error) {
	return value.extract(body)
}

func (value usageExtractorDialect) NewUsageStreamExtractor() dialect.UsageStreamExtractor {
	if value.stream == nil {
		return nil
	}
	return value.stream()
}

func TestUsageCaptureBoundaryExtractsValidNonStreamingResult(t *testing.T) {
	want := usage.Result{
		State:  usage.StateComplete,
		Tokens: usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30},
	}
	boundary := newUsageCaptureBoundary()
	got := boundary.extractNonStreaming(usageExtractorDialect{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		extract: func([]byte) (usage.Result, error) { return want, nil },
	}, make(http.Header), []byte(`{"provider":"body"}`))

	if got != want || boundary.failureTotal.Load() != 0 {
		t.Fatalf("result/failures = %#v/%d, want %#v/0", got, boundary.failureTotal.Load(), want)
	}
}

func TestUsageCaptureBoundaryMissingCapabilityIsQuietMissing(t *testing.T) {
	boundary := newUsageCaptureBoundary()
	var logs bytes.Buffer
	boundary.logger = logrus.New()
	boundary.logger.SetOutput(&logs)
	got := boundary.extractNonStreaming(dialectWithoutUsage{Dialect: dialect.NewOpenAI(http.DefaultClient)}, make(http.Header), []byte(`{}`))

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
			got := boundary.extractNonStreaming(usageExtractorDialect{Dialect: dialect.NewOpenAI(http.DefaultClient), extract: test.extract}, make(http.Header), []byte(`{}`))

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
	} {
		boundary := newUsageCaptureBoundary()
		got := boundary.extractNonStreaming(usageExtractorDialect{Dialect: dialect.NewOpenAI(http.DefaultClient), extract: func([]byte) (usage.Result, error) { return result, nil }}, make(http.Header), []byte(`{}`))
		if got.State != usage.StateMissing || !got.Diagnostics.Has(usage.DiagnosticInvalidPayload) || boundary.failureTotal.Load() != 1 {
			t.Fatalf("input/result/failures = %#v/%#v/%d", result, got, boundary.failureTotal.Load())
		}
	}

	boundary := newUsageCaptureBoundary()
	zero := usage.Result{State: usage.StateComplete}
	if got := boundary.extractNonStreaming(usageExtractorDialect{Dialect: dialect.NewOpenAI(http.DefaultClient), extract: func([]byte) (usage.Result, error) { return zero, nil }}, make(http.Header), []byte(`{}`)); got != zero || boundary.failureTotal.Load() != 0 {
		t.Fatalf("zero complete/failures = %#v/%d", got, boundary.failureTotal.Load())
	}
}

func TestUsageCaptureBoundaryDecodesSupportedContentEncoding(t *testing.T) {
	plain := []byte(`{"canonical":true}`)
	want := usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30}}
	for _, encoding := range []string{"identity", "gzip", "br", "deflate", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			wire, err := utils.CompressResponse(encoding, plain)
			if err != nil {
				t.Fatalf("CompressResponse(%q): %v", encoding, err)
			}
			originalWire := bytes.Clone(wire)
			boundary := newUsageCaptureBoundary()
			got := boundary.extractNonStreaming(usageExtractorDialect{Dialect: dialect.NewOpenAI(http.DefaultClient), extract: func(body []byte) (usage.Result, error) {
				if !bytes.Equal(body, plain) {
					t.Fatalf("decoded body = %q, want %q", body, plain)
				}
				return want, nil
			}}, http.Header{"Content-Encoding": {encoding}}, wire)
			if got != want || boundary.failureTotal.Load() != 0 || !bytes.Equal(wire, originalWire) {
				t.Fatalf("result/failures = %#v/%d", got, boundary.failureTotal.Load())
			}
		})
	}
}

func TestUsageCaptureBoundaryUnsupportedEncodingIsMissing(t *testing.T) {
	tooLargePlain := bytes.Repeat([]byte("x"), int(maxNonStreamingResponseBodyBytes)+1)
	tooLargeWire, err := utils.CompressResponse("gzip", tooLargePlain)
	if err != nil {
		t.Fatalf("compress oversized fixture: %v", err)
	}
	for _, test := range []struct {
		name    string
		headers http.Header
		wire    []byte
	}{
		{name: "multiple values", headers: http.Header{"Content-Encoding": {"identity", "gzip"}}, wire: []byte(`{}`)},
		{name: "comma stacked", headers: http.Header{"Content-Encoding": {"gzip, br"}}, wire: []byte(`{}`)},
		{name: "unknown", headers: http.Header{"Content-Encoding": {"unknown"}}, wire: []byte(`{}`)},
		{name: "corrupt gzip", headers: http.Header{"Content-Encoding": {"gzip"}}, wire: []byte("not-gzip")},
		{name: "decompressed over limit", headers: http.Header{"Content-Encoding": {"gzip"}}, wire: tooLargeWire},
	} {
		t.Run(test.name, func(t *testing.T) {
			boundary := newUsageCaptureBoundary()
			got := boundary.extractNonStreaming(usageExtractorDialect{Dialect: dialect.NewOpenAI(http.DefaultClient), extract: func([]byte) (usage.Result, error) {
				t.Fatal("extractor called after decompression failure")
				return usage.Result{}, nil
			}}, test.headers, test.wire)
			if got != (usage.Result{State: usage.StateMissing}) || boundary.failureTotal.Load() != 1 {
				t.Fatalf("result/failures = %#v/%d", got, boundary.failureTotal.Load())
			}
		})
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
				Dialect: test.dialect,
				Group:   state.GroupView{ID: 1, UpstreamURL: upstream.URL, Timeouts: state.TimeoutConfig{Connect: time.Second, FirstByte: time.Second, Request: time.Second}},
				APIKey:  "sk-test",
				Request: &dialect.ParsedRequest{Method: http.MethodPost, Path: test.path, Header: make(http.Header), Body: []byte(`{}`)},
			})
			if result.Err != nil || result.Usage != want {
				t.Fatalf("result = %#v, want Usage %#v", result, want)
			}
		})
	}
}

func TestForwardUsageCapturePreservesCompressedSuccessWire(t *testing.T) {
	plain := []byte(`{"usage":{"prompt_tokens":100,"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":20}}}`)
	wire, err := utils.CompressResponse("gzip", plain)
	if err != nil {
		t.Fatalf("compress fixture: %v", err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Encoding", "gzip")
		writer.Header().Set("ETag", `"wire-v1"`)
		writer.Header().Set("Digest", "sha-256=wire-digest")
		_, _ = writer.Write(wire)
	}))
	defer upstream.Close()

	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).Forward(context.Background(), ForwardInput{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		Group:   state.GroupView{ID: 1, UpstreamURL: upstream.URL, Timeouts: state.TimeoutConfig{Connect: time.Second, FirstByte: time.Second, Request: time.Second}},
		APIKey:  "sk-test",
		Request: &dialect.ParsedRequest{Method: http.MethodPost, Path: "/v1/chat/completions", Header: make(http.Header), Body: []byte(`{}`)},
	})
	want := usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30}}
	if result.Err != nil || result.Usage != want || !bytes.Equal(result.Body, wire) || result.Header.Get("Content-Encoding") != "gzip" || result.Header.Get("ETag") != `"wire-v1"` || result.Header.Get("Digest") != "sha-256=wire-digest" {
		t.Fatalf("result = %#v", result)
	}
}
