package control

import (
	"fmt"
	"regexp"
	"regexp/syntax"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/requestredact"
	"gpt-load/internal/state"
)

// HomeRequestRules 仅公开当前访问密钥适用的规则，不复用管理配置 DTO。
type HomeRequestRules struct {
	Redaction HomeRedactionRules `json:"redaction"`
	Audit     HomeAuditRules     `json:"audit"`
}

type HomeRedactionRules struct {
	Rules []HomeRedactionRule `json:"rules"`
}

type HomeRedactionRule struct {
	Pattern     string `json:"pattern"`
	Mode        string `json:"mode"`
	Replacement string `json:"replacement,omitempty"`
}

type HomeAuditRules struct {
	Enabled     bool            `json:"enabled"`
	ChannelName string          `json:"channel_name,omitempty"`
	Model       string          `json:"model,omitempty"`
	Rules       []HomeAuditRule `json:"rules"`
}

type HomeAuditRule struct {
	Name         string `json:"name"`
	Instructions string `json:"instructions"`
	Action       string `json:"action"`
}

func (s *Service) homeRequestRules(snapshot *state.ConfigSnapshot, accessKeyID uint) (HomeRequestRules, error) {
	result := HomeRequestRules{
		Redaction: HomeRedactionRules{Rules: []HomeRedactionRule{}},
		Audit:     HomeAuditRules{Enabled: snapshot.RequestAudit.Applies(accessKeyID), Rules: []HomeAuditRule{}},
	}
	rules := snapshot.RequestRedaction.Rules()
	patterns := make([]*regexp.Regexp, 0, len(rules))
	for _, rule := range rules {
		pattern, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return HomeRequestRules{}, fmt.Errorf("read home request rules: invalid compiled redaction rule")
		}
		patterns = append(patterns, pattern)
	}
	diagnostics := redact.New()
	mask := func(value string) string {
		for _, pattern := range patterns {
			value = pattern.ReplaceAllLiteralString(value, redact.Placeholder)
		}
		return diagnostics.String(value)
	}
	for _, rule := range rules {
		mode := rule.Mode
		if mode == "" {
			mode = requestredact.ModeReplace
		}
		view := HomeRedactionRule{Pattern: homeRedactionPattern(rule.Pattern, mask), Mode: mode}
		if mode == requestredact.ModeReplace {
			view.Replacement = mask(rule.Replacement)
		}
		result.Redaction.Rules = append(result.Redaction.Rules, view)
	}
	if !result.Audit.Enabled {
		return result, nil
	}
	result.Audit.Model = mask(snapshot.Jev.Model)
	group := snapshot.Groups[snapshot.Jev.GroupID]
	if descriptor, ok := s.channelRegistry.Get(group.ChannelID); ok {
		result.Audit.ChannelName = descriptor.Name
	}
	for _, rule := range snapshot.RequestAudit.Rules {
		if rule.Enabled {
			result.Audit.Rules = append(result.Audit.Rules, HomeAuditRule{
				Name: mask(rule.Name), Instructions: mask(rule.Instructions), Action: rule.Action,
			})
		}
	}
	return result, nil
}

func homeRedactionPattern(pattern string, mask func(string) string) string {
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return redact.Placeholder
	}
	// 固定原值可能经过正则转义；按解码后的字面量检查，避免公开被保护的原值。
	var sensitiveLiteral func(*syntax.Regexp) bool
	sensitiveLiteral = func(node *syntax.Regexp) bool {
		if node.Op == syntax.OpLiteral {
			literal := string(node.Rune)
			if mask(literal) != literal {
				return true
			}
		}
		for _, child := range node.Sub {
			if sensitiveLiteral(child) {
				return true
			}
		}
		return false
	}
	if sensitiveLiteral(parsed) {
		return redact.Placeholder
	}
	return mask(pattern)
}
