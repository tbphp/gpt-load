package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sync"
	"time"
)

const (
	streamReadBufferSize   = 32 * 1024
	maxFirstSSEEventBytes  = 1 << 20
	downstreamWriteTimeout = 30 * time.Second
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

type streamWriteController struct {
	writer             http.ResponseWriter
	deadlineController *http.ResponseController
	flushController    *http.ResponseController
	timeout            time.Duration
}

func newStreamWriteController(writer http.ResponseWriter, timeout time.Duration) *streamWriteController {
	return &streamWriteController{
		writer:             writer,
		deadlineController: newStreamDeadlineController(writer),
		flushController:    newStreamFlushController(writer),
		timeout:            timeout,
	}
}

func newStreamDeadlineController(writer http.ResponseWriter) *http.ResponseController {
	target, _ := findStreamResponseWriter(writer, func(current http.ResponseWriter) bool {
		_, ok := current.(interface{ SetWriteDeadline(time.Time) error })
		return ok
	})
	if target == nil {
		return nil
	}
	return http.NewResponseController(target)
}

func newStreamFlushController(writer http.ResponseWriter) *http.ResponseController {
	target, safeFallback := findStreamResponseWriter(writer, func(current http.ResponseWriter) bool {
		_, ok := current.(interface{ FlushError() error })
		return ok
	})
	if target != nil {
		return http.NewResponseController(target)
	}
	if safeFallback {
		return http.NewResponseController(writer)
	}
	return nil
}

func findStreamResponseWriter(
	writer http.ResponseWriter,
	supports func(http.ResponseWriter) bool,
) (http.ResponseWriter, bool) {
	const maxUnwrapDepth = 64

	current := writer
	seen := make(map[http.ResponseWriter]struct{})
	for depth := 0; depth < maxUnwrapDepth; depth++ {
		if current == nil {
			return nil, true
		}
		if isTypedNilResponseWriter(current) {
			return nil, false
		}
		currentType := reflect.TypeOf(current)
		if currentType != nil && currentType.Comparable() {
			if _, exists := seen[current]; exists {
				return nil, false
			}
			seen[current] = struct{}{}
		}
		if supports(current) {
			return current, true
		}
		unwrapper, ok := current.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return nil, true
		}
		current = unwrapper.Unwrap()
	}
	return nil, false
}

func isTypedNilResponseWriter(writer http.ResponseWriter) bool {
	value := reflect.ValueOf(writer)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (writer *streamWriteController) arm() error {
	if writer == nil || writer.writer == nil || writer.timeout <= 0 {
		return fmt.Errorf("downstream stream writer is invalid")
	}
	if writer.deadlineController == nil {
		return nil
	}
	err := writer.deadlineController.SetWriteDeadline(time.Now().Add(writer.timeout))
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

func (writer *streamWriteController) clear() error {
	if writer == nil {
		return fmt.Errorf("downstream stream writer is invalid")
	}
	if writer.deadlineController == nil {
		return nil
	}
	err := writer.deadlineController.SetWriteDeadline(time.Time{})
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

func (writer *streamWriteController) writeHeader(status int) error {
	if err := writer.arm(); err != nil {
		return err
	}
	writer.writer.WriteHeader(status)
	return nil
}

func (writer *streamWriteController) write(body []byte) (int, error) {
	if err := writer.arm(); err != nil {
		return 0, err
	}
	return writer.writer.Write(body)
}

func (writer *streamWriteController) flush() error {
	if err := writer.arm(); err != nil {
		return err
	}
	if writer.flushController == nil {
		return fmt.Errorf("%w", http.ErrNotSupported)
	}
	return writer.flushController.Flush()
}

func commitStream(writer *streamWriteController, status int, headers http.Header, prefix []byte) error {
	if writer == nil || writer.writer == nil {
		return fmt.Errorf("downstream response writer is required")
	}
	for name, values := range cloneEndToEndHeaders(headers) {
		for _, value := range values {
			writer.writer.Header().Add(name, value)
		}
	}
	if err := writer.writeHeader(status); err != nil {
		return fmt.Errorf("write downstream stream headers: %w", err)
	}
	if len(prefix) > 0 {
		written, err := writer.write(prefix)
		if err != nil {
			return fmt.Errorf("write first SSE event: %w", err)
		}
		if written != len(prefix) {
			return fmt.Errorf("write first SSE event: %w", io.ErrShortWrite)
		}
	}
	if err := writer.flush(); err != nil {
		return fmt.Errorf("flush first SSE event: %w", err)
	}
	return nil
}

func pumpStream(ctx context.Context, body io.ReadCloser, writer *streamWriteController, idleTimeout time.Duration) error {
	if body == nil || writer == nil || writer.writer == nil {
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
			written, writeErr := writer.write(chunk[:read])
			if writeErr != nil {
				return fmt.Errorf("write upstream stream: %w", writeErr)
			}
			if written != read {
				return fmt.Errorf("write upstream stream: %w", io.ErrShortWrite)
			}
			if flushErr := writer.flush(); flushErr != nil {
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
