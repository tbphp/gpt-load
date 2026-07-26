package dialect

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// InjectStreamUsage returns an independent request with OpenAI stream usage
// enabled, retaining every otherwise-unknown request and option field.
func (d *OpenAI) InjectStreamUsage(req *ParsedRequest) (*ParsedRequest, error) {
	clone, err := cloneParsedRequest(req)
	if err != nil {
		return nil, err
	}
	object, err := decodeJSONObject(clone.Body)
	if err != nil {
		return nil, fmt.Errorf("decode OpenAI stream usage request: %w", err)
	}

	options := make(map[string]json.RawMessage)
	if raw, exists := object["stream_options"]; exists &&
		!bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if err := json.Unmarshal(raw, &options); err != nil || options == nil {
			return nil, fmt.Errorf("OpenAI stream_options must be an object or null")
		}
	}
	options["include_usage"] = json.RawMessage("true")
	encodedOptions, err := json.Marshal(options)
	if err != nil {
		return nil, fmt.Errorf("encode OpenAI stream_options: %w", err)
	}
	object["stream_options"] = encodedOptions
	clone.Body, err = json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("encode OpenAI stream usage request: %w", err)
	}
	return clone, nil
}
