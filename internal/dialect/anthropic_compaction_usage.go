package dialect

import (
	"bytes"
	"encoding/json"

	"gpt-load/internal/usage"
)

// 服务端压缩的额外用量位于 iterations；顶层只包含回答轮次。
// 压缩轮次折入请求模型的用量；advisor_message 轮次由另一个模型执行，
// 按各自模型的价格行单独计价（usage.Result.Extra）；未知类型标记为不可计价。
// message 与 fallback_message 重复描述回答或服务端 fallback 的轮次，不再累加。
func anthropicIterationUsage(object map[string]json.RawMessage) (usage.Tokens, []usage.ModelTokens, usage.Diagnostics, bool) {
	raw, present := object["iterations"]
	if !present || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return usage.Tokens{}, nil, usage.Diagnostics{}, false
	}
	var iterations []map[string]json.RawMessage
	if json.Unmarshal(raw, &iterations) != nil {
		return usage.Tokens{}, nil, usageDiagnostic(usage.DiagnosticInvalidNumber), false
	}
	var tokens usage.Tokens
	var extra []usage.ModelTokens
	var diagnostics usage.Diagnostics
	for _, iteration := range iterations {
		var kind string
		if json.Unmarshal(iteration["type"], &kind) != nil {
			continue
		}
		switch kind {
		case "message", "fallback_message":
			continue
		case "compaction", "advisor_message":
		default:
			diagnostics.Add(usage.DiagnosticUnsupportedBillableDetail)
			continue
		}
		model := ""
		if kind == "advisor_message" {
			if json.Unmarshal(iteration["model"], &model) != nil || model == "" {
				diagnostics.Add(usage.DiagnosticUnsupportedBillableDetail)
				continue
			}
		}
		patch, _ := anthropicUsagePatch(iteration, true, false)
		diagnostics.Merge(patch.Diagnostics)
		if !usageIntegerUsable(patch.Diagnostics) || patch.Diagnostics.Has(usage.DiagnosticMissingRequiredField) {
			return usage.Tokens{}, nil, diagnostics, false
		}
		var accumulator usage.Accumulator
		if err := accumulator.MergePatch(patch); err != nil {
			diagnostics.Add(usage.DiagnosticInvalidNumber)
			return usage.Tokens{}, nil, diagnostics, false
		}
		result, _ := accumulator.Finalize(true)
		if kind == "advisor_message" {
			extra = append(extra, usage.ModelTokens{Model: model, Tokens: result.Tokens})
			continue
		}
		combined, ok := addAnthropicUsageTokens(tokens, result.Tokens)
		if !ok {
			diagnostics.Add(usage.DiagnosticInvalidNumber)
			return usage.Tokens{}, nil, diagnostics, false
		}
		tokens = combined
	}
	return tokens, extra, diagnostics, true
}

func (e *anthropicUsageStreamExtractor) observeCompaction(object map[string]json.RawMessage) usage.Diagnostics {
	tokens, extra, diagnostics, ok := anthropicIterationUsage(object)
	if !ok {
		return diagnostics
	}
	// iterations 是快照，重复上报不能重复计费；字段缺失时保留上次快照。
	e.compaction = tokens
	e.extra = extra
	return diagnostics
}

func addAnthropicUsageTokens(left, right usage.Tokens) (usage.Tokens, bool) {
	for _, field := range []struct {
		target *int64
		value  int64
	}{
		{&left.UncachedInput, right.UncachedInput},
		{&left.CacheRead, right.CacheRead},
		{&left.CacheWrite5M, right.CacheWrite5M},
		{&left.CacheWrite1H, right.CacheWrite1H},
		{&left.CacheWriteUnknown, right.CacheWriteUnknown},
		{&left.Output, right.Output},
	} {
		value, ok := usage.CheckedAdd(*field.target, field.value)
		if !ok {
			return usage.Tokens{}, false
		}
		*field.target = value
	}
	_, ok := usage.CheckedTotal(left)
	return left, ok
}

// extraUsage returns a private copy of the passes, or nil when there are none.
func extraUsage(passes []usage.ModelTokens) *usage.ExtraUsage {
	if len(passes) == 0 {
		return nil
	}
	return &usage.ExtraUsage{Passes: append([]usage.ModelTokens(nil), passes...)}
}
