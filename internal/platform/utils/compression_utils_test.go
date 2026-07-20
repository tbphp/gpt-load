package utils

import (
	"bytes"
	"compress/zlib"
	"errors"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestDecompressResponseLimitedRejectsZstdWindowAboveLimit(t *testing.T) {
	const (
		limit       = 1 << 20
		largeWindow = 8 << 20
	)
	plain := bytes.Repeat([]byte("x"), 2<<20)
	var wire bytes.Buffer
	encoder, err := zstd.NewWriter(
		&wire,
		zstd.WithWindowSize(largeWindow),
		zstd.WithEncoderConcurrency(1),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encoder.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	var header zstd.Header
	if err := header.Decode(wire.Bytes()); err != nil {
		t.Fatal(err)
	}
	if header.WindowSize <= limit || wire.Len() >= zstd.MinWindowSize {
		t.Fatalf("fixture window/wire = %d/%d", header.WindowSize, wire.Len())
	}
	compatDecoded, err := DecompressResponse("zstd", wire.Bytes())
	if err != nil || !bytes.Equal(compatDecoded, plain) {
		t.Fatalf("compatible decode = %d bytes, %v", len(compatDecoded), err)
	}

	decoded, err := DecompressResponseLimited("zstd", wire.Bytes(), limit)
	if !errors.Is(err, zstd.ErrWindowSizeExceeded) || decoded != nil {
		t.Fatalf("limited decode = %q, %v", decoded, err)
	}
}

func TestDecompressResponseLimitedBoundsAllSupportedEncodings(t *testing.T) {
	for _, encoding := range []string{"gzip", "br", "deflate", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			for _, size := range []int{1 << 20, 1<<20 + 1} {
				plain := bytes.Repeat([]byte("x"), size)
				wire := compressResponseWithBoundedZstdWindow(t, encoding, plain)
				decoded, err := DecompressResponseLimited(encoding, wire, 1<<20)
				if size == 1<<20 {
					if err != nil || !bytes.Equal(decoded, plain) {
						t.Fatalf("exact limit = %d bytes, %v", len(decoded), err)
					}
				} else if !errors.Is(err, ErrDecompressedResponseTooLarge) || decoded != nil {
					t.Fatalf("overflow = %d bytes, %v", len(decoded), err)
				}
			}
		})
	}
}

func compressResponseWithBoundedZstdWindow(t *testing.T, encoding string, plain []byte) []byte {
	t.Helper()
	if encoding != "zstd" {
		wire, err := CompressResponse(encoding, plain)
		if err != nil {
			t.Fatal(err)
		}
		return wire
	}
	encoder, err := zstd.NewWriter(
		nil,
		zstd.WithWindowSize(1<<20),
		zstd.WithEncoderConcurrency(1),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	return encoder.EncodeAll(plain, nil)
}

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
