package gateway

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"

	"gpt-load/internal/dialect"
	platformhttp "gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/contentcoding"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/state"
)

func TestReadDecodedRequestBodyNormalizesSupportedEncodings(t *testing.T) {
	plain := []byte(`{"model":"gpt-test","messages":[]}`)
	for _, encoding := range []string{"", "identity", "gzip", "br", "deflate", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			wire := encodeGatewayContentCodingFixture(t, encoding, plain)
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(wire))
			if encoding != "" {
				request.Header.Set("Content-Encoding", encoding)
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Content-Length", strconv.Itoa(len(wire)))
			request.Header.Set("Digest", "sha-256=stale")
			request.Header.Set("Signature", "stale")

			body, headers, err := readDecodedRequestBody(request, 1<<20, 1<<20)
			if err != nil || !bytes.Equal(body, plain) {
				t.Fatalf("readDecodedRequestBody(%q) = %q, %v", encoding, body, err)
			}
			for _, name := range []string{"Content-Encoding", "Content-Length", "Digest", "Signature"} {
				if values := headers.Values(name); values != nil {
					t.Errorf("normalized header %s survived: %#v", name, values)
				}
			}
			if got := headers.Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q", got)
			}
		})
	}
}

func TestReadDecodedRequestBodyReturnsStableContentCodingErrors(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte("body")))
	request.Header.Set("Content-Encoding", "gzip, br")
	if _, _, err := readDecodedRequestBody(request, 1<<20, 1<<20); !errors.Is(err, contentcoding.ErrUnsupportedEncoding) {
		t.Fatalf("stacked encoding error = %v", err)
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte("not-gzip")))
	request.Header.Set("Content-Encoding", "gzip")
	if _, _, err := readDecodedRequestBody(request, 1<<20, 1<<20); !errors.Is(err, contentcoding.ErrInvalidEncoding) {
		t.Fatalf("malformed encoding error = %v", err)
	}

	plain := bytes.Repeat([]byte("x"), 1025)
	wire := encodeGatewayContentCodingFixture(t, "gzip", plain)
	request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(wire))
	request.Header.Set("Content-Encoding", "gzip")
	if _, _, err := readDecodedRequestBody(request, 1<<20, 1024); !errors.Is(err, contentcoding.ErrDecodedTooLarge) {
		t.Fatalf("decoded overflow error = %v", err)
	}
}

func TestNewUpstreamRequestAlwaysUsesPlainContentCoding(t *testing.T) {
	payload := []byte(`{"model":"gpt-test","messages":[]}`)
	input := ForwardInput{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		Group: state.GroupView{
			UpstreamURL: "https://example.com",
			HeaderRules: state.HeaderRules{Set: map[string]string{
				"Content-Encoding": "gzip",
				"Accept-Encoding":  "br",
				"Content-Length":   "1",
				"Digest":           "sha-256=rule-stale",
			}},
		},
		APIKey: "provider-key",
		Request: &dialect.ParsedRequest{
			Method: http.MethodPost,
			Path:   "/v1/chat/completions",
			Header: http.Header{
				"Content-Type":     {"application/json"},
				"Content-Encoding": {"zstd"},
				"Accept-Encoding":  {"gzip"},
				"Digest":           {"sha-256=client-stale"},
			},
			Body: payload,
		},
	}

	request, _, replay, err := NewForwarder(
		platformhttp.NewHTTPClientManager(),
		redact.New(),
	).newUpstreamRequest(t.Context(), input, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = request.Body.Close()
		replay.release()
	})

	if got := request.Header.Get("Accept-Encoding"); got != "identity" {
		t.Fatalf("Accept-Encoding = %q, want identity", got)
	}
	for _, name := range []string{"Content-Encoding", "Content-Length", "Digest"} {
		if values := request.Header.Values(name); values != nil {
			t.Errorf("upstream representation header %s survived: %#v", name, values)
		}
	}
	if request.ContentLength != int64(len(payload)) {
		t.Fatalf("ContentLength = %d, want %d", request.ContentLength, len(payload))
	}
	body, err := io.ReadAll(request.Body)
	if err != nil || !bytes.Equal(body, payload) {
		t.Fatalf("upstream body = %q, %v", body, err)
	}
}

func TestForwardReturnsCompressedNonStreamingResponsesAsPlaintext(t *testing.T) {
	plain := []byte(`{"id":"response","model":"gpt-test"}`)
	for _, encoding := range []string{"gzip", "br", "deflate", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			wire := encodeGatewayContentCodingFixture(t, encoding, plain)
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.Header().Set("Content-Encoding", encoding)
				writer.Header().Set("Content-Length", strconv.Itoa(len(wire)))
				_, _ = writer.Write(wire)
			}))
			defer upstream.Close()

			result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).Forward(
				t.Context(),
				plainForwardInput(upstream.URL, false),
			)
			if result.Err != nil || result.StatusCode != http.StatusOK {
				t.Fatalf("Forward() = %#v", result)
			}
			if !bytes.Equal(result.Body, plain) {
				t.Fatalf("downstream body = %q, want %q", result.Body, plain)
			}
			if got := result.Header.Get("Content-Encoding"); got != "" {
				t.Fatalf("Content-Encoding = %q", got)
			}
			if got := result.Header.Get("Content-Length"); got != strconv.Itoa(len(plain)) {
				t.Fatalf("Content-Length = %q, want %d", got, len(plain))
			}
		})
	}
}

func TestForwardReturnsCompressedErrorResponsesAsPlaintext(t *testing.T) {
	plain := []byte(`{"error":{"message":"denied"}}`)
	wire := encodeGatewayContentCodingFixture(t, "gzip", plain)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Content-Encoding", "gzip")
		writer.Header().Set("Content-Length", strconv.Itoa(len(wire)))
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write(wire)
	}))
	defer upstream.Close()

	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).Forward(
		t.Context(),
		plainForwardInput(upstream.URL, false),
	)
	if result.Err != nil || result.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Forward() = %#v", result)
	}
	if !bytes.Equal(result.Body, plain) || !bytes.Equal(result.ClassificationBody, plain) {
		t.Fatalf("plain bodies = %q / %q", result.Body, result.ClassificationBody)
	}
	if got := result.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q", got)
	}
	if got := result.Header.Get("Content-Length"); got != strconv.Itoa(len(plain)) {
		t.Fatalf("Content-Length = %q, want %d", got, len(plain))
	}
}

func TestForwardStreamRejectsCompressedSuccessBeforeCommit(t *testing.T) {
	wire := encodeGatewayContentCodingFixture(t, "gzip", []byte("data: {\"id\":\"chunk\"}\n\n"))
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set("Content-Encoding", "gzip")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(wire)
	}))
	defer upstream.Close()

	recorder := httptest.NewRecorder()
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
		context.Background(),
		plainForwardInput(upstream.URL, true),
		recorder,
	)
	if !errors.Is(result.Err, ErrUpstreamProtocol) || result.Committed {
		t.Fatalf("ForwardStream() = %#v", result)
	}
	if recorder.Body.Len() != 0 || recorder.Header().Get("Content-Encoding") != "" {
		t.Fatalf("downstream received body/header: %q / %#v", recorder.Body.Bytes(), recorder.Header())
	}
}

func plainForwardInput(upstreamURL string, stream bool) ForwardInput {
	body := []byte(`{"model":"gpt-test","messages":[]}`)
	if stream {
		body = []byte(`{"model":"gpt-test","messages":[],"stream":true}`)
	}
	return ForwardInput{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		Group: state.GroupView{
			UpstreamURL: upstreamURL,
			Timeouts: state.TimeoutConfig{
				Connect:    2 * time.Second,
				FirstByte:  2 * time.Second,
				Request:    5 * time.Second,
				StreamIdle: 2 * time.Second,
			},
		},
		APIKey: "provider-key",
		Request: &dialect.ParsedRequest{
			Method: http.MethodPost,
			Path:   "/v1/chat/completions",
			Header: http.Header{"Content-Type": {"application/json"}},
			Body:   body,
		},
	}
}

func encodeGatewayContentCodingFixture(t *testing.T, encoding string, plain []byte) []byte {
	t.Helper()
	if encoding == "" || encoding == "identity" {
		return bytes.Clone(plain)
	}
	var buffer bytes.Buffer
	var writer io.WriteCloser
	switch encoding {
	case "gzip":
		writer = gzip.NewWriter(&buffer)
	case "br":
		writer = brotli.NewWriter(&buffer)
	case "deflate":
		writer = zlib.NewWriter(&buffer)
	case "zstd":
		encoder, err := zstd.NewWriter(&buffer, zstd.WithEncoderConcurrency(1))
		if err != nil {
			t.Fatal(err)
		}
		writer = encoder
	default:
		t.Fatalf("unsupported fixture encoding %q", encoding)
	}
	if _, err := writer.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
