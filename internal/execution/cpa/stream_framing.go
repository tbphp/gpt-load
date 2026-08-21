package cpa

import (
	"bytes"
	"fmt"
)

const maxSubscriptionSSEEventBytes = 10 << 20

// nativeResponsesSSEAssembler restores complete SSE events from the line-level
// chunks emitted by the embedded Codex and Grok executors.
type nativeResponsesSSEAssembler struct {
	pending []byte
}

func (assembler *nativeResponsesSSEAssembler) push(chunk []byte) ([][]byte, error) {
	assembler.pending = append(assembler.pending, chunk...)
	if len(chunk) == 0 || chunk[len(chunk)-1] != '\n' && chunk[len(chunk)-1] != '\r' {
		assembler.pending = append(assembler.pending, '\n')
	}

	var events [][]byte
	for {
		eventEnd, found := firstCompleteSubscriptionSSEEvent(assembler.pending)
		if !found {
			if len(assembler.pending) > maxSubscriptionSSEEventBytes {
				return nil, fmt.Errorf("subscription SSE event exceeds limit")
			}
			return events, nil
		}
		if eventEnd > maxSubscriptionSSEEventBytes {
			return nil, fmt.Errorf("subscription SSE event exceeds limit")
		}
		event := bytes.Clone(assembler.pending[:eventEnd])
		assembler.pending = assembler.pending[eventEnd:]
		if subscriptionSSEEventHasData(event) {
			events = append(events, event)
		}
	}
}

func (assembler *nativeResponsesSSEAssembler) finish() ([][]byte, error) {
	if assembler == nil || len(bytes.TrimSpace(assembler.pending)) == 0 {
		return nil, nil
	}
	events, err := assembler.push(nil)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 || len(bytes.TrimSpace(assembler.pending)) != 0 {
		return nil, fmt.Errorf("subscription SSE stream ended with an incomplete event")
	}
	return events, nil
}

func firstCompleteSubscriptionSSEEvent(data []byte) (int, bool) {
	lineStart := 0
	for index := 0; index < len(data); {
		if data[index] != '\r' && data[index] != '\n' {
			index++
			continue
		}
		terminatorStart := index
		index++
		if data[terminatorStart] == '\r' && index < len(data) && data[index] == '\n' {
			index++
		}
		if terminatorStart == lineStart {
			return index, true
		}
		lineStart = index
	}
	return 0, false
}

func subscriptionSSEEventHasData(event []byte) bool {
	for offset := 0; offset < len(event); {
		lineEnd := bytes.IndexAny(event[offset:], "\r\n")
		line := event[offset:]
		if lineEnd >= 0 {
			lineEnd += offset
			line = event[offset:lineEnd]
		}
		if bytes.Equal(line, []byte("data")) || bytes.HasPrefix(line, []byte("data:")) {
			return true
		}
		if lineEnd < 0 {
			return false
		}
		offset = lineEnd + 1
		if event[lineEnd] == '\r' && offset < len(event) && event[offset] == '\n' {
			offset++
		}
	}
	return false
}
