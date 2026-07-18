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

	if err := commitStream(writer, http.StatusOK, headers, prefix); err != nil {
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

func TestPumpStreamForwardsChunksAndFlushes(t *testing.T) {
	body := &chunkReadCloser{chunks: [][]byte{[]byte("data: one\n\n"), []byte("data: two\n\n")}}
	writer := newRecordingResponseWriter()

	if err := pumpStream(context.Background(), body, writer, time.Second); err != nil {
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

	err := pumpStream(context.Background(), body, writer, time.Second)
	if !errors.Is(err, wantErr) {
		t.Fatalf("pumpStream() error = %v, want %v", err, wantErr)
	}
}

func TestPumpStreamClosesUpstreamOnCancellation(t *testing.T) {
	body := newBlockingReadCloser()
	writer := newRecordingResponseWriter()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- pumpStream(ctx, body, writer, time.Second) }()

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
	done := make(chan error, 1)
	go func() { done <- pumpStream(context.Background(), body, writer, 20*time.Millisecond) }()

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
	done := make(chan error, 1)
	const idle = 80 * time.Millisecond
	go func() { done <- pumpStream(context.Background(), body, writer, idle) }()

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
	header   http.Header
	body     bytes.Buffer
	status   int
	flushes  int
	writeErr error
	writes   chan struct{}
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

type chunkReadCloser struct {
	chunks [][]byte
	closed bool
}

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
