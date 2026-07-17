package utils

import (
	"bytes"
	"compress/zlib"
	"testing"
)

func TestDecompressResponseSupportsHTTPDeflate(t *testing.T) {
	want := []byte("deflate response body")
	var encoded bytes.Buffer
	writer := zlib.NewWriter(&encoded)
	if _, err := writer.Write(want); err != nil {
		t.Fatalf("encode deflate body: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close deflate writer: %v", err)
	}

	got, err := DecompressResponse("deflate", encoded.Bytes())
	if err != nil {
		t.Fatalf("DecompressResponse() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("DecompressResponse() = %q, want %q", got, want)
	}
}

func TestCompressAndDecompressResponseRoundTrip(t *testing.T) {
	original := []byte(`{"error":{"api_key":"sk-secret-value"}}`)
	for _, encoding := range []string{"", "identity", "gzip", "br", "deflate", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			encoded, err := CompressResponse(encoding, original)
			if err != nil {
				t.Fatalf("CompressResponse(%q) error = %v", encoding, err)
			}
			decoded, err := DecompressResponse(encoding, encoded)
			if err != nil {
				t.Fatalf("DecompressResponse(%q) error = %v", encoding, err)
			}
			if !bytes.Equal(decoded, original) {
				t.Fatalf("round trip = %q, want %q", decoded, original)
			}
		})
	}
}

func TestResponseCompressionRejectsUnknownOrStackedEncoding(t *testing.T) {
	for _, encoding := range []string{"compress", "gzip, br"} {
		if _, err := DecompressResponse(encoding, []byte("body")); err == nil {
			t.Fatalf("DecompressResponse(%q) error = nil", encoding)
		}
		if _, err := CompressResponse(encoding, []byte("body")); err == nil {
			t.Fatalf("CompressResponse(%q) error = nil", encoding)
		}
	}
}

func TestDecompressResponseRejectsMalformedBody(t *testing.T) {
	if _, err := DecompressResponse("gzip", []byte("not gzip")); err == nil {
		t.Fatal("DecompressResponse(malformed gzip) error = nil")
	}
}
