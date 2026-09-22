package requestaudit

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

type Document struct {
	Context json.RawMessage
	Units   []json.RawMessage
	Anchors []int
	Related map[int][]int
	Digests [][32]byte
	Digest  [32]byte
}

// Extract 仅检查本次传入的内容，不查询引用历史，不保留会话正文。
func Extract(body []byte) (Document, string) {
	var doc Document
	value, err := decode(body)
	if err != nil {
		return doc, "unsupported_content"
	}
	root, ok := value.(map[string]any)
	if !ok || root == nil {
		return doc, "unsupported_content"
	}
	for _, key := range []string{"model", "stream", "type", "generate", "stream_id", "reasoning_effort", "previous_response_id", "conversation", "store", "background", "stream_options", "max_tokens", "max_completion_tokens", "max_output_tokens", "temperature", "top_p", "seed", "frequency_penalty", "presence_penalty", "reasoning", "service_tier", "prompt_cache_key", "prompt_cache_retention"} {
		delete(root, key)
	}
	initial := false
	for _, key := range []string{"messages", "input", "contents"} {
		items, exists := root[key]
		if !exists || items == nil {
			continue
		}
		delete(root, key)
		list, ok := items.([]any)
		if !ok {
			list = []any{items}
		}
		for _, item := range list {
			if key == "input" {
				switch item.(type) {
				case json.Number, []any:
					// Embeddings 的 token ID 输入不是可直接审查的文本。
					return doc, "unsupported_content"
				}
			}
			if unsupported(item) {
				return doc, "unsupported_content"
			}
			encoded, err := json.Marshal(map[string]any{"field": key, "content": item})
			if err != nil {
				return doc, "unsupported_content"
			}
			index := len(doc.Units)
			doc.Units = append(doc.Units, encoded)
			role := ""
			if message, ok := item.(map[string]any); ok {
				role, _ = message["role"].(string)
				if key == "contents" && role == "" {
					// Gemini 允许省略用户角色，初始任务仍需作为后续审查锚点。
					role = "user"
				}
			} else if _, ok := item.(string); ok {
				role = "user"
			}
			if role == "system" || role == "developer" || (!initial && role == "user") {
				doc.Anchors = append(doc.Anchors, index)
			}
			initial = initial || role == "user"
		}
	}
	if unsupported(root) {
		return doc, "unsupported_content"
	}
	doc.Context, err = json.Marshal(root)
	if err != nil {
		return doc, "unsupported_content"
	}
	// 顶层提示/工具定义本身也是待检内容，不能只当作已通过的上下文。
	if len(root) > 0 {
		doc.Units = append([]json.RawMessage{doc.Context}, doc.Units...)
		for i := range doc.Anchors {
			doc.Anchors[i]++
		}
		doc.Anchors = append([]int{0}, doc.Anchors...)
	}
	anchors := make([]json.RawMessage, 0, len(doc.Anchors))
	for _, index := range doc.Anchors {
		anchors = append(anchors, doc.Units[index])
	}
	seed, _ := json.Marshal(anchors)
	prefix := sha256.Sum256(seed)
	for _, item := range doc.Units {
		prefix = hashParts(prefix[:], item)
		doc.Digests = append(doc.Digests, prefix)
	}
	doc.Related = map[int][]int{}
	calls := map[string]int{}
	for index, unit := range doc.Units {
		value, err := decode(unit)
		if err != nil {
			return doc, "unsupported_content"
		}
		definitions, references := toolLinks(value)
		for _, reference := range references {
			if related, found := calls[reference]; found {
				doc.Related[index] = append(doc.Related[index], related)
			}
		}
		for _, definition := range definitions {
			calls[definition] = index
		}
	}
	doc.Digest = prefix
	return doc, ""
}

func unsupported(value any) bool {
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			if unsupported(item) {
				return true
			}
		}
	case map[string]any:
		kind, _ := v["type"].(string)
		switch kind {
		case "image", "image_url", "audio_url", "file_url", "input_image", "input_audio", "audio", "video", "input_video", "input_file", "file", "document", "item_reference":
			return true
		}
		for key, item := range v {
			switch key {
			case "parameters", "input_schema", "schema", "responseSchema", "response_schema":
				// Schema 中的 file_id 等只是字段定义；正文仍完整送给 JEV。
				continue
			case "input":
				if kind == "tool_use" {
					continue
				}
			case "functionResponse", "function_response":
				if response, ok := item.(map[string]any); ok {
					// 结构化工具返回是文本数据，parts 中的实际附件继续检查。
					envelope := make(map[string]any, len(response))
					for field, value := range response {
						if field != "response" {
							envelope[field] = value
						}
					}
					if unsupported(envelope) {
						return true
					}
					continue
				}
			case "encrypted_content", "inlineData", "inline_data", "fileData", "file_data", "file_id":
				if item != nil && item != "" {
					return true
				}
			}
			if unsupported(item) {
				return true
			}
		}
	}
	return false
}

// 不使用整数专用 canonicaljson；保留 JSON 数值原文并拒绝重复键。
func decode(raw []byte) (any, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("invalid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	value, err := decodeValue(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing JSON")
	}
	return value, nil
}

func decodeValue(d *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, fmt.Errorf("JSON too deep")
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		result := map[string]any{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("invalid object key")
			}
			if _, exists := result[name]; exists {
				return nil, fmt.Errorf("duplicate object key")
			}
			value, err := decodeValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			result[name] = value
		}
		_, err := d.Token()
		return result, err
	case json.Delim('['):
		result := []any{}
		for d.More() {
			value, err := decodeValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		_, err := d.Token()
		return result, err
	default:
		return token, nil
	}
}

// 工具结果可能与原调用相隔多条消息，仅携带可由本次正文确定的关联。
func toolLinks(value any) (definitions, references []string) {
	add := func(target *[]string, value any, prefix string) {
		if text, ok := value.(string); ok && text != "" {
			*target = append(*target, prefix+text)
		}
	}
	var walk func(any)
	walk = func(value any) {
		switch item := value.(type) {
		case []any:
			for _, child := range item {
				walk(child)
			}
		case map[string]any:
			switch item["type"] {
			case "function_call":
				add(&definitions, item["call_id"], "id:")
			case "tool_use":
				add(&definitions, item["id"], "id:")
			case "function_call_output":
				add(&references, item["call_id"], "id:")
			case "tool_result":
				add(&references, item["tool_use_id"], "id:")
			}
			add(&references, item["tool_call_id"], "id:")
			if calls, ok := item["tool_calls"].([]any); ok {
				for _, call := range calls {
					if call, ok := call.(map[string]any); ok {
						add(&definitions, call["id"], "id:")
					}
				}
			}
			for _, entry := range []struct {
				key    string
				target *[]string
			}{{"functionCall", &definitions}, {"function_call", &definitions}, {"functionResponse", &references}, {"function_response", &references}} {
				if function, ok := item[entry.key].(map[string]any); ok {
					if id, ok := function["id"].(string); ok && id != "" {
						add(entry.target, id, "id:")
					} else {
						add(entry.target, function["name"], "name:")
					}
				}
			}
			for _, child := range item {
				walk(child)
			}
		}
	}
	walk(value)
	return definitions, references
}
