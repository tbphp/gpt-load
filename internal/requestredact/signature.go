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

// 带签名的内容由上游生成，下一轮必须逐字节还给上游。网关把签名块里需要核对的全部字符串
// （见 SignedContent）记在同一段内容的签名里：字段路径、客户端看到的整串哈希，以及网关改写过的
// 片段和上游原文。包装串是 base64(signatureMagic + JSON)，仍是合法 base64，客户端按字节解码签名时
// 不会出错。回传时签名块的字符串与记录完全一致才按位置换回上游原文和原签名；有任何改动、新增或
// 签名之后才到的内容，就按普通上游内容加密。回填只使用记录里的原文，不需要解密，换用其他
// AccessKey 或经过其他网关实例也能还原。
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

// WrapSignature 把上游签名和签名块的字符串记录打包成客户端看到的签名值。没有字符串的签名块
// 同样包装：签名之后才到的内容会让下一轮核对失败，从而按普通上游内容加密。
func WrapSignature(signature string, values []SignedString) string {
	if signature == "" {
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

// SignedContent 列出签名块里需要核对的字符串：签名本身、协议字段与 cache_control 之外的全部字符串，
// args、input 等业务数据里不区分字段名。范围不小于请求侧对上游内容的加密范围，
// 客户端改动或新增其中任何字符串都会让核对失败。
func SignedContent(block gjson.Result, signature []any) ([]SignedString, bool) {
	var values []signedValue
	if !collectSignedContent(block, nil, signature, false, &values, 0) {
		return nil, false
	}
	result := make([]SignedString, len(values))
	for i, value := range values {
		result[i] = value.SignedString
	}
	return result, true
}

type signedValue struct {
	SignedString
	result gjson.Result
}

func collectSignedContent(value gjson.Result, path, signature []any, business bool, values *[]signedValue, depth int) bool {
	if depth > 64 {
		return false
	}
	switch {
	case value.Type == gjson.String:
		if !signaturePathEqual(path, signature) {
			*values = append(*values, signedValue{SignedString{Path: append([]any(nil), path...), Value: value.Str}, value})
		}
	case value.IsObject():
		ok := true
		value.ForEach(func(key, child gjson.Result) bool {
			if !business && (ProtocolField(key.Str) || key.Str == "cache_control") {
				return true
			}
			ok = collectSignedContent(child, append(path, key.Str), signature, business || signedBusinessField(key.Str), values, depth+1)
			return ok
		})
		return ok
	case value.IsArray():
		ok, index := true, 0
		value.ForEach(func(_, child gjson.Result) bool {
			ok = collectSignedContent(child, append(path, index), signature, business, values, depth+1)
			index++
			return ok
		})
		return ok
	}
	return true
}

// signedBusinessField 标出业务数据：请求侧对其中所有字符串加密，不看字段名。
func signedBusinessField(key string) bool {
	switch key {
	case "args", "input", "response", "output", "arguments":
		return true
	}
	return false
}

func signaturePathEqual(left, right []any) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

type signedRestore struct {
	value gjson.Result
	text  string
}

// restoreSignedBlock 核对签名块的字符串与记录完全一致（路径相同、整串哈希相同），
// 返回需要换回上游原文的字符串；任何不一致都返回 false。
func restoreSignedBlock(block gjson.Result, signature []any, record *signatureRecord) ([]signedRestore, bool) {
	var values []signedValue
	if !collectSignedContent(block, nil, signature, false, &values, 0) || len(values) != len(record.Strings) {
		return nil, false
	}
	entries := make(map[string]signatureString, len(record.Strings))
	for _, entry := range record.Strings {
		key, err := json.Marshal(entry.Path)
		if err != nil {
			return nil, false
		}
		if _, duplicate := entries[string(key)]; duplicate {
			return nil, false
		}
		entries[string(key)] = entry
	}
	var restored []signedRestore
	for _, value := range values {
		key, err := json.Marshal(value.Path)
		if err != nil {
			return nil, false
		}
		entry, found := entries[string(key)]
		if !found {
			return nil, false
		}
		text, ok := restoreSignedString(value.Value, entry)
		if !ok {
			return nil, false
		}
		if text != value.Value {
			restored = append(restored, signedRestore{value.result, text})
		}
	}
	return restored, true
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

// signatureArray 报告上游生成内容的对象（消息、推理项）直属的该字段数组是否可能含协议签名块。
func signatureArray(field string) bool {
	switch field {
	case "content", "parts", "reasoning_details", "tool_calls":
		return true
	}
	return false
}

// signaturePosition 返回协议签名位置上的上游签名及其在对象里的路径。只认响应侧会写入还原记录的位置，
// v 是上游生成内容的对象直属数组 field 的元素：content 里的 Claude 思考块与 Responses 推理文本、
// reasoning_details 条目、tool_calls 里 Gemini 兼容接口的 extra_content，以及 Gemini 的 parts。
// 其他位置上形似签名块的内容按普通上游内容处理，否则网关还原到那里的明文会在下一轮原样外发。
func signaturePosition(v gjson.Result, field string) (gjson.Result, []any, bool) {
	var path []any
	switch kind := v.Get("type").Str; {
	case field == "content" && (kind == "thinking" || kind == "reasoning_text"), field == "reasoning_details":
		path = []any{"signature"}
	case field == "tool_calls":
		path = []any{"extra_content", "google", "thought_signature"}
	case field == "parts" && v.Get("thoughtSignature").Exists():
		path = []any{"thoughtSignature"}
	case field == "parts":
		path = []any{"thought_signature"}
	default:
		return gjson.Result{}, nil, false
	}
	signature := PathValue(v, path)
	return signature, path, signature.Type == gjson.String && signature.Str != "" && !placeholderSignature(signature.Str)
}

// placeholderSignature 报告签名是否为 Gemini 文档给出的占位值。客户端转接其他来源的历史时填入，
// 内容不是上游签过的，上游也不校验，按普通上游内容加密不会导致验签失败。
func placeholderSignature(value string) bool {
	return value == "skip_thought_signature_validator" || value == "context_engineering_is_the_way_to_go"
}
