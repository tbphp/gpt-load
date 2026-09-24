package gateway

import (
	"encoding/json"
	"strconv"
	"strings"
)

// 只保留字符串内可能属于密文或未完成转义的尾部，不扣留整个工具参数。
// 容器状态用于区分 JSON 的字段名和值；未改写的字节直接保留。
type redactionStreamDocument struct {
	stack     []byte
	syntax    byte
	inString  bool
	keyString bool
	pending   string
	token     redactionTokenStream
	changed   bool
	invalid   bool
	last      *redactionStreamField
}

func (doc *redactionStreamDocument) push(fragment string, restore func(string) (string, error)) (string, error) {
	doc.token.limit = maxRedactionStreamDocument
	input := doc.pending + fragment
	doc.pending = ""
	var out strings.Builder
	for len(input) > 0 {
		if !doc.inString {
			ch := input[0]
			switch ch {
			case '"':
				doc.inString = true
				doc.keyString = len(doc.stack) > 0 && doc.stack[len(doc.stack)-1] == '{' && (doc.syntax == '{' || doc.syntax == ',')
			case '{', '[':
				if len(doc.stack) >= maxUnaryRestoreDepth {
					return "", errRedactionStream
				}
				doc.stack = append(doc.stack, ch)
			case '}', ']':
				if len(doc.stack) == 0 || (ch == '}' && doc.stack[len(doc.stack)-1] != '{') || (ch == ']' && doc.stack[len(doc.stack)-1] != '[') {
					doc.invalid = true
				} else {
					doc.stack = doc.stack[:len(doc.stack)-1]
				}
			}
			if ch != ' ' && ch != '\t' && ch != '\r' && ch != '\n' {
				doc.syntax = ch
			}
			if err := doc.token.pushUnit(input[:1], input[:1], &out, keepUnquotedRedactionToken, false); err != nil {
				return "", err
			}
			input = input[1:]
			continue
		}
		end, closed := redactionJSONStringSegment(input)
		raw := input[:end]
		text, err := doc.restoreSegment(raw, closed, restore)
		if err != nil {
			return "", err
		}
		out.WriteString(text)
		if !closed {
			doc.pending += input[end:]
			if len(doc.pending) > maxRedactionStreamDocument {
				return "", errRedactionStream
			}
			break
		}
		out.WriteByte('"')
		doc.inString = false
		doc.syntax = '"'
		input = input[end+1:]
	}
	if doc.changed && doc.invalid {
		return "", errRedactionStream
	}
	return out.String(), nil
}

// 返回完整转义之前的边界；Unicode 代理对也允许跨 delta。
func redactionJSONStringSegment(input string) (int, bool) {
	for i := 0; i < len(input); {
		switch input[i] {
		case '"':
			return i, true
		case '\\':
			start := i
			if i+1 >= len(input) {
				return start, false
			}
			if input[i+1] != 'u' {
				i += 2
				continue
			}
			if i+6 > len(input) {
				return start, false
			}
			code, err := strconv.ParseUint(input[i+2:i+6], 16, 16)
			i += 6
			if err == nil && code >= 0xD800 && code <= 0xDBFF {
				if i == len(input) {
					return start, false
				}
				if input[i] == '\\' && (i+1 == len(input) || input[i+1] == 'u') {
					if i+6 > len(input) {
						return start, false
					}
					second, err := strconv.ParseUint(input[i+2:i+6], 16, 16)
					if err == nil && second >= 0xDC00 && second <= 0xDFFF {
						i += 6
					}
				}
			}
		default:
			i++
		}
	}
	return len(input), false
}

func (doc *redactionStreamDocument) restoreSegment(raw string, closed bool, restore func(string) (string, error)) (string, error) {
	if doc.keyString {
		return raw, nil
	}
	doc.token.limit = maxRedactionStreamDocument
	var out strings.Builder
	for len(raw) > 0 {
		slash := strings.IndexByte(raw, '\\')
		if slash < 0 {
			slash = len(raw)
		}
		if err := doc.token.pushString(raw[:slash], &out, restore, true); err != nil {
			return "", err
		}
		raw = raw[slash:]
		if len(raw) == 0 {
			break
		}
		size := 2
		if len(raw) > 1 && raw[1] == 'u' {
			size = 6
			if len(raw) >= 12 && raw[6:8] == `\u` {
				first, e1 := strconv.ParseUint(raw[2:6], 16, 16)
				second, e2 := strconv.ParseUint(raw[8:12], 16, 16)
				if e1 == nil && e2 == nil && first >= 0xD800 && first <= 0xDBFF && second >= 0xDC00 && second <= 0xDFFF {
					size = 12
				}
			}
		}
		if size > len(raw) {
			return "", errRedactionStream
		}
		unit := raw[:size]
		var decoded string
		if err := json.Unmarshal([]byte(`"`+unit+`"`), &decoded); err != nil {
			doc.invalid = true
			if doc.token.changed {
				return "", errRedactionStream
			}
			tail, err := doc.token.finish(restore)
			if err != nil {
				return "", err
			}
			out.WriteString(tail)
			out.WriteString(unit)
		} else if err := doc.token.pushUnit(unit, decoded, &out, restore, true); err != nil {
			return "", err
		}
		raw = raw[size:]
	}
	doc.changed = doc.changed || doc.token.changed
	if closed {
		tail, err := doc.token.finish(restore)
		if err != nil {
			return "", err
		}
		out.WriteString(tail)
	}
	return out.String(), nil
}

// JSON 字符串外的密文不插入明文，原样保留。
func keepUnquotedRedactionToken(value string) (string, error) { return value, nil }

func (doc *redactionStreamDocument) complete() bool {
	return !doc.inString && !doc.blocked() && len(doc.stack) == 0 &&
		(doc.syntax == '}' || doc.syntax == ']' || doc.syntax == '"')
}

func (doc *redactionStreamDocument) blocked() bool { return doc.pending != "" || doc.token.pending() }

func (doc *redactionStreamDocument) finish(restore func(string) (string, error)) (string, error) {
	pending := doc.pending
	doc.pending = ""
	end, _ := redactionJSONStringSegment(pending)
	prefix, err := doc.restoreSegment(pending[:end], false, restore)
	if err != nil || (doc.changed && doc.invalid) {
		return "", errRedactionStream
	}
	tail, err := doc.token.finish(restore)
	if err != nil {
		return "", err
	}
	return prefix + tail + pending[end:], nil
}
