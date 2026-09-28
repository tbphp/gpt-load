package requestaudit

import (
	"encoding/json"
	"unicode/utf8"
)

type excerptOrigin struct {
	Role       string `json:"role,omitempty"`
	Type       string `json:"type,omitempty"`
	Name       string `json:"name,omitempty"`
	CallID     string `json:"call_id,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// 保留头、中、尾及原消息身份，偏移量对应原 JSON 字节；省略内容始终显式标记。
func contentExcerpt(raw json.RawMessage, budget int) json.RawMessage {
	if len(raw) <= budget {
		return raw
	}
	if budget <= 0 {
		return nil
	}
	var envelope struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil
	}
	var origin excerptOrigin
	if len(envelope.Content) > 0 && envelope.Content[0] == '{' {
		if err := json.Unmarshal(envelope.Content, &origin); err != nil {
			return nil
		}
	}
	// 身份线索本身也受预算约束，异常长值留在正文取样内。
	for _, value := range []*string{&origin.Role, &origin.Type, &origin.Name, &origin.CallID, &origin.ToolCallID} {
		if len(*value) > 128 {
			*value = ""
		}
	}
	type excerpt struct {
		Offset int    `json:"offset"`
		Text   string `json:"text"`
	}
	value := struct {
		Truncated   bool          `json:"truncated"`
		SourceBytes int           `json:"source_bytes"`
		Origin      excerptOrigin `json:"origin"`
		Excerpts    []excerpt     `json:"excerpts"`
	}{Truncated: true, SourceBytes: len(raw), Origin: origin}
	var best json.RawMessage
	for low, high := 1, min(len(raw)/3, budget/3); low <= high; {
		length := low + (high-low)/2
		value.Excerpts = nil
		for _, offset := range []int{0, (len(raw) - length) / 2, len(raw) - length} {
			end := offset + length
			for offset < end && !utf8.RuneStart(raw[offset]) {
				offset++
			}
			for end > offset && end < len(raw) && !utf8.RuneStart(raw[end]) {
				end--
			}
			value.Excerpts = append(value.Excerpts, excerpt{Offset: offset, Text: string(raw[offset:end])})
		}
		encoded, _ := json.Marshal(value)
		if len(encoded) <= budget {
			best, low = encoded, length+1
		} else {
			high = length - 1
		}
	}
	return best
}
