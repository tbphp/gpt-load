package bifrost

import "encoding/json"

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
	return json.Marshal(object)
}
