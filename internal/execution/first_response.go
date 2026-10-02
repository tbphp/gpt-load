package execution

import (
	"bytes"
	"context"
	"io"
	"sync"
)

type firstResponseKey struct{}

type firstResponseCallback struct {
	mu       sync.Mutex
	notify   func()
	upstream bool
}

// FirstResponsePending 供独立观测器在已取得首响或请求结束后停止读取副本。
func FirstResponsePending(ctx context.Context) bool {
	callback, _ := ctx.Value(firstResponseKey{}).(*firstResponseCallback)
	if callback == nil {
		return false
	}
	callback.mu.Lock()
	defer callback.mu.Unlock()
	return callback.notify != nil
}

// WithFirstResponseObserver 在一次尝试内仅通知一次。停止后，SDK 后台读取不能再修改请求日志。
// 观察不参与提交、超时、重试或响应内容处理。
func WithFirstResponseObserver(ctx context.Context, notify func()) (context.Context, func()) {
	if notify == nil {
		return ctx, func() {}
	}
	callback := &firstResponseCallback{notify: notify}
	return context.WithValue(ctx, firstResponseKey{}, callback), func() {
		callback.mu.Lock()
		defer callback.mu.Unlock()
		callback.notify = nil
	}
}

// NewFirstResponseSSEObserver 只识别首条非空、非 DONE 的 data 行，不等待完整帧或正文。
func NewFirstResponseSSEObserver(ctx context.Context) func([]byte) {
	return newFirstResponseSSEObserver(ctx, true)
}

// NewFirstResponseSSEFallback 仅供没有上游读取器的执行器；不能把 SDK 合成事件误记为上游首响。
func NewFirstResponseSSEFallback(ctx context.Context) func([]byte) {
	return newFirstResponseSSEObserver(ctx, false)
}

func newFirstResponseSSEObserver(ctx context.Context, upstream bool) func([]byte) {
	callback, _ := ctx.Value(firstResponseKey{}).(*firstResponseCallback)
	if callback != nil && upstream {
		callback.mu.Lock()
		callback.upstream = true
		callback.mu.Unlock()
	}
	var line []byte
	done := callback == nil
	return func(chunk []byte) {
		for !done && len(chunk) > 0 {
			end := bytes.IndexAny(chunk, "\r\n")
			part := chunk
			if end >= 0 {
				part = chunk[:end]
			}
			if len(line)+len(part) > OpenAIImagesSSEEventLimitBytes {
				done, line = true, nil
				return
			}
			line = append(line, part...)
			if end < 0 {
				return
			}
			if bytes.HasPrefix(line, []byte("data:")) {
				payload := bytes.TrimSpace(line[5:])
				if bytes.HasPrefix(payload, []byte("[DONE]")) {
					done, line = true, nil
					return
				}
				if len(payload) > 0 {
					callback.mu.Lock()
					if callback.notify != nil && (upstream || !callback.upstream) {
						callback.notify()
						callback.notify = nil
					}
					callback.mu.Unlock()
					done, line = true, nil
					return
				}
			}
			line = line[:0]
			chunk = chunk[end+1:]
		}
	}
}

// ObserveFirstResponseReader 保持读取字节和错误不变，在协议转换前采样。
func ObserveFirstResponseReader(ctx context.Context, reader io.Reader) io.Reader {
	if _, ok := ctx.Value(firstResponseKey{}).(*firstResponseCallback); !ok {
		return reader
	}
	return &firstResponseReader{Reader: reader, observe: NewFirstResponseSSEObserver(ctx)}
}

type firstResponseReader struct {
	io.Reader
	observe func([]byte)
}

func (reader *firstResponseReader) Read(buffer []byte) (int, error) {
	n, err := reader.Reader.Read(buffer)
	reader.observe(buffer[:n])
	if err == io.EOF {
		reader.observe([]byte{'\n'})
	}
	return n, err
}
