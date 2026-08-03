package gateway

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"

	"gpt-load/internal/platform/contentcoding"
)

func encodeContentCodingForGatewayTest(
	t *testing.T,
	encoding contentcoding.Encoding,
	body []byte,
) []byte {
	t.Helper()
	if encoding == contentcoding.Identity {
		return bytes.Clone(body)
	}

	var buffer bytes.Buffer
	switch encoding {
	case contentcoding.Gzip:
		writer := gzip.NewWriter(&buffer)
		if _, err := writer.Write(body); err != nil {
			t.Fatalf("gzip fixture Write() error = %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("gzip fixture Close() error = %v", err)
		}
	case contentcoding.Brotli:
		writer := brotli.NewWriter(&buffer)
		if _, err := writer.Write(body); err != nil {
			t.Fatalf("brotli fixture Write() error = %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("brotli fixture Close() error = %v", err)
		}
	case contentcoding.Deflate:
		writer := zlib.NewWriter(&buffer)
		if _, err := writer.Write(body); err != nil {
			t.Fatalf("deflate fixture Write() error = %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("deflate fixture Close() error = %v", err)
		}
	case contentcoding.Zstd:
		writer, err := zstd.NewWriter(&buffer, zstd.WithEncoderConcurrency(1))
		if err != nil {
			t.Fatalf("zstd fixture NewWriter() error = %v", err)
		}
		if _, err := writer.Write(body); err != nil {
			t.Fatalf("zstd fixture Write() error = %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("zstd fixture Close() error = %v", err)
		}
	default:
		t.Fatalf("unsupported fixture encoding %q", encoding)
	}
	return buffer.Bytes()
}
