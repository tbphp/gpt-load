package gateway

import (
	"bytes"
	"encoding/json"
	"strings"
)

// 每个候选只解析一次头部并扫描新增字符，避免分片到达时重扫、复制整个尾部。
type redactionTokenStream struct {
	raw         bytes.Buffer
	decoded     bytes.Buffer
	prefix      int
	digits      int
	length      int
	remaining   int
	leadingZero bool
	oversize    bool
	body        bool
	changed     bool
	limit       int
}

func (token *redactionTokenStream) pending() bool { return token.raw.Len() != 0 }

func (token *redactionTokenStream) reset() {
	*token = redactionTokenStream{changed: token.changed, limit: token.limit}
}

func (token *redactionTokenStream) append(raw, decoded string) error {
	limit := token.limit
	if limit == 0 {
		limit = maxRedactionStreamPendingBytes
	}
	if len(raw) > limit-token.raw.Len() {
		return errRedactionStream
	}
	token.raw.WriteString(raw)
	token.decoded.WriteString(decoded)
	return nil
}

func (token *redactionTokenStream) pushString(value string, out *strings.Builder, restore func(string) (string, error), quoted bool) error {
	for len(value) > 0 {
		if token.pending() && !token.body {
			ch := value[0]
			matches := token.prefix < len(redactionStreamPrefix) && ch == redactionStreamPrefix[token.prefix]
			if token.prefix == len(redactionStreamPrefix) {
				matches = ch >= '0' && ch <= '9' || ch == '_' && token.digits > 0
			}
			if !matches {
				out.Write(token.raw.Bytes())
				token.reset()
				continue
			}
		}
		if !token.pending() {
			if index := strings.Index(value, redactionStreamPrefix); index >= 0 {
				out.WriteString(value[:index])
				if err := token.append(redactionStreamPrefix, redactionStreamPrefix); err != nil {
					return err
				}
				token.prefix = len(redactionStreamPrefix)
				value = value[index+len(redactionStreamPrefix):]
				continue
			}
			// 普通内容整段放行，仅末尾至多四个前缀字符需要等下一片。
			cut := len(value)
			for length := min(len(redactionStreamPrefix)-1, len(value)); length > 0; length-- {
				if strings.HasSuffix(value, redactionStreamPrefix[:length]) {
					cut -= length
					break
				}
			}
			out.WriteString(value[:cut])
			value = value[cut:]
			if len(value) == 0 {
				return nil
			}
		}
		if err := token.pushUnit(value[:1], value[:1], out, restore, quoted); err != nil {
			return err
		}
		value = value[1:]
	}
	return nil
}

// raw 保留原始 JSON 转义；decoded 用于识别和认证，不递归扫描恢复出的明文。
func (token *redactionTokenStream) pushUnit(raw, decoded string, out *strings.Builder, restore func(string) (string, error), quoted bool) error {
	ch := byte(0)
	if len(decoded) == 1 {
		ch = decoded[0]
	}
	if token.prefix < len(redactionStreamPrefix) {
		if len(decoded) == 1 && ch == redactionStreamPrefix[token.prefix] {
			if err := token.append(raw, decoded); err != nil {
				return err
			}
			token.prefix++
			return nil
		}
		if token.pending() {
			out.Write(token.raw.Bytes())
			token.reset()
			return token.pushUnit(raw, decoded, out, restore, quoted)
		}
		out.WriteString(raw)
		return nil
	}
	if !token.body {
		if ch >= '0' && ch <= '9' {
			if token.digits == 0 {
				token.leadingZero = ch == '0'
			}
			token.digits++
			if !token.oversize {
				digit := int(ch - '0')
				if token.length > (maxRedactionStreamPendingBytes-digit)/10 {
					token.oversize = true
				} else {
					token.length = token.length*10 + digit
				}
			}
			return token.append(raw, decoded)
		}
		if ch == '_' && token.digits > 0 {
			if err := token.append(raw, decoded); err != nil {
				return err
			}
			if token.oversize || token.length == 0 || (token.leadingZero && token.digits > 1) {
				// 长度字段非法，不可能是网关生成的密文。
				return token.keep(out, restore)
			}
			token.body = true
			token.remaining = token.length
			return nil
		}
		out.Write(token.raw.Bytes())
		token.reset()
		return token.pushUnit(raw, decoded, out, restore, quoted)
	}
	if len(decoded) != 1 || !((ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_') {
		// 密文被截断或抄错：已收集的片段原样放行，当前字符按普通内容处理。
		if err := token.keep(out, restore); err != nil {
			return err
		}
		return token.pushUnit(raw, decoded, out, restore, quoted)
	}
	if err := token.append(raw, decoded); err != nil {
		return err
	}
	token.remaining--
	if token.remaining != 0 {
		return nil
	}
	original := token.decoded.String()
	restored, err := restore(original)
	if err != nil {
		return errRedactionStream
	}
	if restored == original {
		out.Write(token.raw.Bytes())
	} else {
		token.changed = true
		if quoted {
			encoded, err := json.Marshal(restored)
			if err != nil {
				return errRedactionStream
			}
			out.Write(encoded[1 : len(encoded)-1])
		} else {
			out.WriteString(restored)
		}
	}
	token.reset()
	return nil
}

// keep 把无法认证的候选交给 restore 判定并计数，再原样写出；这类候选不可能还原出明文。
func (token *redactionTokenStream) keep(out *strings.Builder, restore func(string) (string, error)) error {
	original := token.decoded.String()
	restored, err := restore(original)
	if err != nil || restored != original {
		return errRedactionStream
	}
	out.Write(token.raw.Bytes())
	token.reset()
	return nil
}

func (token *redactionTokenStream) finish(restore func(string) (string, error)) (string, error) {
	if token.body {
		var out strings.Builder
		if err := token.keep(&out, restore); err != nil {
			return "", err
		}
		return out.String(), nil
	}
	tail := token.raw.String()
	token.reset()
	return tail, nil
}
