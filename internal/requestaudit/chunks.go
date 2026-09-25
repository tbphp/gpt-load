package requestaudit

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxUnitBytes = 8 << 10
const fragmentBytes = 4 << 10
const fragmentOverlap = 256

type reviewDocument struct {
	original Document
	units    []json.RawMessage
	sources  []int
	digests  [][32]byte
}

// 大消息按 JSON 路径拆分，字符串使用有重叠的 UTF-8 片段；不丢弃待检正文。
// 支持性前文仍从原消息提取，避免一条长消息的片段挤掉真正的相邻消息。
func prepareDocument(doc Document) (reviewDocument, error) {
	result := reviewDocument{original: doc}
	for source, raw := range doc.Units {
		parts := []json.RawMessage{raw}
		if len(raw) > maxUnitBytes {
			value, err := decode(raw)
			if err != nil {
				return result, err
			}
			parts = nil
			budget := MaxReviewBatches * MaxRequestBytes
			if err := splitValue(value, "", nil, &parts, &budget); err != nil {
				return result, err
			}
		}
		for index, part := range parts {
			result.units = append(result.units, part)
			result.sources = append(result.sources, source)
			result.digests = append(result.digests, hashParts(doc.Digests[source][:], []byte(strconv.Itoa(index))))
		}
	}
	return result, nil
}

func splitValue(value any, path string, context []map[string]any, parts *[]json.RawMessage, budget *int) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	appendPart := func(value any, offset int) error {
		encoded, err := json.Marshal(map[string]any{"path": path, "context": context, "offset": offset, "value": value, "fragment": true})
		if err != nil {
			return err
		}
		if len(encoded) > maxUnitBytes {
			return fmt.Errorf("fragment metadata exceeds review budget")
		}
		*budget -= len(encoded)
		if *budget < 0 {
			return fmt.Errorf("message exceeds total review budget")
		}
		*parts = append(*parts, encoded)
		return nil
	}
	if len(raw) <= fragmentBytes {
		return appendPart(value, 0)
	}
	switch typed := value.(type) {
	case string:
		for start := 0; start < len(typed); {
			// 按实际 JSON 编码长度装满片段，兼顾中文与转义密集内容。
			end := start
			for low, high := start+1, min(len(typed), start+fragmentBytes); low <= high; {
				middle := low + (high-low)/2
				boundary := middle
				for boundary > start && boundary < len(typed) && !utf8.RuneStart(typed[boundary]) {
					boundary--
				}
				encoded, _ := json.Marshal(typed[start:boundary])
				if len(encoded) <= fragmentBytes {
					end, low = boundary, middle+1
				} else {
					high = middle - 1
				}
			}
			if err := appendPart(typed[start:end], start); err != nil {
				return err
			}
			if end == len(typed) {
				break
			}
			start = max(start+1, end-fragmentOverlap)
			for start < end && !utf8.RuneStart(typed[start]) {
				start++
			}
		}
	case []any:
		for index, item := range typed {
			if err := splitValue(item, path+"/"+strconv.Itoa(index), context, parts, budget); err != nil {
				return err
			}
		}
	case map[string]any:
		// 角色、内容类型和工具身份只作为数据来源线索，不赋予片段指令权限。
		metadata := map[string]any{}
		for _, key := range []string{"field", "role", "type", "name", "id", "call_id", "tool_call_id", "tool_use_id"} {
			if text, ok := typed[key].(string); ok && len(text) <= 128 {
				metadata[key] = text
			}
		}
		if len(metadata) > 0 && len(context) < 4 {
			context = append(append([]map[string]any(nil), context...), metadata)
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			escaped := strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
			if err := splitValue(typed[key], path+"/"+escaped, context, parts, budget); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("oversized non-text value")
	}
	return nil
}

// 辅助内容允许保留头尾；调用方不能用此函数处理待检目标。
func contextExcerpt(raw json.RawMessage, budget int) json.RawMessage {
	if len(raw) <= budget {
		return raw
	}
	var best json.RawMessage
	for low, high := 1, min(len(raw)/2, budget/2); low <= high; {
		limit := low + (high-low)/2
		head, tail := string(raw[:limit]), string(raw[len(raw)-limit:])
		for !utf8.ValidString(head) {
			head = head[:len(head)-1]
		}
		for !utf8.ValidString(tail) {
			tail = tail[1:]
		}
		encoded, _ := json.Marshal(map[string]any{"excerpt": head + "\n[context omitted]\n" + tail, "truncated": true})
		if len(encoded) <= budget {
			best, low = encoded, limit+1
		} else {
			high = limit - 1
		}
	}
	return best
}
