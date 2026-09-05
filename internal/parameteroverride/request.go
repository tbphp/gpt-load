package parameteroverride

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"

	"github.com/buger/jsonparser"
)

// 只为规则涉及的路径建立节点；其他值始终引用原始 JSON，不展开数组或对象。
// raw 为 nil 表示字段不存在，JSON null 则保留为四个字节。
type requestValue struct {
	raw    []byte
	fields map[string]*requestValue
	loaded bool
	dirty  bool
}

func (value *requestValue) field(key string) *requestValue {
	if value.fields == nil {
		value.fields = make(map[string]*requestValue)
	}
	if value.fields[key] == nil {
		value.fields[key] = &requestValue{}
	}
	return value.fields[key]
}

func (value *requestValue) planSet(source map[string]any) {
	for key, item := range source {
		child := value.field(key)
		if object, ok := item.(map[string]any); ok {
			child.planSet(object)
		}
	}
}

func (value *requestValue) planPath(path []string) {
	for _, key := range path {
		value = value.field(key)
	}
}

func (value *requestValue) reset(raw []byte) {
	value.raw, value.loaded, value.dirty = raw, false, false
}

func (value *requestValue) isObject() bool {
	return len(value.raw) > 0 && value.raw[0] == '{'
}

func (value *requestValue) load() error {
	if value.loaded {
		return nil
	}
	for _, child := range value.fields {
		child.reset(nil)
	}
	if err := eachRequestField(value.raw, func(key, raw, _ []byte) {
		if child := value.fields[string(key)]; child != nil {
			// 重复字段与 encoding/json 保持一致，以最后一次出现为准。
			child.reset(raw)
		}
	}); err != nil {
		return err
	}
	value.loaded = true
	return nil
}

func (value *requestValue) remove(path []string) error {
	if !value.isObject() {
		return nil
	}
	if err := value.load(); err != nil {
		return err
	}
	child := value.fields[path[0]]
	if len(path) == 1 {
		child.reset(nil)
		child.dirty = true
	} else if err := child.remove(path[1:]); err != nil {
		return err
	}
	value.dirty = value.dirty || child.dirty
	return nil
}

func (value *requestValue) merge(source any) error {
	if object, ok := source.(map[string]any); ok && value.isObject() {
		if len(object) == 0 {
			return nil
		}
		if err := value.load(); err != nil {
			return err
		}
		for key, item := range object {
			if err := value.fields[key].merge(item); err != nil {
				return err
			}
		}
		value.dirty = true
		return nil
	}
	// 仅序列化受配置大小限制的替换值，客户端的大值不会进入通用 JSON 树。
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(source); err != nil {
		return err
	}
	value.reset(bytes.TrimSuffix(encoded.Bytes(), []byte("\n")))
	value.dirty = true
	return nil
}

type requestOutput struct {
	body []byte
	size int
}

func (output *requestOutput) append(raw []byte) {
	output.size += len(raw)
	if output.body != nil {
		output.body = append(output.body, raw...)
	}
}

func (value *requestValue) write(output *requestOutput) error {
	if !value.dirty || !value.loaded {
		output.append(value.raw)
		return nil
	}
	output.append([]byte{'{'})
	first := true
	separator := func() {
		if !first {
			output.append([]byte{','})
		}
		first = false
	}
	if err := eachRequestField(value.raw, func(key, _, member []byte) {
		if child := value.fields[string(key)]; child != nil && child.dirty {
			return
		}
		separator()
		output.append(member)
	}); err != nil {
		return err
	}
	for key, child := range value.fields {
		if !child.dirty || child.raw == nil {
			continue
		}
		separator()
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return err
		}
		output.append(encodedKey)
		output.append([]byte{':'})
		if err := child.write(output); err != nil {
			return err
		}
	}
	output.append([]byte{'}'})
	return nil
}

// 调用方已用 json.Valid 校验完整请求。这里只扫描字段边界，值引用原始字节；
// 仅含转义或非法 UTF-8 的键需要解码，沿用 encoding/json 的键名语义。
func eachRequestField(body []byte, visit func(key, raw, member []byte)) error {
	remaining := bytes.TrimLeft(body[1:], " \t\r\n")
	for remaining[0] != '}' {
		member := remaining
		key, _, end, err := jsonparser.Get(remaining)
		if err != nil {
			return err
		}
		if bytes.ContainsRune(key, '\\') || !utf8.Valid(key) {
			var decoded string
			if err := json.Unmarshal(remaining[:end], &decoded); err != nil {
				return err
			}
			key = []byte(decoded)
		}
		remaining = bytes.TrimLeft(remaining[end:], " \t\r\n")
		remaining = bytes.TrimLeft(remaining[1:], " \t\r\n") // 跳过冒号。
		_, _, end, err = jsonparser.Get(remaining)
		if err != nil {
			return err
		}
		visit(key, remaining[:end], member[:len(member)-len(remaining)+end])
		remaining = bytes.TrimLeft(remaining[end:], " \t\r\n")
		if remaining[0] == ',' {
			remaining = bytes.TrimLeft(remaining[1:], " \t\r\n")
		}
	}
	return nil
}
