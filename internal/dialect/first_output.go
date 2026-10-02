package dialect

import (
	"bytes"
	"encoding/json"

	"gpt-load/internal/protocol"
)

// FirstOutputObserver 仅供自动选模观察首次交付的生成内容，不参与日志速度或调度决策。
type FirstOutputObserver struct {
	Protocol protocol.Protocol
	ended    bool
}

func (observer *FirstOutputObserver) Observe(event StreamEvent) bool {
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
	observer.ended = observer.ended || produced
	return produced
}

func (observer *FirstOutputObserver) observeResponses(name string, object map[string]json.RawMessage) bool {
	switch name {
	case "response.completed", "response.done", "response.incomplete":
		// 没有增量输出时，完整快照也可以提供首次生成内容。
		observer.ended = true
		response := outputObject(object["response"])
		if outputString(response["status"]) != "failed" {
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

	produced := false
	switch name {
	case "response.output_text.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta",
		"response.refusal.delta", "response.function_call_arguments.delta", "response.custom_tool_call_input.delta",
		"response.code_interpreter_call_code.delta":
		produced = outputTexts(object, "delta")
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
	return produced
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
