package dialect

import (
	"bytes"
	"encoding/json"

	"gpt-load/internal/usage"
)

// anthropicIterations 是顶层用量之外仍需计费的轮次。
type anthropicIterations struct {
	compaction usage.Tokens
	advisor    []usage.ModelTokens
}

// 顶层只包含回答轮次，message 与 fallback_message 不能再累加。
// compaction 并入请求模型；advisor_message 由 advisor 模型执行，按模型单独计价。
// 无法归类或缺少模型的轮次标记为不支持的计费明细，不静默丢弃。
func anthropicIterationUsage(object map[string]json.RawMessage) (anthropicIterations, usage.Diagnostics, bool) {
	raw, present := object["iterations"]
	if !present || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return anthropicIterations{}, usage.Diagnostics{}, false
	}
	var iterations []map[string]json.RawMessage
	if json.Unmarshal(raw, &iterations) != nil {
		return anthropicIterations{}, usageDiagnostic(usage.DiagnosticInvalidNumber), false
	}
	var result anthropicIterations
	var diagnostics usage.Diagnostics
	for _, iteration := range iterations {
		var kind, model string
		if json.Unmarshal(iteration["type"], &kind) != nil {
			kind = ""
		}
		switch kind {
		case "message", "fallback_message":
			continue
		case "compaction":
		case "advisor_message":
			if json.Unmarshal(iteration["model"], &model) != nil || model == "" {
				diagnostics.Add(usage.DiagnosticUnsupportedBillableDetail)
				continue
			}
		default:
			diagnostics.Add(usage.DiagnosticUnsupportedBillableDetail)
			continue
		}
		patch, _ := anthropicUsagePatch(iteration, true, false)
		diagnostics.Merge(patch.Diagnostics)
		if !usageIntegerUsable(patch.Diagnostics) || patch.Diagnostics.Has(usage.DiagnosticMissingRequiredField) {
			return anthropicIterations{}, diagnostics, false
		}
		var accumulator usage.Accumulator
		if err := accumulator.MergePatch(patch); err != nil {
			diagnostics.Add(usage.DiagnosticInvalidNumber)
			return anthropicIterations{}, diagnostics, false
		}
		finalized, _ := accumulator.Finalize(true)
		var ok bool
		if model == "" {
			result.compaction, ok = addAnthropicUsageTokens(result.compaction, finalized.Tokens)
		} else {
			// 每次 advisor 调用单独计价（按各自提示长度选价格档），不按模型合并。
			_, ok = usage.CheckedTotal(finalized.Tokens)
			result.advisor = append(result.advisor, usage.ModelTokens{Model: model, Tokens: finalized.Tokens})
		}
		if !ok {
			diagnostics.Add(usage.DiagnosticInvalidNumber)
			return anthropicIterations{}, diagnostics, false
		}
	}
	return result, diagnostics, true
}

// withAnthropicIterations 把轮次用量合入最终结果；advisor 用量复制一份，避免与快照共享。
func withAnthropicIterations(result usage.Result, iterations anthropicIterations) usage.Result {
	if tokens, ok := addAnthropicUsageTokens(result.Tokens, iterations.compaction); ok {
		result.Tokens = tokens
	} else {
		result.Diagnostics.Add(usage.DiagnosticInvalidNumber)
		result.State = usage.StatePartial
	}
	if len(iterations.advisor) > 0 {
		result.Advisor = &usage.AdvisorUsage{Calls: append([]usage.ModelTokens(nil), iterations.advisor...)}
	}
	return result
}

func (e *anthropicUsageStreamExtractor) observeIterations(object map[string]json.RawMessage) usage.Diagnostics {
	iterations, diagnostics, ok := anthropicIterationUsage(object)
	// iterations 是快照，重复上报不能重复计费；字段缺失或无效时保留上次快照。
	if ok {
		e.iterations = iterations
	}
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
