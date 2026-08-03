package contentcoding

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// Encoding identifies one supported HTTP content coding.
type Encoding string

const (
	EncodingIdentity Encoding = "identity"
	EncodingGzip     Encoding = "gzip"
	EncodingBrotli   Encoding = "br"
	EncodingDeflate  Encoding = "deflate"
	EncodingZstd     Encoding = "zstd"
)

var (
	ErrUnsupportedEncoding = errors.New("unsupported content encoding")
	ErrInvalidEncoding     = errors.New("invalid content encoding")
	ErrEncodedTooLarge     = errors.New("encoded body exceeds size limit")
	ErrDecodedTooLarge     = errors.New("decoded body exceeds size limit")
)

// ParseContentEncoding accepts an absent/identity coding or one supported
// coding. Stacked encodings and repeated fields are intentionally unsupported.
func ParseContentEncoding(values []string) (Encoding, error) {
	if len(values) == 0 {
		return EncodingIdentity, nil
	}
	if len(values) != 1 {
		return "", fmt.Errorf("%w: multiple Content-Encoding fields", ErrUnsupportedEncoding)
	}
	value := strings.ToLower(strings.TrimSpace(values[0]))
	if strings.Contains(value, ",") {
		return "", fmt.Errorf("%w: stacked Content-Encoding %q", ErrUnsupportedEncoding, values[0])
	}
	switch value {
	case "", string(EncodingIdentity):
		return EncodingIdentity, nil
	case string(EncodingGzip):
		return EncodingGzip, nil
	case string(EncodingBrotli):
		return EncodingBrotli, nil
	case string(EncodingDeflate):
		return EncodingDeflate, nil
	case string(EncodingZstd):
		return EncodingZstd, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedEncoding, values[0])
	}
}

// ReadDecodedBody bounds both the received representation and its decoded
// form. It never returns a partial body when either limit is exceeded.
func ReadDecodedBody(
	reader io.Reader,
	contentEncodingValues []string,
	encodedLimit int64,
	decodedLimit int64,
) ([]byte, error) {
	if reader == nil {
		return nil, fmt.Errorf("%w: request body is nil", ErrInvalidEncoding)
	}
	encoding, err := ParseContentEncoding(contentEncodingValues)
	if err != nil {
		return nil, err
	}
	wire, err := readAtMost(reader, encodedLimit, ErrEncodedTooLarge)
	if err != nil {
		return nil, err
	}
	return DecodeBytesLimited(encoding, wire, decodedLimit)
}

// DecodeBytesLimited decodes one representation while bounding decoded output
// and decoder memory/window use where the codec supports it.
func DecodeBytesLimited(encoding Encoding, wire []byte, limit int64) ([]byte, error) {
	if limit < 0 {
		return nil, fmt.Errorf("decoded body limit must not be negative")
	}
	if encoding == EncodingIdentity {
		if int64(len(wire)) > limit {
			return nil, ErrDecodedTooLarge
		}
		return bytes.Clone(wire), nil
	}
	if len(wire) == 0 {
		return nil, fmt.Errorf("%w: empty %s body", ErrInvalidEncoding, encoding)
	}

	var reader io.Reader
	var closeReader func() error
	switch encoding {
	case EncodingGzip:
		decoded, err := gzip.NewReader(bytes.NewReader(wire))
		if err != nil {
			return nil, errors.Join(ErrInvalidEncoding, err)
		}
		reader = decoded
		closeReader = decoded.Close
	case EncodingBrotli:
		reader = brotli.NewReader(bytes.NewReader(wire))
	case EncodingDeflate:
		decoded, err := zlib.NewReader(bytes.NewReader(wire))
		if err != nil {
			return nil, errors.Join(ErrInvalidEncoding, err)
		}
		reader = decoded
		closeReader = decoded.Close
	case EncodingZstd:
		options := []zstd.DOption{
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderLowmem(true),
		}
		if limit < math.MaxInt64 {
			memoryLimit := uint64(limit)
			const minimumDecoderBudget = uint64(1 << 20)
			if memoryLimit < minimumDecoderBudget {
				memoryLimit = minimumDecoderBudget
			}
			options = append(
				options,
				zstd.WithDecoderMaxMemory(memoryLimit),
				zstd.WithDecoderMaxWindow(memoryLimit),
			)
		}
		decoded, err := zstd.NewReader(bytes.NewReader(wire), options...)
		if err != nil {
			return nil, errors.Join(ErrInvalidEncoding, err)
		}
		reader = decoded
		closeReader = func() error {
			decoded.Close()
			return nil
		}
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedEncoding, encoding)
	}
	if closeReader != nil {
		defer func() { _ = closeReader() }()
	}
	decoded, err := readAtMost(reader, limit, ErrDecodedTooLarge)
	if err != nil {
		if errors.Is(err, ErrDecodedTooLarge) {
			return nil, err
		}
		return nil, errors.Join(ErrInvalidEncoding, err)
	}
	return decoded, nil
}

func readAtMost(reader io.Reader, limit int64, overflow error) ([]byte, error) {
	if limit < 0 {
		return nil, fmt.Errorf("body limit must not be negative")
	}
	var limited io.Reader = reader
	if limit < math.MaxInt64 {
		limited = io.LimitReader(reader, limit+1)
	}
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, overflow
	}
	return body, nil
}

// AcceptsIdentity reports whether an identity response is acceptable. Invalid
// preference syntax is compatibility-safe: only a valid explicit rejection
// can reject a plaintext response.
func AcceptsIdentity(values []string) bool {
	if len(values) == 0 {
		return true
	}
	explicitSeen := false
	explicitQ := -1.0
	wildcardSeen := false
	wildcardQ := -1.0
	for _, field := range values {
		for _, rawItem := range strings.Split(field, ",") {
			item := strings.TrimSpace(rawItem)
			if item == "" {
				continue
			}
			parts := strings.Split(item, ";")
			name := strings.ToLower(strings.TrimSpace(parts[0]))
			q := 1.0
			valid := true
			for _, rawParam := range parts[1:] {
				param := strings.TrimSpace(rawParam)
				key, value, found := strings.Cut(param, "=")
				if !found || !strings.EqualFold(strings.TrimSpace(key), "q") {
					continue
				}
				parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
				if err != nil || parsed < 0 || parsed > 1 {
					valid = false
					break
				}
				q = parsed
			}
			if !valid {
				if name == string(EncodingIdentity) || name == "*" {
					return true
				}
				continue
			}
			switch name {
			case string(EncodingIdentity):
				explicitSeen = true
				if q > explicitQ {
					explicitQ = q
				}
			case "*":
				wildcardSeen = true
				if q > wildcardQ {
					wildcardQ = q
				}
			}
		}
	}
	if explicitSeen {
		return explicitQ > 0
	}
	return !wildcardSeen || wildcardQ > 0
}
