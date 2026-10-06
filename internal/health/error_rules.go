package health

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"gpt-load/internal/execution"
)

// ErrorRule 是用户配置的匹配条件和动作，不改变上游执行证据。
type ErrorRule struct {
	StatusCodes     []int          `json:"status_codes,omitempty"`
	Keywords        []string       `json:"keywords,omitempty"`
	Retry           RetryDirective `json:"retry"`
	Effect          Effect         `json:"effect"`
	CooldownSeconds int64          `json:"cooldown_seconds,omitempty"`
}

// ErrorRules 保存已校验的不可变规则列表，来源仅用于请求日志。
type ErrorRules struct {
	entries []ErrorRule
	source  string
}

func CompileErrorRules(value any, source string) (ErrorRules, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ErrorRules{}, fmt.Errorf("encode error rules: %w", err)
	}
	if len(encoded) > 256<<10 || len(encoded) == 0 || encoded[0] != '[' {
		return ErrorRules{}, fmt.Errorf("error rules must be an array within 256 KiB")
	}
	var entries []ErrorRule
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entries); err != nil {
		return ErrorRules{}, fmt.Errorf("decode error rules: %w", err)
	}
	if len(entries) > 100 || (source != "global" && source != "group") {
		return ErrorRules{}, fmt.Errorf("error rules exceed 100 entries or have an invalid source")
	}
	for index := range entries {
		entry := &entries[index]
		codes := make([]int, 0, len(entry.StatusCodes))
		for _, code := range entry.StatusCodes {
			if code < 200 || code > 599 {
				return ErrorRules{}, fmt.Errorf("error rule %d: status code must be between 200 and 599", index+1)
			}
			if !slices.Contains(codes, code) {
				codes = append(codes, code)
			}
		}
		keywords := make([]string, 0, len(entry.Keywords))
		seen := make(map[string]struct{}, len(entry.Keywords))
		for _, keyword := range entry.Keywords {
			keyword = strings.TrimSpace(keyword)
			lower := strings.ToLower(keyword)
			if _, exists := seen[lower]; keyword == "" || exists {
				continue
			}
			seen[lower] = struct{}{}
			keywords = append(keywords, keyword)
		}
		entry.StatusCodes, entry.Keywords = codes, keywords
		if len(codes) == 0 && len(keywords) == 0 {
			return ErrorRules{}, fmt.Errorf("error rule %d: at least one condition is required", index+1)
		}
		if entry.Retry != RetryNone && entry.Retry != RetryNextCandidate {
			return ErrorRules{}, fmt.Errorf("error rule %d: retry must be none or next_candidate", index+1)
		}
		if !entry.Effect.Valid() {
			return ErrorRules{}, fmt.Errorf("error rule %d: effect is invalid", index+1)
		}
		cooldown := entry.Effect == EffectCooldownModel || entry.Effect == EffectCooldownCredential
		if cooldown && (entry.CooldownSeconds <= 0 || entry.CooldownSeconds > math.MaxInt64/int64(time.Second)) ||
			!cooldown && entry.CooldownSeconds != 0 {
			return ErrorRules{}, fmt.Errorf("error rule %d: cooldown seconds do not match the effect", index+1)
		}
	}
	return ErrorRules{entries: entries, source: source}, nil
}

// Rules 返回独立的配置副本，避免管理响应修改请求快照。
func (rules ErrorRules) Rules() []ErrorRule {
	entries := make([]ErrorRule, len(rules.entries))
	for index, entry := range rules.entries {
		entries[index] = entry
		entries[index].StatusCodes = slices.Clone(entry.StatusCodes)
		entries[index].Keywords = slices.Clone(entry.Keywords)
	}
	return entries
}

func (rules ErrorRules) apply(base Decision, attempt ExecutionAttempt, ctx DecisionContext) Decision {
	evidence := attempt.Evidence
	if len(rules.entries) == 0 || evidence == nil || base.Origin != execution.ErrorOriginUpstream ||
		base.Category == FailureCategoryClientError || base.Category == FailureCategoryAuthenticationRequired ||
		evidence.Hint == execution.FailureHintRefreshUnavailable || !attempt.ResponseStarted() ||
		(evidence.Kind != execution.ErrorKindHTTP && evidence.Kind != execution.ErrorKindProvider) {
		return base
	}
	// 保留类别映射内的业务约束，自定义动作不能扩大资源请求或请求级限流的影响。
	if base.RuleID == "model.resource_unavailable" || base.RuleID == "rate_limit.scoped" ||
		evidence.Code == "upstream_protocol_error" || evidence.Code == "upstream_response_incomplete" {
		return base
	}
	status := attempt.StatusCode
	if status == 0 {
		status = evidence.StatusCode
	}
	fields := []string{strings.ToLower(evidence.Code), strings.ToLower(evidence.Type), strings.ToLower(evidence.Summary)}
	for index, rule := range rules.entries {
		if len(rule.StatusCodes) > 0 && !slices.Contains(rule.StatusCodes, status) {
			continue
		}
		matched := len(rule.Keywords) == 0
		for _, keyword := range rule.Keywords {
			for _, field := range fields {
				matched = matched || strings.Contains(field, strings.ToLower(keyword))
			}
		}
		if !matched {
			continue
		}
		result := decision(base.Category, base.Origin, base.Scope, rule.Retry, rule.Effect,
			RuleID(fmt.Sprintf("custom.%s.%d", rules.source, index+1)))
		switch rule.Effect {
		case EffectCooldownCredential, EffectRecordCredentialFailure:
			result.Scope = execution.ErrorScopeCredential
		case EffectCooldownModel:
			if ctx.Operation.UsesModelCooldown() {
				result.Scope = execution.ErrorScopeModel
			} else {
				result.Effect = EffectNone
				result.RuleID = "safety.operation_no_model_cooldown"
			}
		case EffectSkipGroup:
			result.Scope = execution.ErrorScopeGroup
		}
		if result.Effect == EffectCooldownCredential || result.Effect == EffectCooldownModel {
			result.CooldownUntil = attempt.Now.Add(time.Duration(rule.CooldownSeconds) * time.Second)
		}
		if result.Retry != RetryNone {
			if !retryableUpstreamResponse(attempt) {
				result.Retry, result.RuleID = RetryNone, "safety.replay_unknown"
			} else if !requestMayReplayAfterResponse(ctx) && evidence.ReplaySafety != execution.ReplaySafetyRejectedBeforeProcessing {
				result.Retry, result.RuleID = RetryNone, "safety.operation_replay_unsafe"
			}
		}
		return result
	}
	return base
}
