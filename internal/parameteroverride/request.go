package parameteroverride

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/buger/jsonparser"
)

// 只为规则涉及的路径建立节点；其他值始终引用原始 JSON，不展开数组或对象。
// raw 为 nil 表示字段不存在，JSON null 则保留为四个字节。
type requestValue struct {
	raw      []byte
	fields   map[string]*requestValue
	elements []requestValue
	loaded   bool
	dirty    bool
	// 当前字段在父对象原始 JSON 中最后一次出现的位置，不随覆盖值改变。
	start int
	end   int
	// 重复对象通过代次使旧子字段失效，避免每次遍历全部规则字段清空。
	generation       uint
	parentGeneration uint
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
	value.elements = nil
}

func (value *requestValue) isObject() bool {
	return len(value.raw) > 0 && value.raw[0] == '{'
}

func (value *requestValue) isArray() bool {
	return len(value.raw) > 0 && value.raw[0] == '['
}

// 数组元素只复制规则涉及的节点，不展开消息内容和其他未修改字段。
func (value *requestValue) clonePlan() requestValue {
	result := requestValue{}
	if len(value.fields) > 0 {
		result.fields = make(map[string]*requestValue, len(value.fields))
		for key, child := range value.fields {
			cloned := child.clonePlan()
			result.fields[key] = &cloned
		}
	}
	return result
}

func (value *requestValue) load() error {
	if value.loaded {
		return nil
	}
	_, err := value.scan(value.raw)
	return err
}

// 按规则路径直接下行，返回对象终点；父层不先扫描整个子树再递归。
func (value *requestValue) scan(body []byte) (int, error) {
	value.generation++
	var end int
	var err error
	if body[0] == '[' {
		end, err = value.scanArray(body)
	} else {
		end, err = value.eachField(body, true, nil)
	}
	if err != nil {
		return 0, err
	}
	value.raw, value.loaded = body[:end], true
	return end, nil
}

func (value *requestValue) scanArray(body []byte) (int, error) {
	value.elements = nil
	plan := value.fields["*"]
	remaining := bytes.TrimLeft(body[1:], " \t\r\n")
	for remaining[0] != ']' {
		child := requestValue{}
		var end int
		var err error
		if plan != nil && (remaining[0] == '{' || remaining[0] == '[' && plan.fields["*"] != nil) {
			child = plan.clonePlan()
			end, err = child.scan(remaining)
		} else {
			_, _, end, err = jsonparser.Get(remaining)
		}
		if err != nil {
			return 0, err
		}
		child.raw = remaining[:end]
		value.elements = append(value.elements, child)
		remaining = bytes.TrimLeft(remaining[end:], " \t\r\n")
		if remaining[0] == ',' {
			remaining = bytes.TrimLeft(remaining[1:], " \t\r\n")
		}
	}
	return len(body) - len(remaining) + 1, nil
}

func (value *requestValue) currentField(key string) *requestValue {
	child := value.fields[key]
	if child.parentGeneration != value.generation {
		child.reset(nil)
		child.start, child.end = 0, 0
		child.parentGeneration = value.generation
	}
	return child
}

func (value *requestValue) remove(path []string) error {
	if value.isArray() && path[0] == "*" && len(path) > 1 {
		if err := value.load(); err != nil {
			return err
		}
		for index := range value.elements {
			child := &value.elements[index]
			if err := child.remove(path[1:]); err != nil {
				return err
			}
			value.dirty = value.dirty || child.dirty
		}
		return nil
	}
	if !value.isObject() {
		return nil
	}
	if err := value.load(); err != nil {
		return err
	}
	child := value.currentField(path[0])
	if len(path) == 1 {
		child.reset(nil)
		child.dirty = true
	} else if err := child.remove(path[1:]); err != nil {
		return err
	}
	value.dirty = value.dirty || child.dirty
	return nil
}

// 同一条规则的设置值只编码一次，数组各元素共享只读字节，避免批量设置放大中间内存。
type requestSet struct {
	value  any
	fields map[string]*requestSet
	raw    []byte
}

func newRequestSet(source any) *requestSet {
	result := &requestSet{value: source}
	if object, ok := source.(map[string]any); ok {
		result.fields = make(map[string]*requestSet, len(object))
		for key, child := range object {
			result.fields[key] = newRequestSet(child)
		}
	}
	return result
}

func (value *requestValue) merge(source *requestSet) error {
	if value.isArray() {
		if wildcard, exists := source.fields["*"]; exists {
			if wildcard.fields == nil || len(source.fields) != 1 {
				return fmt.Errorf("array wildcard must contain only element field settings")
			}
			if err := value.load(); err != nil {
				return err
			}
			_, nestedWildcard := wildcard.fields["*"]
			for index := range value.elements {
				child := &value.elements[index]
				if !child.isObject() && !(child.isArray() && nestedWildcard) {
					continue
				}
				if err := child.merge(wildcard); err != nil {
					return err
				}
				value.dirty = value.dirty || child.dirty
			}
			return nil
		}
	}
	if source.fields != nil && value.isObject() {
		if len(source.fields) == 0 {
			return nil
		}
		if err := value.load(); err != nil {
			return err
		}
		for key, item := range source.fields {
			if err := value.currentField(key).merge(item); err != nil {
				return err
			}
		}
		value.dirty = true
		return nil
	}
	// 仅序列化受配置大小限制的替换值，客户端的大值不会进入通用 JSON 树。
	if source.raw == nil {
		var encoded bytes.Buffer
		encoder := json.NewEncoder(&encoded)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(source.value); err != nil {
			return err
		}
		source.raw = bytes.TrimSuffix(encoded.Bytes(), []byte("\n"))
	}
	value.reset(source.raw)
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
	if value.isArray() {
		output.append([]byte{'['})
		for index := range value.elements {
			if index > 0 {
				output.append([]byte{','})
			}
			if err := value.elements[index].write(output); err != nil {
				return err
			}
		}
		output.append([]byte{']'})
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
	if _, err := value.eachField(value.raw, false, func(key, member []byte) {
		if child := value.fields[string(key)]; child != nil && child.parentGeneration == value.generation && child.dirty {
			return
		}
		separator()
		output.append(member)
	}); err != nil {
		return err
	}
	for key, child := range value.fields {
		if child.parentGeneration != value.generation || !child.dirty || child.raw == nil {
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
func (value *requestValue) eachField(body []byte, discover bool, visit func(key, member []byte)) (int, error) {
	remaining := bytes.TrimLeft(body[1:], " \t\r\n")
	for remaining[0] != '}' {
		member := remaining
		key, _, end, err := jsonparser.Get(remaining)
		if err != nil {
			return 0, err
		}
		if bytes.ContainsRune(key, '\\') || !utf8.Valid(key) {
			var decoded string
			if err := json.Unmarshal(remaining[:end], &decoded); err != nil {
				return 0, err
			}
			key = []byte(decoded)
		}
		remaining = bytes.TrimLeft(remaining[end:], " \t\r\n")
		remaining = bytes.TrimLeft(remaining[1:], " \t\r\n") // 跳过冒号。
		start := len(body) - len(remaining)
		child := value.fields[string(key)]
		switch {
		case discover && child != nil && (remaining[0] == '{' && len(child.fields) > 0 || remaining[0] == '[' && child.fields["*"] != nil):
			child.reset(nil)
			end, err = child.scan(remaining)
		case !discover && child != nil && child.parentGeneration == value.generation && child.start == start && child.end > start:
			// 测量和输出直接使用已发现的终点，不重复扫描深层的大值。
			end = child.end - start
		default:
			_, _, end, err = jsonparser.Get(remaining)
			if discover && child != nil {
				child.reset(nil)
			}
		}
		if err != nil {
			return 0, err
		}
		if discover && child != nil {
			// 重复键只保留最后一次的位置，索引大小不随客户端字段数量增长。
			child.raw = remaining[:end]
			child.start, child.end = start, start+end
			child.parentGeneration = value.generation
		}
		if visit != nil {
			visit(key, member[:len(member)-len(remaining)+end])
		}
		remaining = bytes.TrimLeft(remaining[end:], " \t\r\n")
		if remaining[0] == ',' {
			remaining = bytes.TrimLeft(remaining[1:], " \t\r\n")
		}
	}
	return len(body) - len(remaining) + 1, nil
}
