package automodel

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"gpt-load/internal/protocol"
)

type TextMessage struct {
	Role string `json:"role"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type TaskState struct {
	CurrentTask        string        `json:"current_task"`
	RecentContext      []TextMessage `json:"recent_context"`
	ClientInstructions []TextMessage `json:"client_instructions"`
	InputFeatures      struct {
		HasTools          bool `json:"has_tools"`
		HasNonTextContent bool `json:"has_non_text_content"`
	} `json:"input_features"`
	ContextTruncated bool `json:"context_truncated"`
}

func Extract(value protocol.Protocol, body []byte) (TaskState, string) {
	state := TaskState{RecentContext: []TextMessage{}, ClientInstructions: []TextMessage{}}
	var raw map[string]any
	if json.Unmarshal(body, &raw) != nil {
		return state, "task_missing"
	}
	state.InputFeatures.HasTools = raw["tools"] != nil
	var messages []TextMessage
	add := func(role, kind, text string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		message := TextMessage{Role: role, Kind: kind, Text: text}
		if role == "system" || role == "developer" {
			state.ClientInstructions = append(state.ClientInstructions, message)
		} else {
			messages = append(messages, message)
		}
	}
	readContent := func(role string, content any) {
		text, tools, nonText := contentText(content)
		state.InputFeatures.HasNonTextContent = state.InputFeatures.HasNonTextContent || nonText
		kind := "message"
		if role == "tool" {
			state.InputFeatures.HasTools = true
			kind = "tool_result"
		}
		add(role, kind, text)
		for _, result := range tools {
			state.InputFeatures.HasTools = true
			add("tool", "tool_result", result)
		}
	}
	switch value {
	case protocol.OpenAICompletions, protocol.Anthropic:
		if value == protocol.Anthropic {
			readContent("system", raw["system"])
		}
		for _, item := range array(raw["messages"]) {
			message, _ := item.(map[string]any)
			role, _ := message["role"].(string)
			if message["tool_calls"] != nil {
				state.InputFeatures.HasTools = true
			}
			readContent(role, message["content"])
		}
	case protocol.OpenAIResponses:
		readContent("system", raw["instructions"])
		if input, ok := raw["input"].(string); ok {
			add("user", "message", input)
		} else {
			for _, item := range array(raw["input"]) {
				message, _ := item.(map[string]any)
				kind, _ := message["type"].(string)
				if kind == "function_call_output" {
					state.InputFeatures.HasTools = true
					text, _, _ := contentText(message["output"])
					add("tool", "tool_result", text)
					continue
				}
				if kind == "function_call" {
					state.InputFeatures.HasTools = true
					continue
				}
				if kind != "" && kind != "message" {
					continue
				}
				role, _ := message["role"].(string)
				if role == "" {
					role = "user"
				}
				readContent(role, message["content"])
			}
		}
	case protocol.Gemini:
		instruction, _ := raw["systemInstruction"].(map[string]any)
		if instruction == nil {
			instruction, _ = raw["system_instruction"].(map[string]any)
		}
		readContent("system", instruction["parts"])
		for _, item := range array(raw["contents"]) {
			message, _ := item.(map[string]any)
			role, _ := message["role"].(string)
			if role == "" {
				role = "user"
			}
			if role == "model" {
				role = "assistant"
			}
			readContent(role, message["parts"])
		}
	default:
		return state, "unsupported_operation"
	}
	latest, previous := -1, -1
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "user" && messages[index].Kind == "message" {
			if latest < 0 {
				latest = index
			} else {
				previous = index
				break
			}
		}
	}
	if latest < 0 {
		return state, "task_missing"
	}
	state.CurrentTask = messages[latest].Text
	base := TaskState{CurrentTask: state.CurrentTask}
	encoded, _ := json.Marshal(base)
	if len(encoded) > MaxStateBytes {
		return state, "task_too_large"
	}
	start := latest
	if previous >= 0 {
		start = previous
	}
	for index := start; index < len(messages); index++ {
		if index != latest {
			state.RecentContext = append(state.RecentContext, messages[index])
		}
	}
	if start > 0 {
		state.ContextTruncated = true
	}
	// 单段保留头尾；移除最早的背景，始终保留完整当前任务。
	for index := range state.RecentContext {
		text := clip(state.RecentContext[index].Text, 4096)
		if text != state.RecentContext[index].Text {
			state.ContextTruncated = true
		}
		state.RecentContext[index].Text = text
	}
	instructions := state.ClientInstructions
	state.ClientInstructions = []TextMessage{}
	for _, message := range instructions {
		originalText := message.Text
		fitted, ok := fitInstruction(state.ClientInstructions, message, 2048)
		if !ok {
			state.ContextTruncated = true
			continue
		}
		if fitted.Text != originalText {
			state.ContextTruncated = true
		}
		state.ClientInstructions = append(state.ClientInstructions, fitted)
	}
	// 序列化一次后按删除项的编码大小扣减，长工具循环不会反复编码整段历史。
	encodedSize := size(state)
	removeSize := func(message TextMessage, count int) {
		encodedSize -= size(message)
		if count > 1 {
			encodedSize--
		}
		if !state.ContextTruncated {
			encodedSize--
			state.ContextTruncated = true
		}
	}
	for encodedSize > MaxStateBytes && len(state.ClientInstructions) > 0 {
		removeSize(state.ClientInstructions[len(state.ClientInstructions)-1], len(state.ClientInstructions))
		state.ClientInstructions = state.ClientInstructions[:len(state.ClientInstructions)-1]
	}
	for encodedSize > MaxStateBytes && len(state.RecentContext) > 0 {
		removeSize(state.RecentContext[0], len(state.RecentContext))
		state.RecentContext = state.RecentContext[1:]
	}
	if encodedSize > MaxStateBytes {
		return state, "task_too_large"
	}
	return state, ""
}

func fitInstruction(existing []TextMessage, message TextMessage, budget int) (TextMessage, bool) {
	if size(append(existing, message)) <= budget {
		return message, true
	}
	low, high := len("\n[truncated]\n")+2, len(message.Text)
	var best TextMessage
	found := false
	for low <= high {
		limit := low + (high-low)/2
		candidate := message
		candidate.Text = clip(message.Text, limit)
		if size(append(existing, candidate)) <= budget {
			best, found, low = candidate, true, limit+1
		} else {
			high = limit - 1
		}
	}
	return best, found
}

func array(value any) []any { result, _ := value.([]any); return result }

func contentText(value any) (string, []string, bool) {
	if text, ok := value.(string); ok {
		return text, nil, false
	}
	var texts, tools []string
	nonText := false
	for _, item := range array(value) {
		part, _ := item.(map[string]any)
		kind, _ := part["type"].(string)
		if kind == "tool_result" {
			text, _, attachment := contentText(part["content"])
			tools = append(tools, text)
			nonText = nonText || attachment
			continue
		}
		if result, ok := part["functionResponse"].(map[string]any); ok {
			encoded, _ := json.Marshal(result["response"])
			tools = append(tools, string(encoded))
			continue
		}
		if part["functionCall"] != nil || kind == "tool_use" {
			tools = append(tools, "[tool call]")
			continue
		}
		if kind == "text" || kind == "input_text" || kind == "output_text" || kind == "" {
			if text, ok := part["text"].(string); ok {
				texts = append(texts, text)
				continue
			}
		}
		if kind == "thinking" || kind == "redacted_thinking" {
			continue
		}
		nonText = true
	}
	return strings.Join(texts, "\n"), tools, nonText
}

func size(value any) int { encoded, _ := json.Marshal(value); return len(encoded) }

func clip(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	half := (limit - len("\n[truncated]\n")) / 2
	head, tail := value[:half], value[len(value)-half:]
	for !utf8.ValidString(head) {
		head = head[:len(head)-1]
	}
	for !utf8.ValidString(tail) {
		tail = tail[1:]
	}
	return head + "\n[truncated]\n" + tail
}
