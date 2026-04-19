package channel

import (
	"fmt"

	"gpt-load/internal/models"
)

func BuildValidationPayload(channelType, mode, model string) (map[string]any, error) {
	if mode == "" {
		switch channelType {
		case "openai-response":
			mode = models.ValidationPayloadModeResponsesSimple
		default:
			mode = models.ValidationPayloadModeChat
		}
	}

	switch mode {
	case models.ValidationPayloadModeResponsesSimple:
		return map[string]any{
			"model": model,
			"input": "hi",
		}, nil
	case models.ValidationPayloadModeResponsesMessages:
		return map[string]any{
			"model": model,
			"input": []map[string]any{
				{
					"type": "message",
					"role": "user",
					"content": []map[string]any{
						{
							"type": "input_text",
							"text": "hi",
						},
					},
				},
			},
		}, nil
	case models.ValidationPayloadModeChat:
		payload := map[string]any{
			"model": model,
			"messages": []map[string]any{
				{
					"role":    "user",
					"content": "hi",
				},
			},
		}
		if channelType == "anthropic" {
			payload["max_tokens"] = 100
		}
		return payload, nil
	default:
		return nil, fmt.Errorf("unsupported validation payload mode: %s", mode)
	}
}
