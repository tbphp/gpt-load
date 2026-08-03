package contentcoding

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

func TestParseContentEncodingAcceptsSupportedSingleValues(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   Encoding
	}{
		{name: "missing", want: EncodingIdentity},
		{name: "empty", values: []string{""}, want: EncodingIdentity},
		{name: "identity", values: []string{" Identity "}, want: EncodingIdentity},
		{name: "gzip", values: []string{"GZip"}, want: EncodingGzip},
		{name: "brotli", values: []string{"br"}, want: EncodingBrotli},
		{name: "deflate", values: []string{"deflate"}, want: EncodingDeflate},
		{name: "zstd", values: []string{"zstd"}, want: EncodingZstd},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseContentEncoding(test.values)
			if err != nil || got != test.want {
				t.Fatalf("ParseContentEncoding(%#v) = %q, %v; want %q", test.values, got, err, test.want)
			}
		})
	}
}

func TestParseContentEncodingRejectsUnsupportedOrStackedValues(t *testing.T) {
	for _, values := range [][]string{
		{"compress"},
		{"gzip, br"},
		{"gzip", "br"},
	} {
		if _, err := ParseContentEncoding(values); !errors.Is(err, ErrUnsupportedEncoding) {
			t.Errorf("ParseContentEncoding(%#v) error = %v, want ErrUnsupportedEncoding", values, err)
		}
	}
}

func TestReadDecodedBodySupportsAllSingleEncodings(t *testing.T) {
	plain := []byte(`{"model":"test-model","stream":false}`)
	for _, encoding := range []Encoding{
		EncodingIdentity,
		EncodingGzip,
		EncodingBrotli,
		EncodingDeflate,
		EncodingZstd,
	} {
		t.Run(string(encoding), func(t *testing.T) {
			wire := encodeContentCodingFixture(t, encoding, plain)
			values := []string(nil)
			if encoding != EncodingIdentity {
				values = []string{string(encoding)}
			}
			got, err := ReadDecodedBody(bytes.NewReader(wire), values, int64(len(wire)), int64(len(plain)))
			if err != nil || !bytes.Equal(got, plain) {
				t.Fatalf("ReadDecodedBody(%q) = %q, %v; want %q", encoding, got, err, plain)
			}
		})
	}
}

func TestReadDecodedBodyRejectsMalformedEncodedData(t *testing.T) {
	for _, encoding := range []Encoding{
		EncodingGzip,
		EncodingBrotli,
		EncodingDeflate,
		EncodingZstd,
	} {
		t.Run(string(encoding), func(t *testing.T) {
			if _, err := ReadDecodedBody(strings.NewReader("not-valid-encoded-data"), []string{string(encoding)}, 1<<20, 1<<20); !errors.Is(err, ErrInvalidEncoding) {
				t.Fatalf("ReadDecodedBody(%q) error = %v, want ErrInvalidEncoding", encoding, err)
			}
	})
	}
}

func TestReadDecodedBodyEnforcesEncodedAndDecodedLimits(t *testing.T) {
	if body, err := ReadDecodedBody(strings.NewReader("12345"), nil, 4, 16); !errors.Is(err, ErrEncodedTooLarge) || body != nil {
		t.Fatalf("encoded overflow = %q, %v", body, err)
	}

	plain := bytes.Repeat([]byte("x"), 1<<20+1)
	wire := encodeContentCodingFixture(t, EncodingGzip, plain)
	if body, err := ReadDecodedBody(bytes.NewReader(wire), []string{"gzip"}, int64(len(wire)), 1<<20); !errors.Is(err, ErrDecodedTooLarge) || body != nil {
		t.Fatalf("decoded overflow = %d bytes, %v", len(body), err)
	}
}

func TestAcceptsIdentityHonorsExplicitRejection(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   bool
	}{
		{name: "missing", want: true},
		{name: "empty", values: []string{""}, want: true},
		{name: "ordinary compression preference", values: []string{"gzip, br"}, want: true},
		{name: "identity positive", values: []string{"identity;q=0.5, *;q=0"}, want: true},
		{name: "identity rejected", values: []string{"gzip, identity;q=0"}},
		{name: "wildcard rejected", values: []string{"gzip, *;q=0"}},
		{name: "malformed q is compatibility safe", values: []string{"identity;q=broken"}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := AcceptsIdentity(test.values); got != test.want {
				t.Fatalf("AcceptsIdentity(%#v) = %t, want %t", test.values, got, test.want)
			}
		})
	}
}

func encodeContentCodingFixture(t *testing.T, encoding Encoding, plain []byte) []byte {
	t.Helper()
	if encoding == EncodingIdentity {
		return bytes.Clone(plain)
	}
	var buffer bytes.Buffer
	var writer io.WriteCloser
	switch encoding {
	case EncodingGzip:
		writer = gzip.NewWriter(&buffer)
	case EncodingBrotli:
		writer = brotli.NewWriter(&buffer)
	case EncodingDeflate:
		writer = zlib.NewWriter(&buffer)
	case EncodingZstd:
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
