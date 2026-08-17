package dialect

import (
	"fmt"
	"strings"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const (
	geminiGenerationPrefix = "/v1beta/models/"
	geminiGenerateSuffix   = ":generateContent"
	geminiStreamSuffix     = ":streamGenerateContent"
	geminiCountSuffix      = ":countTokens"
)

type Gemini struct{}

var _ Dialect = (*Gemini)(nil)

func NewGemini() *Gemini {
	return &Gemini{}
}

func (*Gemini) Protocol() protocol.Protocol {
	return protocol.Gemini
}

func (d *Gemini) InspectRequest(req *ParsedRequest) (RequestMetadata, error) {
	if req == nil {
		return RequestMetadata{}, fmt.Errorf("parsed request is required")
	}
	model, operation, stream, err := parseGeminiRequestPath(req.Path)
	if err != nil {
		return RequestMetadata{}, err
	}
	metadata := RequestMetadata{
		Model:     &model,
		Stream:    stream,
		Operation: operation,
	}
	if operation == execution.OperationCountTokens {
		metadata.RouteRequirement = execution.RouteRequirementAny
		return metadata, nil
	}
	metadata.AffinityPrefix = inspectPromptAffinityPrefix(d.Protocol(), req.Body)
	metadata.ObserveUsage = true
	metadata.Reasoning = inspectGeminiReasoning(req.Body)
	metadata.Operation, metadata.RouteRequirement = chatExecutionMetadata(
		d.Protocol(),
		req.Body,
	)
	return metadata, nil
}

func parseGeminiRequestPath(path string) (
	model string,
	operation execution.Operation,
	stream bool,
	err error,
) {
	if strings.HasSuffix(path, geminiCountSuffix) {
		model = strings.TrimSuffix(strings.TrimPrefix(path, geminiGenerationPrefix), geminiCountSuffix)
		if !strings.HasPrefix(path, geminiGenerationPrefix) ||
			model == "" || strings.Contains(model, "/") || strings.TrimSpace(model) != model {
			return "", "", false, fmt.Errorf("invalid Gemini model")
		}
		return model, execution.OperationCountTokens, false, nil
	}
	model, stream, err = parseGeminiGenerationPath(path)
	return model, execution.OperationChatCompletion, stream, err
}

func parseGeminiGenerationPath(path string) (model string, stream bool, err error) {
	if !strings.HasPrefix(path, geminiGenerationPrefix) {
		return "", false, fmt.Errorf("invalid Gemini generation path")
	}
	modelAndMethod := strings.TrimPrefix(path, geminiGenerationPrefix)
	switch {
	case strings.HasSuffix(modelAndMethod, geminiStreamSuffix):
		model = strings.TrimSuffix(modelAndMethod, geminiStreamSuffix)
		stream = true
	case strings.HasSuffix(modelAndMethod, geminiGenerateSuffix):
		model = strings.TrimSuffix(modelAndMethod, geminiGenerateSuffix)
	default:
		return "", false, fmt.Errorf("invalid Gemini generation method")
	}
	if model == "" || strings.Contains(model, "/") || strings.TrimSpace(model) != model {
		return "", false, fmt.Errorf("invalid Gemini model")
	}
	return model, stream, nil
}
