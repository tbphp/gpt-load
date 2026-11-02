package embedded

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
)

// 与当前 CPA Meta scanner 的单行上限一致；只暂存尚未读完的一行。
const museStreamLineLimit = 52_428_800

type museStreamTerminal struct {
	line       []byte
	overflow   bool
	seen       bool
	incomplete bool
	failed     bool
}

// 原始终态在 CPA 转换成 Chat/Claude/Gemini 前观测，保留已有首响观测器。
func withMuseStreamTerminal(ctx context.Context, terminal *museStreamTerminal) context.Context {
	previous, _ := ctx.Value(streamBodyObserverKey{}).(func(io.ReadCloser, http.Header) io.ReadCloser)
	return WithStreamBodyObserver(ctx, func(body io.ReadCloser, header http.Header) io.ReadCloser {
		if previous != nil {
			body = previous(body, header)
		}
		return &museStreamBody{ReadCloser: body, terminal: terminal}
	})
}

type museStreamBody struct {
	io.ReadCloser
	terminal *museStreamTerminal
}

func (body *museStreamBody) Read(buffer []byte) (int, error) {
	n, err := body.ReadCloser.Read(buffer)
	body.terminal.feed(buffer[:n])
	if err != nil {
		body.terminal.finishLine()
	}
	return n, err
}

func (terminal *museStreamTerminal) feed(data []byte) {
	for len(data) > 0 {
		index := bytes.IndexByte(data, '\n')
		part := data
		if index >= 0 {
			part = data[:index]
		}
		if !terminal.overflow {
			if len(terminal.line)+len(part) > museStreamLineLimit {
				terminal.line = nil
				terminal.overflow = true
			} else {
				terminal.line = append(terminal.line, part...)
			}
		}
		if index < 0 {
			return
		}
		terminal.finishLine()
		data = data[index+1:]
	}
}

func (terminal *museStreamTerminal) finishLine() {
	line, overflow := bytes.TrimSpace(terminal.line), terminal.overflow
	terminal.line, terminal.overflow = terminal.line[:0], false
	if overflow || !bytes.HasPrefix(line, []byte("data:")) {
		return
	}
	var event struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(bytes.TrimSpace(line[len("data:"):]), &event) != nil {
		return
	}
	switch event.Type {
	case "response.completed":
		terminal.seen = true
	case "response.incomplete":
		terminal.seen, terminal.incomplete = true, true
	case "response.failed", "error":
		terminal.seen, terminal.failed = true, true
	}
}
