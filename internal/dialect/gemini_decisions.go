package dialect

import (
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

const geminiDecisionsPath = "/v1beta/decisions"

// GeminiDecisions 实现 Google Gemini 原生 Decisions 协议（POST /v1beta/decisions）。
// 网关只读取并改写 model，input、questions 等字段原样透传，由上游校验。
type GeminiDecisions struct{}

var (
	_ Dialect        = (*GeminiDecisions)(nil)
	_ ModelRewriter  = (*GeminiDecisions)(nil)
	_ UsageExtractor = (*GeminiDecisions)(nil)
)

func NewGeminiDecisions() *GeminiDecisions { return &GeminiDecisions{} }

func (*GeminiDecisions) Protocol() protocol.Protocol { return protocol.GeminiDecisions }

func (d *GeminiDecisions) InspectRequest(request *ParsedRequest) (RequestMetadata, error) {
	return inspectDecisionsRequest(request, d.Protocol(), geminiDecisionsPath)
}

func (d *GeminiDecisions) RewriteRequestModel(request *ParsedRequest, model string) (*ParsedRequest, error) {
	return rewriteDecisionsRequestModel(d, request, model)
}

func (*GeminiDecisions) RewriteResponseModel(body []byte, model string) ([]byte, error) {
	return rewriteDecisionsResponseModel(body, model)
}

func (*GeminiDecisions) ExtractUsage(body []byte) (usage.Result, error) {
	return extractDecisionsUsage(body, []string{"total_input_tokens"}, []string{"total_output_tokens"})
}

func (*GeminiDecisions) NewUsageStreamExtractor() UsageStreamExtractor {
	return &decisionsUnsupportedStreamUsage{}
}
