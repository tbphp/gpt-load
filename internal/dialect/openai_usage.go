package dialect

import (
	"encoding/json"
	"fmt"

	"gpt-load/internal/usage"
)

func (d *OpenAI) ExtractUsage(body []byte) (usage.Result, error) {
	object, err := decodeJSONObject(body)
	if err != nil {
		return usage.Result{}, fmt.Errorf("decode OpenAI usage response")
	}
	patch, found := openAIUsagePatch(object, true)
	var accumulator usage.Accumulator
	if found {
		if err := accumulator.ReplaceSnapshot(patch); err != nil {
			return usage.Result{}, fmt.Errorf("normalize OpenAI usage response")
		}
	} else if err := accumulator.MergePatch(patch); err != nil {
		return usage.Result{}, fmt.Errorf("normalize OpenAI usage response")
	}
	result, _ := accumulator.Finalize(true)
	return result, nil
}

func (d *OpenAI) NewUsageStreamExtractor() UsageStreamExtractor {
	return &openAIUsageStreamExtractor{}
}

type openAIUsageStreamExtractor struct {
	accumulator    usage.Accumulator
	invalidPayload bool
	invalidUsage   bool
}

func (e *openAIUsageStreamExtractor) Observe(payload []byte) error {
	object, err := decodeJSONObject(payload)
	if err != nil {
		e.invalidPayload = true
		if mergeErr := e.accumulator.MergePatch(usage.Patch{Diagnostics: usageDiagnostic(usage.DiagnosticInvalidPayload)}); mergeErr != nil {
			return mergeErr
		}
		return fmt.Errorf("decode OpenAI usage stream payload")
	}

	patch, found := openAIUsagePatch(object, openAIUsageFinal(object))
	if !found {
		if usageFieldPresent(object, "usage") && patch.Diagnostics.Has(usage.DiagnosticInvalidNumber) {
			e.invalidUsage = true
		}
		if err := e.accumulator.MergePatch(patch); err != nil {
			return err
		}
		return nil
	}
	if err := e.accumulator.ReplaceSnapshot(patch); err != nil {
		return err
	}
	return nil
}

func (e *openAIUsageStreamExtractor) Finalize() (usage.Result, bool) {
	result, finalized := e.accumulator.Finalize(true)
	if !finalized {
		return result, false
	}
	if e.invalidPayload {
		result.Diagnostics.Add(usage.DiagnosticInvalidPayload)
	}
	if e.invalidUsage {
		result.Diagnostics.Add(usage.DiagnosticInvalidNumber)
	}
	return result, true
}

func openAIUsagePatch(root map[string]json.RawMessage, final bool) (usage.Patch, bool) {
	usageObject, diagnostics := usageOptionalObject(root, "usage")
	if usageObject == nil {
		return usage.Patch{Diagnostics: diagnostics}, false
	}
	if openAIUnsupportedServiceTier(root) {
		diagnostics.Add(usage.DiagnosticUnsupportedBillableDetail)
	}

	prompt, promptDiagnostics := usageInteger(usageObject, "prompt_tokens", true)
	diagnostics.Merge(promptDiagnostics)
	completion, completionDiagnostics := usageInteger(usageObject, "completion_tokens", true)
	diagnostics.Merge(completionDiagnostics)
	total, totalDiagnostics := usageInteger(usageObject, "total_tokens", false)
	diagnostics.Merge(totalDiagnostics)

	var cached *int64
	var cacheWriteUnknown *int64
	if details, detailDiagnostics := usageOptionalObject(usageObject, "prompt_tokens_details"); details != nil {
		diagnostics.Merge(detailDiagnostics)
		cached, detailDiagnostics = usageInteger(details, "cached_tokens", false)
		diagnostics.Merge(detailDiagnostics)
		cacheWrite, cacheWriteDiagnostics := usageInteger(details, "cache_write_tokens", false)
		diagnostics.Merge(cacheWriteDiagnostics)
		if cacheWrite != nil && *cacheWrite > 0 {
			cacheWriteUnknown = cacheWrite
			diagnostics.Add(usage.DiagnosticUnsupportedBillableDetail)
		}
		diagnostics.Merge(usageUnsupportedPositiveIntegerDetails(details, "audio_tokens"))
	} else {
		diagnostics.Merge(detailDiagnostics)
	}
	if details, detailDiagnostics := usageOptionalObject(usageObject, "completion_tokens_details"); details != nil {
		diagnostics.Merge(detailDiagnostics)
		diagnostics.Merge(usageUnsupportedPositiveIntegerDetails(
			details,
			"reasoning_tokens",
			"audio_tokens",
		))
	} else {
		diagnostics.Merge(detailDiagnostics)
	}

	patch := usage.Patch{Final: final, Diagnostics: diagnostics}
	cacheValue := int64(0)
	if cached != nil {
		cacheValue = *cached
		patch.CacheRead = &cacheValue
	}
	cacheWriteValue := int64(0)
	if cacheWriteUnknown != nil {
		cacheWriteValue = *cacheWriteUnknown
		patch.CacheWriteUnknown = &cacheWriteValue
	}
	if prompt != nil {
		if cached == nil {
			patch.CacheRead = &cacheValue
		}
		uncached := int64(0)
		classifiedInput, classified := usage.CheckedAdd(cacheValue, cacheWriteValue)
		if !classified {
			patch.Diagnostics.Add(usage.DiagnosticInvalidNumber)
			patch.Diagnostics.Add(usage.DiagnosticNegativeValue)
		} else if derived, ok := usage.SubtractCached(*prompt, classifiedInput); ok {
			uncached = derived
		} else {
			patch.Diagnostics.Add(usage.DiagnosticNegativeValue)
		}
		patch.UncachedInput = &uncached
	}
	if completion != nil {
		patch.Output = completion
	}

	tokens := usage.Tokens{}
	if patch.UncachedInput != nil {
		tokens.UncachedInput = *patch.UncachedInput
	}
	if patch.CacheRead != nil {
		tokens.CacheRead = *patch.CacheRead
	}
	if patch.CacheWriteUnknown != nil {
		tokens.CacheWriteUnknown = *patch.CacheWriteUnknown
	}
	if patch.Output != nil {
		tokens.Output = *patch.Output
	}
	usageNormalizedTotal(total, tokens, &patch.Diagnostics)
	return patch, true
}

func openAIUsageFinal(root map[string]json.RawMessage) bool {
	rawChoices, exists := root["choices"]
	if !exists {
		return false
	}
	var choices []map[string]json.RawMessage
	if json.Unmarshal(rawChoices, &choices) != nil || choices == nil {
		return false
	}
	if len(choices) == 0 {
		return true
	}
	for _, choice := range choices {
		rawReason, exists := choice["finish_reason"]
		if !exists {
			continue
		}
		var reason string
		if json.Unmarshal(rawReason, &reason) == nil && reason != "" {
			return true
		}
	}
	return false
}
