package cpa

import (
	"bytes"
	"encoding/json"
)

// CPA 跨协议流在 message_start 中注入全部输入的本地估算，但 Anthropic 的
// input_tokens 表示未缓存输入。用零占位，避免向客户端报告虚假的未缓存输入，
// 或在中断时把本地估算当作已计费用量。原生 Anthropic 不经过此修正。
func normalizeConvertedAnthropicStartUsage(payload []byte) ([]byte, error) {
	if !bytes.Contains(payload, []byte(`"message_start"`)) {
		return payload, nil
	}
	// CPA 转换器以单行 JSON 生成 data 字段；一个 chunk 可以包含多个完整事件。
	lines := bytes.SplitAfter(payload, []byte{'\n'})
	for i, line := range lines {
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		body := bytes.TrimSpace(line[len("data:"):])
		var event map[string]json.RawMessage
		if err := json.Unmarshal(body, &event); err != nil {
			return nil, err
		}
		if string(event["type"]) != `"message_start"` {
			continue
		}
		rawMessage, exists := event["message"]
		if !exists {
			continue
		}
		var message map[string]json.RawMessage
		if err := json.Unmarshal(rawMessage, &message); err != nil {
			return nil, err
		}
		rawUsage, exists := message["usage"]
		if !exists {
			continue
		}
		var usage map[string]json.RawMessage
		if err := json.Unmarshal(rawUsage, &usage); err != nil {
			return nil, err
		}
		if _, exists := usage["input_tokens"]; !exists {
			continue
		}
		usage["input_tokens"] = json.RawMessage("0")
		var err error
		message["usage"], err = json.Marshal(usage)
		if err != nil {
			return nil, err
		}
		event["message"], err = json.Marshal(message)
		if err != nil {
			return nil, err
		}
		rewritten, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		terminator := line[len(bytes.TrimRight(line, "\r\n")):]
		lines[i] = append(append([]byte("data: "), rewritten...), terminator...)
	}
	return bytes.Join(lines, nil), nil
}
