package embedded

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
)

const devinMaxConnectFrameBytes = 16 << 20

// Devin 与 SSE 渠道共用上游读取钩子；二进制帧观察仍封装在 CPA 边界内。
func ObserveDevinStreamBody(body io.ReadCloser, firstResponse func()) io.ReadCloser {
	return &devinObservedBody{ReadCloser: body, state: &devinStreamState{firstResponse: firstResponse, firstOnly: true}}
}

type devinStreamState struct {
	pending       []byte
	stopReason    uint64
	terminal      bool
	invalid       bool
	firstResponse func()
	firstOnly     bool
}

func withDevinStreamState(ctx context.Context, state *devinStreamState) context.Context {
	previous, _ := ctx.Value(streamBodyObserverKey{}).(func(io.ReadCloser, http.Header) io.ReadCloser)
	return WithStreamBodyObserver(ctx, func(body io.ReadCloser, header http.Header) io.ReadCloser {
		if previous != nil {
			body = previous(body, header)
		}
		return &devinObservedBody{ReadCloser: body, state: state}
	})
}

type devinObservedBody struct {
	io.ReadCloser
	state *devinStreamState
}

func (body *devinObservedBody) Read(buffer []byte) (int, error) {
	n, err := body.ReadCloser.Read(buffer)
	body.state.feed(buffer[:n])
	return n, err
}
func (state *devinStreamState) incomplete() bool {
	return state.stopReason == 1 || state.stopReason == 3 || state.stopReason == 11
}
func (state *devinStreamState) feed(raw []byte) {
	for len(raw) > 0 && !state.invalid && !state.terminal {
		needed := 5 - len(state.pending)
		if needed <= 0 {
			length := binary.BigEndian.Uint32(state.pending[1:5])
			// 与 CPA Connect 帧上限保持一致，绝不缓存整个回答。
			if length > devinMaxConnectFrameBytes {
				state.invalid = true
				state.pending = nil
				return
			}
			needed = 5 + int(length) - len(state.pending)
		}
		if needed > len(raw) {
			needed = len(raw)
		}
		state.pending = append(state.pending, raw[:needed]...)
		raw = raw[needed:]
		if len(state.pending) < 5 {
			continue
		}
		length := binary.BigEndian.Uint32(state.pending[1:5])
		if length > devinMaxConnectFrameBytes {
			state.invalid = true
			state.pending = nil
			return
		}
		if len(state.pending) < 5+int(length) {
			continue
		}
		flag, payload, err := helps.ReadConnectFrame(bytes.NewReader(state.pending))
		state.pending = state.pending[:0]
		if err != nil {
			state.invalid = true
			return
		}
		if flag&helps.ConnectFlagEndStream != 0 {
			_, err = helps.ParseDevinTrailerError(payload)
			state.terminal = true
			state.invalid = err != nil
			continue
		}
		frame, err := helps.ParseDevinFrame(payload)
		if err != nil {
			state.invalid = true
			return
		}
		if state.firstResponse != nil && len(payload) > 0 {
			state.firstResponse()
			state.firstResponse = nil
			if state.firstOnly {
				state.terminal = true
				state.pending = nil
				return
			}
		}
		if frame.StopReason != 0 {
			state.stopReason = frame.StopReason
		}
	}
}
