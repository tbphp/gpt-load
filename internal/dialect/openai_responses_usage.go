package dialect

import (
	"encoding/json"
	"fmt"

	"gpt-load/internal/usage"
)

func (d *OpenAIResponses) ExtractUsage(body []byte) (usage.Result, error) {
	object, err := decodeJSONObject(body)
	if err != nil {
		return usage.Result{}, fmt.Errorf("decode OpenAI Responses usage response")
	}
	patch, found := openAIResponsesUsagePatch(object, true)
	var accumulator usage.Accumulator
	if found {
		if err := accumulator.ReplaceSnapshot(patch); err != nil {
			return usage.Result{}, fmt.Errorf("normalize OpenAI Responses usage response")
		}
	} else if err := accumulator.MergePatch(patch); err != nil {
		return usage.Result{}, fmt.Errorf("normalize OpenAI Responses usage response")
	}
	result, _ := accumulator.Finalize(true)
	return result, nil
}

func (d *OpenAIResponses) NewUsageStreamExtractor() UsageStreamExtractor {
	return &openAIResponsesUsageStreamExtractor{}
}

type openAIResponsesUsageStreamExtractor struct {
	accumulator    usage.Accumulator
	invalidPayload bool
}

func (e *openAIResponsesUsageStreamExtractor) Observe(payload []byte) error {
	return e.ObserveStreamEvent(StreamEvent{Payload: payload})
}

func (e *openAIResponsesUsageStreamExtractor) ObserveStreamEvent(
	event StreamEvent,
) error {
	object, err := decodeJSONObject(event.Payload)
	if err != nil {
		e.invalidPayload = true
		return e.accumulator.MergePatch(usage.Patch{
			Diagnostics: usageDiagnostic(usage.DiagnosticInvalidPayload),
		})
	}
	eventType, typeValid := responsesUsageEventType(object)
	if !typeValid {
		e.invalidPayload = true
		if err := e.accumulator.MergePatch(usage.Patch{
			Diagnostics: usageDiagnostic(usage.DiagnosticInvalidPayload),
		}); err != nil {
			return err
		}
	}
	if event.Name != "" && eventType != "" && event.Name != eventType {
		e.invalidPayload = true
		return e.accumulator.MergePatch(usage.Patch{
			Diagnostics: usageDiagnostic(usage.DiagnosticInvalidPayload),
		})
	}
	if event.Name != "" {
		eventType = event.Name
	}
	terminal := responsesUsageTerminal(eventType)
	responseObject, diagnostics := responsesUsageObject(object)
	patch := usage.Patch{Final: terminal, Diagnostics: diagnostics}
	found := false
	if responseObject != nil {
		patch, found = openAIResponsesUsagePatch(responseObject, terminal)
		patch.Diagnostics.Merge(diagnostics)
	}

	if terminal || found {
		if err := e.accumulator.ReplaceSnapshot(patch); err != nil {
			return err
		}
		return nil
	}
	return e.accumulator.MergePatch(patch)
}

func (e *openAIResponsesUsageStreamExtractor) Finalize() (usage.Result, bool) {
	result, finalized := e.accumulator.Finalize(true)
	if !finalized {
		return result, false
	}
	if e.invalidPayload {
		result.Diagnostics.Add(usage.DiagnosticInvalidPayload)
	}
	return result, true
}

func responsesUsageEventType(
	object map[string]json.RawMessage,
) (string, bool) {
	raw, exists := object["type"]
	if !exists {
		return "", true
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || value == "" {
		return "", false
	}
	return value, true
}

func responsesUsageTerminal(eventType string) bool {
	switch eventType {
	case "response.completed", "response.incomplete", "response.failed":
		return true
	default:
		return false
	}
}

func responsesUsageObject(
	root map[string]json.RawMessage,
) (map[string]json.RawMessage, usage.Diagnostics) {
	if _, exists := root["response"]; !exists {
		return root, usage.Diagnostics{}
	}
	return usageOptionalObject(root, "response")
}

func openAIResponsesUsagePatch(
	root map[string]json.RawMessage,
	final bool,
) (usage.Patch, bool) {
	usageObject, diagnostics := usageOptionalObject(root, "usage")
	if usageObject == nil {
		return usage.Patch{Final: final, Diagnostics: diagnostics}, false
	}

	input, inputDiagnostics := usageInteger(
		usageObject,
		"input_tokens",
		true,
	)
	diagnostics.Merge(inputDiagnostics)
	output, outputDiagnostics := usageInteger(
		usageObject,
		"output_tokens",
		true,
	)
	diagnostics.Merge(outputDiagnostics)
	total, totalDiagnostics := usageInteger(
		usageObject,
		"total_tokens",
		false,
	)
	diagnostics.Merge(totalDiagnostics)

	var cached *int64
	if details, detailDiagnostics := usageOptionalObject(
		usageObject,
		"input_tokens_details",
	); details != nil {
		diagnostics.Merge(detailDiagnostics)
		cached, detailDiagnostics = usageInteger(
			details,
			"cached_tokens",
			false,
		)
		diagnostics.Merge(detailDiagnostics)
		diagnostics.Merge(usageUnsupportedPositiveIntegerDetails(details, "audio_tokens"))
	} else {
		diagnostics.Merge(detailDiagnostics)
	}
	if details, detailDiagnostics := usageOptionalObject(
		usageObject,
		"output_tokens_details",
	); details != nil {
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
	if input != nil {
		if cached == nil {
			patch.CacheRead = &cacheValue
		}
		uncached, ok := usage.SubtractCached(*input, cacheValue)
		if !ok {
			uncached = 0
			patch.Diagnostics.Add(usage.DiagnosticNegativeValue)
		}
		patch.UncachedInput = &uncached
	}
	if output != nil {
		patch.Output = output
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
