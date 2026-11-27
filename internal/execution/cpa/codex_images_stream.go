package cpa

import (
	"bytes"
	"fmt"

	"gpt-load/internal/execution"
)

// Codex 原生 Images SDK 返回原始 HTTP 读取块，而非完整的 SSE 行或事件。
// 等到收到真正的事件分隔符再改写内容，不在读取块之间插入字节。
type codexImagesSSEAssembler struct {
	pending   []byte
	lineStart int
	scan      int
}

func (s *codexImagesSSEAssembler) push(chunk []byte, eof bool) ([][]byte, error) {
	s.pending = append(s.pending, chunk...)
	var events [][]byte
	for s.scan < len(s.pending) {
		i := bytes.IndexAny(s.pending[s.scan:], "\r\n")
		if i < 0 {
			s.scan = len(s.pending)
			break
		}
		i += s.scan
		end := i + 1
		if s.pending[i] == '\r' {
			if end == len(s.pending) && !eof {
				// 下一次读取可能以 CRLF 中的 LF 开头。
				s.scan = i
				break
			}
			if end < len(s.pending) && s.pending[end] == '\n' {
				end++
			}
		}
		if end > execution.OpenAIImagesSSEEventLimitBytes {
			return nil, fmt.Errorf("Codex Images SSE event exceeds limit")
		}
		if i != s.lineStart {
			s.lineStart, s.scan = end, end
			continue
		}
		events = append(events, s.pending[:end:end])
		s.pending = s.pending[end:]
		s.lineStart, s.scan = 0, 0
	}
	if len(s.pending) > execution.OpenAIImagesSSEEventLimitBytes {
		return nil, fmt.Errorf("Codex Images SSE event exceeds limit")
	}
	if eof && len(s.pending) > 0 {
		return nil, fmt.Errorf("Codex Images SSE stream ended with an incomplete event")
	}
	if len(s.pending) == 0 {
		s.pending = nil
	}
	return events, nil
}
