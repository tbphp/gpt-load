package cpa

import (
	"compress/gzip"
	"compress/zlib"
	"context"
	"io"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"

	"gpt-load/internal/execution"
)

// 普通流直接旁路观察；压缩流只解码观测副本，CPA 仍读取原始字节、响应头和错误。
// 副本在采到首响后停止，不持有整个回答，也不把观测错误变为转发错误。
func observeStreamBody(ctx context.Context, body io.ReadCloser, encoding string) io.ReadCloser {
	if !execution.FirstResponsePending(ctx) {
		return body
	}
	if encoding == "" || strings.EqualFold(strings.TrimSpace(encoding), "identity") {
		return &observedStreamBody{Reader: execution.ObserveFirstResponseReader(ctx, body), Closer: body}
	}
	reader, writer := io.Pipe()
	done := make(chan struct{})
	observe := execution.NewFirstResponseSSEObserver(ctx)
	go func() {
		defer close(done)
		defer reader.Close()
		stop := context.AfterFunc(ctx, func() { _ = reader.CloseWithError(ctx.Err()) })
		defer stop()
		var decoded io.Reader = reader
		encodings := strings.Split(encoding, ",")
		for i := len(encodings) - 1; i >= 0; i-- {
			switch strings.ToLower(strings.TrimSpace(encodings[i])) {
			case "", "identity":
			case "gzip":
				value, err := gzip.NewReader(decoded)
				if err != nil {
					return
				}
				defer value.Close()
				decoded = value
			case "deflate":
				value, err := zlib.NewReader(decoded)
				if err != nil {
					return
				}
				defer value.Close()
				decoded = value
			case "br":
				decoded = brotli.NewReader(decoded)
			case "zstd":
				value, err := zstd.NewReader(decoded, zstd.WithDecoderConcurrency(1))
				if err != nil {
					return
				}
				defer value.Close()
				decoded = value
			default:
				return
			}
		}
		buffer := make([]byte, 4096)
		limited := &io.LimitedReader{R: decoded, N: execution.OpenAIImagesSSEEventLimitBytes}
		for limited.N > 0 && execution.FirstResponsePending(ctx) {
			n, err := limited.Read(buffer)
			observe(buffer[:n])
			if err != nil {
				if err == io.EOF {
					observe([]byte{'\n'})
				}
				return
			}
		}
	}()
	return &compressedObservedBody{ReadCloser: body, copy: writer, done: done}
}

type observedStreamBody struct {
	io.Reader
	io.Closer
}

type compressedObservedBody struct {
	io.ReadCloser
	copy *io.PipeWriter
	done <-chan struct{}
}

func (body *compressedObservedBody) Read(buffer []byte) (int, error) {
	n, err := body.ReadCloser.Read(buffer)
	select {
	case <-body.done:
	default:
		// 关闭或解码失败只结束副本，不影响原始读取结果。
		_, _ = body.copy.Write(buffer[:n])
	}
	if err != nil {
		_ = body.copy.CloseWithError(err)
	}
	return n, err
}

func (body *compressedObservedBody) Close() error {
	_ = body.copy.Close()
	err := body.ReadCloser.Close()
	<-body.done
	return err
}
