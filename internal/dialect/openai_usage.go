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
	}
	result, _ := accumulator.Finalize(true)
	return result, nil
}

func (d *OpenAI) NewUsageStreamExtractor() UsageStreamExtractor {
	return &openAIUsageStreamExtractor{}
}

type openAIUsageStreamExtractor struct {
	accumulator usage.Accumulator
	diagnostics usage.Diagnostics
}

func (e *openAIUsageStreamExtractor) Observe(payload []byte) error {
	object, err := decodeJSONObject(payload)
	if err != nil {
		e.diagnostics.Add(usage.DiagnosticInvalidPayload)
		if mergeErr := e.accumulator.MergePatch(usage.Patch{Diagnostics: e.diagnostics}); mergeErr != nil {
			return mergeErr
		}
		return fmt.Errorf("decode OpenAI usage stream payload")
	}

	patch, found := openAIUsagePatch(object, openAIUsageFinal(object))
	if !found {
		return nil
	}
	patch.Diagnostics.Merge(e.diagnostics)
	if err := e.accumulator.ReplaceSnapshot(patch); err != nil {
		return err
	}
	return nil
}

func (e *openAIUsageStreamExtractor) Finalize() (usage.Result, bool) {
	return e.accumulator.Finalize(true)
}

func openAIUsagePatch(root map[string]json.RawMessage, final bool) (usage.Patch, bool) {
	usageObject, diagnostics := usageOptionalObject(root, "usage")
	if usageObject == nil {
		return usage.Patch{}, false
	}

	prompt, promptDiagnostics := usageInteger(usageObject, "prompt_tokens", true)
	diagnostics.Merge(promptDiagnostics)
	completion, completionDiagnostics := usageInteger(usageObject, "completion_tokens", true)
	diagnostics.Merge(completionDiagnostics)
	total, totalDiagnostics := usageInteger(usageObject, "total_tokens", false)
	diagnostics.Merge(totalDiagnostics)

	var cached *int64
	if details, detailDiagnostics := usageOptionalObject(usageObject, "prompt_tokens_details"); details != nil {
		diagnostics.Merge(detailDiagnostics)
		cached, detailDiagnostics = usageInteger(details, "cached_tokens", false)
		diagnostics.Merge(detailDiagnostics)
		cacheWrite, cacheWriteDiagnostics := usageInteger(details, "cache_write_tokens", false)
		diagnostics.Merge(cacheWriteDiagnostics)
		if cacheWrite != nil && *cacheWrite > 0 {
			diagnostics.Add(usage.DiagnosticUnsupportedBillableDetail)
		}
	} else {
		diagnostics.Merge(detailDiagnostics)
	}

	patch := usage.Patch{Final: final, Diagnostics: diagnostics}
	if prompt != nil {
		cacheValue := int64(0)
		if cached != nil {
			cacheValue = *cached
		}
		uncached, ok := usage.SubtractCached(*prompt, cacheValue)
		if !ok {
			uncached = 0
			patch.Diagnostics.Add(usage.DiagnosticNegativeValue)
		}
		patch.UncachedInput = &uncached
		patch.CacheRead = &cacheValue
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
	var choices []json.RawMessage
	return json.Unmarshal(rawChoices, &choices) == nil && choices != nil && len(choices) == 0
}
