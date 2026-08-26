package dialect

import (
	"encoding/json"
	"fmt"
	"net/http"

	"gpt-load/internal/protocol"
)

// InspectStandardRequest 使用路由检查简化表单约定的无状态请求结构推导路由元数据。
func InspectStandardRequest(
	clientProtocol protocol.Protocol,
	model string,
) (RequestMetadata, error) {
	selected, request, err := standardRequest(clientProtocol, model)
	if err != nil {
		return RequestMetadata{}, err
	}
	return selected.InspectRequest(request)
}

func standardRequest(
	clientProtocol protocol.Protocol,
	model string,
) (Dialect, *ParsedRequest, error) {
	request := &ParsedRequest{Method: http.MethodPost}
	var selected Dialect
	var body any
	switch clientProtocol {
	case protocol.OpenAICompletions:
		selected = NewOpenAI()
		request.Path = "/v1/chat/completions"
		body = map[string]any{"model": model, "messages": []any{}}
	case protocol.OpenAIResponses:
		selected = NewOpenAIResponses()
		request.Path = "/v1/responses"
		body = map[string]any{"model": model, "input": []any{}, "store": false}
	case protocol.OpenAIImages:
		selected = NewOpenAIImages()
		request.Path = openAIImagesGenerationsPath
		body = map[string]any{"model": model, "prompt": ""}
	case protocol.Anthropic:
		selected = NewAnthropic()
		request.Path = "/v1/messages"
		body = map[string]any{"model": model, "messages": []any{}}
	case protocol.Gemini:
		selected = NewGemini()
		request.Path = geminiGenerationPrefix + model + geminiGenerateSuffix
		body = map[string]any{}
	default:
		return nil, nil, fmt.Errorf("unsupported data protocol %q", clientProtocol)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, nil, fmt.Errorf("encode standard %s request: %w", clientProtocol, err)
	}
	request.Body = encoded
	return selected, request, nil
}
