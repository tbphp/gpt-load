package dialect

import (
	"encoding/json"
	"fmt"

	"gpt-load/internal/usage"
)

func (d *OpenAIImages) ExtractUsage(body []byte) (usage.Result, error) {
	object, err := decodeJSONObject(body)
	if err != nil {
		return usage.Result{}, fmt.Errorf("decode OpenAI Images usage response")
	}
	patch, found := openAIImagesUsagePatch(object, true)
	var accumulator usage.Accumulator
	if found {
		if err := accumulator.ReplaceSnapshot(patch); err != nil {
			return usage.Result{}, fmt.Errorf("normalize OpenAI Images usage response")
		}
	} else if err := accumulator.MergePatch(patch); err != nil {
		return usage.Result{}, fmt.Errorf("normalize OpenAI Images usage response")
	}
	result, _ := accumulator.Finalize(true)
	return result, nil
}

func (d *OpenAIImages) NewUsageStreamExtractor() UsageStreamExtractor {
	return &openAIImagesUsageStreamExtractor{}
}

type openAIImagesUsageStreamExtractor struct {
	accumulator    usage.Accumulator
	invalidPayload bool
	terminal       bool
}

func (e *openAIImagesUsageStreamExtractor) Observe(payload []byte) error {
	return e.ObserveStreamEvent(StreamEvent{Payload: payload})
}

func (e *openAIImagesUsageStreamExtractor) ObserveStreamEvent(
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
	if e.terminal {
		return e.accumulator.MergePatch(usage.Patch{
			Diagnostics: usageDiagnostic(usage.DiagnosticInvalidEventSequence),
		})
	}
	if event.Name != "" {
		eventType = event.Name
	}

	terminal := openAIImagesUsageTerminal(eventType)
	patch, found := openAIImagesUsagePatch(object, terminal)

	if terminal || found {
		if err := e.accumulator.ReplaceSnapshot(patch); err != nil {
			return err
		}
		e.terminal = terminal
		return nil
	}
	return e.accumulator.MergePatch(patch)
}

func (e *openAIImagesUsageStreamExtractor) Finalize() (usage.Result, bool) {
	result, finalized := e.accumulator.Finalize(true)
	if !finalized {
		return result, false
	}
	if e.invalidPayload {
		result.Diagnostics.Add(usage.DiagnosticInvalidPayload)
	}
	return result, true
}

func openAIImagesUsageTerminal(eventType string) bool {
	switch eventType {
	case "image_generation.completed", "image_edit.completed",
		"image_generation.failed", "image_edit.failed", "error":
		return true
	default:
		return false
	}
}

func openAIImagesUsagePatch(
	root map[string]json.RawMessage,
	final bool,
) (usage.Patch, bool) {
	usageObject, diagnostics := usageOptionalObject(root, "usage")
	if usageObject == nil {
		return usage.Patch{Final: final, Diagnostics: diagnostics}, false
	}

	input, inputDiagnostics := usageInteger(usageObject, "input_tokens", true)
	diagnostics.Merge(inputDiagnostics)
	output, outputDiagnostics := usageInteger(usageObject, "output_tokens", true)
	diagnostics.Merge(outputDiagnostics)
	total, totalDiagnostics := usageInteger(usageObject, "total_tokens", false)
	diagnostics.Merge(totalDiagnostics)

	_, detailDiagnostics := usageOptionalObject(usageObject, "input_tokens_details")
	diagnostics.Merge(detailDiagnostics)

	patch := usage.Patch{Final: final, Diagnostics: diagnostics}
	if input != nil {
		patch.UncachedInput = input
	}
	if output != nil {
		patch.Output = output
	}

	tokens := usage.Tokens{}
	if patch.UncachedInput != nil {
		tokens.UncachedInput = *patch.UncachedInput
	}
	if patch.Output != nil {
		tokens.Output = *patch.Output
	}
	usageNormalizedTotal(total, tokens, &patch.Diagnostics)
	return patch, true
}
