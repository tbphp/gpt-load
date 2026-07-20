package utils

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
	"github.com/sirupsen/logrus"
)

// Decompressor defines the interface for different decompression algorithms
type Decompressor interface {
	Decompress(data []byte, limit int64) ([]byte, error)
}

var ErrDecompressedResponseTooLarge = errors.New("decompressed response exceeds size limit")

// decompressorRegistry holds all registered decompressors
var decompressorRegistry = make(map[string]Decompressor)

// init registers default decompressors
func init() {
	RegisterDecompressor("gzip", &GzipDecompressor{})
	RegisterDecompressor("br", &BrotliDecompressor{})
	RegisterDecompressor("deflate", &DeflateDecompressor{})
	RegisterDecompressor("zstd", &ZstdDecompressor{})
}

// RegisterDecompressor allows registering new decompression algorithms
func RegisterDecompressor(encoding string, decompressor Decompressor) {
	decompressorRegistry[encoding] = decompressor
	logrus.Debugf("Registered decompressor for encoding: %s", encoding)
}

// DecompressResponse automatically decompresses response data based on Content-Encoding header
func DecompressResponse(contentEncoding string, data []byte) ([]byte, error) {
	return DecompressResponseLimited(contentEncoding, data, math.MaxInt64)
}

// DecompressResponseLimited decompresses response data up to limit bytes.
func DecompressResponseLimited(contentEncoding string, data []byte, limit int64) ([]byte, error) {
	encoding, err := normalizeContentEncoding(contentEncoding)
	if err != nil {
		return nil, err
	}
	if limit < 0 {
		return nil, fmt.Errorf("decompressed response limit must not be negative")
	}
	if encoding == "" || encoding == "identity" || len(data) == 0 {
		if int64(len(data)) > limit {
			return nil, ErrDecompressedResponseTooLarge
		}
		return bytes.Clone(data), nil
	}
	decompressor, exists := decompressorRegistry[encoding]
	if !exists {
		return nil, fmt.Errorf("unsupported content encoding %q", contentEncoding)
	}
	return decompressor.Decompress(data, limit)
}

func readDecompressedAtMost(reader io.Reader, limit int64) ([]byte, error) {
	var limited io.Reader = reader
	if limit < math.MaxInt64 {
		limited = io.LimitReader(reader, limit+1)
	}
	decoded, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(decoded)) > limit {
		return nil, ErrDecompressedResponseTooLarge
	}
	return decoded, nil
}

// CompressResponse compresses response data based on Content-Encoding header.
func CompressResponse(contentEncoding string, data []byte) ([]byte, error) {
	encoding, err := normalizeContentEncoding(contentEncoding)
	if err != nil {
		return nil, err
	}
	if encoding == "" || encoding == "identity" {
		return bytes.Clone(data), nil
	}

	var buffer bytes.Buffer
	var writer io.WriteCloser
	switch encoding {
	case "gzip":
		writer = gzip.NewWriter(&buffer)
	case "br":
		writer = brotli.NewWriter(&buffer)
	case "deflate":
		writer = zlib.NewWriter(&buffer)
	case "zstd":
		encoder, createErr := zstd.NewWriter(&buffer)
		if createErr != nil {
			return nil, fmt.Errorf("create zstd response writer: %w", createErr)
		}
		writer = encoder
	default:
		return nil, fmt.Errorf("unsupported content encoding %q", contentEncoding)
	}

	if _, err := writer.Write(data); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("compress %s response: %w", encoding, err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close %s response writer: %w", encoding, err)
	}
	return buffer.Bytes(), nil
}

func normalizeContentEncoding(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if strings.Contains(normalized, ",") {
		return "", fmt.Errorf("stacked content encoding %q is not supported", value)
	}
	return normalized, nil
}

// GzipDecompressor handles gzip compression
type GzipDecompressor struct{}

// Decompress implements Decompressor interface for gzip
func (g *GzipDecompressor) Decompress(data []byte, limit int64) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer reader.Close()

	decompressed, err := readDecompressedAtMost(reader, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to read gzip data: %w", err)
	}

	return decompressed, nil
}

// BrotliDecompressor handles brotli compression
type BrotliDecompressor struct{}

// Decompress implements Decompressor interface for brotli
func (b *BrotliDecompressor) Decompress(data []byte, limit int64) ([]byte, error) {
	reader := brotli.NewReader(bytes.NewReader(data))

	decompressed, err := readDecompressedAtMost(reader, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to read brotli data: %w", err)
	}

	return decompressed, nil
}

// DeflateDecompressor handles HTTP deflate's zlib-wrapped DEFLATE stream.
type DeflateDecompressor struct{}

// Decompress implements Decompressor interface for deflate
func (d *DeflateDecompressor) Decompress(data []byte, limit int64) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create deflate reader: %w", err)
	}
	defer reader.Close()

	decompressed, err := readDecompressedAtMost(reader, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to read deflate data: %w", err)
	}

	return decompressed, nil
}

// ZstdDecompressor handles Zstandard compression
type ZstdDecompressor struct{}

// Decompress implements Decompressor interface for zstd
func (z *ZstdDecompressor) Decompress(data []byte, limit int64) ([]byte, error) {
	options := make([]zstd.DOption, 0, 4)
	if limit < math.MaxInt64 {
		memoryLimit := uint64(limit)
		if memoryLimit == 0 {
			memoryLimit = 1
		}
		windowLimit := memoryLimit
		if windowLimit < zstd.MinWindowSize {
			windowLimit = zstd.MinWindowSize
		}
		options = append(
			options,
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderLowmem(true),
			zstd.WithDecoderMaxMemory(memoryLimit),
			zstd.WithDecoderMaxWindow(windowLimit),
		)
	}
	reader, err := zstd.NewReader(bytes.NewReader(data), options...)
	if err != nil {
		return nil, fmt.Errorf("failed to create zstd reader: %w", err)
	}
	defer reader.Close()

	decompressed, err := readDecompressedAtMost(reader, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to read zstd data: %w", err)
	}

	return decompressed, nil
}
