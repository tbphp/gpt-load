package dialect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

var (
	_ ModelRewriter = (*OpenAI)(nil)
	_ ModelRewriter = (*Anthropic)(nil)
	_ ModelRewriter = (*Gemini)(nil)
)

func cloneParsedRequest(req *ParsedRequest) (*ParsedRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("parsed request is required")
	}
	clone := *req
	clone.Header = req.Header.Clone()
	clone.Body = bytes.Clone(req.Body)
	return &clone, nil
}

func (d *OpenAI) RewriteRequestModel(req *ParsedRequest, model string) (*ParsedRequest, error) {
	return rewriteJSONRequestModel(req, model, string(d.Protocol()))
}

func (d *OpenAI) RewriteResponseModel(body []byte, model string) ([]byte, error) {
	if err := validateModelRewriteTarget(model, false); err != nil {
		return nil, err
	}
	return rewriteOptionalJSONField(body, "model", model)
}

func (d *Anthropic) RewriteRequestModel(req *ParsedRequest, model string) (*ParsedRequest, error) {
	return rewriteJSONRequestModel(req, model, string(d.Protocol()))
}

func (d *Anthropic) RewriteResponseModel(body []byte, model string) ([]byte, error) {
	if err := validateModelRewriteTarget(model, false); err != nil {
		return nil, err
	}
	object, err := decodeJSONObject(body)
	if err != nil {
		return nil, err
	}
	if _, exists := object["model"]; exists {
		return marshalRewrittenField(object, "model", model)
	}

	var responseType string
	if rawType, exists := object["type"]; !exists {
		return bytes.Clone(body), nil
	} else if err := json.Unmarshal(rawType, &responseType); err != nil {
		return nil, fmt.Errorf("decode response type: %w", err)
	}
	if responseType != "message_start" {
		return bytes.Clone(body), nil
	}
	rawMessage, exists := object["message"]
	if !exists {
		return bytes.Clone(body), nil
	}
	message, err := decodeJSONObject(rawMessage)
	if err != nil {
		return nil, fmt.Errorf("decode message_start message: %w", err)
	}
	if _, exists := message["model"]; !exists {
		return bytes.Clone(body), nil
	}
	rewrittenMessage, err := marshalRewrittenField(message, "model", model)
	if err != nil {
		return nil, err
	}
	object["message"] = rewrittenMessage
	return json.Marshal(object)
}

func (d *Gemini) RewriteRequestModel(req *ParsedRequest, model string) (*ParsedRequest, error) {
	if err := validateModelRewriteTarget(model, true); err != nil {
		return nil, err
	}
	clone, err := cloneParsedRequest(req)
	if err != nil {
		return nil, err
	}
	_, stream, err := parseGeminiGenerationPath(req.Path)
	if err != nil {
		return nil, err
	}
	suffix := geminiGenerateSuffix
	if stream {
		suffix = geminiStreamSuffix
	}
	clone.Path = geminiGenerationPrefix + model + suffix
	return clone, nil
}

func (d *Gemini) RewriteResponseModel(body []byte, model string) ([]byte, error) {
	if err := validateModelRewriteTarget(model, true); err != nil {
		return nil, err
	}
	return rewriteOptionalJSONField(body, "modelVersion", model)
}

func rewriteJSONRequestModel(req *ParsedRequest, model, protocolName string) (*ParsedRequest, error) {
	if err := validateModelRewriteTarget(model, false); err != nil {
		return nil, err
	}
	clone, err := cloneParsedRequest(req)
	if err != nil {
		return nil, err
	}
	object, err := decodeJSONObject(clone.Body)
	if err != nil {
		return nil, fmt.Errorf("decode %s request: %w", protocolName, err)
	}
	if _, exists := object["model"]; !exists {
		return nil, fmt.Errorf("request model is required")
	}
	body, err := marshalRewrittenField(object, "model", model)
	if err != nil {
		return nil, err
	}
	clone.Body = body
	return clone, nil
}

func rewriteOptionalJSONField(body []byte, field, model string) ([]byte, error) {
	object, err := decodeJSONObject(body)
	if err != nil {
		return nil, err
	}
	if _, exists := object[field]; !exists {
		return bytes.Clone(body), nil
	}
	return marshalRewrittenField(object, field, model)
}

func marshalRewrittenField(object map[string]json.RawMessage, field, model string) ([]byte, error) {
	encodedModel, err := json.Marshal(model)
	if err != nil {
		return nil, fmt.Errorf("encode rewritten model: %w", err)
	}
	object[field] = encodedModel
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("encode rewritten object: %w", err)
	}
	return encoded, nil
}

func decodeJSONObject(body []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil {
		return nil, fmt.Errorf("decode JSON object: %w", err)
	}
	if object == nil {
		return nil, fmt.Errorf("decode JSON object: root must be an object")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode JSON object: multiple root values")
		}
		return nil, fmt.Errorf("decode JSON object trailing data: %w", err)
	}
	return object, nil
}

func validateModelRewriteTarget(model string, rejectSlash bool) error {
	if model == "" || strings.TrimSpace(model) != model {
		return fmt.Errorf("rewrite model must not be empty or contain boundary whitespace")
	}
	if rejectSlash && strings.Contains(model, "/") {
		return fmt.Errorf("Gemini rewrite model must not contain slash")
	}
	return nil
}
