package contentcoding

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

type Encoding string

const (
	Identity Encoding = "identity"
	Gzip     Encoding = "gzip"
	Brotli   Encoding = "br"
	Deflate  Encoding = "deflate"
	Zstd     Encoding = "zstd"
)

const (
	SupportedRequestEncodings = "identity, gzip, br, deflate, zstd"
	ZstdHTTPMaxWindowBytes    = 8 << 20
)

var (
	ErrUnsupportedContentEncoding = errors.New("unsupported content encoding")
	ErrInvalidContentEncoding     = errors.New("invalid content encoding")
	ErrDecodedBodyTooLarge        = errors.New("decoded body exceeds size limit")
)

func ParseContentEncoding(values []string) (Encoding, error) {
	if len(values) == 0 {
		return Identity, nil
	}
	if len(values) != 1 {
		return "", ErrUnsupportedContentEncoding
	}

	value := strings.ToLower(strings.TrimSpace(values[0]))
	if value == "" || value == string(Identity) {
		return Identity, nil
	}
	if strings.Contains(value, ",") {
		return "", ErrUnsupportedContentEncoding
	}

	switch Encoding(value) {
	case Gzip, Brotli, Deflate, Zstd:
		return Encoding(value), nil
	default:
		return "", ErrUnsupportedContentEncoding
	}
}

func DecodeLimited(encoding Encoding, encoded []byte, limit int64) ([]byte, error) {
	if limit < 0 {
		return nil, fmt.Errorf("decoded body limit must not be negative")
	}

	switch encoding {
	case Identity:
		return readAtMost(bytes.NewReader(encoded), limit)
	case Gzip:
		if len(encoded) == 0 {
			return nil, ErrInvalidContentEncoding
		}
		return decodeGzip(encoded, limit)
	case Brotli:
		if len(encoded) == 0 {
			return nil, ErrInvalidContentEncoding
		}
		return readDecoded(brotli.NewReader(bytes.NewReader(encoded)), limit)
	case Deflate:
		if len(encoded) == 0 {
			return nil, ErrInvalidContentEncoding
		}
		return decodeDeflate(encoded, limit)
	case Zstd:
		if len(encoded) == 0 {
			return nil, ErrInvalidContentEncoding
		}
		return decodeZstd(encoded, limit)
	default:
		return nil, ErrUnsupportedContentEncoding
	}
}

func decodeGzip(encoded []byte, limit int64) ([]byte, error) {
	source := bytes.NewReader(encoded)
	reader, err := gzip.NewReader(source)
	if err != nil {
		return nil, invalidContentEncoding(err)
	}
	reader.Multistream(true)

	body, readErr := readDecoded(reader, limit)
	closeErr := reader.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, invalidContentEncoding(closeErr)
	}
	return body, nil
}

func decodeDeflate(encoded []byte, limit int64) ([]byte, error) {
	source := bytes.NewReader(encoded)
	reader, err := zlib.NewReader(source)
	if err != nil {
		return nil, invalidContentEncoding(err)
	}
	body, readErr := readDecoded(reader, limit)
	closeErr := reader.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, invalidContentEncoding(closeErr)
	}
	if source.Len() != 0 {
		return nil, ErrInvalidContentEncoding
	}
	return body, nil
}

func decodeZstd(encoded []byte, limit int64) ([]byte, error) {
	memoryLimit := uint64(max(limit, int64(ZstdHTTPMaxWindowBytes)))
	reader, err := zstd.NewReader(
		bytes.NewReader(encoded),
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderLowmem(true),
		zstd.WithDecoderMaxMemory(memoryLimit),
		zstd.WithDecoderMaxWindow(ZstdHTTPMaxWindowBytes),
	)
	if err != nil {
		return nil, invalidContentEncoding(err)
	}
	defer reader.Close()
	return readDecoded(reader, limit)
}

func readDecoded(reader io.Reader, limit int64) ([]byte, error) {
	body, err := readAtMost(reader, limit)
	if err == nil || errors.Is(err, ErrDecodedBodyTooLarge) {
		return body, err
	}
	return nil, invalidContentEncoding(err)
}

func readAtMost(reader io.Reader, limit int64) ([]byte, error) {
	if limit < 0 {
		return nil, fmt.Errorf("decoded body limit must not be negative")
	}
	limited := io.Reader(reader)
	if limit < math.MaxInt64 {
		limited = io.LimitReader(reader, limit+1)
	}
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, ErrDecodedBodyTooLarge
	}
	return body, nil
}

func invalidContentEncoding(err error) error {
	return fmt.Errorf("%w: %v", ErrInvalidContentEncoding, err)
}
