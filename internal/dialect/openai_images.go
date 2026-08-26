package dialect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const (
	openAIImagesGenerationsPath = "/v1/images/generations"
	openAIImagesEditsPath       = "/v1/images/edits"
)

// OpenAIImages implements the OpenAI Images wire contract.
type OpenAIImages struct{}

var (
	_ Dialect               = (*OpenAIImages)(nil)
	_ ModelRewriter         = (*OpenAIImages)(nil)
	_ StreamEventClassifier = (*OpenAIImages)(nil)
)

func NewOpenAIImages() *OpenAIImages {
	return &OpenAIImages{}
}

func (*OpenAIImages) Protocol() protocol.Protocol {
	return protocol.OpenAIImages
}

func (d *OpenAIImages) InspectRequest(request *ParsedRequest) (RequestMetadata, error) {
	if request == nil {
		return RequestMetadata{}, fmt.Errorf("parsed request is required")
	}
	if request.Method != http.MethodPost {
		return RequestMetadata{}, fmt.Errorf("%s only supports POST", d.Protocol())
	}
	operation := execution.Operation("")
	switch request.Path {
	case openAIImagesGenerationsPath:
		operation = execution.OperationImagesGenerate
	case openAIImagesEditsPath:
		operation = execution.OperationImagesEdit
	default:
		return RequestMetadata{}, fmt.Errorf("unsupported %s path %q", d.Protocol(), request.Path)
	}
	contentType := request.Header.Get("Content-Type")
	mediaType := ""
	if strings.TrimSpace(contentType) != "" {
		parsedMediaType, _, parseErr := mime.ParseMediaType(contentType)
		if parseErr != nil {
			return RequestMetadata{}, fmt.Errorf("decode %s Content-Type: %w", d.Protocol(), parseErr)
		}
		mediaType = strings.ToLower(parsedMediaType)
	}
	if operation == execution.OperationImagesEdit && mediaType == "multipart/form-data" {
		parsed, err := processOpenAIImagesMultipart(request.Body, contentType, nil)
		if err != nil {
			return RequestMetadata{}, fmt.Errorf("decode %s request: %w", d.Protocol(), err)
		}
		model := parsed.model
		return RequestMetadata{
			Model: &model, Stream: parsed.stream,
			Operation: operation, RouteRequirement: execution.RouteRequirementNative,
			ObserveUsage: true,
		}, nil
	}
	if mediaType != "" && mediaType != "application/json" {
		return RequestMetadata{}, fmt.Errorf("unsupported %s Content-Type %q", d.Protocol(), contentType)
	}
	metadata, err := inspectJSONRequestFields(request.Body, true)
	if err != nil {
		return RequestMetadata{}, fmt.Errorf("decode %s request: %w", d.Protocol(), err)
	}
	metadata.Operation = operation
	metadata.RouteRequirement = execution.RouteRequirementNative
	metadata.ObserveUsage = true
	return metadata, nil
}

func (d *OpenAIImages) RewriteRequestModel(request *ParsedRequest, model string) (*ParsedRequest, error) {
	if err := validateModelRewriteTarget(model, false); err != nil {
		return nil, err
	}
	if request != nil && request.Path == openAIImagesEditsPath {
		contentType := request.Header.Get("Content-Type")
		mediaType, _, _ := mime.ParseMediaType(contentType)
		if strings.EqualFold(mediaType, "multipart/form-data") {
			result, err := processOpenAIImagesMultipart(request.Body, contentType, &model)
			if err != nil {
				return nil, fmt.Errorf("rewrite %s multipart request: %w", d.Protocol(), err)
			}
			return applyRebuiltImagesMultipart(request, result)
		}
	}
	return rewriteJSONRequestModel(request, model, string(d.Protocol()))
}

// SanitizeRequestForAttempt rebuilds one Images request for the selected
// upstream model and actual execution mode. The returned request is owned by
// the caller; the original client representation is never mutated.
func (d *OpenAIImages) SanitizeRequestForAttempt(
	request *ParsedRequest,
	model string,
	stream bool,
) (*ParsedRequest, error) {
	if err := validateModelRewriteTarget(model, false); err != nil {
		return nil, err
	}
	if _, err := d.InspectRequest(request); err != nil {
		return nil, err
	}
	contentType := request.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if request.Path == openAIImagesEditsPath && strings.EqualFold(mediaType, "multipart/form-data") {
		result, err := processOpenAIImagesMultipartWithOptions(
			request.Body,
			contentType,
			openAIImagesMultipartOptions{
				replacementModel: &model,
				forceStream:      &stream,
				stripControls:    true,
			},
		)
		if err != nil {
			return nil, fmt.Errorf("sanitize %s multipart request: %w", d.Protocol(), err)
		}
		return applyRebuiltImagesMultipart(request, result)
	}

	object, err := decodeJSONObject(request.Body)
	if err != nil {
		return nil, fmt.Errorf("sanitize %s request: %w", d.Protocol(), err)
	}
	for field := range object {
		if openAIImagesSecurityControlName(field) {
			delete(object, field)
		}
	}
	encodedModel, err := json.Marshal(model)
	if err != nil {
		return nil, fmt.Errorf("encode selected Images model: %w", err)
	}
	object["model"] = encodedModel
	if _, exists := object["stream"]; exists || stream {
		object["stream"] = json.RawMessage(fmt.Sprintf("%t", stream))
	}
	body, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("encode sanitized %s request: %w", d.Protocol(), err)
	}
	clone, err := cloneParsedRequest(request)
	if err != nil {
		return nil, err
	}
	clone.Body = body
	return clone, nil
}

func (*OpenAIImages) RewriteResponseModel(body []byte, model string) ([]byte, error) {
	if err := validateModelRewriteTarget(model, false); err != nil {
		return nil, err
	}
	return rewriteOptionalJSONField(body, "model", model)
}

func (*OpenAIImages) RequiresTerminalEvent() bool {
	return true
}

func (*OpenAIImages) ClassifyStreamEvent(event StreamEvent) (StreamEventClassification, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(event.Payload, &object); err != nil || object == nil {
		return StreamEventClassification{}, fmt.Errorf("decode OpenAI Images stream event")
	}

	payloadType := ""
	if raw, exists := object["type"]; exists {
		if err := json.Unmarshal(raw, &payloadType); err != nil || payloadType == "" {
			return StreamEventClassification{}, fmt.Errorf("decode OpenAI Images stream event type")
		}
	}
	if event.Name != "" && payloadType != "" && event.Name != payloadType {
		return StreamEventClassification{}, fmt.Errorf(
			"OpenAI Images stream event name %q conflicts with payload type %q",
			event.Name,
			payloadType,
		)
	}
	eventType := event.Name
	if eventType == "" {
		eventType = payloadType
	}
	switch eventType {
	case "image_generation.completed", "image_edit.completed":
		return StreamEventClassification{Disposition: StreamEventCompleted}, nil
	case "image_generation.failed", "image_edit.failed", "error":
		return StreamEventClassification{Disposition: StreamEventFailed}, nil
	}
	if raw, exists := object["error"]; exists {
		value := bytes.TrimSpace(raw)
		if len(value) > 0 && !bytes.Equal(value, []byte("null")) {
			return StreamEventClassification{Disposition: StreamEventFailed}, nil
		}
	}
	return StreamEventClassification{Disposition: StreamEventContinue}, nil
}
