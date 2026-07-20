package dialect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

func extractJSONRequestFields(body []byte) (string, bool, error) {
	if !utf8.Valid(body) {
		return "", false, fmt.Errorf("request body must be valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	root, err := decoder.Token()
	if err != nil {
		return "", false, fmt.Errorf("decode request object: %w", err)
	}
	rootDelimiter, ok := root.(json.Delim)
	if !ok || rootDelimiter != '{' {
		return "", false, fmt.Errorf("request body must be a JSON object")
	}

	var model string
	var stream bool
	modelSeen := false
	streamSeen := false
	for decoder.More() {
		fieldToken, err := decoder.Token()
		if err != nil {
			return "", false, fmt.Errorf("decode request field: %w", err)
		}
		field, ok := fieldToken.(string)
		if !ok {
			return "", false, fmt.Errorf("request field name must be a string")
		}

		switch {
		case strings.EqualFold(field, "model"):
			if field != "model" || modelSeen {
				return "", false, fmt.Errorf("model field must be unique lowercase model")
			}
			modelSeen = true
			value, err := decoder.Token()
			if err != nil {
				return "", false, fmt.Errorf("decode model: %w", err)
			}
			var valid bool
			model, valid = value.(string)
			if !valid {
				return "", false, fmt.Errorf("model must be a string")
			}
		case strings.EqualFold(field, "stream"):
			if field != "stream" || streamSeen {
				return "", false, fmt.Errorf("stream field must be unique lowercase stream")
			}
			streamSeen = true
			value, err := decoder.Token()
			if err != nil {
				return "", false, fmt.Errorf("decode stream: %w", err)
			}
			var valid bool
			stream, valid = value.(bool)
			if !valid {
				return "", false, fmt.Errorf("stream must be a boolean")
			}
		default:
			var ignored json.RawMessage
			if err := decoder.Decode(&ignored); err != nil {
				return "", false, fmt.Errorf("decode request field %q: %w", field, err)
			}
		}
	}

	end, err := decoder.Token()
	if err != nil {
		return "", false, fmt.Errorf("close request object: %w", err)
	}
	endDelimiter, ok := end.(json.Delim)
	if !ok || endDelimiter != '}' {
		return "", false, fmt.Errorf("request object is not closed")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return "", false, fmt.Errorf("request body contains multiple JSON values")
		}
		return "", false, fmt.Errorf("decode request tail: %w", err)
	}
	if !modelSeen || model == "" || strings.TrimSpace(model) != model {
		return "", false, fmt.Errorf("model is required without boundary whitespace")
	}
	return model, stream, nil
}
