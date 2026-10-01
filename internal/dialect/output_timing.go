package dialect

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"gpt-load/internal/protocol"
)

const (
	maxOutputTimingKeys          = 4096
	maxOutputTimingKeyBytes      = 4096
	maxOutputTimingRetainedBytes = 256 << 10
)

// OutputTimingObserver 仅观察已交付的生成内容，不参与转发或重试决策。
type OutputTimingObserver struct {
	Protocol   protocol.Protocol
	seen       bool
	ended      bool
	overflowed bool
	keyBytes   int
	items      map[outputTimingKey]bool
}

type outputTimingKey struct {
	item  string
	part  string
	index string
}

func (observer *OutputTimingObserver) Observe(event StreamEvent) bool {
	if observer.ended {
		return false
	}
	if bytes.Equal(bytes.TrimSpace(event.Payload), []byte("[DONE]")) {
		observer.ended = true
		return false
	}
	object := outputObject(event.Payload)
	if object == nil {
		return false
	}
	name := event.Name
	if name == "" {
		name = outputString(object["type"])
	}
	if name == "error" || producedContentValue(object["error"]) {
		observer.ended = true
		return false
	}
	produced := false
	switch observer.Protocol {
	case protocol.OpenAICompletions:
		for _, choice := range outputObjects(object["choices"]) {
			delta := outputObject(choice["delta"])
			produced = produced || outputTexts(delta, "content", "reasoning_content", "reasoning", "refusal") ||
				outputTexts(outputObject(delta["function_call"]), "arguments")
			for _, call := range outputObjects(delta["tool_calls"]) {
				produced = produced || outputTexts(outputObject(call["function"]), "arguments")
			}
			for _, detail := range outputObjects(delta["reasoning_details"]) {
				produced = produced || outputTexts(detail, "text", "summary")
			}
		}
	case protocol.Anthropic:
		switch name {
		case "content_block_delta":
			produced = outputTexts(outputObject(object["delta"]), "text", "thinking", "partial_json")
		case "content_block_start":
			block := outputObject(object["content_block"])
			produced = outputTexts(block, "text", "thinking") || len(outputObject(block["input"])) > 0
		case "message_stop":
			observer.ended = true
		}
	case protocol.Gemini:
		for _, candidate := range outputObjects(object["candidates"]) {
			for _, part := range outputObjects(outputObject(candidate["content"])["parts"]) {
				produced = produced || outputTexts(part, "text") ||
					outputObject(outputObject(part["functionCall"])["args"]) != nil ||
					outputTexts(outputObject(part["executableCode"]), "code")
			}
		}
	case protocol.OpenAIResponses:
		produced = observer.observeResponses(name, object)
	}
	observer.seen = observer.seen || produced
	return produced
}

// Overflowed 表示观测状态超限，调用方必须丢弃此前采样的时点。
func (observer *OutputTimingObserver) Overflowed() bool {
	return observer.overflowed
}

func (observer *OutputTimingObserver) observeResponses(name string, object map[string]json.RawMessage) bool {
	switch name {
	case "response.completed", "response.done", "response.incomplete":
		// 完整快照仅在没有增量输出时提供首响；不能把重复快照当成末 token。
		observer.ended = true
		response := outputObject(object["response"])
		if !observer.seen && outputString(response["status"]) != "failed" {
			for _, item := range outputObjects(response["output"]) {
				if timingOutputItem(item) {
					return true
				}
			}
		}
		return false
	case "response.failed":
		observer.ended = true
		return false
	}

	key := outputTimingKey{item: string(object["output_index"])}
	if key.item == "" {
		key.item = outputString(object["item_id"])
		if key.item == "" {
			key.item = outputString(outputObject(object["item"])["id"])
		}
	}
	switch {
	case strings.HasPrefix(name, "response.reasoning_summary_"):
		key.part, key.index = "summary", string(object["summary_index"])
	case strings.HasPrefix(name, "response.output_text."), strings.HasPrefix(name, "response.reasoning_text."),
		strings.HasPrefix(name, "response.refusal."), strings.HasPrefix(name, "response.content_part."):
		key.part, key.index = "content", string(object["content_index"])
	}
	if key.part != "" && key.index == "" {
		key.index = "0"
	}

	produced, delta := false, false
	switch name {
	case "response.output_text.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta",
		"response.refusal.delta", "response.function_call_arguments.delta", "response.custom_tool_call_input.delta",
		"response.code_interpreter_call_code.delta":
		produced, delta = outputTexts(object, "delta"), true
	case "response.output_item.added", "response.output_item.done":
		produced = timingOutputItem(outputObject(object["item"]))
	case "response.content_part.added", "response.content_part.done",
		"response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
		produced = outputTexts(outputObject(object["part"]), "text", "refusal")
	case "response.output_text.done", "response.reasoning_text.done", "response.reasoning_summary_text.done":
		produced = outputTexts(object, "text")
	case "response.refusal.done":
		produced = outputTexts(object, "refusal")
	case "response.function_call_arguments.done":
		produced = outputTexts(object, "arguments")
	case "response.custom_tool_call_input.done":
		produced = outputTexts(object, "input")
	}
	if !produced || (!delta && observer.items[key]) {
		return false
	}
	// 片段独立去重，条目标记只用于排除其外层重复快照。
	if !observer.rememberOutput(key) || !observer.rememberOutput(outputTimingKey{item: key.item}) {
		return false
	}
	if name == "response.output_item.added" || name == "response.output_item.done" {
		item := outputObject(object["item"])
		for _, field := range []string{"content", "summary"} {
			for index, part := range outputObjects(item[field]) {
				if outputTexts(part, "text", "refusal") &&
					!observer.rememberOutput(outputTimingKey{item: key.item, part: field, index: strconv.Itoa(index)}) {
					return false
				}
			}
		}
	}
	return true
}

func (observer *OutputTimingObserver) rememberOutput(key outputTimingKey) bool {
	if observer.items[key] {
		return true
	}
	size := len(key.item) + len(key.part) + len(key.index)
	if size > maxOutputTimingKeyBytes || len(observer.items) >= maxOutputTimingKeys ||
		observer.keyBytes+size > maxOutputTimingRetainedBytes {
		observer.items = nil
		observer.keyBytes = 0
		observer.overflowed, observer.ended = true, true
		return false
	}
	if observer.items == nil {
		observer.items = make(map[outputTimingKey]bool)
	}
	observer.items[key] = true
	observer.keyBytes += size
	return true
}

func outputObject(raw json.RawMessage) map[string]json.RawMessage {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return nil
	}
	return object
}

func outputObjects(raw json.RawMessage) []map[string]json.RawMessage {
	var objects []map[string]json.RawMessage
	if json.Unmarshal(raw, &objects) != nil {
		return nil
	}
	return objects
}

func outputString(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

func outputTexts(object map[string]json.RawMessage, fields ...string) bool {
	for _, field := range fields {
		if outputString(object[field]) != "" {
			return true
		}
	}
	return false
}

func timingOutputItem(item map[string]json.RawMessage) bool {
	if outputTexts(item, "arguments", "input") {
		return true
	}
	for _, field := range []string{"content", "summary"} {
		for _, part := range outputObjects(item[field]) {
			if outputTexts(part, "text", "refusal") {
				return true
			}
		}
	}
	return false
}
