package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	streamReadBufferSize  = 32 * 1024
	maxFirstSSEEventBytes = 1 << 20
)

var (
	errIncompleteSSEEvent    = errors.New("stream ended before the first SSE data event")
	errFirstSSEEventTooLarge = errors.New("first SSE data event exceeds size limit")
	errStreamIdleTimeout     = errors.New("upstream stream idle timeout")
)

func bufferFirstSSEEvent(reader io.Reader) ([]byte, error) {
	if reader == nil {
		return nil, fmt.Errorf("stream reader is required")
	}

	scanner := &sseEventScanner{}
	var buffered bytes.Buffer
	chunk := make([]byte, streamReadBufferSize)
	for {
		remaining := maxFirstSSEEventBytes - buffered.Len()
		if remaining == 0 {
			return nil, errFirstSSEEventTooLarge
		}
		read, err := reader.Read(chunk[:min(len(chunk), remaining)])
		if read > 0 {
			_, _ = buffered.Write(chunk[:read])
			if _, found := scanner.Feed(chunk[:read]); found {
				return buffered.Bytes(), nil
			}
			if buffered.Len() == maxFirstSSEEventBytes {
				return nil, errFirstSSEEventTooLarge
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return buffered.Bytes(), errIncompleteSSEEvent
			}
			return buffered.Bytes(), fmt.Errorf("read first SSE event: %w", err)
		}
	}
}

func commitStream(writer http.ResponseWriter, status int, headers http.Header, prefix []byte) error {
	if writer == nil {
		return fmt.Errorf("downstream response writer is required")
	}
	for name, values := range cloneEndToEndHeaders(headers) {
		for _, value := range values {
			writer.Header().Add(name, value)
		}
	}
	writer.WriteHeader(status)
	if len(prefix) > 0 {
		written, err := writer.Write(prefix)
		if err != nil {
			return fmt.Errorf("write first SSE event: %w", err)
		}
		if written != len(prefix) {
			return fmt.Errorf("write first SSE event: %w", io.ErrShortWrite)
		}
	}
	if err := flushStream(writer); err != nil {
		return fmt.Errorf("flush first SSE event: %w", err)
	}
	return nil
}

func pumpStream(ctx context.Context, body io.ReadCloser, writer http.ResponseWriter, idleTimeout time.Duration) error {
	if body == nil || writer == nil {
		return fmt.Errorf("stream body and downstream writer are required")
	}
	if idleTimeout <= 0 {
		return fmt.Errorf("stream idle timeout must be positive")
	}

	watchdog := newStreamWatchdog(body, idleTimeout)
	watchdog.reset()
	stopCancellation := context.AfterFunc(ctx, func() {
		watchdog.interrupt(ctx.Err())
	})
	defer func() {
		_ = stopCancellation()
		watchdog.stop()
	}()

	chunk := make([]byte, streamReadBufferSize)
	for {
		read, err := body.Read(chunk)
		if read > 0 {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if cause := watchdog.causeValue(); cause != nil {
				return cause
			}
			watchdog.reset()
			written, writeErr := writer.Write(chunk[:read])
			if writeErr != nil {
				return fmt.Errorf("write upstream stream: %w", writeErr)
			}
			if written != read {
				return fmt.Errorf("write upstream stream: %w", io.ErrShortWrite)
			}
			if flushErr := flushStream(writer); flushErr != nil {
				return fmt.Errorf("flush upstream stream: %w", flushErr)
			}
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			if cause := watchdog.causeValue(); cause != nil {
				return cause
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read upstream stream: %w", err)
		}
	}
}

func flushStream(writer http.ResponseWriter) error {
	return http.NewResponseController(writer).Flush()
}

type streamWatchdog struct {
	mu         sync.Mutex
	body       io.Closer
	timeout    time.Duration
	timer      *time.Timer
	generation uint64
	cause      error
}

func newStreamWatchdog(body io.Closer, timeout time.Duration) *streamWatchdog {
	return &streamWatchdog{body: body, timeout: timeout}
}

func (watchdog *streamWatchdog) reset() {
	watchdog.mu.Lock()
	defer watchdog.mu.Unlock()
	if watchdog.cause != nil {
		return
	}
	watchdog.generation++
	generation := watchdog.generation
	if watchdog.timer != nil {
		watchdog.timer.Stop()
	}
	watchdog.timer = time.AfterFunc(watchdog.timeout, func() {
		watchdog.expire(generation)
	})
}

func (watchdog *streamWatchdog) expire(generation uint64) {
	watchdog.mu.Lock()
	if watchdog.cause != nil || watchdog.generation != generation {
		watchdog.mu.Unlock()
		return
	}
	watchdog.cause = errStreamIdleTimeout
	body := watchdog.body
	watchdog.mu.Unlock()
	_ = body.Close()
}

func (watchdog *streamWatchdog) interrupt(cause error) {
	if cause == nil {
		cause = context.Canceled
	}
	watchdog.mu.Lock()
	if watchdog.cause != nil {
		watchdog.mu.Unlock()
		return
	}
	watchdog.generation++
	if watchdog.timer != nil {
		watchdog.timer.Stop()
	}
	watchdog.cause = cause
	body := watchdog.body
	watchdog.mu.Unlock()
	_ = body.Close()
}

func (watchdog *streamWatchdog) causeValue() error {
	watchdog.mu.Lock()
	defer watchdog.mu.Unlock()
	return watchdog.cause
}

func (watchdog *streamWatchdog) stop() {
	watchdog.mu.Lock()
	defer watchdog.mu.Unlock()
	watchdog.generation++
	if watchdog.timer != nil {
		watchdog.timer.Stop()
	}
}
