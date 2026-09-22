package dialect

import (
	"bytes"
	"encoding/json"
	"strings"
)

var (
	_ StreamContentClassifier = (*OpenAI)(nil)
	_ StreamContentClassifier = (*Anthropic)(nil)
	_ StreamContentClassifier = (*Gemini)(nil)
	_ StreamContentClassifier = (*OpenAIResponses)(nil)
)

// 以下判定一律宽松：载荷形状无法识别时按「有产出」处理。宁可漏判一次空回，
// 也不能把有内容的响应当成空回丢弃并重试。

func (*OpenAI) ClassifyStreamContent(event StreamEvent) StreamContent {
	var object struct {
		Choices json.RawMessage `json:"choices"`
	}
	if json.Unmarshal(event.Payload, &object) != nil {
		return StreamContent{Produced: !bytes.Equal(bytes.TrimSpace(event.Payload), []byte("[DONE]"))}
	}
	produced, explainedStop := openAIChoicesOutcome(object.Choices)
	return StreamContent{Produced: produced, ExplainedStop: explainedStop}
}

func (*Anthropic) ClassifyStreamContent(event StreamEvent) StreamContent {
	var object struct {
		Type         string          `json:"type"`
		ContentBlock json.RawMessage `json:"content_block"`
		Delta        json.RawMessage `json:"delta"`
	}
	if json.Unmarshal(event.Payload, &object) != nil {
		return StreamContent{Produced: true}
	}
	eventType := event.Name
	if eventType == "" {
		eventType = object.Type
	}
	switch eventType {
	case "content_block_start":
		return StreamContent{Produced: anthropicBlockProducedContent(object.ContentBlock)}
	case "content_block_delta":
		return StreamContent{Produced: anthropicDeltaProducedContent(object.Delta)}
	case "message_delta":
		var delta struct {
			StopReason string `json:"stop_reason"`
		}
		if json.Unmarshal(object.Delta, &delta) != nil {
			return StreamContent{}
		}
		return StreamContent{ExplainedStop: anthropicExplainedStop(delta.StopReason)}
	default:
		return StreamContent{}
	}
}

func (*Gemini) ClassifyStreamContent(event StreamEvent) StreamContent {
	var object struct {
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
		Candidates []struct {
			FinishReason string          `json:"finishReason"`
			Content      json.RawMessage `json:"content"`
		} `json:"candidates"`
	}
	if json.Unmarshal(event.Payload, &object) != nil {
		return StreamContent{Produced: true}
	}
	// 终态候选仍可能在同一 chunk 内携带最后一段产出。
	content := StreamContent{ExplainedStop: object.PromptFeedback.BlockReason != ""}
	for _, candidate := range object.Candidates {
		if geminiPartsProducedContent(candidate.Content) {
			content.Produced = true
		}
		if geminiExplainedStop(candidate.FinishReason) {
			content.ExplainedStop = true
		}
	}
	return content
}

// ClassifyStreamContent 对所有 `.delta` 事件按其 delta 载荷判断，output item
// 事件按条目内容判断，其余是协议前导。
func (*OpenAIResponses) ClassifyStreamContent(event StreamEvent) StreamContent {
	var object map[string]json.RawMessage
	if json.Unmarshal(event.Payload, &object) != nil {
		return StreamContent{Produced: true}
	}
	eventType := event.Name
	if eventType == "" {
		_ = json.Unmarshal(object["type"], &eventType)
	}
	switch {
	case strings.HasSuffix(eventType, ".delta"):
		return StreamContent{Produced: producedContentValue(object["delta"])}
	case eventType == "response.output_item.added", eventType == "response.output_item.done":
		return StreamContent{Produced: responsesItemProducedContent(object["item"])}
	default:
		return StreamContent{}
	}
}

// producedContentValue 判断一个 JSON 值是否承载了实际产出。缺失、null、空字符串、
// 空数组与空对象都视为没有产出。
func producedContentValue(raw json.RawMessage) bool {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 {
		return false
	}
	switch string(value) {
	case "null", `""`, "[]", "{}":
		return false
	}
	return true
}

// anyProducedContentValue 在任一字段有产出时返回 true。
func anyProducedContentValue(values ...json.RawMessage) bool {
	for _, value := range values {
		if producedContentValue(value) {
			return true
		}
	}
	return false
}

// openAIDeltaProducedContent 判定 Chat Completions 增量是否带来产出。兼容渠道会
// 扩展出图像、引用等非标准字段，因此除 role 之外任一字段有值都视为产出：宁可
// 漏判一次空回，也不能把有内容的响应当成空回丢弃并重试。
func openAIDeltaProducedContent(delta map[string]json.RawMessage) bool {
	for key, value := range delta {
		if key != "role" && producedContentValue(value) {
			return true
		}
	}
	return false
}

// openAIChoicesOutcome 宽松地读取 choices 的产出与终止原因。分类器对所有流生效，
// 内容判定不能收紧协议校验：形状无法识别时照常转发，并按有产出处理以免误判空回。
func openAIChoicesOutcome(raw json.RawMessage) (produced bool, explainedStop bool) {
	var choices []struct {
		Delta        json.RawMessage `json:"delta"`
		FinishReason json.RawMessage `json:"finish_reason"`
	}
	if json.Unmarshal(raw, &choices) != nil {
		return producedContentValue(raw), false
	}
	for _, choice := range choices {
		var delta map[string]json.RawMessage
		if json.Unmarshal(choice.Delta, &delta) != nil {
			if producedContentValue(choice.Delta) {
				produced = true
			}
		} else if openAIDeltaProducedContent(delta) {
			produced = true
		}
		var reason string
		if json.Unmarshal(choice.FinishReason, &reason) == nil && reason != "" && reason != "stop" {
			explainedStop = true
		}
	}
	return produced, explainedStop
}

// anthropicBlockProducedContent 判定 content_block_start 是否已经带来产出。
// 空文本块只是占位；工具调用与其他非文本块本身即产出。
func anthropicBlockProducedContent(raw json.RawMessage) bool {
	if !producedContentValue(raw) {
		return false
	}
	var block struct {
		Type     string          `json:"type"`
		Text     json.RawMessage `json:"text"`
		Thinking json.RawMessage `json:"thinking"`
	}
	if json.Unmarshal(raw, &block) != nil {
		return false
	}
	switch block.Type {
	case "text":
		return producedContentValue(block.Text)
	case "thinking":
		return producedContentValue(block.Thinking)
	case "":
		return false
	default:
		// 工具调用、脱敏推理等块本身即产出。
		return true
	}
}

// anthropicExplainedStop 判定 stop_reason 是否为自然结束之外的原因。
func anthropicExplainedStop(reason string) bool {
	switch reason {
	case "", "end_turn", "stop_sequence":
		return false
	default:
		return true
	}
}

// geminiExplainedStop 判定 finishReason 是否为自然结束之外的原因。
func geminiExplainedStop(reason string) bool {
	switch reason {
	case "", "STOP", "FINISH_REASON_UNSPECIFIED":
		return false
	default:
		return true
	}
}

// anthropicDeltaProducedContent 判定 content_block_delta 是否携带增量产出。
func anthropicDeltaProducedContent(raw json.RawMessage) bool {
	if !producedContentValue(raw) {
		return false
	}
	var delta struct {
		Text        json.RawMessage `json:"text"`
		Thinking    json.RawMessage `json:"thinking"`
		PartialJSON json.RawMessage `json:"partial_json"`
		Data        json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &delta) != nil {
		return false
	}
	return anyProducedContentValue(delta.Text, delta.Thinking, delta.PartialJSON, delta.Data)
}

// geminiPartsProducedContent 判定候选内容中是否存在实际产出的 part。
func geminiPartsProducedContent(raw json.RawMessage) bool {
	if !producedContentValue(raw) {
		return false
	}
	var content struct {
		Parts []struct {
			Text           json.RawMessage `json:"text"`
			FunctionCall   json.RawMessage `json:"functionCall"`
			InlineData     json.RawMessage `json:"inlineData"`
			FileData       json.RawMessage `json:"fileData"`
			ExecutableCode json.RawMessage `json:"executableCode"`
			CodeResult     json.RawMessage `json:"codeExecutionResult"`
		} `json:"parts"`
	}
	if json.Unmarshal(raw, &content) != nil {
		return false
	}
	for _, part := range content.Parts {
		if anyProducedContentValue(
			part.Text,
			part.FunctionCall,
			part.InlineData,
			part.FileData,
			part.ExecutableCode,
			part.CodeResult,
		) {
			return true
		}
	}
	return false
}

// responsesItemProducedContent 判定 Responses output item 是否构成产出。
// reasoning item 只是推理占位，其摘要通过独立的 delta 事件产出。
func responsesItemProducedContent(raw json.RawMessage) bool {
	if !producedContentValue(raw) {
		return false
	}
	var item struct {
		Type    string          `json:"type"`
		Summary json.RawMessage `json:"summary"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &item) != nil {
		return false
	}
	switch item.Type {
	case "message":
		return responsesMessageProducedContent(item.Content)
	case "reasoning":
		return anyProducedContentValue(item.Summary, item.Content)
	case "":
		return false
	default:
		// 函数调用与各类托管工具调用本身即产出。
		return true
	}
}

// responsesMessageProducedContent 判定 message 条目的内容片段是否带来产出。
// 空回常以「一条文本为空的 message」出现，只看条目类型会漏判。
func responsesMessageProducedContent(raw json.RawMessage) bool {
	var parts []struct {
		Type    string          `json:"type"`
		Text    json.RawMessage `json:"text"`
		Refusal json.RawMessage `json:"refusal"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		// 结构无法识别时按有产出处理，避免误判空回。
		return producedContentValue(raw)
	}
	for _, part := range parts {
		switch part.Type {
		case "output_text":
			if producedContentValue(part.Text) {
				return true
			}
		case "refusal":
			if producedContentValue(part.Refusal) {
				return true
			}
		case "":
		default:
			return true
		}
	}
	return false
}
