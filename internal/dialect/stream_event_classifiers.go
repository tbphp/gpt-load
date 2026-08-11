package dialect

import (
	"bytes"
	"encoding/json"
	"fmt"
)

var (
	_ StreamEventClassifier = (*OpenAI)(nil)
	_ StreamEventClassifier = (*Anthropic)(nil)
	_ StreamEventClassifier = (*Gemini)(nil)
)

func (*OpenAI) RequiresTerminalEvent() bool { return true }

func (*OpenAI) ClassifyStreamEvent(event StreamEvent) (StreamEventClassification, error) {
	if bytes.Equal(bytes.TrimSpace(event.Payload), []byte("[DONE]")) {
		return StreamEventClassification{Disposition: StreamEventCompleted}, nil
	}
	var object struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(event.Payload, &object); err != nil {
		return StreamEventClassification{}, fmt.Errorf("decode OpenAI stream event")
	}
	if meaningfulJSONValue(object.Error) {
		return StreamEventClassification{Disposition: StreamEventFailed}, nil
	}
	return StreamEventClassification{Disposition: StreamEventContinue}, nil
}

func (*Anthropic) RequiresTerminalEvent() bool { return true }

func (*Anthropic) ClassifyStreamEvent(event StreamEvent) (StreamEventClassification, error) {
	eventType, err := matchingStreamEventType(event, "Anthropic")
	if err != nil {
		return StreamEventClassification{}, err
	}
	switch eventType {
	case "message_stop":
		return StreamEventClassification{Disposition: StreamEventCompleted}, nil
	case "error":
		return StreamEventClassification{Disposition: StreamEventFailed}, nil
	default:
		return StreamEventClassification{Disposition: StreamEventContinue}, nil
	}
}

func (*Gemini) RequiresTerminalEvent() bool { return true }

func (*Gemini) ClassifyStreamEvent(event StreamEvent) (StreamEventClassification, error) {
	var object struct {
		Error          json.RawMessage `json:"error"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
		Candidates []struct {
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(event.Payload, &object); err != nil {
		return StreamEventClassification{}, fmt.Errorf("decode Gemini stream event")
	}
	if meaningfulJSONValue(object.Error) {
		return StreamEventClassification{Disposition: StreamEventFailed}, nil
	}
	if object.PromptFeedback.BlockReason != "" {
		return StreamEventClassification{Disposition: StreamEventCompleted}, nil
	}
	for _, candidate := range object.Candidates {
		if candidate.FinishReason != "" {
			return StreamEventClassification{Disposition: StreamEventCompleted}, nil
		}
	}
	return StreamEventClassification{Disposition: StreamEventContinue}, nil
}

func matchingStreamEventType(event StreamEvent, protocolName string) (string, error) {
	var object struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(event.Payload, &object); err != nil {
		return "", fmt.Errorf("decode %s stream event", protocolName)
	}
	if event.Name != "" && object.Type != "" && event.Name != object.Type {
		return "", fmt.Errorf("%s stream event name conflicts with payload type", protocolName)
	}
	if event.Name != "" {
		return event.Name, nil
	}
	return object.Type, nil
}

func meaningfulJSONValue(value json.RawMessage) bool {
	value = bytes.TrimSpace(value)
	return len(value) > 0 && !bytes.Equal(value, []byte("null")) && !bytes.Equal(value, []byte("{}"))
}
