package requestredact

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

// 带签名的内容由上游生成，下一轮必须逐字节还给上游。网关把签名内容里的字符串记在同一段
// 内容的签名里：字段路径、客户端看到的整串哈希，以及网关改写过的片段和上游原文。
// 包装串是 base64(signatureMagic + JSON)，仍是合法 base64，客户端按字节解码签名时不会出错。
// 回传时哈希全部一致才按位置换回上游原文和原签名；客户端改动过内容就按普通上游内容加密。
// 回填只使用记录里的原文，不需要解密，换用其他 AccessKey 或经过其他网关实例也能还原。
const (
	signatureMagic = "GLDSIG"
	// signatureWrapperPrefix 是 base64(signatureMagic)，所有包装串都以它开头，
	// 普通签名只需比较前缀即可排除，无需解码。
	signatureWrapperPrefix = "R0xEU0lH"
	signatureRecordVersion = 2
	signatureHashBytes     = 12
)

var errSignatureRecord = errors.New("invalid signature record")

// RestoreSpan 是客户端字符串里网关改写过的一段，Offset 与 Length 按字节计，Original 是上游原文。
type RestoreSpan struct {
	Offset   int
	Length   int
	Original string
}

// SignedString 描述签名内容里的一个字符串。Path 相对签名所属对象，字段名为 string，
// 数组下标为 int；Value 是客户端看到的完整字符串，只用于计算哈希，不写入记录。
type SignedString struct {
	Path  []any
	Value string
	Spans []RestoreSpan
}

type signatureRecord struct {
	Version   int               `json:"v"`
	Signature string            `json:"s"`
	Strings   []signatureString `json:"f"`
}

type signatureString struct {
	Path  []any           `json:"p"`
	Hash  string          `json:"h"`
	Spans []signatureSpan `json:"r,omitempty"`
}

type signatureSpan RestoreSpan

func (span signatureSpan) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{span.Offset, span.Length, span.Original})
}

func (span *signatureSpan) UnmarshalJSON(data []byte) error {
	var parts []json.RawMessage
	if err := json.Unmarshal(data, &parts); err != nil || len(parts) != 3 {
		return errSignatureRecord
	}
	if json.Unmarshal(parts[0], &span.Offset) != nil || json.Unmarshal(parts[1], &span.Length) != nil ||
		json.Unmarshal(parts[2], &span.Original) != nil || span.Offset < 0 || span.Length < 0 {
		return errSignatureRecord
	}
	return nil
}

// WrapSignature 把上游签名和签名内容里的字符串记录打包成客户端看到的签名值；没有记录时原样返回。
func WrapSignature(signature string, values []SignedString) string {
	if signature == "" || len(values) == 0 {
		return signature
	}
	record := signatureRecord{Version: signatureRecordVersion, Signature: signature, Strings: make([]signatureString, 0, len(values))}
	for _, value := range values {
		entry := signatureString{Path: value.Path, Hash: signatureHash(value.Value)}
		for _, span := range value.Spans {
			entry.Spans = append(entry.Spans, signatureSpan(span))
		}
		record.Strings = append(record.Strings, entry)
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return signature
	}
	return base64.StdEncoding.EncodeToString(append([]byte(signatureMagic), payload...))
}

// HasSignatureRecord 报告请求体里是否可能带着网关包装过的签名。
func HasSignatureRecord(body []byte) bool {
	return bytes.Contains(body, []byte(signatureWrapperPrefix))
}

func signatureHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(sum[:signatureHashBytes])
}

// unwrapSignature 解出网关包装。wrapped 表示带网关前缀；前缀对但解不开时 record 为 nil。
func unwrapSignature(value string) (record *signatureRecord, wrapped bool) {
	if !strings.HasPrefix(value, signatureWrapperPrefix) {
		return nil, false
	}
	// 客户端可能按字节解码后用 URL 安全字母表或无填充形式重新编码。
	normalized := strings.NewReplacer("-", "+", "_", "/").Replace(strings.TrimRight(value, "="))
	raw, err := base64.RawStdEncoding.DecodeString(normalized)
	if err != nil || !bytes.HasPrefix(raw, []byte(signatureMagic)) {
		return nil, true
	}
	var decoded signatureRecord
	if json.Unmarshal(raw[len(signatureMagic):], &decoded) != nil ||
		decoded.Version != signatureRecordVersion || decoded.Signature == "" {
		return nil, true
	}
	for i := range decoded.Strings {
		path, ok := normalizeSignaturePath(decoded.Strings[i].Path)
		if !ok {
			return nil, true
		}
		decoded.Strings[i].Path = path
	}
	return &decoded, true
}

// normalizeSignaturePath 把 JSON 解出的路径转换为字段名与非负整数下标。
func normalizeSignaturePath(path []any) ([]any, bool) {
	if len(path) == 0 {
		return nil, false
	}
	normalized := make([]any, len(path))
	for i, element := range path {
		switch value := element.(type) {
		case string:
			normalized[i] = value
		case float64:
			if value < 0 || value != math.Trunc(value) || value > math.MaxInt32 {
				return nil, false
			}
			normalized[i] = int(value)
		default:
			return nil, false
		}
	}
	return normalized, true
}

// PathValue 按路径找到对象里的值：字段名为 string，数组下标为 int；找不到时返回空结果。
func PathValue(block gjson.Result, path []any) gjson.Result {
	current := block
	for _, element := range path {
		var next gjson.Result
		switch key := element.(type) {
		case string:
			if !current.IsObject() {
				return gjson.Result{}
			}
			current.ForEach(func(name, child gjson.Result) bool {
				if name.Str == key {
					next = child
					return false
				}
				return true
			})
		case int:
			if !current.IsArray() {
				return gjson.Result{}
			}
			position := 0
			current.ForEach(func(_, child gjson.Result) bool {
				if position == key {
					next = child
					return false
				}
				position++
				return true
			})
		}
		if !next.Exists() {
			return gjson.Result{}
		}
		current = next
	}
	return current
}

// restoreSignedString 核对客户端字符串与记录一致后，按片段换回上游原文。
func restoreSignedString(value string, entry signatureString) (string, bool) {
	if signatureHash(value) != entry.Hash {
		return "", false
	}
	var out strings.Builder
	position := 0
	for _, span := range entry.Spans {
		if span.Offset < position || span.Length > len(value)-span.Offset {
			return "", false
		}
		out.WriteString(value[position:span.Offset])
		out.WriteString(span.Original)
		position = span.Offset + span.Length
	}
	out.WriteString(value[position:])
	restored := out.String()
	return restored, utf8.ValidString(restored)
}

// signaturePosition 返回协议签名位置上的非空签名：Claude 思考块、Responses 推理文本、
// reasoning_details 条目、Gemini 兼容接口工具调用的 extra_content，以及 Gemini 片段。
func signaturePosition(v gjson.Result) (gjson.Result, bool) {
	var signature gjson.Result
	kind := v.Get("type").Str
	switch {
	case kind == "thinking" || kind == "reasoning_text" || strings.HasPrefix(kind, "reasoning."):
		signature = v.Get("signature")
	case v.Get("function").IsObject():
		signature = v.Get("extra_content.google.thought_signature")
	default:
		signature = v.Get("thoughtSignature")
		if !signature.Exists() {
			signature = v.Get("thought_signature")
		}
	}
	return signature, signature.Type == gjson.String && signature.Str != ""
}
