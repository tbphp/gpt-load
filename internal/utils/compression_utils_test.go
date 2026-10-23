package utils

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

const samplePayload = `{"error":{"message":"upstream unavailable for model muse-spark-1-3-contributor:free"}}`

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

func zstdBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatalf("zstd write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zstd close: %v", err)
	}
	return buf.Bytes()
}

func zlibBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatalf("zlib write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zlib close: %v", err)
	}
	return buf.Bytes()
}

func rawDeflateBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		t.Fatalf("flate writer: %v", err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatalf("flate write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("flate close: %v", err)
	}
	return buf.Bytes()
}

// TestDecompressResponseSingleEncoding verifies that every coding an upstream
// may select for a proxied response (gzip, br, zstd, deflate in both the
// RFC 7230 zlib form and the widespread raw form) is decoded to plain text.
func TestDecompressResponseSingleEncoding(t *testing.T) {
	payload := []byte(samplePayload)

	cases := []struct {
		name     string
		encoding string
		body     []byte
	}{
		{name: "gzip", encoding: "gzip", body: gzipBytes(t, payload)},
		{name: "brotli", encoding: "br", body: brotliBytes(t, payload)},
		{name: "zstd", encoding: "zstd", body: zstdBytes(t, payload)},
		{name: "deflate zlib wrapped", encoding: "deflate", body: zlibBytes(t, payload)},
		{name: "deflate raw", encoding: "deflate", body: rawDeflateBytes(t, payload)},
		{name: "identity", encoding: "identity", body: payload},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecompressResponse(tc.encoding, tc.body)
			if err != nil {
				t.Fatalf("DecompressResponse(%q) returned error: %v", tc.encoding, err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("DecompressResponse(%q) = %q, want %q", tc.encoding, got, payload)
			}
		})
	}
}

// TestDecompressResponseHeaderVariants covers Content-Encoding values that real
// servers and CDNs emit: different letter case, surrounding whitespace and
// multiple codings applied in sequence (undone in reverse order).
func TestDecompressResponseHeaderVariants(t *testing.T) {
	payload := []byte(samplePayload)
	// Applied by the sender as gzip first, then brotli.
	doubleEncoded := brotliBytes(t, gzipBytes(t, payload))

	cases := []struct {
		name     string
		encoding string
		body     []byte
	}{
		{name: "upper case", encoding: "GZIP", body: gzipBytes(t, payload)},
		{name: "padded", encoding: "  br  ", body: brotliBytes(t, payload)},
		{name: "multiple codings", encoding: "gzip, br", body: doubleEncoded},
		{name: "multiple codings mixed case", encoding: "GZip,BR", body: doubleEncoded},
		{name: "empty header", encoding: "", body: payload},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecompressResponse(tc.encoding, tc.body)
			if err != nil {
				t.Fatalf("DecompressResponse(%q) returned error: %v", tc.encoding, err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("DecompressResponse(%q) = %q, want %q", tc.encoding, got, payload)
			}
		})
	}
}

// TestDecompressResponseErrors verifies that bodies which cannot be decoded are
// reported as errors rather than returned still compressed. Callers rely on the
// error to avoid surfacing binary data as an error message.
func TestDecompressResponseErrors(t *testing.T) {
	payload := []byte(samplePayload)

	cases := []struct {
		name     string
		encoding string
		body     []byte
	}{
		{name: "unknown coding", encoding: "compress", body: payload},
		{name: "unsupported coding in list", encoding: "gzip, compress", body: gzipBytes(t, payload)},
		{name: "corrupt gzip", encoding: "gzip", body: []byte("definitely not gzip")},
		{name: "corrupt brotli", encoding: "br", body: []byte{0xff, 0xff, 0xff, 0xff}},
		{name: "truncated brotli", encoding: "br", body: brotliBytes(t, payload)[:10]},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecompressResponse(tc.encoding, tc.body); err == nil {
				t.Fatalf("DecompressResponse(%q) returned no error, want error", tc.encoding)
			}
		})
	}

	if got, err := DecompressResponse("gzip", nil); err != nil || len(got) != 0 {
		t.Fatalf("DecompressResponse with empty body = (%q, %v), want (empty, nil)", got, err)
	}
}

// TestDecompressResponseLimited verifies the decoded size limit that protects
// callers from decompression bombs, and that a decoded body of exactly the
// allowed size is still accepted.
func TestDecompressResponseLimited(t *testing.T) {
	payload := []byte(samplePayload)

	compressors := map[string]func(*testing.T, []byte) []byte{
		"gzip":    gzipBytes,
		"br":      brotliBytes,
		"zstd":    zstdBytes,
		"deflate": zlibBytes,
	}

	for encoding, compress := range compressors {
		t.Run(encoding, func(t *testing.T) {
			encoded := compress(t, payload)

			decoded, err := DecompressResponseLimited(encoding, encoded, int64(len(payload)))
			if err != nil {
				t.Fatalf("exact size limit returned error: %v", err)
			}
			if !bytes.Equal(decoded, payload) {
				t.Fatalf("decoded body = %q, want %q", decoded, payload)
			}

			if _, err := DecompressResponseLimited(encoding, encoded, int64(len(payload))-1); !errors.Is(err, ErrDecodedBodyTooLarge) {
				t.Fatalf("limit one byte too small: err = %v, want ErrDecodedBodyTooLarge", err)
			}

			// No limit must keep working for callers that need the full body.
			if _, err := DecompressResponseLimited(encoding, encoded, 0); err != nil {
				t.Fatalf("unlimited decode returned error: %v", err)
			}
		})
	}
}
