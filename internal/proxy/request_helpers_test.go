package proxy

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strings"
	"testing"

	app_errors "gpt-load/internal/errors"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

func newResponseWithEncoding(encoding string) *http.Response {
	resp := &http.Response{Header: http.Header{}}
	if encoding != "" {
		resp.Header.Set("Content-Encoding", encoding)
	}
	return resp
}

func brotliBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := brotli.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatalf("brotli write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("brotli close: %v", err)
	}
	return buf.Bytes()
}

// zstdStreamingBytes encodes like a streaming writer: flush before close, which
// makes the frame declare the encoder's full 8 MiB window.
func zstdStreamingBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatalf("zstd write: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("zstd flush: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zstd close: %v", err)
	}
	return buf.Bytes()
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// TestDecodeUpstreamErrorBody verifies that error bodies compressed by the
// upstream (because the proxy forwards the caller's Accept-Encoding) are
// decoded before they are exposed as an error message.
func TestDecodeUpstreamErrorBody(t *testing.T) {
	payload := []byte(`{"error":{"message":"upstream unavailable for model test-model:free"}}`)

	cases := []struct {
		name     string
		encoding string
		body     []byte
	}{
		{name: "no content encoding", encoding: "", body: payload},
		{name: "gzip", encoding: "gzip", body: gzipBytes(t, payload)},
		{name: "brotli", encoding: "br", body: brotliBytes(t, payload)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeUpstreamErrorBody(newResponseWithEncoding(tc.encoding), tc.body)
			if !bytes.Equal(got, payload) {
				t.Fatalf("decodeUpstreamErrorBody(%q) = %q, want %q", tc.encoding, got, payload)
			}
		})
	}
}

// TestDecodeUpstreamErrorBodyZstdStreaming covers a streamed zstd error body: the
// frame declares its full 8 MiB window even for a few bytes, and losing the text
// would make an otherwise uncounted failure count against the key.
func TestDecodeUpstreamErrorBodyZstdStreaming(t *testing.T) {
	payload := []byte(`{"error":"resource has been exhausted"}`)

	got := decodeUpstreamErrorBody(newResponseWithEncoding("zstd"), zstdStreamingBytes(t, payload))
	if !bytes.Equal(got, payload) {
		t.Fatalf("decodeUpstreamErrorBody = %q, want %q", got, payload)
	}
	if !app_errors.IsUnCounted(string(got)) {
		t.Fatalf("decoded body %q is not recognised as an uncounted error", got)
	}
}

// TestDecodeUpstreamErrorBodyRepeatedHeader covers upstreams that send one
// Content-Encoding field line per applied coding instead of a single list.
func TestDecodeUpstreamErrorBodyRepeatedHeader(t *testing.T) {
	payload := []byte(`{"error":{"message":"upstream unavailable"}}`)
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Add("Content-Encoding", "gzip")
	resp.Header.Add("Content-Encoding", "br")

	// Applied by the sender as gzip first, then brotli.
	got := decodeUpstreamErrorBody(resp, brotliBytes(t, gzipBytes(t, payload)))
	if !bytes.Equal(got, payload) {
		t.Fatalf("decodeUpstreamErrorBody with repeated headers = %q, want %q", got, payload)
	}
}

// TestDecodeUpstreamErrorBodyUndecodable verifies that undecodable bodies are
// replaced with a readable diagnostic instead of leaking binary bytes.
func TestDecodeUpstreamErrorBodyUndecodable(t *testing.T) {
	binary := []byte{0x1b, 0x00, 0x00, 0xc4, 0xac, 0xff, 0xfe}

	got := decodeUpstreamErrorBody(newResponseWithEncoding("compress"), binary)
	if bytes.Contains(got, binary) {
		t.Fatalf("decoded body still contains the raw compressed bytes: %q", got)
	}
	if !strings.Contains(string(got), "compress") {
		t.Fatalf("diagnostic body %q does not mention the Content-Encoding", got)
	}
}

// TestDecodeUpstreamErrorBodyMislabeledEncoding verifies that a readable body
// behind a wrong Content-Encoding header is still surfaced to the caller.
func TestDecodeUpstreamErrorBodyMislabeledEncoding(t *testing.T) {
	plain := []byte(`{"error":"rate limit exceeded"}`)

	got := decodeUpstreamErrorBody(newResponseWithEncoding("gzip"), plain)
	if !bytes.Equal(got, plain) {
		t.Fatalf("decodeUpstreamErrorBody = %q, want the plain body %q", got, plain)
	}
}

// TestDecodeUpstreamErrorBodyTooLarge verifies that a decompression bomb is cut
// off instead of being decoded into memory, and that no binary is exposed.
func TestDecodeUpstreamErrorBodyTooLarge(t *testing.T) {
	// Highly compressible body well above maxDecodedErrorBodyBytes.
	bomb := bytes.Repeat([]byte(`{"error":{"message":"spam"}}`+"\n"), maxDecodedErrorBodyBytes/4)
	encoded := brotliBytes(t, bomb)
	if len(encoded) >= len(bomb) {
		t.Fatalf("bomb did not compress: %d -> %d bytes", len(bomb), len(encoded))
	}

	resp := newResponseWithEncoding("br")
	got := decodeUpstreamErrorBody(resp, encoded)

	if bytes.Contains(got, bomb[:64]) {
		t.Fatal("decoded body was not cut off at the size limit")
	}
	if !strings.Contains(string(got), "too large") {
		t.Fatalf("expected a size limit diagnostic, got %q", got)
	}
}
