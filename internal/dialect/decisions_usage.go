package dialect

import (
	"encoding/json"
	"fmt"

	"gpt-load/internal/usage"
)

var _ UsageExtractor = (*Decisions)(nil)

func (*Decisions) ExtractUsage(body []byte) (usage.Result, error) {
	return extractDecisionsUsage(body, []string{"input_tokens", "prompt_tokens"}, []string{"output_tokens", "completion_tokens"})
}

// extractDecisionsUsage 读取决策响应的 usage；不同决策协议只是输入、输出 token 的字段名不同。
func extractDecisionsUsage(body []byte, inputFields, outputFields []string) (usage.Result, error) {
	root, err := decodeJSONObject(body)
	if err != nil {
		return usage.Result{}, fmt.Errorf("decode decisions usage response")
	}
	object, diagnostics := usageOptionalObject(root, "usage")
	if object == nil {
		var accumulator usage.Accumulator
		if err := accumulator.MergePatch(usage.Patch{Diagnostics: diagnostics}); err != nil {
			return usage.Result{}, fmt.Errorf("normalize decisions usage response")
		}
		result, _ := accumulator.Finalize(true)
		return result, nil
	}

	input, inputDiagnostics := decisionsUsageAlias(object, inputFields...)
	diagnostics.Merge(inputDiagnostics)
	output, outputDiagnostics := decisionsUsageAlias(object, outputFields...)
	diagnostics.Merge(outputDiagnostics)
	if input == nil || output == nil {
		diagnostics.Add(usage.DiagnosticMissingRequiredField)
	}
	if input != nil && output != nil {
		total, totalDiagnostics := usageInteger(object, "total_tokens", false)
		diagnostics.Merge(totalDiagnostics)
		usageNormalizedTotal(total, usage.Tokens{UncachedInput: *input, Output: *output}, &diagnostics)
		if diagnostics.Has(usage.DiagnosticInconsistentTotal) || !usageIntegerUsable(totalDiagnostics) {
			input, output = nil, nil
		}
	}

	var accumulator usage.Accumulator
	if err := accumulator.ReplaceSnapshot(usage.Patch{
		UncachedInput: input,
		Output:        output,
		Final:         input != nil && output != nil,
		Diagnostics:   diagnostics,
	}); err != nil {
		return usage.Result{}, fmt.Errorf("normalize decisions usage response")
	}
	result, _ := accumulator.Finalize(true)
	return result, nil
}

// decisionsUsageAlias 按顺序读取同义字段，取第一个出现的值；同时出现且取值不同时视为不一致。
func decisionsUsageAlias(object map[string]json.RawMessage, names ...string) (*int64, usage.Diagnostics) {
	var value *int64
	var diagnostics usage.Diagnostics
	inconsistent := false
	for _, name := range names {
		candidate, candidateDiagnostics := usageInteger(object, name, false)
		diagnostics.Merge(candidateDiagnostics)
		if candidate == nil {
			continue
		}
		if value == nil {
			value = candidate
		} else if *value != *candidate {
			inconsistent = true
		}
	}
	if !usageIntegerUsable(diagnostics) {
		return nil, diagnostics
	}
	if inconsistent {
		diagnostics.Add(usage.DiagnosticInconsistentTotal)
		return nil, diagnostics
	}
	return value, diagnostics
}

func (*Decisions) NewUsageStreamExtractor() UsageStreamExtractor {
	return &decisionsUnsupportedStreamUsage{}
}

type decisionsUnsupportedStreamUsage struct{ finalized bool }

func (*decisionsUnsupportedStreamUsage) Observe([]byte) error {
	return fmt.Errorf("decisions does not support streaming usage")
}

func (e *decisionsUnsupportedStreamUsage) Finalize() (usage.Result, bool) {
	if e.finalized {
		return usage.Result{}, false
	}
	e.finalized = true
	return usage.Result{State: usage.StateNotApplicable}, true
}
