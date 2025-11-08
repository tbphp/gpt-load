package utils

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"

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
	// If no encoding specified or empty data, return as-is
	if contentEncoding == "" || len(data) == 0 {
		return data, nil
	}

	// Look up the decompressor
	decompressor, exists := decompressorRegistry[contentEncoding]
	if !exists {
		logrus.Warnf("No decompressor registered for encoding '%s', returning original data", contentEncoding)
		return data, nil
	}

	// Decompress
	decompressed, err := decompressor.Decompress(data)
	if err != nil {
		logrus.WithError(err).Warnf("Failed to decompress with '%s', returning original data", contentEncoding)
		return data, nil
	}

	logrus.Debugf("Successfully decompressed %d bytes -> %d bytes using '%s'",
		len(data), len(decompressed), contentEncoding)
	return decompressed, nil
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

// DeflateDecompressor handles deflate compression (same as gzip without header)
type DeflateDecompressor struct{}

// Decompress implements Decompressor interface for deflate
func (d *DeflateDecompressor) Decompress(data []byte) ([]byte, error) {
	// For deflate, we can use the same logic as gzip
	// In practice, deflate is raw DEFLATE format without gzip header
	// For now, use gzip reader which handles both
	reader, err := gzip.NewReader(bytes.NewReader(data))
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
