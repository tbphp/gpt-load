package dialect

import (
	"encoding/json"
	"fmt"

	"gpt-load/internal/usage"
)

var _ UsageExtractor = (*Decisions)(nil)

func (*Decisions) ExtractUsage(body []byte) (usage.Result, error) {
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

	input, inputDiagnostics := decisionsUsageAlias(object, "input_tokens", "prompt_tokens")
	diagnostics.Merge(inputDiagnostics)
	output, outputDiagnostics := decisionsUsageAlias(object, "output_tokens", "completion_tokens")
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

func decisionsUsageAlias(object map[string]json.RawMessage, primary, fallback string) (*int64, usage.Diagnostics) {
	primaryValue, primaryDiagnostics := usageInteger(object, primary, false)
	fallbackValue, fallbackDiagnostics := usageInteger(object, fallback, false)
	primaryDiagnostics.Merge(fallbackDiagnostics)
	if !usageIntegerUsable(primaryDiagnostics) {
		return nil, primaryDiagnostics
	}
	if primaryValue != nil && fallbackValue != nil && *primaryValue != *fallbackValue {
		primaryDiagnostics.Add(usage.DiagnosticInconsistentTotal)
		return nil, primaryDiagnostics
	}
	if primaryValue != nil {
		return primaryValue, primaryDiagnostics
	}
	return fallbackValue, primaryDiagnostics
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
