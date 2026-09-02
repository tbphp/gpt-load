package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const maxResponsesStoreRewriteGrowthBytes = 64

func forceStatelessResponsesRequest(payload []byte) ([]byte, error) {
	object, err := decodeResponsesStoreObject(payload)
	if err != nil {
		return nil, fmt.Errorf("normalize stateless Responses request: %w", err)
	}
	object["store"] = json.RawMessage("false")
	encoded, err := encodeResponsesStoreObject(object, len(payload))
	if err != nil {
		return nil, fmt.Errorf("normalize stateless Responses request: %w", err)
	}
	return encoded, nil
}

func normalizeStatelessResponsesSuccess(payload []byte) []byte {
	object, err := decodeResponsesStoreObject(payload)
	if err != nil {
		return bytes.Clone(payload)
	}
	object["store"] = json.RawMessage("false")
	encoded, err := encodeResponsesStoreObject(object, len(payload))
	if err != nil {
		return bytes.Clone(payload)
	}
	return encoded
}

func normalizeStatelessResponsesSSEPayload(
	event dialect.StreamEvent,
	_ bool,
) (sseEventRewriteResult, error) {
	object, err := decodeResponsesStoreObject(event.Payload)
	if err != nil {
		return sseEventRewriteResult{body: bytes.Clone(event.Payload)}, nil
	}
	rawResponse, exists := object["response"]
	if !exists || bytes.Equal(bytes.TrimSpace(rawResponse), []byte("null")) {
		return sseEventRewriteResult{body: bytes.Clone(event.Payload)}, nil
	}
	response, err := decodeResponsesStoreObject(rawResponse)
	if err != nil {
		return sseEventRewriteResult{body: bytes.Clone(event.Payload)}, nil
	}
	response["store"] = json.RawMessage("false")
	object["response"], err = encodeResponsesStoreObject(response, len(rawResponse))
	if err != nil {
		return sseEventRewriteResult{body: bytes.Clone(event.Payload)}, nil
	}
	rewritten, err := encodeResponsesStoreObject(object, len(event.Payload))
	if err != nil {
		return sseEventRewriteResult{body: bytes.Clone(event.Payload)}, nil
	}
	return sseEventRewriteResult{body: rewritten}, nil
}

func decodeResponsesStoreObject(payload []byte) (map[string]json.RawMessage, error) {
	if !utf8.Valid(payload) {
		return nil, fmt.Errorf("body must be valid UTF-8")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return nil, fmt.Errorf("body must be a JSON object")
	}
	return object, nil
}

func encodeResponsesStoreObject(
	object map[string]json.RawMessage,
	sourceSize int,
) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(object); err != nil {
		return nil, fmt.Errorf("encode JSON object: %w", err)
	}
	encoded := output.Bytes()
	if len(encoded) == 0 || encoded[len(encoded)-1] != '\n' {
		return nil, fmt.Errorf("encoded JSON object has no terminator")
	}
	encoded = encoded[:len(encoded)-1]
	if len(encoded) > sourceSize && len(encoded)-sourceSize > maxResponsesStoreRewriteGrowthBytes {
		return nil, fmt.Errorf("encoded JSON object exceeds rewrite growth limit")
	}
	return encoded, nil
}

type statelessResponsesSSEBuffer struct {
	pending       []byte
	maxEventBytes int
	scanner       sseRewriteBoundaryScanner
}

func newStatelessResponsesSSEBuffer() *statelessResponsesSSEBuffer {
	return &statelessResponsesSSEBuffer{
		maxEventBytes: execution.SSEEventLimit(protocol.OpenAIResponses),
	}
}

func (buffer *statelessResponsesSSEBuffer) push(chunk []byte) ([]byte, error) {
	if buffer == nil {
		return nil, fmt.Errorf("stateless Responses SSE buffer is required")
	}
	buffer.pending = append(buffer.pending, chunk...)
	var output bytes.Buffer
	for {
		optionalLF, overflow := buffer.scanner.ConsumeOptionalLineFeed(
			buffer.pending,
			false,
			buffer.maxEventBytes,
		)
		if overflow {
			return nil, errSSEEventTooLarge
		}
		if optionalLF > 0 {
			_, _ = output.Write(buffer.pending[:optionalLF])
			buffer.discard(optionalLF)
			continue
		}

		eventEnd, complete := buffer.scanner.Find(buffer.pending)
		if !complete {
			if len(buffer.pending) > buffer.maxEventBytes {
				return nil, errSSEEventTooLarge
			}
			return output.Bytes(), nil
		}
		if eventEnd > buffer.maxEventBytes {
			return nil, errSSEEventTooLarge
		}
		rewritten, err := rewriteSSEEventWithMetadata(
			buffer.pending[:eventEnd],
			normalizeStatelessResponsesSSEPayload,
		)
		if err != nil {
			return nil, err
		}
		if len(rewritten.body) > buffer.maxEventBytes {
			return nil, errSSEEventTooLarge
		}
		_, _ = output.Write(rewritten.body)
		buffer.discard(eventEnd)
		buffer.scanner.AfterEvent(eventEnd, len(rewritten.body))
	}
}

func (buffer *statelessResponsesSSEBuffer) finish() error {
	if buffer == nil {
		return fmt.Errorf("stateless Responses SSE buffer is required")
	}
	optionalLF, overflow := buffer.scanner.ConsumeOptionalLineFeed(
		buffer.pending,
		true,
		buffer.maxEventBytes,
	)
	if overflow {
		return errSSEEventTooLarge
	}
	if optionalLF > 0 {
		buffer.discard(optionalLF)
	}
	if len(buffer.pending) > 0 {
		return errSSEEventIncomplete
	}
	buffer.scanner.Reset()
	return nil
}

func (buffer *statelessResponsesSSEBuffer) discard(count int) {
	buffer.pending = buffer.pending[count:]
	if len(buffer.pending) == 0 {
		buffer.pending = nil
	}
}
