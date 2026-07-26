package dialect

import (
	"encoding/json"
	"fmt"

	"gpt-load/internal/usage"
)

func (d *Gemini) ExtractUsage(body []byte) (usage.Result, error) {
	root, err := decodeJSONObject(body)
	if err != nil {
		return usage.Result{}, fmt.Errorf("decode Gemini usage response")
	}

	patch, found := geminiUsagePatch(root, true)
	var accumulator usage.Accumulator
	if found {
		if err := accumulator.ReplaceSnapshot(patch); err != nil {
			return usage.Result{}, fmt.Errorf("normalize Gemini usage response")
		}
	} else if err := accumulator.MergePatch(patch); err != nil {
		return usage.Result{}, fmt.Errorf("normalize Gemini usage response")
	}
	result, _ := accumulator.Finalize(true)
	return result, nil
}

func (d *Gemini) NewUsageStreamExtractor() UsageStreamExtractor {
	return &geminiUsageStreamExtractor{}
}

type geminiUsageStreamExtractor struct {
	accumulator     usage.Accumulator
	invalidPayload  bool
	invalidMetadata bool
}

func (e *geminiUsageStreamExtractor) Observe(payload []byte) error {
	root, err := decodeJSONObject(payload)
	if err != nil {
		e.invalidPayload = true
		if mergeErr := e.accumulator.MergePatch(usage.Patch{Diagnostics: usageDiagnostic(usage.DiagnosticInvalidPayload)}); mergeErr != nil {
			return mergeErr
		}
		return fmt.Errorf("decode Gemini usage stream payload")
	}

	final := geminiUsageFinal(root)
	patch, found := geminiUsagePatch(root, final)
	if !found && usageFieldPresent(root, "usageMetadata") && patch.Diagnostics.Has(usage.DiagnosticInvalidNumber) {
		e.invalidMetadata = true
	}
	if found {
		return e.accumulator.ReplaceSnapshot(patch)
	}
	if final {
		patch.Final = true
	}
	return e.accumulator.MergePatch(patch)
}

func (e *geminiUsageStreamExtractor) Finalize() (usage.Result, bool) {
	result, finalized := e.accumulator.Finalize(true)
	if !finalized {
		return result, false
	}
	if e.invalidPayload {
		result.Diagnostics.Add(usage.DiagnosticInvalidPayload)
	}
	if e.invalidMetadata {
		result.Diagnostics.Add(usage.DiagnosticInvalidNumber)
	}
	return result, true
}

func geminiUsagePatch(root map[string]json.RawMessage, final bool) (usage.Patch, bool) {
	metadata, diagnostics := usageOptionalObject(root, "usageMetadata")
	if metadata == nil {
		return usage.Patch{Diagnostics: diagnostics}, false
	}

	prompt, promptDiagnostics := usageInteger(metadata, "promptTokenCount", true)
	diagnostics.Merge(promptDiagnostics)
	cached, cachedDiagnostics := usageInteger(metadata, "cachedContentTokenCount", false)
	diagnostics.Merge(cachedDiagnostics)
	candidates, candidatesDiagnostics := usageInteger(metadata, "candidatesTokenCount", false)
	diagnostics.Merge(candidatesDiagnostics)
	thoughts, thoughtsDiagnostics := usageInteger(metadata, "thoughtsTokenCount", false)
	diagnostics.Merge(thoughtsDiagnostics)
	total, totalDiagnostics := usageInteger(metadata, "totalTokenCount", false)
	diagnostics.Merge(totalDiagnostics)

	patch := usage.Patch{Final: final, Diagnostics: diagnostics}
	cachedValue := usageIntegerValue(cached)
	if cached != nil {
		patch.CacheRead = &cachedValue
	}
	if prompt != nil {
		uncached, ok := usage.SubtractCached(*prompt, cachedValue)
		if !ok {
			uncached = 0
			patch.Diagnostics.Add(usage.DiagnosticNegativeValue)
		}
		patch.UncachedInput = &uncached
	}
	if candidates != nil || thoughts != nil {
		output, ok := usage.CheckedAdd(usageIntegerValue(candidates), usageIntegerValue(thoughts))
		if !ok {
			output = 0
			patch.Diagnostics.Add(usage.DiagnosticInvalidNumber)
		}
		patch.Output = &output
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

func geminiUsageFinal(root map[string]json.RawMessage) bool {
	if geminiCandidateFinished(root) {
		return true
	}
	promptFeedback, diagnostics := usageOptionalObject(root, "promptFeedback")
	if promptFeedback == nil || diagnostics.Has(usage.DiagnosticInvalidNumber) {
		return false
	}
	return geminiNonEmptyString(promptFeedback, "blockReason")
}

func geminiCandidateFinished(root map[string]json.RawMessage) bool {
	rawCandidates, exists := root["candidates"]
	if !exists {
		return false
	}
	var candidates []map[string]json.RawMessage
	if err := json.Unmarshal(rawCandidates, &candidates); err != nil {
		return false
	}
	for _, candidate := range candidates {
		if geminiNonEmptyString(candidate, "finishReason") {
			return true
		}
	}
	return false
}

func geminiNonEmptyString(object map[string]json.RawMessage, field string) bool {
	raw, exists := object[field]
	if !exists {
		return false
	}
	var value string
	return json.Unmarshal(raw, &value) == nil && value != ""
}
