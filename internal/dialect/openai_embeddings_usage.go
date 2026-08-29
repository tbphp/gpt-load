package dialect

import (
	"bytes"
	"encoding/json"
	"fmt"

	"gpt-load/internal/usage"
)

var _ UsageExtractor = (*OpenAIEmbeddings)(nil)

func (*OpenAIEmbeddings) ExtractUsage(body []byte) (usage.Result, error) {
	envelope, err := decodeOpenAIEmbeddingsUsageEnvelope(body)
	if err != nil {
		return usage.Result{}, fmt.Errorf("decode OpenAI Embeddings usage response")
	}
	patch, found := openAIEmbeddingsUsagePatch(openAIEmbeddingsUsageRoot(envelope.Usage))
	var accumulator usage.Accumulator
	if found {
		if err := accumulator.ReplaceSnapshot(patch); err != nil {
			return usage.Result{}, fmt.Errorf("normalize OpenAI Embeddings usage response")
		}
	} else if err := accumulator.MergePatch(patch); err != nil {
		return usage.Result{}, fmt.Errorf("normalize OpenAI Embeddings usage response")
	}
	result, _ := accumulator.Finalize(true)
	return result, nil
}

type openAIEmbeddingsUsageEnvelope struct {
	Usage json.RawMessage `json:"usage"`
}

func decodeOpenAIEmbeddingsUsageEnvelope(body []byte) (openAIEmbeddingsUsageEnvelope, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return openAIEmbeddingsUsageEnvelope{}, fmt.Errorf("decode JSON object")
	}
	var envelope openAIEmbeddingsUsageEnvelope
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return openAIEmbeddingsUsageEnvelope{}, fmt.Errorf("decode JSON object")
	}
	return envelope, nil
}

func openAIEmbeddingsUsageRoot(raw json.RawMessage) map[string]json.RawMessage {
	root := make(map[string]json.RawMessage, 1)
	if len(raw) > 0 {
		root["usage"] = raw
	}
	return root
}

func (*OpenAIEmbeddings) NewUsageStreamExtractor() UsageStreamExtractor {
	return &openAIEmbeddingsUnsupportedStreamUsage{}
}

type openAIEmbeddingsUnsupportedStreamUsage struct {
	finalized bool
}

func (*openAIEmbeddingsUnsupportedStreamUsage) Observe([]byte) error {
	return fmt.Errorf("OpenAI Embeddings does not support streaming usage")
}

func (e *openAIEmbeddingsUnsupportedStreamUsage) Finalize() (usage.Result, bool) {
	if e.finalized {
		return usage.Result{}, false
	}
	e.finalized = true
	return usage.Result{State: usage.StateNotApplicable}, true
}

func openAIEmbeddingsUsagePatch(root map[string]json.RawMessage) (usage.Patch, bool) {
	usageObject, diagnostics := usageOptionalObject(root, "usage")
	if usageObject == nil {
		return usage.Patch{Diagnostics: diagnostics}, false
	}

	prompt, promptDiagnostics := usageInteger(usageObject, "prompt_tokens", true)
	diagnostics.Merge(promptDiagnostics)
	if !usageIntegerUsable(promptDiagnostics) {
		prompt = nil
	}
	total, totalDiagnostics := usageInteger(usageObject, "total_tokens", true)
	diagnostics.Merge(totalDiagnostics)
	if !usageIntegerUsable(totalDiagnostics) {
		total = nil
	}

	patch := usage.Patch{
		UncachedInput: prompt,
		Final:         true,
		Diagnostics:   diagnostics,
	}
	tokens := usage.Tokens{}
	if prompt != nil {
		tokens.UncachedInput = *prompt
	}
	usageNormalizedTotal(total, tokens, &patch.Diagnostics)
	return patch, true
}
