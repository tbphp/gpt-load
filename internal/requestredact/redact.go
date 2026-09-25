// Package requestredact applies configured regular expressions to outbound content.
package requestredact

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"regexp/syntax"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

const SettingKey = "request_redaction"
const MaxRules = 64
const ModeReplace = "replace"
const ModeEncrypt = "encrypt"
const maxTextBytes = 128 << 20
const maxDocumentPatches = 65536

var ErrContent = errors.New("request content cannot be redacted")

type Rule struct {
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
	Mode        string `json:"mode,omitempty"`
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

// TokenCipher is the request-side behavior needed from the authenticated
// AccessKey cipher. Keeping this contract local avoids a state-to-crypto
// dependency through compiled configuration.
type TokenCipher interface {
	EncryptToken(string) (string, error)
	TokenCandidateEnd(string, int) (int, bool)
	ValidTokenAt(string, int) (int, bool)
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
		case r.Mode != "" && r.Mode != ModeReplace && r.Mode != ModeEncrypt:
			issue.Error = "invalid_mode"
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

// Reversible 报告是否配置了可逆加密规则；没有时上游不会拿到新的密文。
func (c *Compiled) Reversible() bool {
	if c == nil {
		return false
	}
	for _, rule := range c.rules {
		if rule.Mode == ModeEncrypt {
			return true
		}
	}
	return false
}

func (c *Compiled) TextWithCipher(value string, cipher TokenCipher) (string, error) {
	return c.textWithCipher(value, cipher, false)
}

func (c *Compiled) textWithCipher(value string, cipher TokenCipher, jsonText bool) (string, error) {
	if c.Empty() {
		return value, nil
	}
	if cipher == nil {
		return c.rewriteText(value, nil, nil, false)
	}
	protected, err := authenticatedTokenSpans(value, cipher)
	if err != nil {
		return "", err
	}
	return c.rewriteText(value, cipher, protected, jsonText)
}

func authenticatedTokenSpans(value string, cipher TokenCipher) ([]tokenSpan, error) {
	const tokenPrefix = "gld1_"
	protected := make([]tokenSpan, 0)
	scan := 0
	attempts, remaining := 0, len(value)
	for scan < len(value) {
		relative := strings.Index(value[scan:], tokenPrefix)
		if relative < 0 {
			break
		}
		start := scan + relative
		attempts++
		if attempts > maxDocumentPatches {
			return nil, ErrContent
		}
		candidateEnd, complete := cipher.TokenCandidateEnd(value, start)
		if !complete {
			scan = start + len(tokenPrefix)
			continue
		}
		if candidateEnd <= start || candidateEnd > len(value) || candidateEnd-start > remaining {
			return nil, ErrContent
		}
		remaining -= candidateEnd - start
		end, valid := cipher.ValidTokenAt(value, start)
		if !valid {
			scan = start + len(tokenPrefix)
			continue
		}
		protected = append(protected, tokenSpan{start, end})
		if len(protected) > maxDocumentPatches {
			return nil, ErrContent
		}
		scan = end
	}
	return protected, nil
}

// Text 在原文上匹配所有规则，重叠片段合并，替换文本按字面使用且不再次匹配。
func (c *Compiled) Text(value string) (string, error) {
	if c.Empty() {
		return value, nil
	}
	return c.rewriteText(value, nil, nil, false)
}

type tokenSpan struct{ start, end int }

func protectedMatch(spans []tokenSpan, start, end int) (inside, crossing bool) {
	index := sort.Search(len(spans), func(i int) bool { return spans[i].end > start })
	if index >= len(spans) || spans[index].start >= end {
		return false, false
	}
	span := spans[index]
	if start >= span.start && end <= span.end {
		return true, false
	}
	return false, true
}

func encryptedTokenLength(plaintextBytes int) int {
	encoded := base64.RawURLEncoding.EncodedLen(plaintextBytes + 16)
	return len("gld1_") + len(strconv.Itoa(encoded)) + 1 + encoded
}

type textMatch struct {
	start, end, rule int
	plaintext        string
}

func (c *Compiled) rewriteText(value string, cipher TokenCipher, protected []tokenSpan, jsonText bool) (string, error) {
	spans := []textMatch{}
	for i, p := range c.patterns {
		matches := p.FindAllStringIndex(value, 65537)
		if len(matches) > 65536 {
			return "", ErrContent
		}
		for _, span := range matches {
			if span[0] == span[1] {
				continue
			}
			if c.rules[i].Mode == ModeEncrypt && cipher == nil {
				return "", ErrContent
			}
			spans = append(spans, textMatch{start: span[0], end: span[1], rule: i})
			if len(spans) > 65536 {
				return "", ErrContent
			}
		}
	}
	var literals []toolJSONString
	if jsonText {
		var err error
		spans, protected, literals, err = c.jsonTextMatches(value, spans, protected, cipher)
		if err != nil {
			return "", err
		}
	}
	filtered := spans[:0]
	for _, span := range spans {
		inside, crossing := protectedMatch(protected, span.start, span.end)
		if crossing {
			return "", ErrContent
		}
		if !inside {
			filtered = append(filtered, span)
		}
	}
	spans = filtered
	if len(spans) == 0 {
		return value, nil
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start == spans[j].start {
			return spans[i].rule < spans[j].rule
		}
		return spans[i].start < spans[j].start
	})
	merged := spans[:0]
	for i := 0; i < len(spans); {
		span := spans[i]
		i++
		for i < len(spans) && spans[i].start < span.end {
			span.end = max(span.end, spans[i].end)
			span.rule = min(span.rule, spans[i].rule)
			i++
		}
		merged = append(merged, span)
	}
	for i := range merged {
		merged[i].plaintext = value[merged[i].start:merged[i].end]
	}
	if jsonText {
		if err := c.decodeJSONMatches(literals, merged); err != nil {
			return "", err
		}
	}
	finalBytes := int64(len(value))
	for _, span := range merged {
		replacementBytes := len(c.rules[span.rule].Replacement)
		if c.rules[span.rule].Mode == ModeEncrypt {
			replacementBytes = encryptedTokenLength(len(span.plaintext))
		}
		finalBytes += int64(replacementBytes) - int64(span.end-span.start)
	}
	if finalBytes > maxTextBytes || finalBytes < 0 {
		return "", ErrContent
	}
	var out strings.Builder
	out.Grow(int(finalBytes))
	pos := 0
	for _, span := range merged {
		replacement := c.rules[span.rule].Replacement
		if c.rules[span.rule].Mode == ModeEncrypt {
			var err error
			replacement, err = cipher.EncryptToken(span.plaintext)
			if err != nil {
				return "", ErrContent
			}
		}
		if out.Len()+span.start-pos+len(replacement) > maxTextBytes {
			return "", ErrContent
		}
		out.WriteString(value[pos:span.start])
		out.WriteString(replacement)
		pos = span.end
	}
	if out.Len()+len(value)-pos > maxTextBytes {
		return "", ErrContent
	}
	out.WriteString(value[pos:])
	return out.String(), nil
}

// ProtocolField 报告字段是否是协议标识或不透明数据。请求加密上游生成的历史内容、
// 响应还原上游输出时都跳过这些字段，保证凡是还原过的内容回到请求里都能加密回去。
func ProtocolField(key string) bool {
	switch key {
	case "id", "type", "role", "name", "status", "model", "modelVersion", "object", "metadata",
		"signature", "thoughtSignature", "thought_signature", "encrypted_content",
		"data", "b64_json", "result", "url", "image_url", "audio_url", "file_url",
		"file_data", "fileData", "inline_data", "inlineData", "mime_type", "mimeType",
		"encrypted_index", "format":
		return true
	}
	return strings.HasSuffix(key, "_id") || strings.HasSuffix(key, "Id")
}

// modelAuthored 识别上游生成、随历史回传的内容：助手/模型角色的消息，
// 以及 Responses 的推理项和各类工具调用项（*_call，不含 *_call_output）。
func modelAuthored(v gjson.Result) bool {
	switch v.Get("role").Str {
	case "assistant", "model":
		return true
	}
	kind := v.Get("type").Str
	return kind == "reasoning" || strings.HasSuffix(kind, "_call")
}

type patch struct {
	start, end int
	value      []byte
}

func (c *Compiled) Apply(body []byte) ([]byte, error) {
	patchCount := 0
	return c.apply(body, false, false, 0, &patchCount, nil)
}

func (c *Compiled) ApplyDecisions(body []byte) ([]byte, error) {
	patchCount := 0
	return c.apply(body, false, true, 0, &patchCount, nil)
}

func (c *Compiled) ApplyWithCipher(body []byte, cipher TokenCipher) ([]byte, error) {
	patchCount := 0
	return c.apply(body, false, false, 0, &patchCount, cipher)
}

func (c *Compiled) ApplyDecisionsWithCipher(body []byte, cipher TokenCipher) ([]byte, error) {
	patchCount := 0
	return c.apply(body, false, true, 0, &patchCount, cipher)
}

// apply 只拼接发生变化的 JSON 字符串；保留未命中字节、属性和消息顺序。
func (c *Compiled) apply(body []byte, data, decisions bool, depth int, patchCount *int, cipher TokenCipher) ([]byte, error) {
	// 没有规则时仍要拆掉网关包装过的签名，否则上游收到的不是它自己的签名。
	if len(bytes.TrimSpace(body)) == 0 || (c.Empty() && !HasSignatureRecord(body)) {
		return body, nil
	}
	if c == nil {
		c = &Compiled{}
	}
	if depth > 64 {
		return nil, ErrContent
	}
	if !json.Valid(body) || !utf8.Valid(body) {
		return nil, ErrContent
	}
	patches := []patch{}
	addPatch := func(p patch) error {
		if *patchCount >= maxDocumentPatches {
			return ErrContent
		}
		*patchCount++
		patches = append(patches, p)
		return nil
	}
	// 工具返回文本保持原始匹配语义；仅调整 JSON 字符串内加密片段的原值。
	jsonToolResults := false
	for _, rule := range c.rules {
		jsonToolResults = jsonToolResults || (cipher != nil && rule.Mode == ModeEncrypt)
	}
	rewriteEmbedded := func(value gjson.Result, depth int) error {
		rewritten, err := c.apply([]byte(value.Str), true, false, depth+1, patchCount, cipher)
		if err != nil || string(rewritten) == value.Str {
			return err
		}
		encoded, err := json.Marshal(string(rewritten))
		if err != nil {
			return err
		}
		return addPatch(patch{value.Index, value.Index + len(value.Raw), encoded})
	}
	// 上游生成、随历史回传的内容（助手消息、推理与各类工具调用）按字段整体加密，
	// 与响应侧整体还原对应，只跳过协议字段。
	inModel := false
	// inSigned 表示正在按普通上游内容处理客户端改动过的签名内容，内部不再识别签名。
	inSigned := false
	var walk func(gjson.Result, bool, bool, int, bool) error
	var toolResult func(gjson.Result, int) error
	// 签名内容由上游生成，必须逐字节还给上游：签名块与记录完全一致时按位置换回上游原文和原签名；
	// 普通签名说明网关没有往里还原过内容，原样发回；有任何改动、新增或签名之后才到的内容时，
	// 按普通上游内容加密，保证明文不外泄，并换回原签名。
	signed := func(v, signature gjson.Result, signaturePath []any, content bool, depth int, questions bool) error {
		record, wrapped := unwrapSignature(signature.Str)
		if !wrapped {
			return nil
		}
		var restored []signedRestore
		exact := false
		if record != nil {
			restored, exact = restoreSignedBlock(v, signaturePath, record)
			encoded, err := json.Marshal(record.Signature)
			if err != nil {
				return err
			}
			if err := addPatch(patch{signature.Index, signature.Index + len(signature.Raw), encoded}); err != nil {
				return err
			}
		}
		if exact {
			for _, item := range restored {
				encoded, err := json.Marshal(item.text)
				if err != nil {
					return err
				}
				if err := addPatch(patch{item.value.Index, item.value.Index + len(item.value.Raw), encoded}); err != nil {
					return err
				}
			}
			return nil
		}
		inSigned = true
		defer func() { inSigned = false }()
		return walk(v, content, false, depth, questions)
	}
	toolResult = func(value gjson.Result, depth int) error {
		if depth > 64 {
			return ErrContent
		}
		if value.Type == gjson.String && json.Valid([]byte(value.Str)) {
			text, err := c.textWithCipher(value.Str, cipher, true)
			if err != nil || text == value.Str {
				return err
			}
			encoded, err := json.Marshal(text)
			if err != nil {
				return err
			}
			return addPatch(patch{value.Index, value.Index + len(value.Raw), encoded})
		}
		if value.IsArray() {
			var failure error
			value.ForEach(func(_, part gjson.Result) bool {
				switch part.Get("type").Str {
				case "text", "input_text", "output_text":
					failure = toolResult(part.Get("text"), depth+1)
				default:
					failure = walk(part, true, false, depth+1, false)
				}
				return failure == nil
			})
			return failure
		}
		return walk(value, true, false, depth, false)
	}
	walk = func(v gjson.Result, content, data bool, depth int, questions bool) error {
		if depth > 64 {
			return ErrContent
		}
		if v.Type == gjson.String {
			if !content && !data {
				return nil
			}
			var text string
			var err error
			if cipher == nil {
				text, err = c.Text(v.Str)
			} else {
				text, err = c.TextWithCipher(v.Str, cipher)
			}
			if err != nil {
				return err
			}
			if text != v.Str {
				encoded, err := json.Marshal(text)
				if err != nil {
					return err
				}
				return addPatch(patch{v.Index, v.Index + len(v.Raw), encoded})
			}
			return nil
		}
		if !v.IsObject() && !v.IsArray() {
			return nil
		}
		if !data {
			switch v.Get("type").Str {
			case "image", "image_url", "input_image", "input_audio", "audio", "video", "input_video", "file", "input_file", "redacted_thinking":
				return nil
			}
		}
		if !data && inModel && !inSigned && v.IsObject() {
			if signature, path, found := signaturePosition(v); found {
				return signed(v, signature, path, content, depth, questions)
			}
		}
		if !data && !inModel && v.IsObject() && modelAuthored(v) {
			inModel = true
			defer func() { inModel = false }()
		}
		var failure error
		v.ForEach(func(k, child gjson.Result) bool {
			if v.IsArray() || data {
				failure = walk(child, content, data, depth+1, questions)
				return failure == nil
			}
			if inModel && ProtocolField(k.Str) {
				return true
			}
			switch k.Str {
			case "questions":
				failure = walk(child, false, false, depth+1, decisions && depth == 0)
			case "state":
				isDecisionsState := decisions && depth == 0
				failure = walk(child, isDecisionsState, isDecisionsState, depth+1, false)
			case "criteria":
				isDecisionsContent := questions && depth == 2
				failure = walk(child, isDecisionsContent, isDecisionsContent, depth+1, questions)
			case "cache_control", "metadata", "signature", "thoughtSignature", "thought_signature", "encrypted_content", "image_url", "audio_url", "file_url", "inlineData", "inline_data", "fileData", "file_data":
				return true
			case "source":
				// 文档只处理纯文本来源的正文；base64、URL、文件等来源保持不变。
				if v.Get("type").Str != "document" {
					return true
				}
				if kind := child.Get("type").Str; kind == "text" || kind == "content" {
					child.ForEach(func(key, value gjson.Result) bool {
						if key.Str == "data" || key.Str == "content" {
							failure = walk(value, true, false, depth+2, questions)
						}
						return failure == nil
					})
				}
			case "context":
				failure = walk(child, inModel || v.Get("type").Str == "document", false, depth+1, questions)
			case "arguments":
				if child.Type == gjson.String && json.Valid([]byte(child.Str)) {
					failure = rewriteEmbedded(child, depth)
				} else {
					failure = walk(child, true, true, depth+1, questions)
				}
			case "output":
				if jsonToolResults && (v.Get("type").Str == "function_call_output" || v.Get("type").Str == "custom_tool_call_output") {
					failure = toolResult(child, depth+1)
					return failure == nil
				}
				isResponsesContent := v.Get("type").Str == "function_call_output" && child.IsArray()
				failure = walk(child, true, !isResponsesContent, depth+1, questions)
			case "args", "response":
				failure = walk(child, true, true, depth+1, questions)
			case "input":
				kind := v.Get("type").Str
				failure = walk(child, true, kind == "tool_use" || kind == "server_tool_use" || kind == "mcp_tool_use", depth+1, questions)
			case "content":
				if jsonToolResults && (v.Get("role").Str == "tool" || v.Get("role").Str == "function" || v.Get("type").Str == "tool_result") {
					failure = toolResult(child, depth+1)
				} else {
					failure = walk(child, true, false, depth+1, questions)
				}
			case "prompt":
				if !child.IsObject() {
					failure = walk(child, true, false, depth+1, questions)
					break
				}
				// Responses 提示词模板：变量值按正文处理，id、version 等引用字段保持不变。
				child.ForEach(func(key, variables gjson.Result) bool {
					if key.Str == "variables" {
						variables.ForEach(func(_, value gjson.Result) bool {
							failure = walk(value, true, false, depth+3, questions)
							return failure == nil
						})
					}
					return failure == nil
				})
			case "text", "system", "system_instruction", "systemInstruction", "instructions", "query", "thinking", "reasoning", "reasoning_content", "summary", "code", "refusal", "description", "title", "documents", "texts":
				failure = walk(child, true, questions && depth == 2 && k.Str == "instructions", depth+1, questions)
			default:
				failure = walk(child, inModel, false, depth+1, questions)
			}
			return failure == nil
		})
		return failure
	}
	if err := walk(gjson.ParseBytes(body), false, data, depth, false); err != nil {
		return nil, err
	}
	if len(patches) == 0 {
		return body, nil
	}
	// 签名内容的补丁不一定按正文顺序产生；排序后重叠仍会被拒绝。
	sort.SliceStable(patches, func(i, j int) bool { return patches[i].start < patches[j].start })
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
