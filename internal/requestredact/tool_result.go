package requestredact

import (
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

type toolJSONString struct {
	start, end   int
	raw, decoded string
}

// 原文规则与解码后的 encrypt 命中统一映射到原始坐标，随后只合并、替换一次。
// 字符串内部的 encrypt 只按解码后的字符匹配；replace 和跨 JSON 语法的规则保留旧语义。
func (c *Compiled) jsonTextMatches(text string, rawMatches []textMatch, protected []tokenSpan, cipher TokenCipher) ([]textMatch, []tokenSpan, []toolJSONString, error) {
	sort.Slice(rawMatches, func(i, j int) bool { return rawMatches[i].start < rawMatches[j].start })
	ignored := make([]bool, len(rawMatches))
	var decodedMatches []textMatch
	var literals []toolJSONString
	var walk func(gjson.Result, int) error
	walk = func(value gjson.Result, depth int) error {
		if depth > 64 {
			return ErrContent
		}
		if value.Type == gjson.String {
			literal := toolJSONString{start: value.Index + 1, end: value.Index + len(value.Raw) - 1, raw: value.Raw[1 : len(value.Raw)-1], decoded: value.Str}
			for i := sort.Search(len(rawMatches), func(i int) bool { return rawMatches[i].start >= literal.start }); i < len(rawMatches) && rawMatches[i].start < literal.end; i++ {
				if rawMatches[i].end <= literal.end && c.rules[rawMatches[i].rule].Mode == ModeEncrypt {
					ignored[i] = true
				}
			}
			var matches []textMatch
			positions := map[int]int{}
			for rule, p := range c.patterns {
				if c.rules[rule].Mode != ModeEncrypt {
					continue
				}
				found := p.FindAllStringIndex(value.Str, maxDocumentPatches+1)
				if len(found) > maxDocumentPatches {
					return ErrContent
				}
				for _, span := range found {
					if span[0] == span[1] {
						continue
					}
					matches = append(matches, textMatch{start: span[0], end: span[1], rule: rule})
					if len(decodedMatches)+len(matches) > maxDocumentPatches {
						return ErrContent
					}
					positions[span[0]], positions[span[1]] = -1, -1
				}
			}
			var encodedTokens []tokenSpan
			if strings.Contains(literal.raw, `\`) && strings.Contains(value.Str, "gld1_") {
				var err error
				encodedTokens, err = authenticatedTokenSpans(value.Str, cipher)
				if err != nil {
					return err
				}
				for _, span := range encodedTokens {
					positions[span.start], positions[span.end] = -1, -1
				}
			}
			if len(positions) == 0 {
				return nil
			}
			if err := jsonStringPositions(literal, positions, true); err != nil {
				return err
			}
			for _, m := range matches {
				m.start = literal.start + positions[m.start]
				m.end = literal.start + positions[m.end]
				decodedMatches = append(decodedMatches, m)
			}
			for _, span := range encodedTokens {
				protected = append(protected, tokenSpan{literal.start + positions[span.start], literal.start + positions[span.end]})
			}
			if len(protected) > maxDocumentPatches {
				return ErrContent
			}
			if len(matches) > 0 {
				literals = append(literals, literal)
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
	if err := walk(gjson.Parse(text), 0); err != nil {
		return nil, nil, nil, err
	}
	out := rawMatches[:0]
	for i, m := range rawMatches {
		if !ignored[i] {
			out = append(out, m)
		}
	}
	if len(out)+len(decodedMatches) > maxDocumentPatches {
		return nil, nil, nil, ErrContent
	}
	out = append(out, decodedMatches...)
	sort.Slice(protected, func(i, j int) bool { return protected[i].start < protected[j].start })
	return out, protected, literals, nil
}

// 合并后的加密范围若仍在同一字符串内，直接截取解码文本；不再拼引号解码片段。
func (c *Compiled) decodeJSONMatches(literals []toolJSONString, matches []textMatch) error {
	for _, literal := range literals {
		first := sort.Search(len(matches), func(i int) bool { return matches[i].start >= literal.start })
		positions := map[int]int{}
		for i := first; i < len(matches) && matches[i].start < literal.end; i++ {
			m := matches[i]
			if m.end <= literal.end && c.rules[m.rule].Mode == ModeEncrypt {
				positions[m.start-literal.start], positions[m.end-literal.start] = -1, -1
			}
		}
		if len(positions) == 0 {
			continue
		}
		if err := jsonStringPositions(literal, positions, false); err != nil {
			return err
		}
		for i := first; i < len(matches) && matches[i].start < literal.end; i++ {
			m := &matches[i]
			if m.end <= literal.end && c.rules[m.rule].Mode == ModeEncrypt {
				m.plaintext = literal.decoded[positions[m.start-literal.start]:positions[m.end-literal.start]]
			}
		}
	}
	return nil
}

// 只保存命中边界，内存随命中数增长；单次扫描同时验证边界不切入转义或 Unicode 字符。
func jsonStringPositions(literal toolJSONString, positions map[int]int, fromDecoded bool) error {
	raw, decoded := literal.raw, literal.decoded
	record := func(r, d int) {
		if fromDecoded {
			if _, ok := positions[d]; ok {
				positions[d] = r
			}
		} else {
			if _, ok := positions[r]; ok {
				positions[r] = d
			}
		}
	}
	r, d := 0, 0
	record(r, d)
	for r < len(raw) {
		ch, size := utf8.DecodeRuneInString(raw[r:])
		r += size
		if ch == '\\' {
			if r >= len(raw) {
				return ErrContent
			}
			escaped := raw[r]
			r++
			switch escaped {
			case '"', '\\', '/':
				ch = rune(escaped)
			case 'b':
				ch = '\b'
			case 'f':
				ch = '\f'
			case 'n':
				ch = '\n'
			case 'r':
				ch = '\r'
			case 't':
				ch = '\t'
			case 'u':
				if r+4 > len(raw) {
					return ErrContent
				}
				code, err := strconv.ParseUint(raw[r:r+4], 16, 16)
				if err != nil {
					return ErrContent
				}
				r += 4
				ch = rune(code)
				if utf16.IsSurrogate(ch) {
					if ch >= 0xD800 && ch <= 0xDBFF && r+6 <= len(raw) && raw[r:r+2] == `\u` {
						second, err := strconv.ParseUint(raw[r+2:r+6], 16, 16)
						if err == nil && second >= 0xDC00 && second <= 0xDFFF {
							ch = utf16.DecodeRune(ch, rune(second))
							r += 6
						} else {
							ch = utf8.RuneError
						}
					} else {
						ch = utf8.RuneError
					}
				}
			default:
				return ErrContent
			}
		}
		// 与实际解码值校验，避免把错误坐标用于切片。
		if d >= len(decoded) {
			return ErrContent
		}
		actual, width := utf8.DecodeRuneInString(decoded[d:])
		if actual != ch {
			return ErrContent
		}
		d += width
		record(r, d)
	}
	if d != len(decoded) {
		return ErrContent
	}
	for _, value := range positions {
		if value < 0 {
			return ErrContent
		}
	}
	return nil
}
