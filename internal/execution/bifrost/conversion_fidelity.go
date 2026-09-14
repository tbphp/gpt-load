package bifrost

import (
	"context"

	"github.com/maximhq/bifrost/core/providers/anthropic"
	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
)

func finishConvertedPreparation(spec execution.AttemptSpec, providerKind channel.ProviderKind, prepared preparedAttempt) (preparedAttempt, *execution.AttemptResult) {
	if spec.RouteMode != execution.RouteConverted ||
		(spec.Operation != execution.OperationChatCompletion && spec.Operation != execution.OperationResponsesCreate) {
		return prepared, nil
	}
	vertexChat := providerKind == channel.ProviderGoogleVertex && vertexUsesChatFallback(spec.UpstreamModel)
	if providerKind == channel.ProviderAnthropic || providerKind == channel.ProviderGemini ||
		providerKind == channel.ProviderAWSBedrock || (providerKind == channel.ProviderGoogleVertex && !vertexChat) {
		preserveResponsesGlobalInstructions(prepared.responsesRequest)
	}
	chatFallback := providerKind == channel.ProviderOpenAICompatible || providerKind == channel.ProviderGroq ||
		providerKind == channel.ProviderDeepSeek || vertexChat
	if chatFallback {
		if chatFallbackDropsTools(prepared.responsesRequest) {
			failure := notSentConversionFailure(execution.ErrorCodeCriticalSemanticLoss, "Chat conversion cannot preserve requested tools or tool choice")
			return preparedAttempt{}, &failure
		}
		normalizeChatFallbackToolMode(prepared.responsesRequest)
		if providerKind == channel.ProviderDeepSeek && deepSeekConversionDisablesThinking(prepared.responsesRequest) {
			failure := notSentConversionFailure(execution.ErrorCodeCriticalSemanticLoss, "DeepSeek conversion cannot preserve explicit thinking with this tool choice or history")
			return preparedAttempt{}, &failure
		}
	}
	wantSystems := dialect.CountMidConversationSystemMessages(spec.ClientProtocol, spec.Body)
	if wantSystems == 0 {
		return prepared, nil
	}
	preserved := true
	switch providerKind {
	case channel.ProviderGemini, channel.ProviderAWSBedrock:
		// 这些现有转换器只能把中途系统指令前移或变成用户文本。
		preserved = false
	case channel.ProviderGoogleVertex:
		// Vertex 的非 Claude/Gemini 模型使用 OpenAI wire，可以原位保留系统消息。
		preserved = vertexChat
	case channel.ProviderAnthropic:
		// 仅对此输入形状复用 SDK 的 ModelCaps 和实际位置规则，避免复制模型表或近似工具分组。
		ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
		defer ctx.Cancel()
		var request *anthropic.AnthropicMessageRequest
		var err error
		if prepared.request != nil {
			request, err = anthropic.ToAnthropicChatRequest(ctx, prepared.request)
		} else {
			request, err = anthropic.ToAnthropicResponsesRequest(ctx, prepared.responsesRequest)
		}
		keptSystems := 0
		if err == nil && request != nil {
			for _, message := range request.Messages {
				if message.Role == anthropic.AnthropicMessageRoleSystem {
					keptSystems++
				}
			}
		}
		preserved = keptSystems == wantSystems
	}
	if !preserved {
		failure := notSentConversionFailure(execution.ErrorCodeCriticalSemanticLoss, "conversion cannot preserve mid-conversation system instructions")
		return preparedAttempt{}, &failure
	}
	return prepared, nil
}

func vertexUsesChatFallback(model string) bool {
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	defer ctx.Cancel()
	return !schemas.IsAnthropicModelFamily(ctx, model) && !schemas.IsGeminiModelFamily(ctx, model) &&
		!schemas.IsGemmaModelFamily(ctx, model) && !schemas.IsAllDigitsASCII(model)
}

func preserveResponsesGlobalInstructions(request *schemas.BifrostResponsesRequest) {
	if request == nil || request.Params == nil || request.Params.Instructions == nil || *request.Params.Instructions == "" {
		return
	}
	// 这些 SDK 转换器优先使用 input 内的 system，可能覆盖并存的 instructions。
	// 将全局指令放在输入前端，让同一转换器按顺序处理，并避免重复发送。
	request.Input = append([]schemas.ResponsesMessage{{
		Type:    schemas.Ptr(schemas.ResponsesMessageTypeMessage),
		Role:    schemas.Ptr(schemas.ResponsesInputMessageRoleSystem),
		Content: &schemas.ResponsesMessageContent{ContentStr: request.Params.Instructions},
	}}, request.Input...)
	request.Params.Instructions = nil
}

func chatFallbackDropsTools(request *schemas.BifrostResponsesRequest) bool {
	if request == nil || request.Params == nil {
		return false
	}
	// 仅检查 SDK 的参数映射；不重复转换消息，也不自行维护工具能力表。
	converted := (&schemas.BifrostResponsesRequest{Params: request.Params}).ToChatRequest()
	if len(converted.Params.Tools) != len(request.Params.Tools) {
		return true
	}
	choice := request.Params.ToolChoice
	if choice == nil || converted.Params.ToolChoice != nil {
		return false
	}
	// 没有声明工具时，省略 auto/none 不会改变行为；强制调用约束仍不能丢失。
	mode := ""
	if choice.ResponsesToolChoiceStr != nil {
		mode = *choice.ResponsesToolChoiceStr
	} else if value := choice.ResponsesToolChoiceStruct; value != nil && len(value.Tools) == 0 && value.Name == nil && value.ServerLabel == nil {
		mode = string(value.Type)
	}
	return mode != "auto" && mode != "none"
}

func normalizeChatFallbackToolMode(request *schemas.BifrostResponsesRequest) {
	if request == nil || request.Params == nil || request.Params.ToolChoice == nil {
		return
	}
	choice := request.Params.ToolChoice.ResponsesToolChoiceStruct
	if choice == nil || len(choice.Tools) != 0 || choice.Name != nil || choice.ServerLabel != nil {
		return
	}
	switch choice.Type {
	case schemas.ResponsesToolChoiceTypeAuto, schemas.ResponsesToolChoiceTypeNone, schemas.ResponsesToolChoiceTypeRequired:
		// SDK 的结构化 mode:auto 会误映射为 any；采用同义字符串保留原始约束。
		request.Params.ToolChoice = &schemas.ResponsesToolChoice{ResponsesToolChoiceStr: schemas.Ptr(string(choice.Type))}
	}
}

func deepSeekConversionDisablesThinking(request *schemas.BifrostResponsesRequest) bool {
	if request == nil || request.Params == nil || request.Params.Reasoning == nil {
		return false
	}
	reasoning := request.Params.Reasoning
	enabled := (reasoning.Effort != nil && *reasoning.Effort != "" && *reasoning.Effort != "none") ||
		(reasoning.MaxTokens != nil && *reasoning.MaxTokens != 0)
	if !enabled {
		return false
	}
	chat := request.ToChatRequest()
	// 对齐锁定 SDK 的 requiresDeepSeekThinkingDisabled，仅阻断显式开启思考的冲突输入。
	if choice := chat.Params.ToolChoice; choice != nil {
		var kind schemas.ChatToolChoiceType
		if choice.ChatToolChoiceStr != nil {
			kind = schemas.ChatToolChoiceType(*choice.ChatToolChoiceStr)
		} else if choice.ChatToolChoiceStruct != nil {
			kind = choice.ChatToolChoiceStruct.Type
		}
		switch kind {
		case schemas.ChatToolChoiceTypeRequired, schemas.ChatToolChoiceTypeAny, schemas.ChatToolChoiceTypeFunction,
			schemas.ChatToolChoiceTypeCustom, schemas.ChatToolChoiceTypeAllowedTools:
			return true
		}
	}
	for _, message := range chat.Input {
		if message.Role == schemas.ChatMessageRoleAssistant && message.ChatAssistantMessage != nil &&
			len(message.ToolCalls) != 0 && message.Reasoning == nil {
			return true
		}
	}
	return false
}
