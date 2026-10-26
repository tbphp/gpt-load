package utils

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"github.com/sirupsen/logrus"
)

// ErrDecodedBodyTooLarge is returned when decoded data exceeds the requested limit.
var ErrDecodedBodyTooLarge = errors.New("decoded body exceeds size limit")

// Decompressor decompresses a body. A limit <= 0 means no limit, otherwise
// ErrDecodedBodyTooLarge is returned once more than limit bytes are decoded.
type Decompressor interface {
	Decompress(data []byte, limit int64) ([]byte, error)
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
	decompressorRegistry[strings.ToLower(encoding)] = decompressor
	logrus.Debugf("Registered decompressor for encoding: %s", encoding)
}

// DecompressResponse decompresses response data based on the Content-Encoding
// header, with no size limit; see DecompressResponseLimited.
func DecompressResponse(contentEncoding string, data []byte) ([]byte, error) {
	return DecompressResponseLimited(contentEncoding, data, 0)
}

// DecompressResponseLimited decompresses response data based on the
// Content-Encoding header, decoding at most limit bytes.
//
// Codings are listed in the order the sender applied them, so they are undone in
// reverse. An unknown coding is an error rather than a silent passthrough, so
// callers never treat still compressed bytes as text.
func DecompressResponseLimited(contentEncoding string, data []byte, limit int64) ([]byte, error) {
	// Nothing to decode
	if len(data) == 0 {
		return data, nil
	}

	codings := parseContentEncodings(contentEncoding)
	for i := len(codings) - 1; i >= 0; i-- {
		coding := codings[i]

		// "identity" means the data was not encoded
		if coding == "identity" {
			continue
		}

		decompressor, exists := decompressorRegistry[coding]
		if !exists {
			return nil, fmt.Errorf("no decompressor registered for encoding %q", coding)
		}

		decompressed, err := decompressor.Decompress(data, limit)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress %q encoded data (%d bytes): %w", coding, len(data), err)
		}

		logrus.Debugf("Successfully decompressed %d bytes -> %d bytes using '%s'",
			len(data), len(decompressed), coding)
		data = decompressed
	}

	return data, nil
}

// readAllLimited reads a decoded stream, refusing more than limit bytes.
func readAllLimited(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return io.ReadAll(r)
	}

	decoded, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(decoded)) > limit {
		return nil, ErrDecodedBodyTooLarge
	}
	return decoded, nil
}

// parseContentEncodings splits a Content-Encoding value into lower case codings,
// keeping their order.
func parseContentEncodings(contentEncoding string) []string {
	if strings.TrimSpace(contentEncoding) == "" {
		return nil
	}

	parts := strings.Split(contentEncoding, ",")
	codings := make([]string, 0, len(parts))
	for _, part := range parts {
		coding := strings.ToLower(strings.TrimSpace(part))
		if coding == "" {
			continue
		}
		codings = append(codings, coding)
	}
	return codings
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

	decompressed, err := readAllLimited(reader, limit)
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

	decompressed, err := readAllLimited(reader, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to read brotli data: %w", err)
	}

	return decompressed, nil
}

// DeflateDecompressor handles deflate compression
type DeflateDecompressor struct{}

// Decompress implements Decompressor interface for deflate.
//
// RFC 7230 defines "deflate" as zlib wrapped DEFLATE, but many servers send raw
// DEFLATE, so the zlib header decides which form to expect. A body with a valid
// header must decode completely; damage is reported instead of guessed at, so a
// corrupt body can never turn into garbage text.
func (d *DeflateDecompressor) Decompress(data []byte, limit int64) ([]byte, error) {
	if reader, err := zlib.NewReader(bytes.NewReader(data)); err == nil {
		decompressed, readErr := readAllLimited(reader, limit)
		closeErr := reader.Close()
		if readErr == nil && closeErr == nil {
			return decompressed, nil
		}
		return nil, fmt.Errorf("failed to read deflate data: %w", errors.Join(readErr, closeErr))
	}

	reader := flate.NewReader(bytes.NewReader(data))
	defer reader.Close()

	decompressed, err := readAllLimited(reader, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to read deflate data: %w", err)
	}

	return decompressed, nil
}

// ZstdDecompressor handles Zstandard compression
type ZstdDecompressor struct{}

// Decompress implements Decompressor interface for zstd
func (z *ZstdDecompressor) Decompress(data []byte, limit int64) ([]byte, error) {
	var options []zstd.DOption
	if limit > 0 {
		// A frame may declare a window far larger than its payload, and the decoder
		// reserves it before reading any output. 8 MiB is the window that streaming
		// encoders declare, so the cap must not be tied to the decoded size limit.
		options = append(options, zstd.WithDecoderMaxWindow(8<<20))
	}

	reader, err := zstd.NewReader(bytes.NewReader(data), options...)
	if err != nil {
		return nil, fmt.Errorf("failed to create zstd reader: %w", err)
	}
	defer reader.Close()

	decompressed, err := readAllLimited(reader, limit)
	if err != nil {
		// An oversized frame is rejected by the decoder itself.
		if errors.Is(err, zstd.ErrWindowSizeExceeded) {
			return nil, ErrDecodedBodyTooLarge
		}
		return nil, fmt.Errorf("failed to read zstd data: %w", err)
	}

	return decompressed, nil
}
