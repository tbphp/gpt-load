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

const rerankPath = "/v1/rerank"

// Rerank 实现 query/documents 纯文本原生接口。
type Rerank struct{}

func NewRerank() *Rerank { return &Rerank{} }

func (*Rerank) Protocol() protocol.Protocol { return protocol.Rerank }

func (d *Rerank) InspectRequest(request *ParsedRequest) (RequestMetadata, error) {
	if request == nil || request.Method != http.MethodPost || request.Path != rerankPath {
		return RequestMetadata{}, fmt.Errorf("rerank requires POST %s", rerankPath)
	}
	if value := request.Header.Get("Content-Type"); strings.TrimSpace(value) != "" {
		mediaType, _, err := mime.ParseMediaType(value)
		if err != nil || !strings.EqualFold(mediaType, "application/json") {
			return RequestMetadata{}, fmt.Errorf("rerank requires application/json")
		}
	}
	metadata, err := inspectJSONRequestFields(request.Body, true)
	if err != nil {
		return RequestMetadata{}, err
	}
	if metadata.Stream {
		return RequestMetadata{}, fmt.Errorf("rerank does not support streaming")
	}
	if err := validateRerankInput(request.Body); err != nil {
		return RequestMetadata{}, err
	}
	metadata.Operation = execution.OperationRerank
	metadata.RouteRequirement = execution.RouteRequirementNative
	metadata.ObserveUsage = true
	return metadata, nil
}

func validateRerankInput(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("decode rerank request")
	}
	seen := make(map[string]bool, 4)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("decode rerank field")
		}
		field, ok := token.(string)
		if !ok {
			return fmt.Errorf("invalid rerank field")
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return fmt.Errorf("decode rerank value")
		}
		name := strings.ToLower(field)
		switch name {
		case "query", "documents", "top_n", "return_documents":
			if field != name || seen[name] {
				return fmt.Errorf("rerank %s must be unique and lowercase", name)
			}
			seen[name] = true
		default:
			continue
		}
		switch name {
		case "query":
			var query string
			if json.Unmarshal(raw, &query) != nil || strings.TrimSpace(query) == "" {
				return fmt.Errorf("rerank query must be a non-empty string")
			}
		case "documents":
			var documents []json.RawMessage
			if json.Unmarshal(raw, &documents) != nil || len(documents) == 0 {
				return fmt.Errorf("rerank documents must be a non-empty string array")
			}
			for _, document := range documents {
				if !jsonEmbeddingString(document) {
					return fmt.Errorf("rerank documents must contain only strings")
				}
			}
		case "top_n":
			value := strings.TrimSpace(string(raw))
			if !jsonInteger(raw) || value[0] == '-' || value == "0" {
				return fmt.Errorf("rerank top_n must be a positive integer")
			}
		case "return_documents":
			value := string(bytes.TrimSpace(raw))
			if value != "true" && value != "false" {
				return fmt.Errorf("rerank return_documents must be a boolean")
			}
		}
	}
	if !seen["query"] || !seen["documents"] {
		return fmt.Errorf("rerank query and documents are required")
	}
	return nil
}

func (d *Rerank) RewriteRequestModel(request *ParsedRequest, model string) (*ParsedRequest, error) {
	if err := validateModelRewriteTarget(model, false); err != nil {
		return nil, err
	}
	if _, err := d.InspectRequest(request); err != nil {
		return nil, err
	}
	object, err := decodeJSONObject(request.Body)
	if err != nil {
		return nil, err
	}
	for field := range object {
		if openAIEmbeddingsControlField(field) || field == "stream" {
			delete(object, field)
		}
	}
	encodedModel, err := json.Marshal(model)
	if err != nil {
		return nil, fmt.Errorf("encode rerank model: %w", err)
	}
	object["model"] = encodedModel
	body, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("encode rerank request: %w", err)
	}
	clone, err := cloneParsedRequest(request)
	if err != nil {
		return nil, err
	}
	clone.Body = body
	if clone.Header == nil {
		clone.Header = make(http.Header)
	}
	if clone.Header.Get("Content-Type") == "" {
		clone.Header.Set("Content-Type", "application/json")
	}
	return clone, nil
}

func (*Rerank) RewriteResponseModel(body []byte, model string) ([]byte, error) {
	if err := validateModelRewriteTarget(model, false); err != nil {
		return nil, err
	}
	return rewriteOptionalJSONField(body, "model", model)
}
