package bifrost

import (
	"bytes"
	"encoding/json"
)

// Vertex 从 URL 选择模型；清空协议位置的 model，避免 Gemini 短资源名与 Vertex 完整资源名冲突。
// 只改写模型字段，保留原生请求的多模态内容和扩展参数。
func vertexEmbeddingsBody(body []byte, batch bool) ([]byte, error) {
	object, err := decodeNativeJSONObject(body)
	if err != nil {
		return nil, err
	}
	if _, exists := object["model"]; exists {
		object["model"] = json.RawMessage(`""`)
	}
	if batch {
		var requests []map[string]json.RawMessage
		if err := json.Unmarshal(object["requests"], &requests); err != nil {
			return nil, err
		}
		for _, request := range requests {
			request["model"] = json.RawMessage(`""`)
		}
		object["requests"], err = json.Marshal(requests)
		if err != nil {
			return nil, err
		}
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	if !bytes.ContainsRune(encoded, '/') {
		return encoded, nil
	}
	// Bifrost 1.14.1 会对整个 JSON 用正则改写 models/、projects/ 和 /locations/。
	// 协议模型已在上面处理；将斜杠作等价 JSON 转义，避免 SDK 改写文本、媒体 URI 和扩展字段。
	escaped := make([]byte, 0, len(encoded)+bytes.Count(encoded, []byte{'/'}))
	for index := 0; index < len(encoded); index++ {
		value := encoded[index]
		if value == '\\' {
			// Marshal 已保证 JSON 有效；原样保留已有转义，避免改变反斜杠或已转义的斜杠。
			escaped = append(escaped, value, encoded[index+1])
			index++
			continue
		}
		if value == '/' {
			escaped = append(escaped, '\\')
		}
		escaped = append(escaped, value)
	}
	return escaped, nil
}
