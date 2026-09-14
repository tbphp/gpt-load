package dialect

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

// inspectPromptCacheKey 只提取有界、无歧义的顶层软亲和信号，不修改客户端请求。
func inspectPromptCacheKey(body []byte) string {
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return ""
	}
	var key string
	seen := false
	for decoder.More() {
		field, err := decoder.Token()
		if err != nil {
			return ""
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return ""
		}
		if field != "prompt_cache_key" {
			continue
		}
		if seen {
			return ""
		}
		seen = true
		if json.Unmarshal(value, &key) != nil {
			return ""
		}
	}
	if key == "" || len(key) > 256 || !utf8.ValidString(key) || strings.TrimSpace(key) != key {
		return ""
	}
	for _, r := range key {
		if unicode.IsControl(r) {
			return ""
		}
	}
	return key
}
