package dialect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

func inspectJSONRequestFields(body []byte, requireModel bool) (RequestMetadata, error) {
	if !utf8.Valid(body) {
		return RequestMetadata{}, fmt.Errorf("request body must be valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	root, err := decoder.Token()
	if err != nil {
		return RequestMetadata{}, fmt.Errorf("decode request object: %w", err)
	}
	rootDelimiter, ok := root.(json.Delim)
	if !ok || rootDelimiter != '{' {
		return RequestMetadata{}, fmt.Errorf("request body must be a JSON object")
	}

	result := RequestMetadata{}
	modelSeen := false
	streamSeen := false
	for decoder.More() {
		fieldToken, err := decoder.Token()
		if err != nil {
			return RequestMetadata{}, fmt.Errorf("decode request field: %w", err)
		}
		field, ok := fieldToken.(string)
		if !ok {
			return RequestMetadata{}, fmt.Errorf("request field name must be a string")
		}

		switch {
		case strings.EqualFold(field, "model"):
			if field != "model" || modelSeen {
				return RequestMetadata{}, fmt.Errorf("model field must be unique lowercase model")
			}
			modelSeen = true
			value, err := decoder.Token()
			if err != nil {
				return RequestMetadata{}, fmt.Errorf("decode model: %w", err)
			}
			model, valid := value.(string)
			if !valid {
				return RequestMetadata{}, fmt.Errorf("model must be a string")
			}
			if model == "" || strings.TrimSpace(model) != model {
				return RequestMetadata{}, fmt.Errorf(
					"model must be non-empty without boundary whitespace",
				)
			}
			result.Model = &model
		case strings.EqualFold(field, "stream"):
			if field != "stream" || streamSeen {
				return RequestMetadata{}, fmt.Errorf("stream field must be unique lowercase stream")
			}
			streamSeen = true
			value, err := decoder.Token()
			if err != nil {
				return RequestMetadata{}, fmt.Errorf("decode stream: %w", err)
			}
			var valid bool
			result.Stream, valid = value.(bool)
			if !valid {
				return RequestMetadata{}, fmt.Errorf("stream must be a boolean")
			}
		default:
			var ignored json.RawMessage
			if err := decoder.Decode(&ignored); err != nil {
				return RequestMetadata{}, fmt.Errorf("decode request field %q: %w", field, err)
			}
		}
	}

	end, err := decoder.Token()
	if err != nil {
		return RequestMetadata{}, fmt.Errorf("close request object: %w", err)
	}
	endDelimiter, ok := end.(json.Delim)
	if !ok || endDelimiter != '}' {
		return RequestMetadata{}, fmt.Errorf("request object is not closed")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return RequestMetadata{}, fmt.Errorf("request body contains multiple JSON values")
		}
		return RequestMetadata{}, fmt.Errorf("decode request tail: %w", err)
	}
	if requireModel && !modelSeen {
		return RequestMetadata{}, fmt.Errorf("model is required")
	}
	return result, nil
}
