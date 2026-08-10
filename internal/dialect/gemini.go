package dialect

import (
	"fmt"
	"strings"

	"gpt-load/internal/protocol"
)

const (
	geminiGenerationPrefix = "/v1beta/models/"
	geminiGenerateSuffix   = ":generateContent"
	geminiStreamSuffix     = ":streamGenerateContent"
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
	model, stream, err := parseGeminiGenerationPath(req.Path)
	if err != nil {
		return RequestMetadata{}, err
	}
	metadata := RequestMetadata{
		Model:        &model,
		Stream:       stream,
		ObserveUsage: true,
		Reasoning:    inspectGeminiReasoning(req.Body),
	}
	metadata.Operation, metadata.RequiredFeatures = chatExecutionMetadata(
		req.Body,
		metadata.Stream,
		metadata.Reasoning,
	)
	return metadata, nil
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
