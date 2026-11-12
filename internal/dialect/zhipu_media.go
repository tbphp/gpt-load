package dialect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"gpt-load/internal/execution"
	"gpt-load/internal/media"
	"gpt-load/internal/protocol"
)

const (
	zhipuImagesPath      = "/v1/images/generations"
	zhipuImagesAsyncPath = "/v1/async/images/generations"
	zhipuVideosPath      = "/v1/videos/generations"
	zhipuAsyncResultPath = "/v1/async-result"
)

// ZhipuMedia implements the OpenAI-style Zhipu media wire contract.
type ZhipuMedia struct{}

var (
	_ Dialect       = (*ZhipuMedia)(nil)
	_ ModelRewriter = (*ZhipuMedia)(nil)
)

func NewZhipuMedia() *ZhipuMedia {
	return &ZhipuMedia{}
}

func (*ZhipuMedia) Protocol() protocol.Protocol {
	return protocol.ZhipuMedia
}

func (d *ZhipuMedia) InspectRequest(request *ParsedRequest) (RequestMetadata, error) {
	if request == nil {
		return RequestMetadata{}, fmt.Errorf("parsed request is required")
	}
	switch request.Path {
	case zhipuImagesPath, zhipuImagesAsyncPath:
		if request.Method != http.MethodPost {
			return RequestMetadata{}, fmt.Errorf("%s only supports POST on %s", d.Protocol(), request.Path)
		}
		create, err := media.ParseZhipuImageRequest(request.Body, request.Path == zhipuImagesAsyncPath)
		if err != nil {
			return RequestMetadata{}, err
		}
		request.Body = create.NormalizedBody
		model := create.ClientModel
		return RequestMetadata{
			Model: &model, Operation: execution.Operation(create.Operation),
			MediaCreate:      &create,
			RouteRequirement: execution.RouteRequirementNative,
		}, nil
	case zhipuVideosPath:
		if request.Method != http.MethodPost {
			return RequestMetadata{}, fmt.Errorf("%s only supports POST on %s", d.Protocol(), request.Path)
		}
		create, err := media.ParseZhipuVideoRequest(request.Body)
		if err != nil {
			return RequestMetadata{}, err
		}
		request.Body = create.NormalizedBody
		model := create.ClientModel
		return RequestMetadata{
			Model: &model, Operation: execution.Operation(create.Operation),
			MediaCreate:      &create,
			RouteRequirement: execution.RouteRequirementNative,
		}, nil
	}
	remainder, ok := strings.CutPrefix(request.Path, zhipuAsyncResultPath+"/")
	if !ok || remainder == "" || strings.Contains(remainder, "/") || !validMediaTaskID(remainder) {
		return RequestMetadata{}, fmt.Errorf("unsupported %s path %q", d.Protocol(), request.Path)
	}
	operation := execution.OperationZhipuMediaRetrieve
	switch request.Method {
	case http.MethodGet:
	case http.MethodDelete:
		operation = execution.OperationZhipuMediaCancel
	default:
		return RequestMetadata{}, fmt.Errorf("unsupported %s method %q", d.Protocol(), request.Method)
	}
	return RequestMetadata{
		MediaTaskID: remainder, Operation: operation,
		RouteRequirement: execution.RouteRequirementNative,
	}, nil
}

func (d *ZhipuMedia) RewriteRequestModel(request *ParsedRequest, model string) (*ParsedRequest, error) {
	return d.rewriteBody(request, model)
}

func (d *ZhipuMedia) SanitizeRequestForAttempt(
	request *ParsedRequest,
	model string,
	stream bool,
) (*ParsedRequest, error) {
	if stream {
		return nil, fmt.Errorf("%s does not support streaming", d.Protocol())
	}
	return d.rewriteBody(request, model)
}

func (d *ZhipuMedia) rewriteBody(request *ParsedRequest, model string) (*ParsedRequest, error) {
	if request == nil {
		return nil, fmt.Errorf("parsed request is required")
	}
	if request.Path != zhipuImagesPath && request.Path != zhipuImagesAsyncPath && request.Path != zhipuVideosPath {
		return cloneParsedRequest(request)
	}
	if err := validateModelRewriteTarget(model, false); err != nil {
		return nil, err
	}
	object, err := decodeJSONObject(request.Body)
	if err != nil {
		return nil, fmt.Errorf("decode Zhipu media request: %w", err)
	}
	encodedModel, err := json.Marshal(model)
	if err != nil {
		return nil, fmt.Errorf("encode selected Zhipu model: %w", err)
	}
	object["model"] = encodedModel
	body, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("encode Zhipu media request: %w", err)
	}
	clone, err := cloneParsedRequest(request)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(clone.Body, body) {
		clone.Body = body
	}
	return clone, nil
}

func (*ZhipuMedia) RewriteResponseModel(body []byte, externalModel string) ([]byte, error) {
	if err := validateModelRewriteTarget(externalModel, false); err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return body, nil
	}
	return rewriteOptionalJSONField(body, "model", externalModel)
}
