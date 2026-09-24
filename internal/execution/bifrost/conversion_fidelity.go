package bifrost

import (
	"context"
	"strings"

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
	var toolConstraintsValid bool
	var needsToolHistoryCheck bool
	prepared, needsToolHistoryCheck, toolConstraintsValid = prepareConvertedToolConstraints(spec.ClientProtocol, providerKind, spec.UpstreamModel, prepared)
	if !toolConstraintsValid {
		failure := notSentConversionFailure(execution.ErrorCodeCriticalSemanticLoss, "conversion cannot preserve requested tools or tool choice")
		return preparedAttempt{}, &failure
	}
	vertexChat := providerKind == channel.ProviderGoogleVertex && vertexUsesChatFallback(spec.UpstreamModel)
	if providerKind == channel.ProviderAnthropic || providerKind == channel.ProviderGemini ||
		providerKind == channel.ProviderAWSBedrock || (providerKind == channel.ProviderGoogleVertex && !vertexChat) {
		preserveResponsesGlobalInstructions(prepared.responsesRequest)
	}
	chatFallback := providerKind == channel.ProviderOpenAICompatible || providerKind == channel.ProviderGroq ||
		providerKind == channel.ProviderDeepSeek || vertexChat
	if chatFallback {
		dropsTools, newlyAllowed := chatFallbackToolCompatibility(prepared.responsesRequest)
		needsToolHistoryCheck = needsToolHistoryCheck || newlyAllowed
		// Compatible 保持 SDK 的尽力转换行为：不支持的工具及选择可被过滤，
		// 不因此拒绝整个请求；普通函数白名单仍由前面的准备阶段适配。
		if dropsTools && providerKind != channel.ProviderOpenAICompatible {
			failure := notSentConversionFailure(execution.ErrorCodeCriticalSemanticLoss, "Chat conversion cannot preserve requested tools or tool choice")
			return preparedAttempt{}, &failure
		}
		// Responses 的 reasoning.effort 会被 Chat Completions 序列化成
		// reasoning_effort。OpenAI Compatible 上游常把它当成大额 reasoning budget，
		// 小输出上限模型会直接 400。兼容目标不转发这个字段。
		if providerKind == channel.ProviderOpenAICompatible {
			dropCompatibleResponsesReasoning(prepared.responsesRequest)
			flattenCompatibleToolOutputs(prepared.responsesRequest)
		}
		if providerKind == channel.ProviderDeepSeek && deepSeekConversionDisablesThinking(prepared.responsesRequest) {
			failure := notSentConversionFailure(execution.ErrorCodeCriticalSemanticLoss, "DeepSeek conversion cannot preserve explicit thinking with this tool choice or history")
			return preparedAttempt{}, &failure
		}
	}
	if needsToolHistoryCheck && !convertedTargetPreservesToolHistory(providerKind, spec.UpstreamModel, prepared.responsesRequest) {
		failure := notSentConversionFailure(execution.ErrorCodeCriticalSemanticLoss, "conversion cannot preserve requested tool history")
		return preparedAttempt{}, &failure
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

func dropCompatibleResponsesReasoning(request *schemas.BifrostResponsesRequest) {
	if request == nil || request.Params == nil {
		return
	}
	request.Params.Reasoning = nil
}

// Markers for tool-output parts a string-only Chat Completions upstream cannot
// carry. They match the wording CPA (CLIProxyAPI) uses for the same case so a
// gateway migration keeps the same model-visible text.
const (
	compatibleToolOutputImageMarker = "[image omitted: unsupported by upstream]"
	compatibleToolOutputFileMarker  = "[file omitted: unsupported by upstream]"
	compatibleToolOutputAudioMarker = "[audio omitted: unsupported by upstream]"
	compatibleToolOutputPartMarker  = "[content omitted: unsupported by upstream]"
)

// flattenCompatibleToolOutputs rewrites array-shaped function_call_output
// content into a single string.
//
// The Responses surface allows a tool result to be a list of content blocks,
// and Bifrost's Responses→Chat conversion carries that list into the chat tool
// message's `content`. Chat Completions types tool content as a string, and
// strict upstreams (OpenCode Go, for one) reject the array with
// "Input should be a valid string". Text-only arrays happen to be coerced by
// some upstreams, but any image/file/audio part in a tool result fails, so the
// whole list is folded into a string here: text is kept, unsupported parts are
// replaced by a marker.
func flattenCompatibleToolOutputs(request *schemas.BifrostResponsesRequest) {
	if request == nil {
		return
	}
	for i := range request.Input {
		item := &request.Input[i]
		if item.Type == nil || *item.Type != schemas.ResponsesMessageTypeFunctionCallOutput {
			continue
		}
		toolMessage := item.ResponsesToolMessage
		if toolMessage == nil || toolMessage.Output == nil {
			continue
		}
		blocks := toolMessage.Output.ResponsesFunctionToolCallOutputBlocks
		if len(blocks) == 0 {
			continue
		}
		flattened := flattenToolOutputBlocks(blocks)
		toolMessage.Output.ResponsesToolCallOutputStr = &flattened
		toolMessage.Output.ResponsesFunctionToolCallOutputBlocks = nil
		// The SDK mirrors `output` into Content when it converts, so keep the
		// already-populated mirror from resurrecting the block array.
		if item.Content != nil && len(item.Content.ContentBlocks) > 0 {
			item.Content.ContentBlocks = nil
			if item.Content.ContentStr == nil {
				item.Content.ContentStr = &flattened
			}
		}
	}
}

func flattenToolOutputBlocks(blocks []schemas.ResponsesMessageContentBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Text != nil && isTextContentBlockType(block.Type) {
			parts = append(parts, *block.Text)
			continue
		}
		parts = append(parts, toolOutputOmittedMarker(block.Type))
	}
	return strings.Join(parts, "\n\n")
}

func isTextContentBlockType(blockType schemas.ResponsesMessageContentBlockType) bool {
	switch blockType {
	case schemas.ResponsesInputMessageContentBlockTypeText,
		schemas.ResponsesOutputMessageContentTypeText,
		schemas.ResponsesOutputMessageContentTypeSummaryText,
		schemas.ResponsesOutputMessageContentTypeReasoning:
		return true
	}
	return false
}

func toolOutputOmittedMarker(blockType schemas.ResponsesMessageContentBlockType) string {
	switch blockType {
	case schemas.ResponsesInputMessageContentBlockTypeImage:
		return compatibleToolOutputImageMarker
	case schemas.ResponsesInputMessageContentBlockTypeFile:
		return compatibleToolOutputFileMarker
	case schemas.ResponsesInputMessageContentBlockTypeAudio:
		return compatibleToolOutputAudioMarker
	}
	return compatibleToolOutputPartMarker
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
