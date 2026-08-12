package dialect

import (
	"fmt"

	"gpt-load/internal/protocol"
)

type Anthropic struct{}

var _ Dialect = (*Anthropic)(nil)

func NewAnthropic() *Anthropic {
	return &Anthropic{}
}

func (*Anthropic) Protocol() protocol.Protocol {
	return protocol.Anthropic
}

func (d *Anthropic) InspectRequest(req *ParsedRequest) (RequestMetadata, error) {
	if req == nil {
		return RequestMetadata{}, fmt.Errorf("parsed request is required")
	}

	metadata, err := inspectJSONRequestFields(req.Body, true)
	if err != nil {
		return RequestMetadata{}, fmt.Errorf("decode %s request: %w", d.Protocol(), err)
	}
	metadata.ObserveUsage = true
	metadata.AffinityPrefix = inspectPromptAffinityPrefix(d.Protocol(), req.Body)
	metadata.Reasoning = inspectAnthropicReasoning(req.Body)
	metadata.Operation, metadata.RouteRequirement = chatExecutionMetadata(
		d.Protocol(),
		req.Body,
	)
	return metadata, nil
}
