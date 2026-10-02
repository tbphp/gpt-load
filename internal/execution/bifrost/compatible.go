package bifrost

import (
	"strings"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
)

func (r *Runtime) usesCompatibleChat(spec execution.AttemptSpec) bool {
	return spec.RouteMode == execution.RouteConverted && r.providerKind(spec) == channel.ProviderOpenAICompatible &&
		(spec.Operation == execution.OperationChatCompletion || spec.Operation == execution.OperationResponsesCreate)
}

func (r *Runtime) compatibleResponsesRequest(ctx *schemas.BifrostContext, request *schemas.BifrostResponsesRequest) (*schemas.BifrostResponsesResponse, *schemas.BifrostError) {
	chat, aliases, err := prepareCompatibleChatRequest(ctx, request)
	if err != nil {
		return nil, err
	}
	response, err := r.core.ChatCompletionRequest(ctx, chat)
	if err != nil || response == nil {
		return nil, err
	}
	converted := response.ToBifrostResponsesResponse()
	converted.BackfillParams(request)
	providerUtils.RestoreResponsesNamespaceToolCalls(aliases, &schemas.BifrostResponse{ResponsesResponse: converted})
	return converted, nil
}

// 对齐锁定 SDK core/v1.11.0 的 prepareResponsesRequest 中适用于 Compatible 的准备。
// 该渠道没有 SDK 自动缓存注入或 Anthropic 私有计费头元数据；转换入口恢复后可删除此衔接。
func prepareCompatibleChatRequest(ctx *schemas.BifrostContext, request *schemas.BifrostResponsesRequest) (*schemas.BifrostChatRequest, map[string]schemas.NamespaceToolAlias, *schemas.BifrostError) {
	ctx.SetValue(schemas.BifrostContextKeyBaseProviderType, schemas.OpenAI)
	model := schemas.ResolveCanonicalModel(ctx, request.Model)
	if request.Params != nil && (schemas.IsDeepSeekModel(model) || schemas.IsMoonshotModel(model)) {
		rewrite := providerUtils.ComposePatternRewriters(providerUtils.NormalizeRegexNULEscape, providerUtils.StripRegexLookaround)
		if tools, changed := providerUtils.RewriteResponsesToolSchemas(request.Params.Tools, rewrite); changed {
			copyRequest, params := *request, *request.Params
			params.Tools = tools
			copyRequest.Params = &params
			request = &copyRequest
		}
	}
	var err *schemas.BifrostError
	supported := providerUtils.ResponsesNamespaceToolsSupported(ctx, schemas.OpenAI, request.Model)
	if !supported {
		request, err = hoistCompatibleAdditionalTools(request)
		if err != nil {
			return nil, nil, err
		}
	}
	request, err = providerUtils.UnwrapDefaultNamespaceTools(request)
	if err != nil {
		return nil, nil, err
	}
	if !supported {
		request, err = providerUtils.FlattenResponsesNamespaceTools(ctx, request)
		if err != nil {
			return nil, nil, err
		}
	}
	chat := request.ToChatRequest()
	preserveCompatibleToolTurnReasoning(chat.Input)
	return chat, request.NamespaceToolAliases, nil
}

// SDK 的同名准备步骤未导出；在能力判断一致时提升 additional_tools，保持请求隔离。
func hoistCompatibleAdditionalTools(request *schemas.BifrostResponsesRequest) (*schemas.BifrostResponsesRequest, *schemas.BifrostError) {
	var tools []schemas.ResponsesTool
	found := false
	for _, item := range request.Input {
		if item.Type == nil || *item.Type != schemas.ResponsesMessageTypeAdditionalTools {
			continue
		}
		found = true
		var declared []schemas.ResponsesTool
		if err := schemas.Unmarshal(item.AdditionalTools, &declared); err != nil {
			return nil, providerUtils.NewBifrostBadRequestError("invalid tools in additional_tools input item")
		}
		tools = append(tools, declared...)
	}
	if !found {
		return request, nil
	}
	copyRequest := *request
	params := schemas.ResponsesParameters{}
	if request.Params != nil {
		params = *request.Params
	}
	params.Tools = append(append([]schemas.ResponsesTool(nil), params.Tools...), tools...)
	copyRequest.Params = &params
	copyRequest.Input = make([]schemas.ResponsesMessage, 0, len(request.Input))
	for _, item := range request.Input {
		if item.Type == nil || *item.Type != schemas.ResponsesMessageTypeAdditionalTools {
			copyRequest.Input = append(copyRequest.Input, item)
		}
	}
	return &copyRequest, nil
}

// #666：仅修正每次 attempt 新生成的 Chat 请求，不修改共享历史/响应转换。
// 完整文本跨整个助手回合优先；有加密状态时不将摘要当成完整推理。
// SDK 完整修复并通过同套回归后，连同专用 Chat 入口一起移除。
func preserveCompatibleToolTurnReasoning(messages []schemas.ChatMessage) {
	for start := 0; start < len(messages); {
		if messages[start].Role != schemas.ChatMessageRoleAssistant {
			start++
			continue
		}
		end := start
		var reasoning, summaries []string
		hasEncrypted := false
		for end < len(messages) && messages[end].Role == schemas.ChatMessageRoleAssistant {
			assistant := messages[end].ChatAssistantMessage
			end++
			if assistant == nil {
				continue
			}
			if assistant.Reasoning != nil && *assistant.Reasoning != "" {
				reasoning = append(reasoning, *assistant.Reasoning)
			}
			for _, detail := range assistant.ReasoningDetails {
				switch detail.Type {
				case schemas.BifrostReasoningDetailsTypeSummary:
					if detail.Summary != nil && *detail.Summary != "" {
						summaries = append(summaries, *detail.Summary)
					}
				case schemas.BifrostReasoningDetailsTypeEncrypted:
					hasEncrypted = hasEncrypted || detail.Data != nil && *detail.Data != ""
				}
			}
		}
		if len(reasoning) == 0 && !hasEncrypted {
			reasoning = summaries
		}
		if len(reasoning) > 0 {
			text := strings.Join(reasoning, "\n")
			for index := start; index < end; index++ {
				if assistant := messages[index].ChatAssistantMessage; assistant != nil && len(assistant.ToolCalls) > 0 {
					assistant.Reasoning = schemas.Ptr(text)
				}
			}
		}
		start = end
	}
}
