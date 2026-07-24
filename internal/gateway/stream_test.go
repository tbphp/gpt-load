package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBufferFirstSSEEventAcceptsEventAtHardLimit(t *testing.T) {
	const framingBytes = len("data: \n\n")
	input := "data: " + strings.Repeat("x", maxFirstSSEEventBytes-framingBytes) + "\n\n"

	prefix, err := bufferFirstSSEEvent(strings.NewReader(input))
	if err != nil {
		t.Fatalf("bufferFirstSSEEvent() error = %v", err)
	}
	if len(prefix) != maxFirstSSEEventBytes || string(prefix) != input {
		t.Fatalf("prefix length/content = %d/%t, want %d/true", len(prefix), string(prefix) == input, maxFirstSSEEventBytes)
	}
}

func TestBufferFirstSSEEventRejectsIncompletePrefixAtHardLimit(t *testing.T) {
	input := ":" + strings.Repeat("x", maxFirstSSEEventBytes-1)

	prefix, err := bufferFirstSSEEvent(strings.NewReader(input))
	if !errors.Is(err, errFirstSSEEventTooLarge) {
		t.Fatalf("bufferFirstSSEEvent() error = %v, want first event too large", err)
	}
	if prefix != nil {
		t.Fatalf("prefix length = %d, want nil on overflow", len(prefix))
	}
}

func TestBufferFirstSSEEventRejectsIncompleteStream(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "data line without event boundary", input: "data: x\n"},
		{name: "comment event", input: ": keepalive\n\n"},
		{name: "empty data event", input: "data:\n\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefix, err := bufferFirstSSEEvent(bytes.NewBufferString(tt.input))
			if !errors.Is(err, errIncompleteSSEEvent) {
				t.Fatalf("bufferFirstSSEEvent() error = %v, want incomplete event", err)
			}
			if string(prefix) != tt.input {
				t.Fatalf("prefix = %q, want %q", prefix, tt.input)
			}
		})
	}
}

func TestBufferFirstSSEEventPreservesWireBytesAndReadAhead(t *testing.T) {
	const input = ": keepalive\n\ndata: first\n\ndata: second\n\n"
	reader := &eofWithDataReader{data: []byte(input)}

	prefix, err := bufferFirstSSEEvent(reader)
	if err != nil {
		t.Fatalf("bufferFirstSSEEvent() error = %v", err)
	}
	if string(prefix) != input {
		t.Fatalf("prefix = %q, want every byte already read %q", prefix, input)
	}
}

func TestCommitStreamWritesHeadersPrefixAndFlushes(t *testing.T) {
	writer := newRecordingResponseWriter()
	headers := http.Header{
		"Content-Type":   {"text/event-stream"},
		"X-Upstream":     {"kept"},
		"Connection":     {"X-Upstream-Hop"},
		"X-Upstream-Hop": {"drop"},
	}
	prefix := []byte("data: first\n\n")
	controller := newStreamWriteController(writer, time.Second)

	if err := commitStream(controller, http.StatusOK, headers, prefix); err != nil {
		t.Fatalf("commitStream() error = %v", err)
	}
	if writer.status != http.StatusOK || !bytes.Equal(writer.body.Bytes(), prefix) || writer.flushes != 1 {
		t.Fatalf("writer status/body/flushes = %d/%q/%d", writer.status, writer.body.Bytes(), writer.flushes)
	}
	if writer.header.Get("Content-Type") != "text/event-stream" || writer.header.Get("X-Upstream") != "kept" {
		t.Fatalf("end-to-end headers = %#v", writer.header)
	}
	if writer.header.Get("Connection") != "" || writer.header.Get("X-Upstream-Hop") != "" {
		t.Fatalf("hop-by-hop headers reached downstream: %#v", writer.header)
	}
}

func TestStreamWriteControllerArmsEveryOperationAndClears(t *testing.T) {
	writer := newRecordingResponseWriter()
	controller := newStreamWriteController(writer, time.Minute)
	if err := controller.writeHeader(http.StatusOK); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.write([]byte("data: one\n\n")); err != nil {
		t.Fatal(err)
	}
	if err := controller.flush(); err != nil {
		t.Fatal(err)
	}
	if err := controller.clear(); err != nil {
		t.Fatal(err)
	}
	if len(writer.deadlines) != 4 {
		t.Fatalf("deadlines = %#v, want three arm calls and one clear", writer.deadlines)
	}
	for index, deadline := range writer.deadlines[:3] {
		if deadline.IsZero() {
			t.Fatalf("deadline %d is zero", index)
		}
	}
	if !writer.deadlines[3].IsZero() {
		t.Fatalf("clear deadline = %v, want zero", writer.deadlines[3])
	}
}

func TestStreamWriteControllerReturnsUnwrappedFlushError(t *testing.T) {
	wantErr := errors.New("downstream flush failed")
	inner := &flushErrorResponseWriter{
		recordingResponseWriter: newRecordingResponseWriter(),
		flushErr:                wantErr,
	}
	outer := &swallowingFlushResponseWriter{writer: inner}
	controller := newStreamWriteController(outer, time.Minute)

	if err := controller.writeHeader(http.StatusAccepted); err != nil {
		t.Fatal(err)
	}
	const body = "data: ready\n\n"
	if _, err := controller.write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := controller.flush(); !errors.Is(err, wantErr) {
		t.Fatalf("flush() error = %v, want %v", err, wantErr)
	}
	if inner.status != http.StatusAccepted || inner.body.String() != body {
		t.Fatalf("status/body = %d/%q, want %d/%q", inner.status, inner.body.String(), http.StatusAccepted, body)
	}
}

func TestStreamWriteControllerHandlesUnsafeUnwrapChains(t *testing.T) {
	type writerFactory func() (http.ResponseWriter, func())
	scenarios := []struct {
		name string
		new  writerFactory
	}{
		{
			name: "self cycle",
			new: func() (http.ResponseWriter, func()) {
				writer := newMutableUnwrapResponseWriter()
				writer.setNext(writer)
				return writer, func() { writer.setNext(newRecordingResponseWriter()) }
			},
		},
		{
			name: "two node cycle",
			new: func() (http.ResponseWriter, func()) {
				first := newMutableUnwrapResponseWriter()
				second := newMutableUnwrapResponseWriter()
				first.setNext(second)
				second.setNext(first)
				return first, func() { first.setNext(newRecordingResponseWriter()) }
			},
		},
		{
			name: "typed nil",
			new: func() (http.ResponseWriter, func()) {
				var writer *typedNilResponseWriter
				return writer, func() {}
			},
		},
	}
	operations := []struct {
		name    string
		run     func(http.ResponseWriter) error
		wantErr error
	}{
		{
			name: "construct",
			run: func(writer http.ResponseWriter) error {
				if newStreamWriteController(writer, time.Second) == nil {
					return errors.New("controller is nil")
				}
				return nil
			},
		},
		{
			name: "arm",
			run: func(writer http.ResponseWriter) error {
				return newStreamWriteController(writer, time.Second).arm()
			},
		},
		{
			name: "clear",
			run: func(writer http.ResponseWriter) error {
				return newStreamWriteController(writer, time.Second).clear()
			},
		},
		{
			name: "flush",
			run: func(writer http.ResponseWriter) error {
				return newStreamWriteController(writer, time.Second).flush()
			},
			wantErr: http.ErrNotSupported,
		},
	}

	for _, scenario := range scenarios {
		for _, operation := range operations {
			t.Run(scenario.name+"/"+operation.name, func(t *testing.T) {
				writer, release := scenario.new()
				var releaseOnce sync.Once
				releaseCycle := func() { releaseOnce.Do(release) }
				defer releaseCycle()

				type operationResult struct {
					err        error
					panicValue any
				}
				done := make(chan operationResult, 1)
				go func() {
					result := operationResult{}
					defer func() {
						result.panicValue = recover()
						done <- result
					}()
					result.err = operation.run(writer)
				}()

				select {
				case result := <-done:
					assertControllerOperationResult(t, result.err, result.panicValue, operation.wantErr)
				case <-time.After(100 * time.Millisecond):
					releaseCycle()
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Fatal("controller operation goroutine did not stop after breaking unwrap cycle")
					}
					t.Fatalf("controller operation %s hung on %s", operation.name, scenario.name)
				}
			})
		}
	}
}

func assertControllerOperationResult(t *testing.T, err error, panicValue any, wantErr error) {
	t.Helper()
	if panicValue != nil {
		t.Fatalf("controller operation panicked: %v", panicValue)
	}
	if wantErr == nil && err != nil {
		t.Fatalf("controller operation error = %v, want nil", err)
	}
	if wantErr != nil && !errors.Is(err, wantErr) {
		t.Fatalf("controller operation error = %v, want %v", err, wantErr)
	}
}

func TestPumpStreamForwardsChunksAndFlushes(t *testing.T) {
	body := &chunkReadCloser{chunks: [][]byte{[]byte("data: one\n\n"), []byte("data: two\n\n")}}
	writer := newRecordingResponseWriter()
	controller := newStreamWriteController(writer, time.Second)

	if err := pumpStream(context.Background(), body, controller, time.Second); err != nil {
		t.Fatalf("pumpStream() error = %v", err)
	}
	if got, want := writer.body.String(), "data: one\n\ndata: two\n\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if writer.flushes != 2 {
		t.Fatalf("flushes = %d, want 2", writer.flushes)
	}
}

func TestPumpStreamReturnsDownstreamWriteFailure(t *testing.T) {
	wantErr := errors.New("downstream write failed")
	body := &chunkReadCloser{chunks: [][]byte{[]byte("data: one\n\n")}}
	writer := newRecordingResponseWriter()
	writer.writeErr = wantErr
	controller := newStreamWriteController(writer, time.Second)

	err := pumpStream(context.Background(), body, controller, time.Second)
	if !errors.Is(err, wantErr) {
		t.Fatalf("pumpStream() error = %v, want %v", err, wantErr)
	}
}

func TestPumpStreamReturnsTypedTerminationWithoutChangingWrappedError(t *testing.T) {
	assertFailure := func(
		t *testing.T,
		err error,
		wantKind streamFailureKind,
		wantWrapped error,
	) {
		t.Helper()
		var failure *streamFailure
		if !errors.As(err, &failure) {
			t.Fatalf("pumpStream() error type = %T, want *streamFailure: %v", err, err)
		}
		if failure.kind != wantKind {
			t.Fatalf("stream failure kind = %d, want %d", failure.kind, wantKind)
		}
		if !errors.Is(err, wantWrapped) {
			t.Fatalf("pumpStream() error = %v, want wrapped %v", err, wantWrapped)
		}
	}

	t.Run("upstream read", func(t *testing.T) {
		wantErr := errors.New("upstream read failed")
		body := &failingStreamReadCloser{err: wantErr}
		err := pumpStream(
			context.Background(),
			body,
			newStreamWriteController(newRecordingResponseWriter(), time.Second),
			time.Second,
		)
		assertFailure(t, err, streamFailureUpstreamRead, wantErr)
	})

	t.Run("idle timeout", func(t *testing.T) {
		body := newBlockingReadCloser()
		done := make(chan error, 1)
		go func() {
			done <- pumpStream(
				context.Background(),
				body,
				newStreamWriteController(newRecordingResponseWriter(), time.Second),
				20*time.Millisecond,
			)
		}()
		waitForSignal(t, body.started, "upstream read start")
		select {
		case err := <-done:
			assertFailure(t, err, streamFailureIdle, errStreamIdleTimeout)
		case <-time.After(time.Second):
			t.Fatal("pumpStream() did not stop after idle timeout")
		}
	})

	t.Run("downstream write", func(t *testing.T) {
		wantErr := errors.New("downstream write failed")
		body := &chunkReadCloser{chunks: [][]byte{[]byte("data: one\n\n")}}
		writer := newRecordingResponseWriter()
		writer.writeErr = wantErr
		err := pumpStream(
			context.Background(),
			body,
			newStreamWriteController(writer, time.Second),
			time.Second,
		)
		assertFailure(t, err, streamFailureDownstreamWrite, wantErr)
	})

	t.Run("downstream flush", func(t *testing.T) {
		wantErr := errors.New("downstream flush failed")
		body := &chunkReadCloser{chunks: [][]byte{[]byte("data: one\n\n")}}
		writer := &flushErrorResponseWriter{
			recordingResponseWriter: newRecordingResponseWriter(),
			flushErr:                wantErr,
		}
		err := pumpStream(
			context.Background(),
			body,
			newStreamWriteController(writer, time.Second),
			time.Second,
		)
		assertFailure(t, err, streamFailureDownstreamWrite, wantErr)
	})

	t.Run("client cancellation", func(t *testing.T) {
		body := newBlockingReadCloser()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- pumpStream(
				ctx,
				body,
				newStreamWriteController(newRecordingResponseWriter(), time.Second),
				time.Second,
			)
		}()
		waitForSignal(t, body.started, "upstream read start")
		cancel()
		select {
		case err := <-done:
			assertFailure(t, err, streamFailureClientCanceled, context.Canceled)
		case <-time.After(time.Second):
			t.Fatal("pumpStream() did not stop after cancellation")
		}
	})
}

func TestPumpStreamClosesUpstreamOnCancellation(t *testing.T) {
	body := newBlockingReadCloser()
	writer := newRecordingResponseWriter()
	controller := newStreamWriteController(writer, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- pumpStream(ctx, body, controller, time.Second) }()

	waitForSignal(t, body.started, "upstream read start")
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("pumpStream() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pumpStream() did not stop after cancellation")
	}
	waitForSignal(t, body.closed, "upstream close")
}

func TestPumpStreamClosesUpstreamOnIdleTimeout(t *testing.T) {
	body := newBlockingReadCloser()
	writer := newRecordingResponseWriter()
	controller := newStreamWriteController(writer, time.Second)
	done := make(chan error, 1)
	go func() { done <- pumpStream(context.Background(), body, controller, 20*time.Millisecond) }()

	waitForSignal(t, body.started, "upstream read start")
	select {
	case err := <-done:
		if !errors.Is(err, errStreamIdleTimeout) {
			t.Fatalf("pumpStream() error = %v, want idle timeout", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pumpStream() did not stop after idle timeout")
	}
	waitForSignal(t, body.closed, "upstream close")
}

func TestPumpStreamResetsIdleTimeoutAfterData(t *testing.T) {
	body := newControlledReadCloser()
	writer := newRecordingResponseWriter()
	controller := newStreamWriteController(writer, time.Second)
	done := make(chan error, 1)
	const idle = 80 * time.Millisecond
	go func() { done <- pumpStream(context.Background(), body, controller, idle) }()

	waitForSignal(t, body.reads, "first upstream read")
	time.Sleep(50 * time.Millisecond)
	body.chunks <- []byte("data: keepalive\n\n")
	waitForSignal(t, writer.writes, "downstream write")
	waitForSignal(t, body.reads, "second upstream read")

	select {
	case err := <-done:
		t.Fatalf("pumpStream() stopped before reset idle deadline: %v", err)
	case <-time.After(45 * time.Millisecond):
	}
	close(body.chunks)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("pumpStream() after clean EOF error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pumpStream() did not finish after EOF")
	}
}

type eofWithDataReader struct {
	data []byte
	done bool
}

func (reader *eofWithDataReader) Read(destination []byte) (int, error) {
	if reader.done {
		return 0, io.EOF
	}
	reader.done = true
	return copy(destination, reader.data), io.EOF
}

type recordingResponseWriter struct {
	header    http.Header
	body      bytes.Buffer
	status    int
	flushes   int
	writeErr  error
	writes    chan struct{}
	deadlines []time.Time
}

func newRecordingResponseWriter() *recordingResponseWriter {
	return &recordingResponseWriter{header: make(http.Header), writes: make(chan struct{}, 8)}
}

func (writer *recordingResponseWriter) Header() http.Header { return writer.header }

func (writer *recordingResponseWriter) WriteHeader(status int) { writer.status = status }

func (writer *recordingResponseWriter) Write(body []byte) (int, error) {
	if writer.writeErr != nil {
		return 0, writer.writeErr
	}
	written, err := writer.body.Write(body)
	select {
	case writer.writes <- struct{}{}:
	default:
	}
	return written, err
}

func (writer *recordingResponseWriter) Flush() { writer.flushes++ }

func (writer *recordingResponseWriter) SetWriteDeadline(value time.Time) error {
	writer.deadlines = append(writer.deadlines, value)
	return nil
}

type flushErrorResponseWriter struct {
	*recordingResponseWriter
	flushErr error
}

func (writer *flushErrorResponseWriter) FlushError() error {
	writer.flushes++
	return writer.flushErr
}

type swallowingFlushResponseWriter struct {
	writer http.ResponseWriter
}

func (writer *swallowingFlushResponseWriter) Header() http.Header {
	return writer.writer.Header()
}

func (writer *swallowingFlushResponseWriter) WriteHeader(status int) {
	writer.writer.WriteHeader(status)
}

func (writer *swallowingFlushResponseWriter) Write(body []byte) (int, error) {
	return writer.writer.Write(body)
}

func (writer *swallowingFlushResponseWriter) Flush() {
	if flusher, ok := writer.writer.(interface{ FlushError() error }); ok {
		_ = flusher.FlushError()
		return
	}
	writer.writer.(http.Flusher).Flush()
}

func (writer *swallowingFlushResponseWriter) Unwrap() http.ResponseWriter {
	return writer.writer
}

type mutableUnwrapResponseWriter struct {
	mu     sync.RWMutex
	header http.Header
	next   http.ResponseWriter
}

func newMutableUnwrapResponseWriter() *mutableUnwrapResponseWriter {
	return &mutableUnwrapResponseWriter{header: make(http.Header)}
}

func (writer *mutableUnwrapResponseWriter) Header() http.Header { return writer.header }

func (writer *mutableUnwrapResponseWriter) WriteHeader(int) {}

func (writer *mutableUnwrapResponseWriter) Write(body []byte) (int, error) {
	return len(body), nil
}

func (writer *mutableUnwrapResponseWriter) Unwrap() http.ResponseWriter {
	writer.mu.RLock()
	defer writer.mu.RUnlock()
	return writer.next
}

func (writer *mutableUnwrapResponseWriter) setNext(next http.ResponseWriter) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.next = next
}

type typedNilResponseWriter struct {
	header http.Header
}

func (writer *typedNilResponseWriter) Header() http.Header { return writer.header }

func (writer *typedNilResponseWriter) WriteHeader(int) {}

func (writer *typedNilResponseWriter) Write(body []byte) (int, error) {
	return len(body), nil
}

func (writer *typedNilResponseWriter) SetWriteDeadline(time.Time) error {
	if writer == nil {
		panic("typed-nil SetWriteDeadline called")
	}
	return nil
}

func (writer *typedNilResponseWriter) FlushError() error {
	if writer == nil {
		panic("typed-nil FlushError called")
	}
	return nil
}

type chunkReadCloser struct {
	chunks [][]byte
	closed bool
}

type failingStreamReadCloser struct {
	err error
}

func (reader *failingStreamReadCloser) Read([]byte) (int, error) {
	return 0, reader.err
}

func (*failingStreamReadCloser) Close() error { return nil }

func (reader *chunkReadCloser) Read(destination []byte) (int, error) {
	if len(reader.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := reader.chunks[0]
	reader.chunks = reader.chunks[1:]
	return copy(destination, chunk), nil
}

func (reader *chunkReadCloser) Close() error {
	reader.closed = true
	return nil
}

type blockingReadCloser struct {
	started chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func newBlockingReadCloser() *blockingReadCloser {
	return &blockingReadCloser{started: make(chan struct{}), closed: make(chan struct{})}
}

func (reader *blockingReadCloser) Read([]byte) (int, error) {
	reader.once.Do(func() { close(reader.started) })
	<-reader.closed
	return 0, errors.New("upstream body closed")
}

func (reader *blockingReadCloser) Close() error {
	select {
	case <-reader.closed:
	default:
		close(reader.closed)
	}
	return nil
}

type controlledReadCloser struct {
	chunks chan []byte
	reads  chan struct{}
	closed chan struct{}
	once   sync.Once
}

func newControlledReadCloser() *controlledReadCloser {
	return &controlledReadCloser{
		chunks: make(chan []byte), reads: make(chan struct{}, 8), closed: make(chan struct{}),
	}
}

func (reader *controlledReadCloser) Read(destination []byte) (int, error) {
	select {
	case reader.reads <- struct{}{}:
	default:
	}
	select {
	case chunk, ok := <-reader.chunks:
		if !ok {
			return 0, io.EOF
		}
		return copy(destination, chunk), nil
	case <-reader.closed:
		return 0, errors.New("upstream body closed")
	}
}

func (reader *controlledReadCloser) Close() error {
	reader.once.Do(func() { close(reader.closed) })
	return nil
}

func waitForSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}
