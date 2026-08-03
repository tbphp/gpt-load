package gateway

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"

	"gpt-load/internal/platform/contentcoding"
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
			request.Header.Set("Accept-Encoding", "gzip")
			request.Header.Set("Digest", "sha-256=stale")

			body, headers, err := readDecodedRequestBody(request, 1<<20, 1<<20)
			if err != nil || !bytes.Equal(body, plain) {
				t.Fatalf("readDecodedRequestBody(%q) = %q, %v", encoding, body, err)
			}
			for _, name := range []string{"Content-Encoding", "Content-Length", "Digest"} {
				if values := headers.Values(name); values != nil {
					t.Errorf("normalized header %s survived: %#v", name, values)
				}
			}
			if headers.Get("Accept-Encoding") != "identity" || headers.Get("Content-Type") != "application/json" {
				t.Fatalf("normalized headers = %#v", headers)
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

func TestPlaintextResponseWriterDecodesCompressedBufferedResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	plain := []byte(`{"id":"response","model":"gpt-test"}`)
	for _, encoding := range []string{"gzip", "br", "deflate", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			wire := encodeGatewayContentCodingFixture(t, encoding, plain)
			recorder := httptest.NewRecorder()
			engine := gin.New()
			engine.GET("/", (&Handler{}).normalizeDownstreamContentCoding(), func(context *gin.Context) {
				context.Header("Content-Type", "application/json")
				context.Header("Content-Encoding", encoding)
				context.Header("Content-Length", strconv.Itoa(len(wire)))
				context.Header("ETag", "stale-compressed-etag")
				context.Status(http.StatusOK)
				_, _ = context.Writer.Write(wire)
				context.Writer.Flush()
			})
			engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
			if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), plain) {
				t.Fatalf("response = %d %q, want %q", recorder.Code, recorder.Body.Bytes(), plain)
			}
			if recorder.Header().Get("Content-Encoding") != "" || recorder.Header().Get("ETag") != "" ||
				recorder.Header().Get("Content-Length") != strconv.Itoa(len(plain)) {
				t.Fatalf("plaintext headers = %#v", recorder.Header())
			}
		})
	}
}

func TestPlaintextResponseWriterPreservesValidIdentityMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"ok":true}`)
	recorder := httptest.NewRecorder()
	engine := gin.New()
	engine.GET("/", (&Handler{}).normalizeDownstreamContentCoding(), func(context *gin.Context) {
		context.Header("Content-Encoding", "identity")
		context.Header("ETag", "valid-identity-etag")
		context.Status(http.StatusOK)
		_, _ = context.Writer.Write(body)
		context.Writer.Flush()
	})
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if !bytes.Equal(recorder.Body.Bytes(), body) || recorder.Header().Get("Content-Encoding") != "" ||
		recorder.Header().Get("ETag") != "valid-identity-etag" {
		t.Fatalf("identity response = %q headers=%#v", recorder.Body.Bytes(), recorder.Header())
	}
}

func TestPlaintextResponseWriterStreamsIdentitySSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	engine := gin.New()
	engine.GET("/", (&Handler{}).normalizeDownstreamContentCoding(), func(context *gin.Context) {
		context.Header("Content-Type", "text/event-stream")
		context.Header("Content-Encoding", "identity")
		context.Header("ETag", "stale-stream-etag")
		context.Status(http.StatusOK)
		_, _ = context.Writer.WriteString("data: one\n\n")
		context.Writer.Flush()
		_, _ = context.Writer.WriteString("data: two\n\n")
		context.Writer.Flush()
	})
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Body.String() != "data: one\n\ndata: two\n\n" {
		t.Fatalf("stream body = %q", recorder.Body.String())
	}
	if recorder.Header().Get("Content-Encoding") != "" || recorder.Header().Get("Content-Length") != "" ||
		recorder.Header().Get("ETag") != "" {
		t.Fatalf("stream headers = %#v", recorder.Header())
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
