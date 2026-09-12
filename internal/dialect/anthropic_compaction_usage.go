package dialect

import (
	"bytes"
	"encoding/json"

	"gpt-load/internal/usage"
)

// 服务端压缩的额外用量位于 iterations；顶层只包含回答轮次。
// 仅计入 compaction，不能再累加重复描述回答或服务端 fallback 的轮次。
func (e *anthropicUsageStreamExtractor) observeCompaction(object map[string]json.RawMessage) usage.Diagnostics {
	raw, present := object["iterations"]
	if !present || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return usage.Diagnostics{}
	}
	var iterations []map[string]json.RawMessage
	if json.Unmarshal(raw, &iterations) != nil {
		return usageDiagnostic(usage.DiagnosticInvalidNumber)
	}
	var tokens usage.Tokens
	var diagnostics usage.Diagnostics
	for _, iteration := range iterations {
		var kind string
		if json.Unmarshal(iteration["type"], &kind) != nil || kind != "compaction" {
			continue
		}
		patch, _ := anthropicUsagePatch(iteration, true, false)
		diagnostics.Merge(patch.Diagnostics)
		if !usageIntegerUsable(patch.Diagnostics) || patch.Diagnostics.Has(usage.DiagnosticMissingRequiredField) {
			return diagnostics
		}
		var accumulator usage.Accumulator
		if err := accumulator.MergePatch(patch); err != nil {
			diagnostics.Add(usage.DiagnosticInvalidNumber)
			return diagnostics
		}
		result, _ := accumulator.Finalize(true)
		combined, ok := addAnthropicUsageTokens(tokens, result.Tokens)
		if !ok {
			diagnostics.Add(usage.DiagnosticInvalidNumber)
			return diagnostics
		}
		tokens = combined
	}
	// iterations 是快照，重复上报不能重复计费；字段缺失时保留上次快照。
	e.compaction = tokens
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
