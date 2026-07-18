package gateway

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestRequestReplayReleaseKeepsActiveBodyButDropsCanonicalPayload(t *testing.T) {
	payload := []byte("request body")
	replay := newRequestReplay(payload)
	active := replay.open()

	replay.release()

	replay.mu.RLock()
	retained := len(replay.payload)
	replay.mu.RUnlock()
	if retained != 0 {
		t.Fatalf("canonical replay retains %d bytes after release", retained)
	}
	got, err := io.ReadAll(active)
	if err != nil {
		t.Fatalf("read active replay body: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("active replay body = %q, want %q", got, payload)
	}
	future, err := io.ReadAll(replay.open())
	if err != nil {
		t.Fatalf("read replay body opened after release: %v", err)
	}
	if len(future) != 0 {
		t.Fatalf("replay body opened after release = %q, want empty", future)
	}
}

func TestReplayBodyCloseDropsReader(t *testing.T) {
	body := newReplayBody([]byte("request body"))
	if err := body.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	body.mu.Lock()
	retainsReader := body.reader != nil
	body.mu.Unlock()
	if retainsReader {
		t.Fatal("closed replay body still retains its bytes.Reader")
	}
	buffer := make([]byte, 1)
	if read, err := body.Read(buffer); read != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("Read() after Close = %d, %v; want 0, EOF", read, err)
	}
}
