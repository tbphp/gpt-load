package dialect

import (
	"bytes"
	"encoding/json"
	"fmt"

	"gpt-load/internal/usage"
)

func (d *OpenAIImages) ExtractUsage(body []byte) (usage.Result, error) {
	envelope, err := decodeOpenAIImagesUsageEnvelope(body)
	if err != nil {
		return usage.Result{}, fmt.Errorf("decode OpenAI Images usage response")
	}
	patch, found := openAIImagesUsagePatch(
		openAIImagesUsageRoot(envelope.Usage),
		true,
	)
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

type openAIImagesUsageEnvelope struct {
	Type  json.RawMessage `json:"type"`
	Usage json.RawMessage `json:"usage"`
}

func (e *openAIImagesUsageStreamExtractor) Observe(payload []byte) error {
	return e.ObserveStreamEvent(StreamEvent{Payload: payload})
}

func (e *openAIImagesUsageStreamExtractor) ObserveStreamEvent(
	event StreamEvent,
) error {
	envelope, err := decodeOpenAIImagesUsageEnvelope(event.Payload)
	if err != nil {
		e.invalidPayload = true
		return e.accumulator.MergePatch(usage.Patch{
			Diagnostics: usageDiagnostic(usage.DiagnosticInvalidPayload),
		})
	}
	eventType, typeValid := openAIImagesUsageStreamEventType(envelope.Type)
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
	patch, found := openAIImagesUsagePatch(
		openAIImagesUsageRoot(envelope.Usage),
		terminal,
	)

	if terminal || found {
		if err := e.accumulator.ReplaceSnapshot(patch); err != nil {
			return err
		}
		e.terminal = terminal
		return nil
	}
	return e.accumulator.MergePatch(patch)
}

func decodeOpenAIImagesUsageEnvelope(
	payload []byte,
) (openAIImagesUsageEnvelope, error) {
	if trimmed := bytes.TrimSpace(payload); len(trimmed) == 0 || trimmed[0] != '{' {
		return openAIImagesUsageEnvelope{}, fmt.Errorf("decode JSON object")
	}
	var envelope openAIImagesUsageEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return openAIImagesUsageEnvelope{}, fmt.Errorf("decode JSON object")
	}
	return envelope, nil
}

func openAIImagesUsageRoot(raw json.RawMessage) map[string]json.RawMessage {
	root := make(map[string]json.RawMessage, 1)
	if len(raw) > 0 {
		root["usage"] = raw
	}
	return root
}

func openAIImagesUsageStreamEventType(raw json.RawMessage) (string, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", true
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || value == "" {
		return "", false
	}
	return value, true
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
