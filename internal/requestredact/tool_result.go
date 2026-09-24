package requestredact

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
)

// decodeJSONMatches 仅解码完全位于 JSON 字符串内的 encrypt 命中。
// 匹配范围、重叠合并及规则优先级仍由原始文本决定，不二次匹配替换结果。
func (c *Compiled) decodeJSONMatches(text string, matches []textMatch) error {
	if !strings.Contains(text, `\`) {
		return nil
	}
	var walk func(gjson.Result, int) error
	walk = func(value gjson.Result, depth int) error {
		if depth > 64 {
			return ErrContent
		}
		if value.Type == gjson.String {
			if !strings.Contains(value.Raw, `\`) {
				return nil
			}
			start, end := value.Index+1, value.Index+len(value.Raw)-1
			i := sort.Search(len(matches), func(i int) bool { return matches[i].start >= start })
			for ; i < len(matches) && matches[i].start < end; i++ {
				m := &matches[i]
				if m.end > end || c.rules[m.rule].Mode != ModeEncrypt {
					continue
				}
				// JSON 解码失败不能回退成错误的原值或明文。
				if json.Unmarshal([]byte(`"`+text[m.start:m.end]+`"`), &m.plaintext) != nil {
					return ErrContent
				}
			}
			return nil
		}
		if !value.IsObject() && !value.IsArray() {
			return nil
		}
		var failure error
		value.ForEach(func(key, child gjson.Result) bool {
			if value.IsObject() {
				failure = walk(key, depth+1)
			}
			if failure == nil {
				failure = walk(child, depth+1)
			}
			return failure == nil
		})
		return failure
	}
	return walk(gjson.Parse(text), 0)
}
