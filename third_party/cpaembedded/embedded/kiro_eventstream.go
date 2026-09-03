package embedded

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"strings"
)

// Kiro event stream is an AWS EventStream binary encoding: each frame is a
// prelude (total length, headers length, prelude CRC), a headers block, a
// payload, and a message CRC that covers the prelude + headers + payload.

const maxKiroFrameSize = 4 * 1024 * 1024

var kiroCRC32Table = crc32.IEEETable

// kiroFrameHeader describes one parsed event stream frame.
type kiroFrameHeader struct {
	MessageType string
	EventType   string
}

// kiroFrame is a raw decoded event stream frame.
type kiroFrame struct {
	headers kiroFrameHeader
	payload []byte
}

// readKiroFrame reads and validates one AWS EventStream frame.
// It returns io.EOF at a clean end of stream.
func readKiroFrame(reader *bufio.Reader) (kiroFrame, error) {
	var prelude [12]byte
	read, err := io.ReadFull(reader, prelude[:])
	if err != nil {
		if read == 0 && (err == io.EOF || err == io.ErrUnexpectedEOF) {
			return kiroFrame{}, io.EOF
		}
		if err == io.ErrUnexpectedEOF {
			return kiroFrame{}, fmt.Errorf("truncated prelude: read %d/12 bytes", read)
		}
		return kiroFrame{}, fmt.Errorf("reading prelude: %w", err)
	}
	preludeCRC := binary.BigEndian.Uint32(prelude[8:12])
	if computed := crc32.Checksum(prelude[:8], kiroCRC32Table); computed != preludeCRC {
		return kiroFrame{}, fmt.Errorf("prelude CRC mismatch: got %08x, want %08x", computed, preludeCRC)
	}
	totalLength := binary.BigEndian.Uint32(prelude[0:4])
	headersLength := binary.BigEndian.Uint32(prelude[4:8])
	if totalLength < 16 {
		return kiroFrame{}, fmt.Errorf("invalid frame: total_length %d too small", totalLength)
	}
	if totalLength > maxKiroFrameSize {
		return kiroFrame{}, fmt.Errorf("invalid frame: total_length %d exceeds max", totalLength)
	}
	bodyLength := totalLength - 12
	if headersLength > bodyLength-4 {
		return kiroFrame{}, fmt.Errorf("invalid frame: headers_length %d exceeds body", headersLength)
	}
	body := make([]byte, bodyLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return kiroFrame{}, fmt.Errorf("reading frame body: %w", err)
	}
	messageCRC := binary.BigEndian.Uint32(body[len(body)-4:])
	hasher := crc32.New(kiroCRC32Table)
	_, _ = hasher.Write(prelude[:])
	_, _ = hasher.Write(body[:len(body)-4])
	if computed := hasher.Sum32(); computed != messageCRC {
		return kiroFrame{}, fmt.Errorf("message CRC mismatch: got %08x, want %08x", computed, messageCRC)
	}
	headers := body[:headersLength]
	payload := body[headersLength : len(body)-4]
	return kiroFrame{headers: parseKiroFrameHeaders(headers), payload: append([]byte(nil), payload...)}, nil
}

// kiroHeaderValueSizes maps AWS EventStream value-type IDs to fixed byte sizes.
// A value of -1 means variable length, prefixed by a 2-byte length.
var kiroHeaderValueSizes = map[byte]int{
	0: 0,  // bool true
	1: 0,  // bool false
	2: 1,  // byte
	3: 2,  // short
	4: 4,  // int
	5: 8,  // long
	6: -1, // byte array
	7: -1, // string
	8: 8,  // timestamp
	9: 16, // uuid
}

// parseKiroFrameHeaders extracts the :message-type and :event-type / :exception-type.
func parseKiroFrameHeaders(headers []byte) kiroFrameHeader {
	var result kiroFrameHeader
	index := 0
	for index < len(headers) {
		nameLength := int(headers[index])
		index++
		if index+nameLength > len(headers) {
			break
		}
		name := string(headers[index : index+nameLength])
		index += nameLength
		if index >= len(headers) {
			break
		}
		valueType := headers[index]
		index++
		size, known := kiroHeaderValueSizes[valueType]
		if !known {
			break
		}
		var valueLength int
		if size >= 0 {
			valueLength = size
		} else {
			if index+2 > len(headers) {
				break
			}
			valueLength = int(binary.BigEndian.Uint16(headers[index : index+2]))
			index += 2
		}
		if index+valueLength > len(headers) {
			break
		}
		value := headers[index : index+valueLength]
		index += valueLength
		if valueType == 7 {
			switch name {
			case ":message-type":
				result.MessageType = string(value)
			case ":event-type", ":exception-type":
				result.EventType = string(value)
			}
		}
	}
	return result
}

// Kiro event type constants.
const (
	kiroEventAssistantResponse = "assistantResponseEvent"
	kiroEventReasoningContent  = "reasoningContentEvent"
	kiroEventToolUse           = "toolUseEvent"
	kiroEventMetadata          = "metadataEvent"
	kiroEventMetering          = "meteringEvent"
	kiroEventInvalidState      = "invalidStateEvent"
	kiroEventException         = "exception"
	kiroEventMessageMetadata   = "messageMetadataEvent"
	kiroEventContextUsage      = "contextUsageEvent"
)

// kiroEvent is a parsed, semantically normalized Kiro event.
type kiroEvent struct {
	Type                   string
	Content                string
	ThinkingText           string
	ToolName               string
	ToolUseID              string
	ToolInput              string
	ToolStop               bool
	Credits                float64
	InputTokens            int
	OutputTokens           int
	TotalTokens            int
	UncachedInputTokens    int
	CacheReadInputTokens   int
	CacheWriteInputTokens  int
	Signature              string
	RedactedContent        string
	ContextUsagePercentage float64
	ConversationID         string
	ErrorMessage           string
	InvalidStateReason     string
}

// ErrorText returns the most specific error text, falling back to the invalid
// state reason when no explicit message was delivered.
func (event kiroEvent) ErrorText() string {
	if strings.TrimSpace(event.ErrorMessage) != "" {
		return event.ErrorMessage
	}
	return event.InvalidStateReason
}

// kiroToolUseAccumulator accumulates fragmented toolUseEvent input JSON.
type kiroToolUseAccumulator struct {
	toolUseID string
	toolName  string
	inputBuf  []byte
	started   bool
}

// update ingests one toolUseEvent payload. A frame carrying a full toolUseId and
// input yields a ready kiroEvent immediately. When input arrives split across
// frames (fragmented), each chunk is appended to the accumulator and emitted on
// flush at end of stream.
func (acc *kiroToolUseAccumulator) update(raw map[string]json.RawMessage) (kiroEvent, bool) {
	toolUseID := asKiroString(raw["toolUseId"])
	toolName := asKiroString(raw["name"])
	input := raw["input"]

	// Starting a new tool while one is in progress: flush the previous fragment.
	if acc.started && toolUseID != "" && toolUseID != acc.toolUseID {
		acc.started = false
		acc.toolUseID = ""
		acc.toolName = ""
		acc.inputBuf = nil
	}
	if toolUseID == "" {
		return kiroEvent{}, false
	}
	// When a full input arrives together with the toolUseId, this call is
	// complete in this frame: emit immediately.
	if len(input) > 0 {
		acc.started = false
		acc.inputBuf = nil
		return kiroEvent{
			Type:      kiroEventToolUse,
			ToolUseID: toolUseID,
			ToolName:  toolName,
			ToolInput: string(input),
			ToolStop:  true,
		}, true
	}
	// Fragmented: begin or continue accumulation, appending each partial input
	// chunk so flush can emit the concatenated input instead of an empty blob.
	if !acc.started {
		acc.toolUseID = toolUseID
		acc.toolName = toolName
		acc.started = true
		acc.inputBuf = nil
	}
	acc.inputBuf = append(acc.inputBuf, input...)
	return kiroEvent{}, false
}

// flush finalizes and returns the currently-accumulated tool call, if any.
func (acc *kiroToolUseAccumulator) flush() (kiroEvent, bool) {
	if !acc.started {
		return kiroEvent{}, false
	}
	event := kiroEvent{
		Type:      kiroEventToolUse,
		ToolUseID: acc.toolUseID,
		ToolName:  acc.toolName,
		ToolInput: string(acc.inputBuf),
		ToolStop:  true,
	}
	acc.started = false
	acc.toolUseID = ""
	acc.toolName = ""
	acc.inputBuf = nil
	return event, true
}

func asKiroString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return value
}

// parseKiroStream reads event frames and invokes callback until it returns true.
func parseKiroStream(reader io.Reader, callback func(kiroEvent) bool) error {
	buffered := bufio.NewReader(reader)
	var accumulator kiroToolUseAccumulator
	for {
		frame, err := readKiroFrame(buffered)
		if err == io.EOF {
			if event, ok := accumulator.flush(); ok {
				callback(event)
			}
			return nil
		}
		if err != nil {
			return err
		}
		if frame.headers.MessageType == "exception" {
			var payload struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(frame.payload, &payload)
			callback(kiroEvent{Type: kiroEventException, ErrorMessage: payload.Message})
			return nil
		}
		var stop bool
		switch frame.headers.EventType {
		case kiroEventAssistantResponse:
			var payload struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal(frame.payload, &payload); err != nil {
				return fmt.Errorf("decode %s: %w", frame.headers.EventType, err)
			}
			stop = callback(kiroEvent{Type: kiroEventAssistantResponse, Content: payload.Content})
		case kiroEventReasoningContent:
			var payload struct {
				Text            string `json:"text"`
				Signature       string `json:"signature"`
				RedactedContent string `json:"redactedContent"`
			}
			if err := json.Unmarshal(frame.payload, &payload); err != nil {
				return fmt.Errorf("decode %s: %w", frame.headers.EventType, err)
			}
			stop = callback(kiroEvent{
				Type: kiroEventReasoningContent, ThinkingText: payload.Text,
				Signature: payload.Signature, RedactedContent: payload.RedactedContent,
			})
		case kiroEventToolUse:
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(frame.payload, &raw); err != nil {
				continue
			}
			if event, emitted := accumulator.update(raw); emitted {
				stop = callback(event)
			}
		case kiroEventMetadata:
			var payload struct {
				TokenUsage struct {
					UncachedInputTokens   int `json:"uncachedInputTokens"`
					OutputTokens          int `json:"outputTokens"`
					TotalTokens           int `json:"totalTokens"`
					CacheReadInputTokens  int `json:"cacheReadInputTokens"`
					CacheWriteInputTokens int `json:"cacheWriteInputTokens"`
				} `json:"tokenUsage"`
			}
			if err := json.Unmarshal(frame.payload, &payload); err == nil {
				usage := payload.TokenUsage
				stop = callback(kiroEvent{
					Type:                  kiroEventMetadata,
					UncachedInputTokens:   usage.UncachedInputTokens,
					CacheReadInputTokens:  usage.CacheReadInputTokens,
					CacheWriteInputTokens: usage.CacheWriteInputTokens,
					OutputTokens:          usage.OutputTokens,
					TotalTokens:           usage.TotalTokens,
					InputTokens:           usage.UncachedInputTokens + usage.CacheReadInputTokens,
				})
			}
		case kiroEventMetering:
			var payload struct {
				Usage        float64 `json:"usage"`
				InputTokens  int     `json:"inputTokens"`
				OutputTokens int     `json:"outputTokens"`
			}
			if err := json.Unmarshal(frame.payload, &payload); err == nil {
				stop = callback(kiroEvent{
					Type: kiroEventMetering, Credits: payload.Usage,
					InputTokens: payload.InputTokens, OutputTokens: payload.OutputTokens,
				})
			}
		case kiroEventInvalidState:
			var payload struct {
				Reason  string `json:"reason"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(frame.payload, &payload); err != nil {
				return fmt.Errorf("decode %s: %w", frame.headers.EventType, err)
			}
			stop = callback(kiroEvent{
				Type: kiroEventInvalidState, InvalidStateReason: payload.Reason,
				ErrorMessage: payload.Message,
			})
		case kiroEventMessageMetadata:
			var payload struct {
				ConversationID string `json:"conversationId"`
				UtteranceID    string `json:"utteranceId"`
			}
			if err := json.Unmarshal(frame.payload, &payload); err == nil {
				stop = callback(kiroEvent{Type: kiroEventMessageMetadata, ConversationID: payload.ConversationID})
			}
		case kiroEventContextUsage:
			var payload struct {
				ContextUsagePercentage float64 `json:"contextUsagePercentage"`
			}
			if err := json.Unmarshal(frame.payload, &payload); err == nil {
				stop = callback(kiroEvent{Type: kiroEventContextUsage, ContextUsagePercentage: payload.ContextUsagePercentage})
			}
		default:
			// Ignore unknown / no-op events.
		}
		if stop {
			return nil
		}
	}
}
