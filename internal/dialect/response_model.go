package dialect

import (
	"bytes"
	"encoding/json"
	"strings"
)

var (
	_ ResponseModelInspector = (*OpenAI)(nil)
	_ ResponseModelInspector = (*OpenAIResponses)(nil)
	_ ResponseModelInspector = (*Anthropic)(nil)
	_ ResponseModelInspector = (*Gemini)(nil)
)

func (*OpenAI) InspectResponseModels(payload []byte) []string {
	object, err := decodeJSONObject(payload)
	if err != nil {
		return nil
	}
	return appendJSONModel(nil, object, "model")
}

func (*OpenAIResponses) InspectResponseModels(payload []byte) []string {
	object, err := decodeJSONObject(payload)
	if err != nil {
		return nil
	}
	models := appendJSONModel(nil, object, "model")
	rawResponse, exists := object["response"]
	if !exists || bytes.Equal(bytes.TrimSpace(rawResponse), []byte("null")) {
		return models
	}
	response, err := decodeJSONObject(rawResponse)
	if err != nil {
		return models
	}
	return appendJSONModel(models, response, "model")
}

func (*Anthropic) InspectResponseModels(payload []byte) []string {
	object, err := decodeJSONObject(payload)
	if err != nil {
		return nil
	}
	models := appendJSONModel(nil, object, "model")
	if len(models) != 0 {
		return models
	}

	responseType, ok := jsonStringField(object, "type")
	if !ok || responseType != "message_start" {
		return nil
	}
	rawMessage, exists := object["message"]
	if !exists || bytes.Equal(bytes.TrimSpace(rawMessage), []byte("null")) {
		return nil
	}
	message, err := decodeJSONObject(rawMessage)
	if err != nil {
		return nil
	}
	return appendJSONModel(nil, message, "model")
}

func (*Gemini) InspectResponseModels(payload []byte) []string {
	object, err := decodeJSONObject(payload)
	if err != nil {
		return nil
	}
	return appendJSONModel(nil, object, "modelVersion")
}

func appendJSONModel(models []string, object map[string]json.RawMessage, field string) []string {
	model, ok := jsonStringField(object, field)
	if !ok || strings.TrimSpace(model) == "" {
		return models
	}
	return append(models, model)
}

func jsonStringField(object map[string]json.RawMessage, field string) (string, bool) {
	raw, exists := object[field]
	if !exists {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}
