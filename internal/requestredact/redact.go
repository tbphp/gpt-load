// Package requestredact applies configured regular expressions to outbound content.
package requestredact

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

const SettingKey = "request_redaction"
const MaxRules = 64
const maxTextBytes = 128 << 20

var ErrContent = errors.New("request content cannot be redacted")

type Rule struct {
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
}

type Issue struct {
	Index   int    `json:"index"`
	Error   string `json:"error,omitempty"`
	Warning string `json:"warning,omitempty"`
}

type Compiled struct {
	rules    []Rule
	patterns []*regexp.Regexp
}

func Decode(raw []byte) ([]Rule, error) {
	if len(raw) > 1<<20 || !utf8.Valid(raw) {
		return nil, fmt.Errorf("invalid redaction configuration")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var rules []Rule
	if err := d.Decode(&rules); err != nil {
		return nil, err
	}
	if d.Decode(new(any)) != io.EOF || rules == nil {
		return nil, fmt.Errorf("expected redaction rules")
	}
	return rules, nil
}

// Validate 仅检查常见过宽规则；告警不影响配置保存。错误不回显可能含有秘密的表达式。
func Validate(rules []Rule) []Issue {
	issues := make([]Issue, 0)
	if len(rules) > MaxRules {
		return append(issues, Issue{Index: -1, Error: "too_many_rules"})
	}
	encoded, err := json.Marshal(rules)
	if err != nil || len(encoded) > 32<<10 {
		return append(issues, Issue{Index: -1, Error: "configuration_too_large"})
	}
	for i, r := range rules {
		issue := Issue{Index: i}
		switch {
		case r.Pattern == "":
			issue.Error = "empty_pattern"
		case len(r.Pattern) > 4096 || len(r.Replacement) > 4096 || !utf8.ValidString(r.Pattern) || !utf8.ValidString(r.Replacement):
			issue.Error = "invalid_length"
		default:
			p, err := regexp.Compile(r.Pattern)
			if err != nil {
				issue.Error = "invalid_pattern"
				var syntaxErr *syntax.Error
				if errors.As(err, &syntaxErr) {
					issue.Error = string(syntaxErr.Code)
				}
			} else {
				broad := p.MatchString("")
				covered := 0
				for _, sample := range []string{"A normal message with several words.", "这是一段普通的请求内容。", "alpha 123\nbeta 456"} {
					length := 0
					for _, span := range p.FindAllStringIndex(sample, -1) {
						length += span[1] - span[0]
					}
					if length*10 >= len(sample)*8 {
						covered++
					}
				}
				if broad || covered >= 2 {
					issue.Warning = "broad_pattern"
				}
			}
		}
		if issue.Error != "" || issue.Warning != "" {
			issues = append(issues, issue)
		}
	}
	return issues
}

func Compile(rules []Rule) (*Compiled, error) {
	for _, issue := range Validate(rules) {
		if issue.Error != "" {
			return nil, fmt.Errorf("invalid redaction rule %d: %s", issue.Index, issue.Error)
		}
	}
	c := &Compiled{rules: append([]Rule{}, rules...)}
	for _, rule := range rules {
		p, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return nil, err
		}
		c.patterns = append(c.patterns, p)
	}
	return c, nil
}

func (c *Compiled) Rules() []Rule {
	if c == nil {
		return []Rule{}
	}
	return append([]Rule{}, c.rules...)
}

func (c *Compiled) Empty() bool { return c == nil || len(c.rules) == 0 }

// Text 在原文上匹配所有规则，重叠片段合并，替换文本按字面使用且不再次匹配。
func (c *Compiled) Text(value string) (string, error) {
	if c.Empty() {
		return value, nil
	}
	type match struct{ start, end, rule int }
	spans := []match{}
	for i, p := range c.patterns {
		matches := p.FindAllStringIndex(value, 65537)
		if len(matches) > 65536 {
			return "", ErrContent
		}
		for _, span := range matches {
			if span[0] == span[1] {
				continue
			}
			spans = append(spans, match{span[0], span[1], i})
			if len(spans) > 65536 {
				return "", ErrContent
			}
		}
	}
	if len(spans) == 0 {
		return value, nil
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start == spans[j].start {
			return spans[i].rule < spans[j].rule
		}
		return spans[i].start < spans[j].start
	})
	var out strings.Builder
	pos := 0
	for i := 0; i < len(spans); {
		span := spans[i]
		i++
		for i < len(spans) && spans[i].start < span.end {
			span.end = max(span.end, spans[i].end)
			span.rule = min(span.rule, spans[i].rule)
			i++
		}
		if out.Len()+span.start-pos+len(c.rules[span.rule].Replacement) > maxTextBytes {
			return "", ErrContent
		}
		out.WriteString(value[pos:span.start])
		out.WriteString(c.rules[span.rule].Replacement)
		pos = span.end
	}
	if out.Len()+len(value)-pos > maxTextBytes {
		return "", ErrContent
	}
	out.WriteString(value[pos:])
	return out.String(), nil
}

type patch struct {
	start, end int
	value      []byte
}

func (c *Compiled) Apply(body []byte) ([]byte, error) { return c.apply(body, false) }

// apply 只拼接发生变化的 JSON 字符串；保留未命中字节、属性和消息顺序。
func (c *Compiled) apply(body []byte, data bool) ([]byte, error) {
	if c.Empty() || len(bytes.TrimSpace(body)) == 0 {
		return body, nil
	}
	if !json.Valid(body) || !utf8.Valid(body) {
		return nil, ErrContent
	}
	patches := []patch{}
	var walk func(gjson.Result, bool, bool, int) error
	walk = func(v gjson.Result, content, data bool, depth int) error {
		if depth > 64 {
			return ErrContent
		}
		if v.Type == gjson.String {
			if !content && !data {
				return nil
			}
			text, err := c.Text(v.Str)
			if err != nil {
				return err
			}
			if text != v.Str {
				encoded, err := json.Marshal(text)
				if err != nil {
					return err
				}
				patches = append(patches, patch{v.Index, v.Index + len(v.Raw), encoded})
			}
			return nil
		}
		if !v.IsObject() && !v.IsArray() {
			return nil
		}
		if !data {
			switch v.Get("type").Str {
			case "image", "image_url", "input_image", "input_audio", "audio", "video", "input_video", "file", "input_file", "document", "redacted_thinking":
				return nil
			}
		}
		start := len(patches)
		var failure error
		v.ForEach(func(k, child gjson.Result) bool {
			if v.IsArray() || data {
				failure = walk(child, content, data, depth+1)
				return failure == nil
			}
			switch k.Str {
			case "cache_control", "metadata", "signature", "thoughtSignature", "thought_signature", "encrypted_content", "image_url", "audio_url", "file_url", "inlineData", "inline_data", "fileData", "file_data", "source":
				return true
			case "arguments":
				if child.Type == gjson.String && json.Valid([]byte(child.Str)) {
					var rewritten []byte
					rewritten, failure = c.apply([]byte(child.Str), true)
					if failure == nil && string(rewritten) != child.Str {
						var encoded []byte
						encoded, failure = json.Marshal(string(rewritten))
						patches = append(patches, patch{child.Index, child.Index + len(child.Raw), encoded})
					}
				} else {
					failure = walk(child, true, true, depth+1)
				}
			case "args", "output", "response":
				failure = walk(child, true, true, depth+1)
			case "input":
				kind := v.Get("type").Str
				failure = walk(child, true, kind == "tool_use" || kind == "server_tool_use" || kind == "mcp_tool_use", depth+1)
			case "content", "text", "system", "system_instruction", "systemInstruction", "instructions", "prompt", "query", "thinking", "summary", "code", "refusal", "description", "title", "documents", "texts":
				failure = walk(child, true, false, depth+1)
			default:
				failure = walk(child, false, false, depth+1)
			}
			return failure == nil
		})
		if failure != nil {
			return failure
		}
		if !data && len(patches) > start && (v.Get("signature").Str != "" || v.Get("thoughtSignature").Str != "" || v.Get("thought_signature").Str != "") {
			return ErrContent
		}
		return nil
	}
	if err := walk(gjson.ParseBytes(body), false, data, 0); err != nil {
		return nil, err
	}
	if len(patches) == 0 {
		return body, nil
	}
	var out bytes.Buffer
	pos := 0
	for _, p := range patches {
		if p.start < pos || p.end > len(body) || out.Len()+p.start-pos+len(p.value) > maxTextBytes {
			return nil, ErrContent
		}
		out.Write(body[pos:p.start])
		out.Write(p.value)
		pos = p.end
	}
	if out.Len()+len(body)-pos > maxTextBytes {
		return nil, ErrContent
	}
	out.Write(body[pos:])
	return out.Bytes(), nil
}
