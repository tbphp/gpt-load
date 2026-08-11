package dialect

import (
	"fmt"

	"gpt-load/internal/protocol"
)

type OpenAI struct{}

var _ Dialect = (*OpenAI)(nil)

func NewOpenAI() *OpenAI {
	return &OpenAI{}
}

func (d *OpenAI) Protocol() protocol.Protocol {
	return protocol.OpenAICompletions
}

func (d *OpenAI) InspectRequest(req *ParsedRequest) (RequestMetadata, error) {
	if req == nil {
		return RequestMetadata{}, fmt.Errorf("parsed request is required")
	}

	metadata, err := inspectJSONRequestFields(req.Body, true)
	if err != nil {
		return RequestMetadata{}, fmt.Errorf("decode %s request: %w", d.Protocol(), err)
	}
	metadata.ObserveUsage = true
	metadata.UsageDiagnostics = openAIRequestPricingDiagnostics(req.Body)
	metadata.Reasoning = inspectOpenAICompletionsReasoning(req.Body)
	metadata.Operation, metadata.RequiredFeatures = chatExecutionMetadata(
		d.Protocol(),
		req.Body,
		metadata.Stream,
		metadata.Reasoning,
	)
	return metadata, nil
}
