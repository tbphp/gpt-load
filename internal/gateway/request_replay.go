package gateway

import (
	"bytes"
	"io"
	"net/http"
	"sync"
)

type requestReplay struct {
	mu      sync.RWMutex
	payload []byte
}

func newRequestReplay(payload []byte) *requestReplay {
	return &requestReplay{payload: payload}
}

func (replay *requestReplay) open() io.ReadCloser {
	replay.mu.RLock()
	payload := replay.payload
	replay.mu.RUnlock()
	if len(payload) == 0 {
		return http.NoBody
	}
	return newReplayBody(payload)
}

func (replay *requestReplay) release() {
	replay.mu.Lock()
	replay.payload = nil
	replay.mu.Unlock()
}

type replayBody struct {
	mu     sync.Mutex
	reader *bytes.Reader
}

func newReplayBody(payload []byte) *replayBody {
	return &replayBody{reader: bytes.NewReader(payload)}
}

func (body *replayBody) Read(destination []byte) (int, error) {
	body.mu.Lock()
	defer body.mu.Unlock()
	if body.reader == nil {
		return 0, io.EOF
	}
	read, err := body.reader.Read(destination)
	if err == io.EOF {
		body.reader = nil
	}
	return read, err
}

func (body *replayBody) Close() error {
	body.mu.Lock()
	body.reader = nil
	body.mu.Unlock()
	return nil
}
