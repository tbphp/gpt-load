package gateway

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/dialect"
	platformhttp "gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/state"
)

func TestForwarderPreservesEndToEndRequestAndSuccessfulResponse(t *testing.T) {
	var received *http.Request
	var receivedBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		received = request.Clone(request.Context())
		received.Header = request.Header.Clone()
		receivedBody, _ = io.ReadAll(request.Body)
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Upstream", "kept")
		writer.Header().Set("Connection", "X-Upstream-Hop")
		writer.Header().Add("Connection", "X-Upstream-Hop-Second")
		writer.Header().Set("X-Upstream-Hop", "drop")
		writer.Header().Set("X-Upstream-Hop-Second", "drop")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"id":"chatcmpl-1"}`))
	}))
	defer upstream.Close()

	forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
	result := forwarder.Forward(context.Background(), ForwardInput{
		Dialect: dialect.NewOpenAI(upstream.Client()),
		Group: state.GroupView{
			ID: 1, Name: "openai", UpstreamURL: upstream.URL,
			Timeouts:    state.TimeoutConfig{Connect: time.Second, FirstByte: time.Second, Request: time.Second},
			HeaderRules: state.HeaderRules{Set: map[string]string{"X-Custom": "prefix-${API_KEY}"}},
		},
		APIKey: "sk-upstream-secret",
		Request: &dialect.ParsedRequest{
			Method:   http.MethodPost,
			Path:     "/v1/chat/completions",
			RawQuery: "trace=true",
			Header: http.Header{
				"Content-Type":  {"application/json"},
				"X-Passthrough": {"kept"},
				"Authorization": {"Bearer downstream-key"},
				"Connection":    {"X-Drop", "X-Drop-Second"},
				"X-Drop":        {"drop"},
				"X-Drop-Second": {"drop"},
			},
			Body: []byte(`{"model":"gpt-4o"}`),
		},
	})

	if result.Err != nil || result.StatusCode != http.StatusOK || !result.RequestWritten {
		t.Fatalf("Forward() result = %#v", result)
	}
	if string(result.Body) != `{"id":"chatcmpl-1"}` || len(result.ClassificationBody) != 0 {
		t.Fatalf("response bodies = wire %q classify %q", result.Body, result.ClassificationBody)
	}
	if result.Header.Get("X-Upstream") != "kept" ||
		result.Header.Get("X-Upstream-Hop") != "" ||
		result.Header.Get("X-Upstream-Hop-Second") != "" {
		t.Fatalf("response headers = %#v", result.Header)
	}
	if received.URL.RawQuery != "trace=true" || string(receivedBody) != `{"model":"gpt-4o"}` {
		t.Fatalf("upstream request URL/body = %s?%s %q", received.URL.Path, received.URL.RawQuery, receivedBody)
	}
	if received.Header.Get("Authorization") != "Bearer sk-upstream-secret" ||
		received.Header.Get("X-Custom") != "prefix-sk-upstream-secret" ||
		received.Header.Get("X-Passthrough") != "kept" {
		t.Fatalf("upstream headers = %#v", received.Header)
	}
	if received.Header.Get("X-Drop") != "" ||
		received.Header.Get("X-Drop-Second") != "" ||
		strings.Contains(received.Header.Get("Authorization"), "downstream-key") {
		t.Fatalf("upstream retained forbidden header: %#v", received.Header)
	}
}

func TestForwarderRedactsCompressedErrorAndPreservesEncoding(t *testing.T) {
	const secret = "custom-upstream-secret"
	plain := []byte(`{"error":{"api_key":"` + secret + `","code":"invalid_api_key"}}`)
	encoded, err := utils.CompressResponse("gzip", plain)
	if err != nil {
		t.Fatalf("compress fixture: %v", err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Encoding", "gzip")
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write(encoded)
	}))
	defer upstream.Close()

	result := testForward(t, upstream.URL, secret, time.Second)
	if result.Err != nil || result.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Forward() result = %#v", result)
	}
	if result.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", result.Header.Get("Content-Encoding"))
	}
	decoded, err := utils.DecompressResponse("gzip", result.Body)
	if err != nil {
		t.Fatalf("decode forwarded body: %v", err)
	}
	for _, body := range [][]byte{decoded, result.ClassificationBody} {
		if bytes.Contains(body, []byte(secret)) || !bytes.Contains(body, []byte(redact.Placeholder)) {
			t.Fatalf("safe body = %q, want placeholder and no secret", body)
		}
	}
	if result.Header.Get("Content-Length") != strconv.Itoa(len(result.Body)) {
		t.Fatalf("Content-Length = %q, body length = %d", result.Header.Get("Content-Length"), len(result.Body))
	}
}

func TestForwarderPreservesUnchangedCompressedErrorWireBytes(t *testing.T) {
	plain := []byte(`{"error":{"code":"rate_limited"}}`)
	encoded, err := utils.CompressResponse("gzip", plain)
	if err != nil {
		t.Fatalf("compress fixture: %v", err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Encoding", "gzip")
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = writer.Write(encoded)
	}))
	defer upstream.Close()

	result := testForward(t, upstream.URL, "custom-upstream-secret", time.Second)
	if result.Err != nil || result.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("Forward() result = %#v", result)
	}
	if !bytes.Equal(result.Body, encoded) {
		t.Fatalf("wire body changed without redaction: got %x want %x", result.Body, encoded)
	}
	if !bytes.Equal(result.ClassificationBody, plain) {
		t.Fatalf("ClassificationBody = %q, want %q", result.ClassificationBody, plain)
	}
	if result.Header.Get("Content-Encoding") != "gzip" ||
		result.Header.Get("Content-Length") != strconv.Itoa(len(encoded)) {
		t.Fatalf("compressed response headers = %#v", result.Header)
	}
}

func TestForwarderFailsClosedForUndecodableError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Encoding", "unsupported")
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = writer.Write([]byte("opaque-secret-body"))
	}))
	defer upstream.Close()

	result := testForward(t, upstream.URL, "opaque-secret-body", time.Second)
	if result.Err != nil || result.StatusCode != http.StatusBadGateway {
		t.Fatalf("Forward() result = %#v", result)
	}
	if result.Header.Get("Content-Encoding") != "" ||
		result.Header.Get("Content-Type") != "text/plain; charset=utf-8" ||
		result.Header.Get("Content-Length") != strconv.Itoa(len(redact.Placeholder)) ||
		string(result.Body) != redact.Placeholder ||
		string(result.ClassificationBody) != redact.Placeholder {
		t.Fatalf("fail-closed result headers/body = %#v %q", result.Header, result.Body)
	}
}

func TestForwarderFailsClosedForMalformedEncoding(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Encoding", "gzip, br")
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = writer.Write([]byte("opaque-body"))
	}))
	defer upstream.Close()

	result := testForward(t, upstream.URL, "sk-secret", time.Second)
	if result.Header.Get("Content-Encoding") != "" ||
		string(result.Body) != redact.Placeholder ||
		string(result.ClassificationBody) != redact.Placeholder {
		t.Fatalf("fail-closed result headers/body = %#v %q", result.Header, result.Body)
	}
}

func TestForwarderFailsClosedForMultipleEncodingFieldValues(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Add("Content-Encoding", "identity")
		writer.Header().Add("Content-Encoding", "gzip")
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = writer.Write([]byte("opaque-multi-value-body"))
	}))
	defer upstream.Close()

	result := testForward(t, upstream.URL, "sk-secret", time.Second)
	if result.Err != nil || result.StatusCode != http.StatusBadGateway {
		t.Fatalf("Forward() result = %#v", result)
	}
	if len(result.Header.Values("Content-Encoding")) != 0 ||
		string(result.Body) != redact.Placeholder ||
		string(result.ClassificationBody) != redact.Placeholder {
		t.Fatalf("fail-closed result headers/body = %#v %q", result.Header, result.Body)
	}
}

func TestForwarderFailsClosedForUnsupportedEmptyBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Encoding", "unsupported")
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer upstream.Close()

	result := testForward(t, upstream.URL, "sk-secret", time.Second)
	if result.Err != nil || result.StatusCode != http.StatusBadGateway {
		t.Fatalf("Forward() result = %#v", result)
	}
	if result.Header.Get("Content-Encoding") != "" ||
		string(result.Body) != redact.Placeholder ||
		string(result.ClassificationBody) != redact.Placeholder {
		t.Fatalf("fail-closed result headers/body = %#v %q", result.Header, result.Body)
	}
}

func TestForwarderFailsClosedForGzipEmptyBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Encoding", "gzip")
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer upstream.Close()

	result := testForward(t, upstream.URL, "sk-secret", time.Second)
	if result.Err != nil || result.StatusCode != http.StatusBadGateway {
		t.Fatalf("Forward() result = %#v", result)
	}
	if result.Header.Get("Content-Encoding") != "" ||
		string(result.Body) != redact.Placeholder ||
		string(result.ClassificationBody) != redact.Placeholder {
		t.Fatalf("fail-closed result headers/body = %#v %q", result.Header, result.Body)
	}
}

func TestForwarderMarksConnectionFailureAsNotWritten(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := upstream.URL
	upstream.Close()

	result := testForward(t, url, "sk-secret", 200*time.Millisecond)
	if result.Err == nil || result.RequestWritten {
		t.Fatalf("connection failure result = %#v, want error before write", result)
	}
}

func TestForwarderMarksTimeoutAfterRequestWrite(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		<-request.Context().Done()
	}))
	defer upstream.Close()

	result := testForward(t, upstream.URL, "sk-secret", 25*time.Millisecond)
	if result.Err == nil || !result.RequestWritten || !isTimeoutError(result.Err) {
		t.Fatalf("post-write timeout result = %#v", result)
	}
}

func testForward(t *testing.T, upstreamURL, apiKey string, timeout time.Duration) UpstreamResult {
	t.Helper()
	forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
	return forwarder.Forward(context.Background(), ForwardInput{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		Group: state.GroupView{
			ID: 1, UpstreamURL: upstreamURL,
			Timeouts: state.TimeoutConfig{Connect: timeout, FirstByte: timeout, Request: timeout},
		},
		APIKey: apiKey,
		Request: &dialect.ParsedRequest{
			Method: http.MethodPost, Path: "/v1/chat/completions",
			Header: make(http.Header), Body: []byte(`{"model":"gpt-4o"}`),
		},
	})
}
