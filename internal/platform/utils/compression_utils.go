package utils

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/sirupsen/logrus"
)

// Decompressor defines the interface for different decompression algorithms
type Decompressor interface {
	Decompress(data []byte) ([]byte, error)
}

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
	encoding, err := normalizeContentEncoding(contentEncoding)
	if err != nil {
		return nil, err
	}
	if encoding == "" || encoding == "identity" || len(data) == 0 {
		return bytes.Clone(data), nil
	}
	decompressor, exists := decompressorRegistry[encoding]
	if !exists {
		return nil, fmt.Errorf("unsupported content encoding %q", contentEncoding)
	}
	decompressed, err := decompressor.Decompress(data)
	if err != nil {
		return nil, fmt.Errorf("decompress %s response: %w", encoding, err)
	}
	return decompressed, nil
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
func (g *GzipDecompressor) Decompress(data []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer reader.Close()

	decompressed, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read gzip data: %w", err)
	}

	return decompressed, nil
}

// BrotliDecompressor handles brotli compression
type BrotliDecompressor struct{}

// Decompress implements Decompressor interface for brotli
func (b *BrotliDecompressor) Decompress(data []byte) ([]byte, error) {
	reader := brotli.NewReader(bytes.NewReader(data))

	decompressed, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read brotli data: %w", err)
	}

	return decompressed, nil
}

// DeflateDecompressor handles HTTP deflate's zlib-wrapped DEFLATE stream.
type DeflateDecompressor struct{}

// Decompress implements Decompressor interface for deflate
func (d *DeflateDecompressor) Decompress(data []byte) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create deflate reader: %w", err)
	}
	defer reader.Close()

	decompressed, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read deflate data: %w", err)
	}

	return decompressed, nil
}

// ZstdDecompressor handles Zstandard compression
type ZstdDecompressor struct{}

// Decompress implements Decompressor interface for zstd
func (z *ZstdDecompressor) Decompress(data []byte) ([]byte, error) {
	reader, err := zstd.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create zstd reader: %w", err)
	}
	defer reader.Close()

	decompressed, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read zstd data: %w", err)
	}

	return decompressed, nil
}
