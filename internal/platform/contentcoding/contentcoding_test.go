package contentcoding

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

func TestParseContentEncoding(t *testing.T) {
	tests := []struct {
		name    string
		values  []string
		want    Encoding
		wantErr error
	}{
		{name: "missing is identity", want: Identity},
		{name: "empty is identity", values: []string{"  "}, want: Identity},
		{name: "identity is case insensitive", values: []string{" IDENTITY "}, want: Identity},
		{name: "gzip", values: []string{"gzip"}, want: Gzip},
		{name: "brotli", values: []string{"BR"}, want: Brotli},
		{name: "deflate", values: []string{"deflate"}, want: Deflate},
		{name: "zstd", values: []string{"zstd"}, want: Zstd},
		{name: "multiple fields", values: []string{"gzip", "br"}, wantErr: ErrUnsupportedContentEncoding},
		{name: "stacked coding", values: []string{"gzip, br"}, wantErr: ErrUnsupportedContentEncoding},
		{name: "unknown coding", values: []string{"compress"}, wantErr: ErrUnsupportedContentEncoding},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseContentEncoding(tt.values)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ParseContentEncoding(%q) error = %v, want errors.Is(_, %v)", tt.values, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("ParseContentEncoding(%q) = %q, want %q", tt.values, got, tt.want)
			}
		})
	}
}

func TestDecodeLimited(t *testing.T) {
	plain := []byte("content coding boundary")
	tests := []struct {
		name     string
		encoding Encoding
		encoded  []byte
	}{
		{name: "identity", encoding: Identity, encoded: plain},
		{name: "gzip", encoding: Gzip, encoded: encodeGzip(t, plain)},
		{name: "brotli", encoding: Brotli, encoded: encodeBrotli(t, plain)},
		{name: "zlib deflate", encoding: Deflate, encoded: encodeZlib(t, plain)},
		{name: "zstd", encoding: Zstd, encoded: encodeZstd(t, plain, ZstdHTTPMaxWindowBytes)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeLimited(tt.encoding, tt.encoded, int64(len(plain)))
			if err != nil {
				t.Fatalf("DecodeLimited(%q) error = %v", tt.encoding, err)
			}
			if !bytes.Equal(got, plain) {
				t.Fatalf("DecodeLimited(%q) = %q, want %q", tt.encoding, got, plain)
			}
		})
	}
}

func TestDecodeLimitedRejectsInvalidStreams(t *testing.T) {
	plain := []byte("content coding boundary")
	tests := []struct {
		name     string
		encoding Encoding
		encoded  []byte
	}{
		{name: "gzip empty", encoding: Gzip},
		{name: "brotli empty", encoding: Brotli},
		{name: "deflate empty", encoding: Deflate},
		{name: "zstd empty", encoding: Zstd},
		{name: "gzip truncated", encoding: Gzip, encoded: truncate(encodeGzip(t, plain))},
		{name: "brotli truncated", encoding: Brotli, encoded: truncate(encodeBrotli(t, plain))},
		{name: "deflate truncated", encoding: Deflate, encoded: truncate(encodeZlib(t, plain))},
		{name: "zstd truncated", encoding: Zstd, encoded: truncate(encodeZstd(t, plain, ZstdHTTPMaxWindowBytes))},
		{name: "gzip corrupt", encoding: Gzip, encoded: []byte("not a gzip stream")},
		{name: "brotli corrupt", encoding: Brotli, encoded: []byte("not a brotli stream")},
		{name: "deflate corrupt", encoding: Deflate, encoded: []byte("not a zlib stream")},
		{name: "zstd corrupt", encoding: Zstd, encoded: []byte("not a zstd stream")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeLimited(tt.encoding, tt.encoded, 1024)
			if !errors.Is(err, ErrInvalidContentEncoding) || got != nil {
				t.Fatalf("DecodeLimited(%q, %q) = %q, %v; want invalid content encoding", tt.encoding, tt.encoded, got, err)
			}
		})
	}
}

func TestDecodeLimitedHonorsDecodedLimit(t *testing.T) {
	plain := []byte("limit")
	tests := []struct {
		name     string
		encoding Encoding
		encoded  []byte
	}{
		{name: "identity", encoding: Identity, encoded: plain},
		{name: "gzip", encoding: Gzip, encoded: encodeGzip(t, plain)},
		{name: "brotli", encoding: Brotli, encoded: encodeBrotli(t, plain)},
		{name: "deflate", encoding: Deflate, encoded: encodeZlib(t, plain)},
		{name: "zstd", encoding: Zstd, encoded: encodeZstd(t, plain, ZstdHTTPMaxWindowBytes)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeLimited(tt.encoding, tt.encoded, int64(len(plain)-1))
			if !errors.Is(err, ErrDecodedBodyTooLarge) || got != nil {
				t.Fatalf("DecodeLimited(%q) = %q, %v; want decoded body too large", tt.encoding, got, err)
			}
		})
	}
}

func TestDecodeLimitedIdentityReturnsCopy(t *testing.T) {
	encoded := []byte("identity")
	got, err := DecodeLimited(Identity, encoded, int64(len(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	got[0] = 'I'
	if encoded[0] != 'i' {
		t.Fatal("DecodeLimited(Identity) returned an aliased slice")
	}
}

func TestDecodeLimitedRejectsRawDeflate(t *testing.T) {
	plain := []byte("raw deflate is not HTTP deflate")
	var encoded bytes.Buffer
	writer, err := flate.NewWriter(&encoded, flate.DefaultCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := DecodeLimited(Deflate, encoded.Bytes(), 1024)
	if !errors.Is(err, ErrInvalidContentEncoding) || got != nil {
		t.Fatalf("DecodeLimited(raw deflate) = %q, %v; want invalid content encoding", got, err)
	}
}

func TestDecodeLimitedRejectsTrailingData(t *testing.T) {
	plain := []byte("trailing bytes must not be ignored")
	tests := []struct {
		name     string
		encoding Encoding
		encoded  []byte
	}{
		{name: "gzip", encoding: Gzip, encoded: append(encodeGzip(t, plain), []byte("trailing")...)},
		{name: "zlib deflate", encoding: Deflate, encoded: append(encodeZlib(t, plain), []byte("trailing")...)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeLimited(tt.encoding, tt.encoded, 1024)
			if !errors.Is(err, ErrInvalidContentEncoding) || got != nil {
				t.Fatalf("DecodeLimited(%q) = %q, %v; want invalid content encoding", tt.encoding, got, err)
			}
		})
	}
}

func TestDecodeLimitedAcceptsConcatenatedGzipMembers(t *testing.T) {
	first := []byte("first ")
	second := []byte("second")
	encoded := append(encodeGzip(t, first), encodeGzip(t, second)...)

	got, err := DecodeLimited(Gzip, encoded, int64(len(first)+len(second)))
	if err != nil {
		t.Fatalf("DecodeLimited() error = %v", err)
	}
	if want := append(first, second...); !bytes.Equal(got, want) {
		t.Fatalf("DecodeLimited() = %q, want %q", got, want)
	}
}

func TestZstdHTTPWindow(t *testing.T) {
	plain := []byte("small body with an explicitly declared zstd window")
	t.Run("maximum window is accepted below decoded limit", func(t *testing.T) {
		encoded := encodeZstdWithDeclaredWindow(t, plain, ZstdHTTPMaxWindowBytes)
		got, err := DecodeLimited(Zstd, encoded, int64(len(plain)))
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("DecodeLimited() = %q, %v", got, err)
		}
	})
	t.Run("window above maximum is rejected", func(t *testing.T) {
		encoded := encodeZstdWithDeclaredWindow(t, plain, 16<<20)
		got, err := DecodeLimited(Zstd, encoded, 1024)
		if !errors.Is(err, ErrInvalidContentEncoding) || got != nil {
			t.Fatalf("DecodeLimited() = %q, %v; want invalid content encoding", got, err)
		}
	})
	t.Run("output limit is independent from window limit", func(t *testing.T) {
		encoded := encodeZstdWithDeclaredWindow(t, plain, ZstdHTTPMaxWindowBytes)
		got, err := DecodeLimited(Zstd, encoded, int64(len(plain)-1))
		if !errors.Is(err, ErrDecodedBodyTooLarge) || got != nil {
			t.Fatalf("DecodeLimited() = %q, %v; want decoded body too large", got, err)
		}
	})
}

func encodeZstdWithDeclaredWindow(t *testing.T, plain []byte, window int) []byte {
	t.Helper()
	encoded := encodeZstd(t, plain, window)

	var header zstd.Header
	if err := header.Decode(encoded); err != nil {
		t.Fatalf("decode generated zstd header: %v", err)
	}
	header.SingleSegment = false
	header.WindowSize = uint64(window)
	rebuilt, err := header.AppendTo(nil)
	if err != nil {
		t.Fatalf("rebuild zstd header: %v", err)
	}
	rebuilt = append(rebuilt, encoded[header.HeaderSize:]...)

	var got zstd.Header
	if err := got.Decode(rebuilt); err != nil {
		t.Fatalf("decode rebuilt zstd header: %v", err)
	}
	if got.SingleSegment || got.WindowSize != uint64(window) {
		t.Fatalf("zstd fixture header = {SingleSegment:%t WindowSize:%d}, want {false %d}", got.SingleSegment, got.WindowSize, window)
	}
	return rebuilt
}

func encodeGzip(t *testing.T, plain []byte) []byte {
	t.Helper()
	var encoded bytes.Buffer
	writer := gzip.NewWriter(&encoded)
	writeAndClose(t, writer, plain)
	return encoded.Bytes()
}

func encodeBrotli(t *testing.T, plain []byte) []byte {
	t.Helper()
	var encoded bytes.Buffer
	writer := brotli.NewWriter(&encoded)
	writeAndClose(t, writer, plain)
	return encoded.Bytes()
}

func encodeZlib(t *testing.T, plain []byte) []byte {
	t.Helper()
	var encoded bytes.Buffer
	writer := zlib.NewWriter(&encoded)
	writeAndClose(t, writer, plain)
	return encoded.Bytes()
}

func encodeZstd(t *testing.T, plain []byte, window int) []byte {
	t.Helper()
	encoder, err := zstd.NewWriter(
		nil,
		zstd.WithEncoderConcurrency(1),
		zstd.WithSingleSegment(false),
		zstd.WithWindowSize(window),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	return encoder.EncodeAll(plain, nil)
}

func writeAndClose(t *testing.T, writer io.WriteCloser, plain []byte) {
	t.Helper()
	if _, err := writer.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}

func truncate(encoded []byte) []byte {
	return append([]byte(nil), encoded[:len(encoded)-1]...)
}
