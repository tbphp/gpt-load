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
