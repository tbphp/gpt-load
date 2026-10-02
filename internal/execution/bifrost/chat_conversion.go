package bifrost

import "github.com/maximhq/bifrost/core/schemas"

// 锁定版本的 SDK 每次只读取一个工具增量；逐个交给同一状态机，避免并行调用丢失。
func chatToResponsesStreamEvents(chunk *schemas.BifrostChatResponse, state *schemas.ChatToResponsesStreamState) []*schemas.BifrostResponsesStreamResponse {
	if len(chunk.Choices) != 1 || chunk.Choices[0].ChatStreamResponseChoice == nil || chunk.Choices[0].Delta == nil || len(chunk.Choices[0].Delta.ToolCalls) < 2 {
		return chunk.ToBifrostResponsesStreamResponse(state)
	}
	choice := chunk.Choices[0]
	var events []*schemas.BifrostResponsesStreamResponse
	for index, call := range choice.Delta.ToolCalls {
		part := *chunk
		partChoice := choice
		delta := *choice.Delta
		if index > 0 {
			delta = schemas.ChatStreamResponseChoiceDelta{}
		}
		delta.ToolCalls = []schemas.ChatAssistantMessageToolCall{call}
		partChoice.ChatStreamResponseChoice = &schemas.ChatStreamResponseChoice{Delta: &delta}
		if index != len(choice.Delta.ToolCalls)-1 {
			partChoice.FinishReason = nil
			part.Usage = nil
		}
		part.Choices = []schemas.BifrostResponseChoice{partChoice}
		events = append(events, part.ToBifrostResponsesStreamResponse(state)...)
	}
	return events
}
