package automodel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"gpt-load/internal/protocol"
)

const (
	ExecutionPhaseUserTask         = "user_task"
	ExecutionPhaseToolContinuation = "tool_continuation"
	ExecutionPhaseOther            = "other"
	maxClientInstructionBytes      = 1 << 10
	maxRecentConversationBytes     = 2 << 10
	maxRecentToolBytes             = 1 << 10
)

type TextMessage struct {
	Role string `json:"role"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type TaskState struct {
	ExecutionPhase     string        `json:"execution_phase"`
	TaskFingerprint    string        `json:"-"`
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
	state := TaskState{ExecutionPhase: ExecutionPhaseOther, RecentContext: []TextMessage{}, ClientInstructions: []TextMessage{}}
	var raw map[string]any
	if json.Unmarshal(body, &raw) != nil {
		return state, "task_missing"
	}
	state.InputFeatures.HasTools = raw["tools"] != nil
	var messages []TextMessage
	latestTaskMessage := -1
	var taskPrefix any
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
	readContent := func(role string, content any) (bool, bool, int) {
		text, tools, nonText := contentText(content)
		state.InputFeatures.HasNonTextContent = state.InputFeatures.HasNonTextContent || nonText
		before := len(messages)
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
		isTask := role == "user" && (strings.TrimSpace(text) != "" || nonText)
		isTool := role == "tool" || len(tools) > 0
		textIndex := -1
		if isTask && strings.TrimSpace(text) != "" {
			textIndex = before
		}
		return isTask, isTool, textIndex
	}
	switch value {
	case protocol.OpenAICompletions, protocol.Anthropic:
		if value == protocol.Anthropic {
			readContent("system", raw["system"])
		}
		items := array(raw["messages"])
		for index, item := range items {
			message, _ := item.(map[string]any)
			role, _ := message["role"].(string)
			if message["tool_calls"] != nil {
				state.InputFeatures.HasTools = true
			}
			isTask, isTool, textIndex := readContent(role, message["content"])
			if isTask {
				state.ExecutionPhase, latestTaskMessage = ExecutionPhaseUserTask, textIndex
				taskPrefix = items[:index+1]
			} else if isTool {
				state.ExecutionPhase = ExecutionPhaseToolContinuation
			}
		}
	case protocol.OpenAIResponses:
		readContent("system", raw["instructions"])
		if input, ok := raw["input"].(string); ok {
			if strings.TrimSpace(input) != "" {
				add("user", "message", input)
				state.ExecutionPhase, latestTaskMessage, taskPrefix = ExecutionPhaseUserTask, len(messages)-1, input
			}
		} else {
			items := array(raw["input"])
			for index, item := range items {
				message, _ := item.(map[string]any)
				kind, _ := message["type"].(string)
				if kind == "function_call_output" || kind == "custom_tool_call_output" {
					state.InputFeatures.HasTools = true
					text, _, _ := contentText(message["output"])
					add("tool", "tool_result", text)
					state.ExecutionPhase = ExecutionPhaseToolContinuation
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
				isTask, isTool, textIndex := readContent(role, message["content"])
				if isTask {
					state.ExecutionPhase, latestTaskMessage = ExecutionPhaseUserTask, textIndex
					taskPrefix = items[:index+1]
				} else if isTool {
					state.ExecutionPhase = ExecutionPhaseToolContinuation
				}
			}
		}
	case protocol.Gemini:
		instruction, _ := raw["systemInstruction"].(map[string]any)
		if instruction == nil {
			instruction, _ = raw["system_instruction"].(map[string]any)
		}
		readContent("system", instruction["parts"])
		items := array(raw["contents"])
		for index, item := range items {
			message, _ := item.(map[string]any)
			role, _ := message["role"].(string)
			if role == "" {
				role = "user"
			}
			if role == "model" {
				role = "assistant"
			}
			isTask, isTool, textIndex := readContent(role, message["parts"])
			if isTask {
				state.ExecutionPhase, latestTaskMessage = ExecutionPhaseUserTask, textIndex
				taskPrefix = items[:index+1]
			} else if isTool {
				state.ExecutionPhase = ExecutionPhaseToolContinuation
			}
		}
	default:
		return state, "unsupported_operation"
	}
	state.TaskFingerprint = fingerprintTask(value, raw, taskPrefix)
	if state.ExecutionPhase == ExecutionPhaseUserTask && latestTaskMessage < 0 {
		return state, "non_text_task"
	}
	latest, previous := latestTaskMessage, -1
	for index := latest - 1; index >= 0; index-- {
		if messages[index].Role == "user" && messages[index].Kind == "message" {
			previous = index
			break
		}
	}
	if latest < 0 {
		return state, "task_missing"
	}
	state.CurrentTask = messages[latest].Text
	// 给其他证据和 JSON 包装预留预算；只裁剪决策副本，保留任务头尾。
	const taskBudget = MaxStateBytes - maxClientInstructionBytes - maxRecentConversationBytes - maxRecentToolBytes - 512
	if size(messages[latest]) > taskBudget {
		fitted, ok := fitTextMessage(messages[latest], taskBudget)
		if !ok {
			return state, "task_too_large"
		}
		state.CurrentTask = fitted.Text
		state.ContextTruncated = true
	}
	start := latest
	if previous >= 0 {
		start = previous
	}
	var recent []TextMessage
	for index := start; index < len(messages); index++ {
		if index != latest {
			recent = append(recent, messages[index])
		}
	}
	if start > 0 {
		state.ContextTruncated = true
	}
	var truncated bool
	state.RecentContext, truncated = compactRecentContext(recent)
	if truncated {
		state.ContextTruncated = true
	}
	state.ClientInstructions, truncated = compactFair(state.ClientInstructions, maxClientInstructionBytes)
	if truncated {
		state.ContextTruncated = true
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

func fingerprintTask(value protocol.Protocol, raw map[string]any, taskPrefix any) string {
	if taskPrefix == nil {
		return ""
	}
	material := map[string]any{"protocol": value, "prompt_version": PromptVersion, "task_prefix": taskPrefix}
	for _, key := range []string{"instructions", "system", "systemInstruction", "system_instruction", "tools", "tool_choice", "parallel_tool_calls"} {
		if field, exists := raw[key]; exists {
			material[key] = field
		}
	}
	encoded, err := json.Marshal(material)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

type indexedMessage struct {
	index   int
	message TextMessage
}

func compactRecentContext(messages []TextMessage) ([]TextMessage, bool) {
	regular, tools := []indexedMessage{}, []indexedMessage{}
	for index, message := range messages {
		item := indexedMessage{index: index, message: message}
		if message.Kind == "tool_result" {
			tools = append(tools, item)
		} else {
			regular = append(regular, item)
		}
	}
	selected := map[int]TextMessage{}
	regularSelected, regularTruncated := compactNewest(regular, maxRecentConversationBytes)
	toolSelected, toolTruncated := compactNewest(tools, maxRecentToolBytes)
	for index, message := range regularSelected {
		selected[index] = message
	}
	for index, message := range toolSelected {
		selected[index] = message
	}
	result := make([]TextMessage, 0, len(selected))
	for index := range messages {
		if message, exists := selected[index]; exists {
			result = append(result, message)
		}
	}
	return result, regularTruncated || toolTruncated
}

func compactNewest(messages []indexedMessage, budget int) (map[int]TextMessage, bool) {
	selected := map[int]TextMessage{}
	used, count, truncated := 2, 0, false
	for index := len(messages) - 1; index >= 0; index-- {
		item := messages[index]
		separator := 0
		if count > 0 {
			separator = 1
		}
		remaining := budget - used - separator
		if remaining <= 0 {
			truncated = true
			continue
		}
		message := item.message
		if size(message) > remaining {
			var ok bool
			message, ok = fitTextMessage(message, remaining)
			if !ok {
				truncated = true
				continue
			}
			truncated = true
		}
		selected[item.index] = message
		used += separator + size(message)
		count++
	}
	return selected, truncated
}

func compactFair(messages []TextMessage, budget int) ([]TextMessage, bool) {
	if size(messages) <= budget {
		return messages, false
	}
	if len(messages) == 0 || budget <= 2+len(messages)-1 {
		return []TextMessage{}, len(messages) > 0
	}
	available := budget - 2 - (len(messages) - 1)
	allocations := make([]int, len(messages))
	active := make(map[int]struct{}, len(messages))
	for index := range messages {
		active[index] = struct{}{}
	}
	for len(active) > 0 {
		share := available / len(active)
		settled := false
		for index := range active {
			amount := size(messages[index])
			if amount <= share {
				allocations[index] = amount
				available -= amount
				delete(active, index)
				settled = true
			}
		}
		if settled {
			continue
		}
		for index := range active {
			allocations[index] = share
		}
		break
	}
	result := make([]TextMessage, 0, len(messages))
	for index, message := range messages {
		if size(message) <= allocations[index] {
			result = append(result, message)
			continue
		}
		fitted, ok := fitTextMessage(message, allocations[index])
		if ok {
			result = append(result, fitted)
		}
	}
	return result, true
}

func fitTextMessage(message TextMessage, budget int) (TextMessage, bool) {
	low, high := len("\n[truncated]\n")+2, len(message.Text)
	var best TextMessage
	found := false
	for low <= high {
		limit := low + (high-low)/2
		candidate := message
		candidate.Text = clip(message.Text, limit)
		if size(candidate) <= budget {
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
