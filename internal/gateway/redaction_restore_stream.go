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
	signer        *redactionSigner
	structured    bool
	maxEventBytes int

	input                   []byte
	scanner                 sseRewriteBoundaryScanner
	queue                   []*redactionStreamEvent
	queueBytes              int
	texts                   map[string]*redactionStreamText
	documents               map[string]*redactionStreamDocument
	signedChannels          map[string]*redactionSignedChannel
	signedBytes             int
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
	handled     []unaryRestoreRange
	replacement []byte
	terminal    bool
	// whole 表示整条事件已完整处理（终态快照或错误遮蔽），不再做通用还原。
	whole bool
	// signatures 是本条事件里的协议签名位置，渲染时写入还原记录。
	signatures []redactionSignedBlock
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
		maxEventBytes:  maxEventBytes,
		texts:          make(map[string]*redactionStreamText),
		documents:      make(map[string]*redactionStreamDocument),
		signedChannels: make(map[string]*redactionSignedChannel),
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
		part, err := event.render(stream.maxEventBytes, stream.signer)
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
	stream.signedChannels = nil
	stream.terminalBoundaryPending = false
	return err
}

func (event *redactionStreamEvent) field(value gjson.Result) *redactionStreamField {
	field := &redactionStreamField{
		start: value.Index, end: value.Index + len(value.Raw),
		original: value.Str, value: value.Str,
	}
	event.fields = append(event.fields, field)
	event.handled = append(event.handled, unaryRestoreRange{field.start, field.end})
	return field
}

func (event *redactionStreamEvent) render(maxEventBytes int, signer *redactionSigner) ([]byte, error) {
	if len(event.fields) == 0 && len(event.direct) == 0 && event.replacement == nil && len(event.signatures) == 0 {
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
		if len(patches) > 0 {
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
		// 签名记录依赖事件内正文的最终结果，必须在其他改写之后生成。
		signed, err := signer.signPayload(event.payload, payload, event.signatures)
		if err != nil {
			return nil, errRedactionStream
		}
		payload = signed
		if bytes.Equal(payload, event.payload) {
			return event.raw, nil
		}
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
	var err error
	switch stream.protocol {
	case protocol.OpenAICompletions:
		err = stream.chat(event, root)
	case protocol.OpenAIResponses:
		err = stream.responses(event, root, name)
	case protocol.Anthropic:
		err = stream.anthropic(event, root, name)
	case protocol.Gemini:
		err = stream.gemini(event, root)
	default:
		return nil
	}
	if err != nil || event.whole {
		return err
	}
	return stream.restoreRemaining(event, root)
}

// restoreRemaining 还原事件里增量通道之外的字段，只处理事件内完整的密文，不扣留数据。
func (stream *redactionRestoreSSE) restoreRemaining(event *redactionStreamEvent, root gjson.Result) error {
	if !bytes.Contains(event.payload, []byte(redactionStreamPrefix)) && !bytes.Contains(event.payload, []byte(`\u`)) {
		return nil
	}
	ctx := unaryRestoreContext{body: event.payload, restore: stream.restore, handled: event.handled}
	if err := ctx.restoreRemaining(root); err != nil {
		return errRedactionStream
	}
	event.direct = append(event.direct, ctx.patches...)
	return nil
}

func (stream *redactionRestoreSSE) maskErrorPayload(event *redactionStreamEvent, payload []byte) error {
	event.whole = true
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

func (stream *redactionRestoreSSE) addText(event *redactionStreamEvent, key string, value gjson.Result) error {
	if value.Type != gjson.String {
		return nil
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
	stream.recordChannel(key, value.Str, field.value)
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
	stream.recordChannel(key, value.Str, restored)
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
		tail, err := state.token.finish(stream.restore)
		if err != nil {
			return err
		}
		state.last.value += tail
		stream.recordChannel(key, "", tail)
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
			stream.recordChannel(key, "", tail)
		}
		delete(stream.documents, key)
	}
	return nil
}

// releaseAmbiguousTail 在转入工具调用时放行只像密文开头的文本尾巴（g、gl、gld、gld1），
// 避免它扣住后续工具调用；已开始的密文仍保持扣留，正文与工具调用交错输出时也能完整还原。
func (stream *redactionRestoreSSE) releaseAmbiguousTail(key string) error {
	state := stream.texts[key]
	if state == nil || state.token.prefix >= len(redactionStreamPrefix) {
		return nil
	}
	return stream.closeMatching(key)
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
	event.handled = append(event.handled, ctx.handled...)
	return nil
}

func (stream *redactionRestoreSSE) chat(event *redactionStreamEvent, root gjson.Result) error {
	return unaryRestoreField(root, "choices", func(choices gjson.Result) error {
		index := 0
		return unaryRestoreArray(choices, func(choice gjson.Result) error {
			choicePath := "choices." + strconv.Itoa(index) + ".delta"
			choiceIndex := redactionStreamIndex(choice.Get("index"), index)
			index++
			prefix := "chat/" + choiceIndex + "/"
			if err := unaryRestoreField(choice, "delta", func(delta gjson.Result) error {
				if err := unaryRestoreField(delta, "content", func(value gjson.Result) error {
					return stream.addText(event, prefix+"text", value)
				}); err != nil {
					return err
				}
				for _, name := range []string{"refusal", "reasoning_content", "reasoning"} {
					if err := unaryRestoreField(delta, name, func(value gjson.Result) error {
						return stream.addPlainText(event, prefix+name, value)
					}); err != nil {
						return err
					}
				}
				if err := unaryRestoreField(delta, "reasoning_details", func(details gjson.Result) error {
					position := 0
					return unaryRestoreArray(details, func(detail gjson.Result) error {
						key := prefix + "reasoning_details/" + redactionStreamIndex(detail.Get("index"), position) + "/"
						detailPath := choicePath + ".reasoning_details." + strconv.Itoa(position)
						position++
						for _, name := range []string{"text", "summary"} {
							if err := unaryRestoreField(detail, name, func(value gjson.Result) error {
								return stream.addPlainText(event, key+name, value)
							}); err != nil {
								return err
							}
						}
						if !redactionSignaturePresent(detail.Get("signature")) {
							return nil
						}
						// 签名在该条推理结束后下发：先收尾，记录里才包含该条全部正文。
						if err := stream.closeMatching(key); err != nil {
							return err
						}
						stream.signAt(event, redactionSignedBlock{
							block: detailPath, signature: []any{"signature"}, streamed: true,
							values: stream.channelValues(
								redactionChannelField{[]any{"text"}, key + "text"},
								redactionChannelField{[]any{"summary"}, key + "summary"},
							),
						})
						return nil
					})
				}); err != nil {
					return err
				}
				// 推理不会再续写：正文开始时收尾推理，避免推理末尾的疑似前缀扣住后续正文。
				if delta.Get("content").Str != "" || delta.Get("refusal").Str != "" ||
					delta.Get("tool_calls").IsArray() || delta.Get("function_call").IsObject() {
					for _, name := range []string{"reasoning_content", "reasoning", "reasoning_details/"} {
						if err := stream.closeMatching(prefix + name); err != nil {
							return err
						}
					}
				}
				if delta.Get("tool_calls").IsArray() || delta.Get("function_call").IsObject() {
					if err := stream.releaseAmbiguousTail(prefix + "text"); err != nil {
						return err
					}
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
						callPath := choicePath + ".tool_calls." + strconv.Itoa(callIndex)
						callIndex++
						if err := unaryRestoreField(call, "function", func(function gjson.Result) error {
							return unaryRestoreField(function, "arguments", func(value gjson.Result) error {
								return stream.addDocument(event, key, value)
							})
						}); err != nil {
							return err
						}
						if err := unaryRestoreField(call, "custom", func(custom gjson.Result) error {
							return unaryRestoreField(custom, "input", func(value gjson.Result) error {
								return stream.addPlainText(event, key, value)
							})
						}); err != nil {
							return err
						}
						// Gemini 兼容接口把签名放在工具调用的 extra_content 里，参数通常随签名一次下发。
						if call.Get("extra_content.google.thought_signature").Exists() {
							block := chatToolCallSignedBlock(callPath)
							block.streamed = true
							block.values = stream.channelValues(redactionChannelField{[]any{"function", "arguments"}, key})
							stream.signAt(event, block)
						}
						return nil
					})
				})
			}); err != nil {
				return err
			}
			if reason := choice.Get("finish_reason"); reason.Exists() &&
				reason.Type == gjson.String && reason.Str != "" {
				if err := stream.closeMatching(prefix); err != nil {
					return err
				}
				stream.dropSignedChannels(prefix)
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
	refusalKey := "response/refusal/" + outputIndex + "/" + contentIndex
	summaryKey := "response/reasoning/" + outputIndex + "/summary/" + redactionStreamIndex(root.Get("summary_index"), 0)
	reasoningKey := "response/reasoning/" + outputIndex + "/content/" + contentIndex
	switch kind {
	case "response.output_text.delta":
		return unaryRestoreField(root, "delta", func(value gjson.Result) error {
			return stream.addText(event, textKey, value)
		})
	case "response.refusal.delta":
		return unaryRestoreField(root, "delta", func(value gjson.Result) error {
			return stream.addPlainText(event, refusalKey, value)
		})
	case "response.refusal.done":
		if err := stream.closeMatching(refusalKey); err != nil {
			return err
		}
		return stream.direct(event, func(ctx *unaryRestoreContext) error {
			return unaryRestoreField(root, "refusal", ctx.plainText)
		})
	case "response.reasoning_summary_text.delta":
		if err := unaryRestoreField(root, "delta", func(value gjson.Result) error {
			return stream.addPlainText(event, summaryKey, value)
		}); err != nil {
			return err
		}
		return stream.signReasoningDelta(event, root, summaryKey)
	case "response.reasoning_summary_text.done":
		if err := stream.closeMatching(summaryKey); err != nil {
			return err
		}
		return stream.direct(event, func(ctx *unaryRestoreContext) error {
			return unaryRestoreField(root, "text", ctx.plainText)
		})
	case "response.reasoning_summary_part.done":
		if err := stream.closeMatching(summaryKey); err != nil {
			return err
		}
		return stream.direct(event, func(ctx *unaryRestoreContext) error {
			return unaryRestoreField(root, "part", func(part gjson.Result) error {
				if part.Get("type").Str == "summary_text" {
					return unaryRestoreField(part, "text", ctx.plainText)
				}
				return nil
			})
		})
	case "response.reasoning_text.delta":
		if err := unaryRestoreField(root, "delta", func(value gjson.Result) error {
			return stream.addPlainText(event, reasoningKey, value)
		}); err != nil {
			return err
		}
		return stream.signReasoningDelta(event, root, reasoningKey)
	case "response.reasoning_text.done":
		if err := stream.closeMatching(reasoningKey); err != nil {
			return err
		}
		return stream.direct(event, func(ctx *unaryRestoreContext) error {
			return unaryRestoreField(root, "text", ctx.plainText)
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
	case "response.content_part.added":
		stream.signReasoningPart(event, root)
	case "response.content_part.done":
		stream.signReasoningPart(event, root)
		if err := stream.closeMatching(textKey); err != nil {
			return err
		}
		if err := stream.closeMatching(refusalKey); err != nil {
			return err
		}
		return stream.direct(event, func(ctx *unaryRestoreContext) error {
			return unaryRestoreField(root, "part", func(part gjson.Result) error {
				switch part.Get("type").Str {
				case "output_text":
					return unaryRestoreField(part, "text", ctx.text)
				case "refusal":
					return unaryRestoreField(part, "refusal", ctx.plainText)
				default:
					return nil
				}
			})
		})
	case "response.output_item.done", "response.output_item.added":
		for _, block := range responsesReasoningSignedBlocks("item", root.Get("item")) {
			stream.signAt(event, block)
		}
		if kind == "response.output_item.added" && root.Get("item.type").Str != "custom_tool_call" {
			return nil
		}
		if kind == "response.output_item.done" {
			defer stream.dropSignedChannels("response/reasoning/" + outputIndex + "/")
			if err := stream.closeMatching("response/text/" + outputIndex + "/"); err != nil {
				return err
			}
			if err := stream.closeMatching(toolKey); err != nil {
				return err
			}
			if err := stream.closeMatching("response/refusal/" + outputIndex + "/"); err != nil {
				return err
			}
			if err := stream.closeMatching("response/reasoning/" + outputIndex + "/"); err != nil {
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
				case "reasoning":
					return ctx.responsesReasoning(item)
				case "message":
					return unaryRestoreField(item, "content", func(content gjson.Result) error {
						return unaryRestoreArray(content, func(part gjson.Result) error {
							switch part.Get("type").Str {
							case "output_text":
								return unaryRestoreField(part, "text", ctx.text)
							case "refusal":
								return unaryRestoreField(part, "refusal", ctx.plainText)
							default:
								return nil
							}
						})
					})
				}
				return nil
			})
		})
	case "response.completed", "response.incomplete":
		event.terminal = true
		event.whole = true
		if err := stream.closeMatching(""); err != nil {
			return err
		}
		stream.dropSignedChannels("")
		restored, err := restoreUnaryBusinessFields(event.payload, protocol.OpenAIResponses, stream.restore, stream.structured, stream.signer)
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

// signReasoningDelta 处理转换通道在推理增量事件上附带的签名（非 OpenAI 标准字段）：
// 签名对应该推理通道累计的全部正文。
func (stream *redactionRestoreSSE) signReasoningDelta(event *redactionStreamEvent, root gjson.Result, key string) error {
	if !redactionSignaturePresent(root.Get("signature")) {
		return nil
	}
	if err := stream.closeMatching(key); err != nil {
		return err
	}
	stream.signAt(event, redactionSignedBlock{
		signature: []any{"signature"}, streamed: true,
		values: stream.channelValues(redactionChannelField{[]any{"text"}, key}),
	})
	return nil
}

func (stream *redactionRestoreSSE) signReasoningPart(event *redactionStreamEvent, root gjson.Result) {
	if root.Get("part.type").Str == "reasoning_text" {
		stream.signAt(event, redactionSignedBlock{block: "part", signature: []any{"signature"}})
	}
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
					return stream.addText(event, prefix+"text", value)
				})
			case "thinking":
				if err := unaryRestoreField(block, "thinking", func(value gjson.Result) error {
					return stream.addPlainText(event, prefix+"thinking", value)
				}); err != nil {
					return err
				}
				stream.signAt(event, redactionSignedBlock{block: "content_block", signature: []any{"signature"}})
				return nil
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
					return stream.addText(event, prefix+"text", value)
				})
			case "input_json_delta":
				return unaryRestoreField(delta, "partial_json", func(value gjson.Result) error {
					return stream.addDocument(event, prefix+"tool", value)
				})
			case "thinking_delta":
				return unaryRestoreField(delta, "thinking", func(value gjson.Result) error {
					return stream.addPlainText(event, prefix+"thinking", value)
				})
			case "signature_delta":
				// 签名在思考结束后下发：先收尾该块思考，记录里才包含该块全部正文。
				if err := stream.closeMatching(prefix); err != nil {
					return err
				}
				stream.signAt(event, redactionSignedBlock{
					block: "delta", signature: []any{"signature"}, streamed: true,
					values: stream.channelValues(redactionChannelField{[]any{"thinking"}, prefix + "thinking"}),
				})
			}
			return nil
		})
	case "content_block_stop":
		if err := stream.closeMatching(prefix); err != nil {
			return err
		}
		stream.dropSignedChannels(prefix)
	case "message_stop":
		event.terminal = true
		if err := stream.closeMatching(""); err != nil {
			return err
		}
		stream.dropSignedChannels("")
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
			partsPath := "candidates." + strconv.Itoa(candidatePosition) + ".content.parts."
			candidateIndex := redactionStreamIndex(candidate.Get("index"), candidatePosition)
			candidatePosition++
			prefix := "gemini/" + candidateIndex + "/"
			if err := unaryRestoreField(candidate, "content", func(content gjson.Result) error {
				return unaryRestoreField(content, "parts", func(parts gjson.Result) error {
					partPosition := 0
					return unaryRestoreArray(parts, func(part gjson.Result) error {
						// parts 是当前分片的列表，位置不能标识跨分片的文本。
						key := prefix + "answer"
						// 带签名的 part 同样还原，签名里记下还原过的片段，下一轮逐字节换回上游原文。
						for _, block := range geminiSignedBlocks(partsPath+strconv.Itoa(partPosition), part) {
							stream.signAt(event, block)
						}
						partPosition++
						if part.Get("thought").Bool() {
							return unaryRestoreField(part, "text", func(value gjson.Result) error {
								return stream.addPlainText(event, prefix+"thought/text", value)
							})
						}
						// 思考不会再续写：答案开始时收尾思考，避免思考末尾的疑似前缀扣住后续答案。
						if err := stream.closeMatching(prefix + "thought/text"); err != nil {
							return err
						}
						if part.Get("functionCall").Exists() {
							if err := stream.releaseAmbiguousTail(key + "/text"); err != nil {
								return err
							}
						}
						if err := unaryRestoreField(part, "text", func(value gjson.Result) error {
							return stream.addText(event, key+"/text", value)
						}); err != nil {
							return err
						}
						return stream.direct(event, func(ctx *unaryRestoreContext) error {
							return unaryRestoreField(part, "functionCall", func(call gjson.Result) error {
								return unaryRestoreField(call, "args", ctx.jsonValue)
							})
						})
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
