package dialect

import (
	"encoding/json"
	"fmt"

	"gpt-load/internal/usage"
)

func (d *Anthropic) ExtractUsage(body []byte) (usage.Result, error) {
	root, err := decodeJSONObject(body)
	if err != nil {
		return usage.Result{}, fmt.Errorf("decode Anthropic usage response")
	}

	usageObject, diagnostics := usageOptionalObject(root, "usage")
	var accumulator usage.Accumulator
	if usageObject == nil {
		if err := accumulator.MergePatch(usage.Patch{Diagnostics: diagnostics}); err != nil {
			return usage.Result{}, fmt.Errorf("normalize Anthropic usage response")
		}
	} else {
		patch, _ := anthropicUsagePatch(usageObject, true, true)
		patch.Diagnostics.Merge(diagnostics)
		if err := accumulator.MergePatch(patch); err != nil {
			return usage.Result{}, fmt.Errorf("normalize Anthropic usage response")
		}
	}
	result, _ := accumulator.Finalize(true)
	return result, nil
}

func (d *Anthropic) NewUsageStreamExtractor() UsageStreamExtractor {
	return &anthropicUsageStreamExtractor{}
}

type anthropicUsageStreamExtractor struct {
	accumulator usage.Accumulator
	validStart  bool
}

func (e *anthropicUsageStreamExtractor) Observe(payload []byte) error {
	root, err := decodeJSONObject(payload)
	if err != nil {
		if mergeErr := e.mergeDiagnostics(usageDiagnostic(usage.DiagnosticInvalidPayload)); mergeErr != nil {
			return mergeErr
		}
		return fmt.Errorf("decode Anthropic usage stream payload")
	}

	switch anthropicEventType(root) {
	case "message_start":
		return e.observeStart(root)
	case "message_delta":
		return e.observeDelta(root)
	default:
		return nil
	}
}

func (e *anthropicUsageStreamExtractor) Finalize() (usage.Result, bool) {
	return e.accumulator.Finalize(true)
}

func (e *anthropicUsageStreamExtractor) observeStart(root map[string]json.RawMessage) error {
	message, diagnostics := usageOptionalObject(root, "message")
	if message == nil {
		if !diagnostics.Has(usage.DiagnosticInvalidNumber) {
			diagnostics.Add(usage.DiagnosticMissingRequiredField)
		}
		return e.mergeDiagnostics(diagnostics)
	}

	usageObject, usageDiagnostics := usageOptionalObject(message, "usage")
	diagnostics.Merge(usageDiagnostics)
	if usageObject == nil {
		if !diagnostics.Has(usage.DiagnosticInvalidNumber) {
			diagnostics.Add(usage.DiagnosticMissingRequiredField)
		}
		return e.mergeDiagnostics(diagnostics)
	}

	patch, validInput := anthropicUsagePatch(usageObject, false, false)
	patch.Diagnostics.Merge(diagnostics)
	if err := e.accumulator.MergePatch(patch); err != nil {
		return err
	}
	if validInput {
		e.validStart = true
	}
	return nil
}

func (e *anthropicUsageStreamExtractor) observeDelta(root map[string]json.RawMessage) error {
	usageObject, diagnostics := usageOptionalObject(root, "usage")
	if usageObject == nil {
		if !diagnostics.Has(usage.DiagnosticInvalidNumber) {
			diagnostics.Add(usage.DiagnosticMissingRequiredField)
		}
		return e.mergeDiagnostics(diagnostics)
	}

	output, outputDiagnostics := usageInteger(usageObject, "output_tokens", true)
	diagnostics.Merge(outputDiagnostics)
	validOutput := output != nil && !outputDiagnostics.Has(usage.DiagnosticInvalidNumber) && !outputDiagnostics.Has(usage.DiagnosticNegativeValue)
	patch := usage.Patch{Output: output, Diagnostics: diagnostics}
	if validOutput {
		if e.validStart {
			patch.Final = true
		} else {
			patch.Diagnostics.Add(usage.DiagnosticInvalidEventSequence)
		}
	}
	return e.accumulator.MergePatch(patch)
}

func (e *anthropicUsageStreamExtractor) mergeDiagnostics(diagnostics usage.Diagnostics) error {
	return e.accumulator.MergePatch(usage.Patch{Diagnostics: diagnostics})
}

func anthropicUsagePatch(usageObject map[string]json.RawMessage, includeOutput, final bool) (usage.Patch, bool) {
	input, diagnostics := usageInteger(usageObject, "input_tokens", true)
	validInput := input != nil && !diagnostics.Has(usage.DiagnosticInvalidNumber) && !diagnostics.Has(usage.DiagnosticNegativeValue)

	patch := usage.Patch{Final: final, Diagnostics: diagnostics}
	if input != nil {
		patch.UncachedInput = input
	}

	cacheRead, cacheReadDiagnostics := usageInteger(usageObject, "cache_read_input_tokens", false)
	patch.Diagnostics.Merge(cacheReadDiagnostics)
	patch.CacheRead = usageValueOrZero(cacheRead)

	aggregate, aggregateDiagnostics := usageInteger(usageObject, "cache_creation_input_tokens", false)
	patch.Diagnostics.Merge(aggregateDiagnostics)
	aggregateValid := aggregate != nil && !aggregateDiagnostics.Has(usage.DiagnosticInvalidNumber) && !aggregateDiagnostics.Has(usage.DiagnosticNegativeValue)
	detail, detailDiagnostics := usageOptionalObject(usageObject, "cache_creation")
	patch.Diagnostics.Merge(detailDiagnostics)
	if detail == nil {
		patch.CacheWrite5M = usageValueOrZero(aggregate)
		patch.CacheWrite1H = usageValueOrZero(nil)
		if aggregateValid {
			patch.Diagnostics.Add(usage.DiagnosticCacheWriteDefaulted5M)
		}
	} else {
		write5M, write5MDiagnostics := usageInteger(detail, "ephemeral_5m_input_tokens", false)
		patch.Diagnostics.Merge(write5MDiagnostics)
		write1H, write1HDiagnostics := usageInteger(detail, "ephemeral_1h_input_tokens", false)
		patch.Diagnostics.Merge(write1HDiagnostics)
		patch.CacheWrite5M = usageValueOrZero(write5M)
		patch.CacheWrite1H = usageValueOrZero(write1H)
		if aggregateValid {
			detailTotal, ok := usage.CheckedAdd(*patch.CacheWrite5M, *patch.CacheWrite1H)
			if !ok {
				patch.Diagnostics.Add(usage.DiagnosticInvalidNumber)
			} else if *aggregate != detailTotal {
				patch.Diagnostics.SetTotalDelta(*aggregate - detailTotal)
			}
		}
	}

	if includeOutput {
		output, outputDiagnostics := usageInteger(usageObject, "output_tokens", true)
		patch.Diagnostics.Merge(outputDiagnostics)
		if output != nil {
			patch.Output = output
		}
	}
	return patch, validInput
}

func anthropicEventType(root map[string]json.RawMessage) string {
	raw, exists := root["type"]
	if !exists {
		return ""
	}
	var eventType string
	if err := json.Unmarshal(raw, &eventType); err != nil {
		return ""
	}
	return eventType
}

func usageValueOrZero(value *int64) *int64 {
	zero := int64(0)
	if value != nil {
		zero = *value
	}
	return &zero
}
