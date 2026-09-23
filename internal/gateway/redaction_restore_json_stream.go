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
	changed   bool
	invalid   bool
	last      *redactionStreamField
}

func (doc *redactionStreamDocument) push(fragment string, restore func(string) (string, error)) (string, error) {
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
			out.WriteByte(ch)
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
	decoded := raw
	escaped := strings.Contains(raw, `\`)
	if escaped {
		if err := json.Unmarshal([]byte(`"`+raw+`"`), &decoded); err != nil {
			doc.invalid = true
			if strings.Contains(raw, redactionStreamPrefix) || doc.changed {
				return "", errRedactionStream
			}
			return raw, nil
		}
	}
	cut, err := redactionStreamSafeCut(decoded)
	if err != nil {
		return "", err
	}
	if closed {
		if redactionStreamIncompleteCandidate(decoded[cut:]) {
			return "", errRedactionStream
		}
		cut = len(decoded)
	}
	rawCut := cut
	if escaped && cut < len(decoded) {
		boundaries, err := unaryJSONDecodedBoundaries(`"`+raw+`"`, decoded)
		if err != nil || boundaries[cut] < 1 {
			return "", errRedactionStream
		}
		rawCut = boundaries[cut] - 1
	} else if cut == len(decoded) {
		rawCut = len(raw)
	}
	doc.pending = strings.Clone(raw[rawCut:])
	visible := decoded[:cut]
	if !strings.Contains(visible, redactionStreamPrefix) {
		return raw[:rawCut], nil
	}
	restored, err := restore(visible)
	if err != nil {
		return "", errRedactionStream
	}
	if restored == visible {
		return raw[:rawCut], nil
	}
	doc.changed = true
	encoded, err := json.Marshal(restored)
	if err != nil {
		return "", errRedactionStream
	}
	return string(encoded[1 : len(encoded)-1]), nil
}

func (doc *redactionStreamDocument) finish(restore func(string) (string, error)) (string, error) {
	pending := doc.pending
	doc.pending = ""
	end, _ := redactionJSONStringSegment(pending)
	tail, err := doc.restoreSegment(pending[:end], true, restore)
	if err != nil || (doc.changed && (doc.inString || len(doc.stack) != 0 || doc.invalid)) {
		return "", errRedactionStream
	}
	doc.pending = ""
	return tail + pending[end:], nil
}
