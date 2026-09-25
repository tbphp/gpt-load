package requestredact

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
)

// 带签名的内容由上游生成，下一轮必须逐字节还给上游。网关在响应里还原过哪些密文，
// 就记在同一段内容的签名里：包装串是 base64(signatureMagic + JSON)，仍是合法 base64，
// 客户端按字节解码签名时不会出错。客户端原样回传后，请求侧只把记录里的值加密回去，
// 其余文字（包括模型自己写的敏感样式文字）原样保留，并换回上游原签名。
const (
	signatureMagic = "GLDSIG"
	// signatureWrapperPrefix 是 base64(signatureMagic)，所有包装串都以它开头，
	// 普通签名只需比较前缀即可排除，无需解码。
	signatureWrapperPrefix = "R0xEU0lH"
	signatureRecordVersion = 1
)

type signatureRecord struct {
	Version   int      `json:"v"`
	Signature string   `json:"s"`
	Tokens    []string `json:"t"`
}

// WrapSignature 把上游签名和本次响应还原过的密文打包成客户端看到的签名值；没有还原时原样返回。
func WrapSignature(signature string, tokens []string) string {
	if signature == "" || len(tokens) == 0 {
		return signature
	}
	payload, err := json.Marshal(signatureRecord{Version: signatureRecordVersion, Signature: signature, Tokens: tokens})
	if err != nil {
		return signature
	}
	return base64.StdEncoding.EncodeToString(append([]byte(signatureMagic), payload...))
}

// HasSignatureRecord 报告请求体里是否可能带着网关包装过的签名。
func HasSignatureRecord(body []byte) bool {
	return bytes.Contains(body, []byte(signatureWrapperPrefix))
}

// unwrapSignature 解出上游原签名与还原记录；不是网关生成的包装时返回 false。
func unwrapSignature(value string) (string, []string, bool) {
	if !strings.HasPrefix(value, signatureWrapperPrefix) {
		return "", nil, false
	}
	// 客户端可能按字节解码后用 URL 安全字母表或无填充形式重新编码。
	normalized := strings.NewReplacer("-", "+", "_", "/").Replace(strings.TrimRight(value, "="))
	raw, err := base64.RawStdEncoding.DecodeString(normalized)
	if err != nil || !bytes.HasPrefix(raw, []byte(signatureMagic)) {
		return "", nil, false
	}
	var record signatureRecord
	if json.Unmarshal(raw[len(signatureMagic):], &record) != nil ||
		record.Version != signatureRecordVersion || record.Signature == "" {
		return "", nil, false
	}
	return record.Signature, record.Tokens, true
}

// signatureValue 返回对象上的非空签名字段（Claude 思考、Gemini 内容片段、reasoning_details 等）。
func signatureValue(v gjson.Result) (gjson.Result, bool) {
	var found gjson.Result
	v.ForEach(func(key, value gjson.Result) bool {
		switch key.Str {
		case "signature", "thoughtSignature", "thought_signature":
			if value.Type == gjson.String && value.Str != "" {
				found = value
				return false
			}
		}
		return true
	})
	return found, found.Exists()
}

// restoredReplacer 解出还原记录里每个密文的原文，长原文优先匹配；记录解不开时不能安全处理。
func restoredReplacer(tokens []string, cipher TokenCipher) (*strings.Replacer, error) {
	if len(tokens) == 0 {
		return nil, nil
	}
	if cipher == nil {
		return nil, ErrContent
	}
	type restoredValue struct{ plaintext, token string }
	values := make([]restoredValue, 0, len(tokens))
	for _, token := range tokens {
		plaintext, err := cipher.RestoreText(token)
		if err != nil || plaintext == token || plaintext == "" {
			return nil, ErrContent
		}
		values = append(values, restoredValue{plaintext, token})
	}
	sort.SliceStable(values, func(i, j int) bool { return len(values[i].plaintext) > len(values[j].plaintext) })
	pairs := make([]string, 0, len(values)*2)
	for _, value := range values {
		pairs = append(pairs, value.plaintext, value.token)
	}
	return strings.NewReplacer(pairs...), nil
}
