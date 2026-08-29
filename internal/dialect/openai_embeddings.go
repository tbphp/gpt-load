package dialect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const openAIEmbeddingsPath = "/v1/embeddings"

// OpenAIEmbeddings implements the OpenAI-compatible Embeddings wire contract.
type OpenAIEmbeddings struct{}

var (
	_ Dialect       = (*OpenAIEmbeddings)(nil)
	_ ModelRewriter = (*OpenAIEmbeddings)(nil)
)

func NewOpenAIEmbeddings() *OpenAIEmbeddings {
	return &OpenAIEmbeddings{}
}

func (*OpenAIEmbeddings) Protocol() protocol.Protocol {
	return protocol.OpenAIEmbeddings
}

func (d *OpenAIEmbeddings) InspectRequest(request *ParsedRequest) (RequestMetadata, error) {
	if request == nil {
		return RequestMetadata{}, fmt.Errorf("parsed request is required")
	}
	if request.Method != http.MethodPost {
		return RequestMetadata{}, fmt.Errorf("%s only supports POST", d.Protocol())
	}
	if request.Path != openAIEmbeddingsPath {
		return RequestMetadata{}, fmt.Errorf("unsupported %s path %q", d.Protocol(), request.Path)
	}
	contentType := request.Header.Get("Content-Type")
	if strings.TrimSpace(contentType) != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil {
			return RequestMetadata{}, fmt.Errorf("decode %s Content-Type: %w", d.Protocol(), err)
		}
		if !strings.EqualFold(mediaType, "application/json") {
			return RequestMetadata{}, fmt.Errorf("unsupported %s Content-Type %q", d.Protocol(), contentType)
		}
	}
	metadata, err := inspectJSONRequestFields(request.Body, true)
	if err != nil {
		return RequestMetadata{}, fmt.Errorf("decode %s request: %w", d.Protocol(), err)
	}
	if metadata.Stream {
		return RequestMetadata{}, fmt.Errorf("%s does not support streaming", d.Protocol())
	}
	if err := validateOpenAIEmbeddingsInput(request.Body); err != nil {
		return RequestMetadata{}, fmt.Errorf("decode %s request: %w", d.Protocol(), err)
	}
	metadata.Operation = execution.OperationEmbeddingsCreate
	metadata.RouteRequirement = execution.RouteRequirementNative
	metadata.ObserveUsage = true
	return metadata, nil
}

func (d *OpenAIEmbeddings) RewriteRequestModel(
	request *ParsedRequest,
	model string,
) (*ParsedRequest, error) {
	return rewriteJSONRequestModel(request, model, string(d.Protocol()))
}

// SanitizeRequestForAttempt rebuilds one request for the selected model while
// removing client controls that must never reach an upstream.
func (d *OpenAIEmbeddings) SanitizeRequestForAttempt(
	request *ParsedRequest,
	model string,
) (*ParsedRequest, error) {
	if err := validateModelRewriteTarget(model, false); err != nil {
		return nil, err
	}
	if _, err := d.InspectRequest(request); err != nil {
		return nil, err
	}
	object, err := decodeJSONObject(request.Body)
	if err != nil {
		return nil, fmt.Errorf("sanitize %s request: %w", d.Protocol(), err)
	}
	for field := range object {
		if openAIEmbeddingsControlField(field) || field == "stream" {
			delete(object, field)
		}
	}
	encodedModel, err := json.Marshal(model)
	if err != nil {
		return nil, fmt.Errorf("encode selected Embeddings model: %w", err)
	}
	object["model"] = encodedModel
	body, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("encode sanitized %s request: %w", d.Protocol(), err)
	}
	clone, err := cloneParsedRequest(request)
	if err != nil {
		return nil, err
	}
	clone.Body = body
	if clone.Header == nil {
		clone.Header = make(http.Header)
	}
	if strings.TrimSpace(clone.Header.Get("Content-Type")) == "" {
		clone.Header.Set("Content-Type", "application/json")
	}
	return clone, nil
}

func (*OpenAIEmbeddings) RewriteResponseModel(body []byte, model string) ([]byte, error) {
	if err := validateModelRewriteTarget(model, false); err != nil {
		return nil, err
	}
	return rewriteOptionalJSONField(body, "model", model)
}

func validateOpenAIEmbeddingsInput(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	root, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode request object: %w", err)
	}
	rootDelimiter, ok := root.(json.Delim)
	if !ok || rootDelimiter != '{' {
		return fmt.Errorf("request body must be a JSON object")
	}

	inputSeen := false
	for decoder.More() {
		fieldToken, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("decode request field: %w", err)
		}
		field, ok := fieldToken.(string)
		if !ok {
			return fmt.Errorf("request field name must be a string")
		}
		if strings.EqualFold(field, "input") {
			if field != "input" || inputSeen {
				return fmt.Errorf("input field must be unique lowercase input")
			}
			inputSeen = true
			var input json.RawMessage
			if err := decoder.Decode(&input); err != nil {
				return fmt.Errorf("decode input: %w", err)
			}
			if !validOpenAIEmbeddingsInput(input) {
				return fmt.Errorf("input must be a string, string array, token array, or token array list")
			}
			continue
		}
		var ignored json.RawMessage
		if err := decoder.Decode(&ignored); err != nil {
			return fmt.Errorf("decode request field %q: %w", field, err)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("close request object: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("request body contains multiple JSON values")
		}
		return fmt.Errorf("decode request tail: %w", err)
	}
	if !inputSeen {
		return fmt.Errorf("input is required")
	}
	return nil
}

func validOpenAIEmbeddingsInput(raw json.RawMessage) bool {
	if jsonEmbeddingString(raw) {
		return true
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return false
	}
	if len(values) == 0 {
		return true
	}
	if jsonEmbeddingString(values[0]) {
		for _, value := range values[1:] {
			if !jsonEmbeddingString(value) {
				return false
			}
		}
		return true
	}
	if jsonInteger(values[0]) {
		for _, value := range values[1:] {
			if !jsonInteger(value) {
				return false
			}
		}
		return true
	}
	if jsonIntegerArray(values[0]) {
		for _, value := range values[1:] {
			if !jsonIntegerArray(value) {
				return false
			}
		}
		return true
	}
	return false
}

func jsonEmbeddingString(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return false
	}
	var value string
	return json.Unmarshal(trimmed, &value) == nil
}

func jsonIntegerArray(raw json.RawMessage) bool {
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return false
	}
	for _, value := range values {
		if !jsonInteger(value) {
			return false
		}
	}
	return true
}

func jsonInteger(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return false
	}
	if value[0] == '-' {
		value = value[1:]
		if value == "" {
			return false
		}
	}
	if value == "0" {
		return true
	}
	if value[0] < '1' || value[0] > '9' {
		return false
	}
	for index := 1; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func openAIEmbeddingsControlField(name string) bool {
	normalized := strings.ReplaceAll(strings.ToLower(name), "-", "_")
	switch normalized {
	case "provider", "fallback", "fallbacks", "authorization", "proxy_authorization",
		"api_key", "apikey", "x_api_key", "x_goog_api_key":
		return true
	default:
		return false
	}
}
