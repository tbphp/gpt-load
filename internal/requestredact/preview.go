package requestredact

import (
	"errors"
	"unicode/utf8"
)

const MaxPreviewTextBytes = 64 << 10
const maxPreviewMatches = 200

var ErrPreviewText = errors.New("invalid preview text")
var ErrPreviewMatches = errors.New("too many preview matches")

type PreviewSegment struct {
	Kind        string  `json:"kind"`
	Text        string  `json:"text"`
	RuleIndex   *int    `json:"rule_index,omitempty"`
	Mode        string  `json:"mode,omitempty"`
	Replacement *string `json:"replacement,omitempty"`
}

type Preview struct {
	MatchCount int              `json:"match_count"`
	Segments   []PreviewSegment `json:"segments"`
}

// PreviewText 只预览原始文本的匹配，不创建密文或处理历史密文与协议结构。
func (c *Compiled) PreviewText(value string) (Preview, error) {
	result := Preview{Segments: []PreviewSegment{}}
	if len(value) > MaxPreviewTextBytes || !utf8.ValidString(value) {
		return result, ErrPreviewText
	}
	var matches []textMatch
	if !c.Empty() {
		var err error
		matches, err = c.textMatches(value)
		if err != nil {
			return result, ErrPreviewMatches
		}
		matches = mergeTextMatches(matches)
	}
	if len(matches) > maxPreviewMatches {
		return result, ErrPreviewMatches
	}
	last := 0
	for _, match := range matches {
		if match.start > last {
			result.Segments = append(result.Segments, PreviewSegment{Kind: "text", Text: value[last:match.start]})
		}
		index := match.rule
		rule := c.rules[index]
		segment := PreviewSegment{Kind: "match", Text: value[match.start:match.end], RuleIndex: &index, Mode: rule.Mode}
		if segment.Mode != ModeEncrypt {
			segment.Mode = ModeReplace
			segment.Replacement = &rule.Replacement
		}
		result.Segments = append(result.Segments, segment)
		last = match.end
	}
	if last < len(value) {
		result.Segments = append(result.Segments, PreviewSegment{Kind: "text", Text: value[last:]})
	}
	result.MatchCount = len(matches)
	return result, nil
}
