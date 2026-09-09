package dialect

import (
	"encoding/json"
	"fmt"

	"gpt-load/internal/usage"
)

func (*Rerank) ExtractUsage(body []byte) (usage.Result, error) {
	root, err := decodeJSONObject(body)
	if err != nil {
		return usage.Result{}, fmt.Errorf("decode rerank usage response")
	}
	object, diagnostics := usageOptionalObject(root, "usage")
	meta, metaDiagnostics := usageOptionalObject(root, "meta")
	diagnostics.Merge(metaDiagnostics)
	tokens, tokenDiagnostics := usageOptionalObject(meta, "tokens")
	diagnostics.Merge(tokenDiagnostics)
	billed, billedDiagnostics := usageOptionalObject(meta, "billed_units")
	diagnostics.Merge(billedDiagnostics)
	var input *int64
	invalid := !usageIntegerUsable(diagnostics)
	// 只采用明确且一致的 Token 证据；search_units 不属于 Token。
	for _, source := range []struct {
		value *int64
		diag  usage.Diagnostics
	}{
		rerankUsageInteger(object, "prompt_tokens"),
		rerankUsageInteger(object, "input_tokens"),
		rerankUsageInteger(object, "total_tokens"),
		rerankUsageInteger(tokens, "input_tokens"),
		rerankUsageInteger(billed, "input_tokens"),
	} {
		diagnostics.Merge(source.diag)
		if !usageIntegerUsable(source.diag) {
			invalid = true
		}
		if source.value == nil {
			continue
		}
		if input != nil && *input != *source.value {
			diagnostics.Add(usage.DiagnosticInconsistentTotal)
			invalid = true
		}
		input = source.value
	}
	for _, source := range []struct {
		value *int64
		diag  usage.Diagnostics
	}{
		rerankUsageInteger(object, "output_tokens"),
		rerankUsageInteger(object, "completion_tokens"),
		rerankUsageInteger(tokens, "output_tokens"),
		rerankUsageInteger(billed, "output_tokens"),
	} {
		diagnostics.Merge(source.diag)
		if !usageIntegerUsable(source.diag) || source.value != nil && *source.value != 0 {
			diagnostics.Add(usage.DiagnosticUnsupportedBillableDetail)
			invalid = true
		}
	}
	if invalid {
		input = nil
	}
	var accumulator usage.Accumulator
	if err := accumulator.ReplaceSnapshot(usage.Patch{UncachedInput: input, Final: true, Diagnostics: diagnostics}); err != nil {
		return usage.Result{}, fmt.Errorf("normalize rerank usage response")
	}
	result, _ := accumulator.Finalize(true)
	return result, nil
}

func (*Rerank) NewUsageStreamExtractor() UsageStreamExtractor {
	return &rerankUnsupportedStreamUsage{}
}

type rerankUnsupportedStreamUsage struct{ finalized bool }

func (*rerankUnsupportedStreamUsage) Observe([]byte) error {
	return fmt.Errorf("rerank does not support streaming usage")
}

func (e *rerankUnsupportedStreamUsage) Finalize() (usage.Result, bool) {
	if e.finalized {
		return usage.Result{}, false
	}
	e.finalized = true
	return usage.Result{State: usage.StateNotApplicable}, true
}

func rerankUsageInteger(object map[string]json.RawMessage, field string) struct {
	value *int64
	diag  usage.Diagnostics
} {
	value, diag := usageInteger(object, field, false)
	return struct {
		value *int64
		diag  usage.Diagnostics
	}{value, diag}
}
