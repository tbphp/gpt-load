package dialect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/video"
)

const openAIVideosPath = "/v1/videos"

// OpenAIVideos implements the OpenAI Videos wire contract. The gateway owns
// task binding and credential pinning; this dialect only validates the client
// shape and normalizes the outbound create body.
type OpenAIVideos struct{}

var (
	_ Dialect       = (*OpenAIVideos)(nil)
	_ ModelRewriter = (*OpenAIVideos)(nil)
)

func NewOpenAIVideos() *OpenAIVideos {
	return &OpenAIVideos{}
}

func (*OpenAIVideos) Protocol() protocol.Protocol {
	return protocol.OpenAIVideos
}

func (d *OpenAIVideos) InspectRequest(request *ParsedRequest) (RequestMetadata, error) {
	if request == nil {
		return RequestMetadata{}, fmt.Errorf("parsed request is required")
	}
	if request.Path != openAIVideosPath && !strings.HasPrefix(request.Path, openAIVideosPath+"/") {
		return RequestMetadata{}, fmt.Errorf("unsupported %s path %q", d.Protocol(), request.Path)
	}
	if request.Path == openAIVideosPath {
		if request.Method != http.MethodPost {
			return RequestMetadata{}, fmt.Errorf("%s only supports POST on %s", d.Protocol(), openAIVideosPath)
		}
		parsed, err := video.ParseCreateRequest(request.Body)
		if err != nil {
			return RequestMetadata{}, err
		}
		model := parsed.Model
		request.Body = parsed.NormalizedBody
		return RequestMetadata{
			Model: &model, Operation: execution.OperationVideosCreate,
			VideoCreate:      &parsed,
			RouteRequirement: execution.RouteRequirementNative,
		}, nil
	}

	remainder := strings.TrimPrefix(request.Path, openAIVideosPath+"/")
	parts := strings.Split(remainder, "/")
	if len(parts) == 0 || len(parts) > 2 || !validVideoTaskID(parts[0]) {
		return RequestMetadata{}, fmt.Errorf("invalid %s resource path", d.Protocol())
	}
	operation := execution.OperationVideosRetrieve
	switch len(parts) {
	case 1:
		switch request.Method {
		case http.MethodGet:
			operation = execution.OperationVideosRetrieve
		case http.MethodDelete:
			operation = execution.OperationVideosDelete
		default:
			return RequestMetadata{}, fmt.Errorf("unsupported %s method %q", d.Protocol(), request.Method)
		}
	case 2:
		if parts[1] != "content" || request.Method != http.MethodGet {
			return RequestMetadata{}, fmt.Errorf("unsupported %s resource path", d.Protocol())
		}
		operation = execution.OperationVideosContent
	}
	return RequestMetadata{
		VideoTaskID: parts[0], Operation: operation,
		RouteRequirement: execution.RouteRequirementNative,
	}, nil
}

func (d *OpenAIVideos) RewriteRequestModel(request *ParsedRequest, model string) (*ParsedRequest, error) {
	if err := validateModelRewriteTarget(model, false); err != nil {
		return nil, err
	}
	if request == nil {
		return nil, fmt.Errorf("parsed request is required")
	}
	if request.Path != openAIVideosPath {
		return cloneParsedRequest(request)
	}
	parsed, err := video.ParseCreateRequest(request.Body)
	if err != nil {
		return nil, err
	}
	object, err := decodeJSONObject(parsed.NormalizedBody)
	if err != nil {
		return nil, fmt.Errorf("decode video request: %w", err)
	}
	encodedModel, err := json.Marshal(model)
	if err != nil {
		return nil, fmt.Errorf("encode selected video model: %w", err)
	}
	object["model"] = encodedModel
	body, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("encode video request: %w", err)
	}
	clone, err := cloneParsedRequest(request)
	if err != nil {
		return nil, err
	}
	clone.Body = body
	return clone, nil
}

func (d *OpenAIVideos) SanitizeRequestForAttempt(
	request *ParsedRequest,
	model string,
	stream bool,
) (*ParsedRequest, error) {
	if stream {
		return nil, fmt.Errorf("%s does not support streaming", d.Protocol())
	}
	if request == nil {
		return nil, fmt.Errorf("parsed request is required")
	}
	if request.Path != openAIVideosPath {
		return cloneParsedRequest(request)
	}
	parsed, err := video.ParseCreateRequest(request.Body)
	if err != nil {
		return nil, err
	}
	object, err := decodeJSONObject(parsed.NormalizedBody)
	if err != nil {
		return nil, fmt.Errorf("decode video request: %w", err)
	}
	encodedModel, err := json.Marshal(model)
	if err != nil {
		return nil, fmt.Errorf("encode selected video model: %w", err)
	}
	object["model"] = encodedModel
	body, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("encode video request: %w", err)
	}
	if !bytes.Equal(body, request.Body) {
		clone, err := cloneParsedRequest(request)
		if err != nil {
			return nil, err
		}
		clone.Body = body
		return clone, nil
	}
	return cloneParsedRequest(request)
}

func (*OpenAIVideos) RewriteResponseModel(body []byte, externalModel string) ([]byte, error) {
	if err := validateModelRewriteTarget(externalModel, false); err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return body, nil
	}
	return rewriteOptionalJSONField(body, "model", externalModel)
}

func validVideoTaskID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' || char == '_' || char == '-' || char == '.' {
			continue
		}
		return false
	}
	return value != "." && value != ".."
}
