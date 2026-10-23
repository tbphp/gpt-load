package cpa

import (
	"bytes"
	"encoding/json"
)

const (
	codexBootstrapEvents = 8
	codexBootstrapChunks = 48
	codexBootstrapBytes  = 256 << 10
)

// CPA 会把未识别的过载错误排在前导之后交付。只暂存没有产出的开始事件，
// 让这类容量拒绝仍能作为提交前错误交给网关。读取继续使用原有首包、空闲和
// 请求超时；任一产出、工具、未知事件或缓冲上限都会结束这次预读。
type codexBootstrapBuffer struct {
	pending  [][]byte
	size     int
	chunks   int
	released bool
}

// 空行和 SSE 注释同样消耗预读预算，不能靠持续心跳无限延迟提交。
func (buffer *codexBootstrapBuffer) observeChunk() bool {
	if buffer == nil || buffer.released {
		return false
	}
	buffer.chunks++
	return buffer.chunks >= codexBootstrapChunks
}

func (buffer *codexBootstrapBuffer) filter(events [][]byte) [][]byte {
	if buffer == nil || buffer.released {
		return events
	}
	for index, event := range events {
		if !codexBootstrapPreamble(event) || len(buffer.pending)+1 >= codexBootstrapEvents ||
			buffer.size+len(event) >= codexBootstrapBytes {
			return append(buffer.flush(), events[index:]...)
		}
		buffer.pending = append(buffer.pending, event)
		buffer.size += len(event)
	}
	return nil
}

func (buffer *codexBootstrapBuffer) flush() [][]byte {
	if buffer == nil {
		return nil
	}
	pending := buffer.pending
	buffer.pending, buffer.size, buffer.released = nil, 0, true
	return pending
}

func codexBootstrapPreamble(event []byte) bool {
	var data [][]byte
	var name []byte
	for _, line := range bytes.FieldsFunc(event, func(r rune) bool { return r == '\r' || r == '\n' }) {
		if value, ok := bytes.CutPrefix(line, []byte("data:")); ok {
			data = append(data, bytes.TrimSpace(value))
		} else if value, ok := bytes.CutPrefix(line, []byte("event:")); ok {
			name = bytes.TrimSpace(value)
		}
	}
	payload := bytes.Join(data, []byte{'\n'})
	if len(payload) == 0 {
		return true
	}
	var envelope struct {
		Type     string `json:"type"`
		Response struct {
			Output []json.RawMessage `json:"output"`
		} `json:"response"`
	}
	if json.Unmarshal(payload, &envelope) != nil || len(envelope.Response.Output) != 0 ||
		len(name) > 0 && string(name) != envelope.Type {
		return false
	}
	return envelope.Type == "response.created" || envelope.Type == "response.in_progress"
}
