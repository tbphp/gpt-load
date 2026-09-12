package bifrost

import (
	"encoding/json"
	"fmt"

	"github.com/maximhq/bifrost/core/providers/anthropic"
	"github.com/maximhq/bifrost/core/schemas"
)

// 继续使用 SDK 原有的 Anthropic→Chat 内容转换，仅将用量读取与其丢弃
// message_delta 的 Chat 传输循环解耦，保留工具索引、思考和签名的转换行为。
type anthropicChatStreamEncoder struct {
	state           *anthropic.AnthropicStreamState
	id              string
	model           string
	structuredIndex *int
}

func (e *anthropicChatStreamEncoder) encode(ctx *schemas.BifrostContext, response *schemas.BifrostResponsesStreamResponse, raw any) ([][]byte, error) {
	if raw == nil {
		return nil, nil
	}
	body, ok := rawRequestJSON(raw)
	var event anthropic.AnthropicStreamEvent
	if !ok || json.Unmarshal(body, &event) != nil {
		return nil, fmt.Errorf("decode Anthropic stream event")
	}
	if event.Message != nil {
		e.id, e.model = event.Message.ID, event.Message.Model
	}
	if event.Type == anthropic.AnthropicStreamEventTypeMessageStop {
		chat := response.ToBifrostChatResponse()
		chat.ID, chat.Model = e.id, e.model
		if response.Response != nil {
			chat.ServiceTier = response.Response.ServiceTier
			if response.Response.StopReason != nil && len(chat.Choices) > 0 {
				chat.Choices[0].FinishReason = response.Response.StopReason
			}
		}
		payload, _, err := encodeChatResponse(chat)
		if err != nil {
			return nil, err
		}
		return [][]byte{frameSSE(payload), []byte("data: [DONE]\n\n")}, nil
	}
	structuredTool, _ := ctx.Value(schemas.BifrostContextKeyStructuredOutputToolName).(string)
	if event.Type == anthropic.AnthropicStreamEventTypeContentBlockStart && event.ContentBlock != nil &&
		structuredTool != "" && event.ContentBlock.Name != nil && *event.ContentBlock.Name == structuredTool {
		e.structuredIndex = event.Index
		return nil, nil
	}
	if e.structuredIndex != nil && event.Index != nil && *e.structuredIndex == *event.Index {
		if event.Type == anthropic.AnthropicStreamEventTypeContentBlockStop {
			e.structuredIndex = nil
			return nil, nil
		}
		if event.Delta != nil && event.Delta.Type == anthropic.AnthropicStreamDeltaTypeInputJSON {
			event.Delta = &anthropic.AnthropicStreamDelta{Type: anthropic.AnthropicStreamDeltaTypeText, Text: event.Delta.PartialJSON}
		}
	}
	chat, sdkErr, _ := event.ToBifrostChatCompletionStream(ctx, structuredTool, e.state)
	if sdkErr != nil {
		return nil, fmt.Errorf("convert Anthropic stream event")
	}
	if chat == nil {
		return nil, nil
	}
	chat.ID, chat.Model = e.id, e.model
	payload, _, err := encodeChatResponse(chat)
	if err != nil {
		return nil, err
	}
	return [][]byte{frameSSE(payload)}, nil
}
