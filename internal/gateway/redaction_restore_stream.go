package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
)

const (
	maxRedactionStreamPendingBytes = 32 << 20
	maxRedactionStreamDocument     = 10 << 20
	maxRedactionStreamChannels     = 64
	redactionStreamPrefix          = "gld1_"
)

var errRedactionStream = errors.New("cannot restore streaming response")

// redactionRestoreSSE is local to one HTTP response. Push accepts arbitrary
// transport chunks and releases complete SSE events only in upstream order.
type redactionRestoreSSE struct {
	protocol      protocol.Protocol
	restore       func(string) (string, error)
	structured    bool
	maxEventBytes int

	input                   []byte
	scanner                 sseRewriteBoundaryScanner
	queue                   []*redactionStreamEvent
	queueBytes              int
	texts                   map[string]*redactionStreamText
	documents               map[string]*redactionStreamDocument
	terminalBoundaryPending bool
	terminalReleased        bool
	finished                bool
	failed                  bool
}

type redactionStreamEvent struct {
	raw         []byte
	payload     []byte
	fields      []*redactionStreamField
	direct      []unaryRestorePatch
	replacement []byte
	terminal    bool
}

type redactionStreamField struct {
	start, end int
	original   string
	value      string
}

type redactionStreamText struct {
	token redactionTokenStream
	last  *redactionStreamField
}

func newRedactionRestoreSSE(
	clientProtocol protocol.Protocol,
	restore func(string) (string, error),
	structuredOutput bool,
) *redactionRestoreSSE {
	return newRedactionRestoreSSEWithLimit(clientProtocol, restore, structuredOutput, maxSSEEventBytes)
}

func newRedactionRestoreSSEWithLimit(
	clientProtocol protocol.Protocol,
	restore func(string) (string, error),
	structuredOutput bool,
	maxEventBytes int,
) *redactionRestoreSSE {
	return &redactionRestoreSSE{
		protocol: clientProtocol, restore: restore, structured: structuredOutput,
		maxEventBytes: maxEventBytes,
		texts:         make(map[string]*redactionStreamText),
		documents:     make(map[string]*redactionStreamDocument),
	}
}

func (stream *redactionRestoreSSE) Push(chunk []byte) ([]byte, error) {
	if stream == nil || stream.finished || stream.failed {
		return nil, errRedactionStream
	}
	var output []byte
	for len(chunk) > 0 {
		feed := min(len(chunk), 64<<10)
		stream.input = append(stream.input, chunk[:feed]...)
		chunk = chunk[feed:]
		part, err := stream.drain()
		if err != nil {
			return nil, stream.fail(err)
		}
		output = append(output, part...)
	}
	return output, nil
}

func (stream *redactionRestoreSSE) drain() ([]byte, error) {
	var output []byte
	for {
		optional, overflow := stream.scanner.ConsumeOptionalLineFeed(stream.input, false, stream.maxEventBytes)
		if overflow {
			return nil, errSSEEventTooLarge
		}
		if !stream.scanner.optionalLineFeed {
			stream.terminalBoundaryPending = false
		}
		if optional > 0 {
			stream.input = stream.input[optional:]
			if err := stream.enqueue(&redactionStreamEvent{raw: []byte{'\n'}}); err != nil {
				return nil, err
			}
			if !stream.blocked() {
				part, err := stream.release()
				if err != nil {
					return nil, err
				}
				output = append(output, part...)
			}
		}
		end, complete := stream.scanner.Find(stream.input)
		if !complete {
			break
		}
		if end > stream.maxEventBytes {
			return nil, errSSEEventTooLarge
		}
		event := &redactionStreamEvent{raw: bytes.Clone(stream.input[:end])}
		stream.input = stream.input[end:]
		stream.scanner.AfterEvent(end, end)
		if err := stream.enqueue(event); err != nil {
			return nil, err
		}
		if err := stream.process(event); err != nil {
			return nil, err
		}
		if event.terminal && stream.scanner.optionalLineFeed {
			stream.terminalBoundaryPending = true
		}
		if !stream.blocked() {
			part, err := stream.release()
			if err != nil {
				return nil, err
			}
			output = append(output, part...)
		}
	}
	if len(stream.input) > stream.maxEventBytes {
		return nil, errSSEEventTooLarge
	}
	return output, nil
}

func (stream *redactionRestoreSSE) Finish() ([]byte, error) {
	if stream == nil || stream.failed {
		return nil, errRedactionStream
	}
	if stream.finished {
		return nil, nil
	}
	optional, overflow := stream.scanner.ConsumeOptionalLineFeed(stream.input, true, stream.maxEventBytes)
	if overflow {
		return nil, stream.fail(errSSEEventTooLarge)
	}
	if optional > 0 {
		stream.input = stream.input[optional:]
		if err := stream.enqueue(&redactionStreamEvent{raw: []byte{'\n'}}); err != nil {
			return nil, stream.fail(err)
		}
	}
	stream.terminalBoundaryPending = false
	if len(stream.input) != 0 {
		return nil, stream.fail(errSSEEventIncomplete)
	}
	if err := stream.closeMatching(""); err != nil {
		return nil, stream.fail(err)
	}
	output, err := stream.release()
	if err != nil {
		return nil, stream.fail(err)
	}
	stream.finished = true
	return output, nil
}

// TerminalReleased reports whether a protocol terminal event has actually
// left the pending queue. The caller must still confirm its own write succeeds.
func (stream *redactionRestoreSSE) TerminalReleased() bool {
	return stream != nil && stream.terminalReleased
}

func (stream *redactionRestoreSSE) blocked() bool {
	if len(stream.texts) != 0 || stream.terminalBoundaryPending {
		return true
	}
	for _, doc := range stream.documents {
		if doc.blocked() {
			return true
		}
	}
	return false
}

func (stream *redactionRestoreSSE) enqueue(event *redactionStreamEvent) error {
	if len(event.raw) > stream.maxEventBytes ||
		stream.queueBytes > maxRedactionStreamPendingBytes-len(event.raw) {
		return errRedactionStream
	}
	stream.queue = append(stream.queue, event)
	stream.queueBytes += len(event.raw)
	return nil
}

func (stream *redactionRestoreSSE) release() ([]byte, error) {
	if stream.blocked() {
		return nil, errRedactionStream
	}
	var output []byte
	terminal := false
	for _, event := range stream.queue {
		part, err := event.render(stream.maxEventBytes)
		if err != nil {
			return nil, err
		}
		if len(part) > maxRedactionStreamPendingBytes-len(output) {
			return nil, errRedactionStream
		}
		output = append(output, part...)
		terminal = terminal || event.terminal
	}
	stream.terminalReleased = stream.terminalReleased || terminal
	stream.queue = nil
	stream.queueBytes = 0
	return output, nil
}

func (stream *redactionRestoreSSE) fail(err error) error {
	stream.failed = true
	stream.input = nil
	stream.queue = nil
	stream.texts = nil
	stream.documents = nil
	stream.terminalBoundaryPending = false
	return err
}

func (event *redactionStreamEvent) field(value gjson.Result) *redactionStreamField {
	field := &redactionStreamField{
		start: value.Index, end: value.Index + len(value.Raw),
		original: value.Str, value: value.Str,
	}
	event.fields = append(event.fields, field)
	return field
}

func (event *redactionStreamEvent) render(maxEventBytes int) ([]byte, error) {
	if len(event.fields) == 0 && len(event.direct) == 0 && event.replacement == nil {
		return event.raw, nil
	}
	payload := event.payload
	if event.replacement != nil {
		payload = event.replacement
	} else {
		patches := append([]unaryRestorePatch(nil), event.direct...)
		for _, field := range event.fields {
			if field.value == field.original {
				continue
			}
			if !utf8.ValidString(field.value) {
				return nil, errRedactionStream
			}
			encoded, err := json.Marshal(field.value)
			if err != nil {
				return nil, errRedactionStream
			}
			patches = append(patches, unaryRestorePatch{
				start: field.start, end: field.end, value: encoded,
			})
		}
		if len(patches) == 0 {
			return event.raw, nil
		}
		sort.Slice(patches, func(i, j int) bool { return patches[i].start < patches[j].start })
		var out bytes.Buffer
		previous := 0
		for _, patch := range patches {
			if patch.start < previous || patch.end > len(payload) || patch.start > patch.end {
				return nil, errRedactionStream
			}
			out.Write(payload[previous:patch.start])
			out.Write(patch.value)
			previous = patch.end
			if out.Len() > maxEventBytes {
				return nil, errSSEEventTooLarge
			}
		}
		out.Write(payload[previous:])
		payload = out.Bytes()
	}
	rewritten, err := rewriteSSEEventWithMetadata(event.raw,
		func(_ dialect.StreamEvent, _ bool) (sseEventRewriteResult, error) {
			return sseEventRewriteResult{body: payload}, nil
		})
	if err != nil || len(rewritten.body) > maxEventBytes {
		return nil, errRedactionStream
	}
	return rewritten.body, nil
}

func redactionSSEData(event []byte) ([]byte, string, bool) {
	var data [][]byte
	var name string
	for _, line := range splitSSEEventLines(event) {
		if value, ok := parseSSEEventName(line.content); ok {
			name = string(value)
		}
		if line.isData {
			data = append(data, line.data)
		}
	}
	if len(data) == 0 {
		return nil, name, false
	}
	return bytes.Join(data, []byte{'\n'}), name, true
}

func (stream *redactionRestoreSSE) process(event *redactionStreamEvent) error {
	if stream.restore == nil {
		return nil
	}
	payload, name, hasData := redactionSSEData(event.raw)
	if !hasData {
		return nil
	}
	if bytes.Equal(payload, []byte("[DONE]")) {
		event.terminal = true
		return stream.closeMatching("")
	}
	if name == "error" {
		event.terminal = true
		if err := stream.closeMatching(""); err != nil {
			return err
		}
		return stream.maskErrorPayload(event, payload)
	}
	if len(payload) == 0 || !json.Valid(payload) || !utf8.Valid(payload) {
		if bytes.Contains(payload, []byte(redactionStreamPrefix)) {
			return errRedactionStream
		}
		return nil
	}
	event.payload = payload
	root := gjson.ParseBytes(payload)
	if isSSEErrorPayload(payload) {
		event.terminal = true
		if err := stream.closeMatching(""); err != nil {
			return err
		}
		return stream.maskErrorPayload(event, payload)
	}
	switch stream.protocol {
	case protocol.OpenAICompletions:
		return stream.chat(event, root)
	case protocol.OpenAIResponses:
		return stream.responses(event, root, name)
	case protocol.Anthropic:
		return stream.anthropic(event, root, name)
	case protocol.Gemini:
		return stream.gemini(event, root)
	default:
		return nil
	}
}

func (stream *redactionRestoreSSE) maskErrorPayload(event *redactionStreamEvent, payload []byte) error {
	if !bytes.Contains(payload, []byte(redactionStreamPrefix)) && !bytes.Contains(payload, []byte(`\u`)) {
		return nil
	}
	safe := payload
	if json.Valid(payload) && utf8.Valid(payload) {
		ctx := unaryRestoreContext{
			body: payload,
			restore: func(value string) (string, error) {
				return redact.MaskRedactionTokens(value), nil
			},
		}
		if err := ctx.walkJSONValues(gjson.ParseBytes(payload), 0, ctx.addPatch); err != nil {
			return errRedactionStream
		}
		var err error
		safe, err = ctx.apply()
		if err != nil {
			return errRedactionStream
		}
	}
	masked := redact.MaskRedactionTokens(string(safe))
	if masked != string(payload) {
		event.replacement = []byte(masked)
	}
	return nil
}

func (stream *redactionRestoreSSE) addText(
	event *redactionStreamEvent, key string, value gjson.Result, signed bool,
) error {
	if value.Type != gjson.String {
		return nil
	}
	if signed {
		if err := stream.closeMatching(key); err != nil {
			return err
		}
		return stream.direct(event, func(ctx *unaryRestoreContext) error {
			if err := ctx.text(value); err != nil {
				return err
			}
			if len(ctx.patches) != 0 {
				return errRedactionStream
			}
			return nil
		})
	}
	if stream.structured {
		return stream.addDocument(event, key, value)
	}
	return stream.addPlainText(event, key, value)
}

func (stream *redactionRestoreSSE) addPlainText(event *redactionStreamEvent, key string, value gjson.Result) error {
	if value.Type != gjson.String {
		return nil
	}
	field := event.field(value)
	state := stream.texts[key]
	if state == nil {
		state = &redactionStreamText{}
	}
	var visible strings.Builder
	if err := state.token.pushString(value.Str, &visible, stream.restore, false); err != nil {
		return err
	}
	field.value = visible.String()
	if !state.token.pending() {
		delete(stream.texts, key)
		return nil
	}
	if stream.texts[key] == nil {
		if len(stream.texts)+len(stream.documents) >= maxRedactionStreamChannels {
			return errRedactionStream
		}
		stream.texts[key] = state
	}
	state.last = field
	return nil
}

func (stream *redactionRestoreSSE) addDocument(
	event *redactionStreamEvent, key string, value gjson.Result,
) error {
	if value.Type != gjson.String {
		return nil
	}
	state := stream.documents[key]
	if state == nil && strings.Trim(value.Str, " \t\r\n") == "" {
		return nil
	}
	if state == nil {
		state = &redactionStreamDocument{}
	}
	restored, err := state.push(value.Str, stream.restore)
	if err != nil {
		return err
	}
	// 完整参数不再占用并行解析名额，后续空白也无需保留状态。
	if state.complete() {
		delete(stream.documents, key)
	} else {
		if stream.documents[key] == nil && len(stream.texts)+len(stream.documents) >= maxRedactionStreamChannels {
			return errRedactionStream
		}
		stream.documents[key] = state
	}
	field := event.field(value)
	field.value = restored
	if state.blocked() {
		state.last = field
	} else {
		state.last = nil
	}
	return nil
}

func (stream *redactionRestoreSSE) closeMatching(prefix string) error {
	for key, state := range stream.texts {
		if key != prefix && prefix != "" && !(strings.HasSuffix(prefix, "/") && strings.HasPrefix(key, prefix)) {
			continue
		}
		tail, err := state.token.finish()
		if err != nil {
			return err
		}
		state.last.value += tail
		delete(stream.texts, key)
	}
	for key, state := range stream.documents {
		if key != prefix && prefix != "" && !(strings.HasSuffix(prefix, "/") && strings.HasPrefix(key, prefix)) {
			continue
		}
		tail, err := state.finish(stream.restore)
		if err != nil {
			return err
		}
		if state.last != nil {
			state.last.value += tail
		}
		delete(stream.documents, key)
	}
	return nil
}

func redactionStreamIndex(value gjson.Result, fallback int) string {
	if value.Exists() {
		return value.Raw
	}
	return strconv.Itoa(fallback)
}

func (stream *redactionRestoreSSE) direct(
	event *redactionStreamEvent,
	visit func(*unaryRestoreContext) error,
) error {
	ctx := unaryRestoreContext{body: event.payload, restore: stream.restore, structured: stream.structured}
	if err := visit(&ctx); err != nil {
		return errRedactionStream
	}
	event.direct = append(event.direct, ctx.patches...)
	return nil
}

func (stream *redactionRestoreSSE) chat(event *redactionStreamEvent, root gjson.Result) error {
	return unaryRestoreField(root, "choices", func(choices gjson.Result) error {
		index := 0
		return unaryRestoreArray(choices, func(choice gjson.Result) error {
			choiceIndex := redactionStreamIndex(choice.Get("index"), index)
			index++
			prefix := "chat/" + choiceIndex + "/"
			if err := unaryRestoreField(choice, "delta", func(delta gjson.Result) error {
				if err := unaryRestoreField(delta, "content", func(value gjson.Result) error {
					return stream.addText(event, prefix+"text", value, false)
				}); err != nil {
					return err
				}
				if err := unaryRestoreField(delta, "function_call", func(call gjson.Result) error {
					return unaryRestoreField(call, "arguments", func(value gjson.Result) error {
						return stream.addDocument(event, prefix+"function", value)
					})
				}); err != nil {
					return err
				}
				return unaryRestoreField(delta, "tool_calls", func(calls gjson.Result) error {
					callIndex := 0
					return unaryRestoreArray(calls, func(call gjson.Result) error {
						key := prefix + "tool/" + redactionStreamIndex(call.Get("index"), callIndex)
						callIndex++
						if err := unaryRestoreField(call, "function", func(function gjson.Result) error {
							return unaryRestoreField(function, "arguments", func(value gjson.Result) error {
								return stream.addDocument(event, key, value)
							})
						}); err != nil {
							return err
						}
						return unaryRestoreField(call, "custom", func(custom gjson.Result) error {
							return unaryRestoreField(custom, "input", func(value gjson.Result) error {
								return stream.addPlainText(event, key, value)
							})
						})
					})
				})
			}); err != nil {
				return err
			}
			if reason := choice.Get("finish_reason"); reason.Exists() &&
				reason.Type == gjson.String && reason.Str != "" {
				return stream.closeMatching(prefix)
			}
			return nil
		})
	})
}

func (stream *redactionRestoreSSE) responses(event *redactionStreamEvent, root gjson.Result, eventName string) error {
	kind := root.Get("type").Str
	if kind == "" {
		kind = eventName
	}
	// GET 流没有原始请求体；响应对象携带的显式格式声明同样有效。
	stream.structured = stream.structured || redactionFormatType(root.Get("response.text.format.type"), true)
	outputIndex := redactionStreamIndex(root.Get("output_index"), 0)
	contentIndex := redactionStreamIndex(root.Get("content_index"), 0)
	textKey := "response/text/" + outputIndex + "/" + contentIndex
	toolKey := "response/tool/" + outputIndex
	switch kind {
	case "response.output_text.delta":
		return unaryRestoreField(root, "delta", func(value gjson.Result) error {
			return stream.addText(event, textKey, value, false)
		})
	case "response.custom_tool_call_input.delta":
		return unaryRestoreField(root, "delta", func(value gjson.Result) error {
			return stream.addPlainText(event, toolKey, value)
		})
	case "response.custom_tool_call_input.done":
		if err := stream.closeMatching(toolKey); err != nil {
			return err
		}
		return stream.direct(event, func(ctx *unaryRestoreContext) error { return ctx.customToolInput(root) })
	case "response.function_call_arguments.delta":
		return unaryRestoreField(root, "delta", func(value gjson.Result) error {
			return stream.addDocument(event, toolKey, value)
		})
	case "response.output_text.done":
		if err := stream.closeMatching(textKey); err != nil {
			return err
		}
		return stream.direct(event, func(ctx *unaryRestoreContext) error {
			return unaryRestoreField(root, "text", ctx.text)
		})
	case "response.function_call_arguments.done":
		if err := stream.closeMatching(toolKey); err != nil {
			return err
		}
		return stream.direct(event, func(ctx *unaryRestoreContext) error {
			return unaryRestoreField(root, "arguments", ctx.jsonDocument)
		})
	case "response.content_part.done":
		if err := stream.closeMatching(textKey); err != nil {
			return err
		}
		return stream.direct(event, func(ctx *unaryRestoreContext) error {
			return unaryRestoreField(root, "part", func(part gjson.Result) error {
				if part.Get("type").Str == "output_text" {
					return unaryRestoreField(part, "text", ctx.text)
				}
				return nil
			})
		})
	case "response.output_item.done", "response.output_item.added":
		if kind == "response.output_item.added" && root.Get("item.type").Str != "custom_tool_call" {
			return nil
		}
		if kind == "response.output_item.done" {
			if err := stream.closeMatching("response/text/" + outputIndex + "/"); err != nil {
				return err
			}
			if err := stream.closeMatching(toolKey); err != nil {
				return err
			}
		}
		return stream.direct(event, func(ctx *unaryRestoreContext) error {
			return unaryRestoreField(root, "item", func(item gjson.Result) error {
				switch item.Get("type").Str {
				case "custom_tool_call":
					return ctx.customToolInput(item)
				case "function_call":
					return ctx.arguments(item)
				case "message":
					return unaryRestoreField(item, "content", func(content gjson.Result) error {
						return unaryRestoreArray(content, func(part gjson.Result) error {
							if part.Get("type").Str == "output_text" {
								return unaryRestoreField(part, "text", ctx.text)
							}
							return nil
						})
					})
				}
				return nil
			})
		})
	case "response.completed", "response.incomplete":
		event.terminal = true
		if err := stream.closeMatching(""); err != nil {
			return err
		}
		restored, err := restoreUnaryBusinessFields(event.payload, protocol.OpenAIResponses, stream.restore, stream.structured)
		if err != nil {
			return errRedactionStream
		}
		if !bytes.Equal(restored, event.payload) {
			event.replacement = restored
		}
	case "response.failed":
		event.terminal = true
		if err := stream.closeMatching(""); err != nil {
			return err
		}
		return stream.maskErrorPayload(event, event.payload)
	}
	return nil
}

func (stream *redactionRestoreSSE) anthropic(event *redactionStreamEvent, root gjson.Result, eventName string) error {
	kind := root.Get("type").Str
	if kind == "" {
		kind = eventName
	}
	index := redactionStreamIndex(root.Get("index"), 0)
	prefix := "anthropic/" + index + "/"
	switch kind {
	case "content_block_start":
		return unaryRestoreField(root, "content_block", func(block gjson.Result) error {
			switch block.Get("type").Str {
			case "text":
				return unaryRestoreField(block, "text", func(value gjson.Result) error {
					return stream.addText(event, prefix+"text", value, false)
				})
			case "tool_use":
				return stream.direct(event, func(ctx *unaryRestoreContext) error {
					return unaryRestoreField(block, "input", ctx.jsonValue)
				})
			}
			return nil
		})
	case "content_block_delta":
		return unaryRestoreField(root, "delta", func(delta gjson.Result) error {
			switch delta.Get("type").Str {
			case "text_delta":
				return unaryRestoreField(delta, "text", func(value gjson.Result) error {
					return stream.addText(event, prefix+"text", value, false)
				})
			case "input_json_delta":
				return unaryRestoreField(delta, "partial_json", func(value gjson.Result) error {
					return stream.addDocument(event, prefix+"tool", value)
				})
			}
			return nil
		})
	case "content_block_stop":
		return stream.closeMatching(prefix)
	case "message_stop":
		event.terminal = true
		return stream.closeMatching("")
	}
	return nil
}

func (stream *redactionRestoreSSE) gemini(event *redactionStreamEvent, root gjson.Result) error {
	if root.Get("promptFeedback.blockReason").Str != "" {
		event.terminal = true
		if err := stream.closeMatching(""); err != nil {
			return err
		}
	}
	return unaryRestoreField(root, "candidates", func(candidates gjson.Result) error {
		candidatePosition := 0
		return unaryRestoreArray(candidates, func(candidate gjson.Result) error {
			candidateIndex := redactionStreamIndex(candidate.Get("index"), candidatePosition)
			candidatePosition++
			prefix := "gemini/" + candidateIndex + "/"
			if err := unaryRestoreField(candidate, "content", func(content gjson.Result) error {
				return unaryRestoreField(content, "parts", func(parts gjson.Result) error {
					return unaryRestoreArray(parts, func(part gjson.Result) error {
						// parts 是当前分片的列表，位置不能标识跨分片的文本。
						key := prefix + "answer"
						if part.Get("thought").Bool() {
							return nil
						}
						signed := part.Get("thoughtSignature").Exists()
						if err := unaryRestoreField(part, "text", func(value gjson.Result) error {
							return stream.addText(event, key+"/text", value, signed)
						}); err != nil {
							return err
						}
						before := len(event.direct)
						if err := stream.direct(event, func(ctx *unaryRestoreContext) error {
							return unaryRestoreField(part, "functionCall", func(call gjson.Result) error {
								return unaryRestoreField(call, "args", func(args gjson.Result) error {
									return ctx.walkJSONValues(args, 0, ctx.addPatch)
								})
							})
						}); err != nil {
							return err
						}
						if signed && len(event.direct) > before {
							return errRedactionStream
						}
						return nil
					})
				})
			}); err != nil {
				return err
			}
			if candidate.Get("finishReason").Str != "" {
				event.terminal = true
				return stream.closeMatching(prefix)
			}
			return nil
		})
	})
}
