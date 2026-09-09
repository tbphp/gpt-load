package dialect

import (
	"bytes"
	"encoding/json"
	"fmt"

	"gpt-load/internal/execution"
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

// AnthropicRequestsZeroOutput 判断请求是否显式要求零输出，不把缺失、null 或字符串视为数值零。
func AnthropicRequestsZeroOutput(body []byte) bool {
	var request struct {
		MaxTokens json.RawMessage `json:"max_tokens"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return false
	}
	mantissa := request.MaxTokens
	if index := bytes.IndexAny(mantissa, "eE"); index >= 0 {
		mantissa = mantissa[:index]
	}
	// 按有效数字判断，避免极小的非零数值经浮点下溢被误判为零。
	return bytes.IndexByte(mantissa, '0') >= 0 && len(bytes.Trim(mantissa, "-0.")) == 0
}

func (d *Anthropic) InspectRequest(req *ParsedRequest) (RequestMetadata, error) {
	if req == nil {
		return RequestMetadata{}, fmt.Errorf("parsed request is required")
	}

	metadata, err := inspectJSONRequestFields(req.Body, true, false)
	if err != nil {
		return RequestMetadata{}, fmt.Errorf("decode %s request: %w", d.Protocol(), err)
	}
	if req.Path == "/v1/messages/count_tokens" {
		metadata.Stream = false
		metadata.ObserveUsage = false
		metadata.Operation = execution.OperationCountTokens
		metadata.RouteRequirement = execution.RouteRequirementAny
		return metadata, nil
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
