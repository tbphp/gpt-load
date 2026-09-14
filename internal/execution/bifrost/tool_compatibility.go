package bifrost

import (
	"strings"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/protocol"
)

type functionAllowlist struct {
	mode  string
	names map[string]struct{}
}

// 只适配锁定 SDK 的目标转换器无法保留的工具选择；目标能原生表达白名单时保留完整工具列表。
func prepareConvertedToolConstraints(
	clientProtocol protocol.Protocol,
	providerKind channel.ProviderKind,
	model string,
	prepared preparedAttempt,
) (preparedAttempt, bool, bool) {
	needsToolHistoryCheck := false
	if prepared.responsesRequest != nil && prepared.responsesRequest.Params != nil {
		request := prepared.responsesRequest
		fallback := responsesTargetNeedsAllowlistFallback(providerKind, model)
		if fallback || clientProtocol == protocol.Gemini && responsesAllowlistNeedsCanonicalType(request) {
			var allowlist functionAllowlist
			var present, valid bool
			request, allowlist, present, valid = responsesFunctionAllowlist(request)
			if present && !valid {
				return preparedAttempt{}, false, false
			}
			if present && fallback {
				request = applyResponsesFunctionAllowlist(request, allowlist)
				needsToolHistoryCheck = true
			} else if present {
				request = canonicalizeResponsesFunctionAllowlist(request)
			}
		}
		request = normalizeConvertedResponseToolMode(request)
		prepared.responsesRequest = request
	}

	if prepared.request != nil && prepared.request.Params != nil && chatTargetNeedsAllowlistFallback(providerKind, model) {
		request, allowlist, present, valid := chatFunctionAllowlist(prepared.request)
		if present && !valid {
			return preparedAttempt{}, false, false
		}
		if present {
			prepared.request = applyChatFunctionAllowlist(request, allowlist)
		}
	}
	return prepared, needsToolHistoryCheck, true
}

func responsesAllowlistNeedsCanonicalType(request *schemas.BifrostResponsesRequest) bool {
	if request == nil || request.Params == nil || request.Params.ToolChoice == nil ||
		request.Params.ToolChoice.ResponsesToolChoiceStruct == nil {
		return false
	}
	choice := request.Params.ToolChoice.ResponsesToolChoiceStruct
	return choice.Type != schemas.ResponsesToolChoiceTypeAllowedTools && len(choice.Tools) > 0
}

func responsesFunctionAllowlist(
	request *schemas.BifrostResponsesRequest,
) (*schemas.BifrostResponsesRequest, functionAllowlist, bool, bool) {
	choice := request.Params.ToolChoice
	if choice == nil || choice.ResponsesToolChoiceStruct == nil {
		return request, functionAllowlist{}, false, true
	}
	value := choice.ResponsesToolChoiceStruct
	present := value.Type == schemas.ResponsesToolChoiceTypeAllowedTools || len(value.Tools) != 0
	if !present {
		return request, functionAllowlist{}, false, true
	}
	if value.Mode == nil || (*value.Mode != "auto" && *value.Mode != "required") || len(value.Tools) == 0 {
		return request, functionAllowlist{}, true, false
	}

	names := make(map[string]struct{}, len(value.Tools))
	for _, allowed := range value.Tools {
		if allowed.Type != string(schemas.ResponsesToolTypeFunction) || allowed.Name == nil || strings.TrimSpace(*allowed.Name) == "" {
			return request, functionAllowlist{}, true, false
		}
		if _, duplicate := names[*allowed.Name]; duplicate {
			return request, functionAllowlist{}, true, false
		}
		names[*allowed.Name] = struct{}{}
	}
	if !responsesFunctionsResolveUniquely(request.Params.Tools, names) {
		return request, functionAllowlist{}, true, false
	}
	return request, functionAllowlist{mode: *value.Mode, names: names}, true, true
}

func chatFunctionAllowlist(
	request *schemas.BifrostChatRequest,
) (*schemas.BifrostChatRequest, functionAllowlist, bool, bool) {
	choice := request.Params.ToolChoice
	if choice == nil || choice.ChatToolChoiceStruct == nil ||
		choice.ChatToolChoiceStruct.Type != schemas.ChatToolChoiceTypeAllowedTools {
		return request, functionAllowlist{}, false, true
	}
	allowed := choice.ChatToolChoiceStruct.AllowedTools
	if allowed == nil || (allowed.Mode != "auto" && allowed.Mode != "required") || len(allowed.Tools) == 0 {
		return request, functionAllowlist{}, true, false
	}
	names := make(map[string]struct{}, len(allowed.Tools))
	for _, tool := range allowed.Tools {
		name := tool.Function.Name
		if tool.Type != string(schemas.ChatToolTypeFunction) || strings.TrimSpace(name) == "" {
			return request, functionAllowlist{}, true, false
		}
		if _, duplicate := names[name]; duplicate {
			return request, functionAllowlist{}, true, false
		}
		names[name] = struct{}{}
	}
	if !chatFunctionsResolveUniquely(request.Params.Tools, names) {
		return request, functionAllowlist{}, true, false
	}
	return request, functionAllowlist{mode: allowed.Mode, names: names}, true, true
}

func responsesFunctionsResolveUniquely(tools []schemas.ResponsesTool, names map[string]struct{}) bool {
	counts := make(map[string]int, len(names))
	for _, tool := range tools {
		if tool.Type != schemas.ResponsesToolTypeFunction || tool.Name == nil {
			continue
		}
		if _, selected := names[*tool.Name]; selected {
			counts[*tool.Name]++
		}
	}
	for name := range names {
		if counts[name] != 1 {
			return false
		}
	}
	return true
}

func chatFunctionsResolveUniquely(tools []schemas.ChatTool, names map[string]struct{}) bool {
	counts := make(map[string]int, len(names))
	for _, tool := range tools {
		if tool.Type != schemas.ChatToolTypeFunction || tool.Function == nil {
			continue
		}
		if _, selected := names[tool.Function.Name]; selected {
			counts[tool.Function.Name]++
		}
	}
	for name := range names {
		if counts[name] != 1 {
			return false
		}
	}
	return true
}

func applyResponsesFunctionAllowlist(
	request *schemas.BifrostResponsesRequest,
	allowlist functionAllowlist,
) *schemas.BifrostResponsesRequest {
	cloned, params := cloneResponsesRequestParams(request)
	params.Tools = make([]schemas.ResponsesTool, 0, len(allowlist.names))
	for _, tool := range request.Params.Tools {
		if tool.Type == schemas.ResponsesToolTypeFunction && tool.Name != nil {
			if _, selected := allowlist.names[*tool.Name]; selected {
				params.Tools = append(params.Tools, tool)
			}
		}
	}
	params.ToolChoice = &schemas.ResponsesToolChoice{ResponsesToolChoiceStr: schemas.Ptr(allowlist.mode)}
	return cloned
}

func canonicalizeResponsesFunctionAllowlist(request *schemas.BifrostResponsesRequest) *schemas.BifrostResponsesRequest {
	choice := request.Params.ToolChoice.ResponsesToolChoiceStruct
	if choice.Type == schemas.ResponsesToolChoiceTypeAllowedTools {
		return request
	}
	cloned, params := cloneResponsesRequestParams(request)
	choiceCopy := *choice
	choiceCopy.Type = schemas.ResponsesToolChoiceTypeAllowedTools
	params.ToolChoice = &schemas.ResponsesToolChoice{ResponsesToolChoiceStruct: &choiceCopy}
	return cloned
}

func applyChatFunctionAllowlist(
	request *schemas.BifrostChatRequest,
	allowlist functionAllowlist,
) *schemas.BifrostChatRequest {
	cloned := *request
	params := *request.Params
	cloned.Params = &params
	params.Tools = make([]schemas.ChatTool, 0, len(allowlist.names))
	for _, tool := range request.Params.Tools {
		if tool.Type == schemas.ChatToolTypeFunction && tool.Function != nil {
			if _, selected := allowlist.names[tool.Function.Name]; selected {
				params.Tools = append(params.Tools, tool)
			}
		}
	}
	params.ToolChoice = &schemas.ChatToolChoice{ChatToolChoiceStr: schemas.Ptr(allowlist.mode)}
	return &cloned
}

func normalizeConvertedResponseToolMode(request *schemas.BifrostResponsesRequest) *schemas.BifrostResponsesRequest {
	choice := request.Params.ToolChoice
	if choice == nil {
		return request
	}
	mode := ""
	if choice.ResponsesToolChoiceStr != nil {
		mode = *choice.ResponsesToolChoiceStr
	} else if value := choice.ResponsesToolChoiceStruct; value != nil &&
		len(value.Tools) == 0 && value.Name == nil && value.ServerLabel == nil {
		mode = string(value.Type)
		if mode == "" && value.Mode != nil {
			mode = *value.Mode
		}
	}
	if mode == "any" {
		mode = "required"
	}
	if mode != "auto" && mode != "none" && mode != "required" {
		return request
	}
	if choice.ResponsesToolChoiceStr != nil && *choice.ResponsesToolChoiceStr == mode {
		return request
	}
	cloned, params := cloneResponsesRequestParams(request)
	params.ToolChoice = &schemas.ResponsesToolChoice{ResponsesToolChoiceStr: schemas.Ptr(mode)}
	return cloned
}

func cloneResponsesRequestParams(
	request *schemas.BifrostResponsesRequest,
) (*schemas.BifrostResponsesRequest, *schemas.ResponsesParameters) {
	cloned := *request
	params := *request.Params
	cloned.Params = &params
	return &cloned, &params
}

func responsesTargetNeedsAllowlistFallback(providerKind channel.ProviderKind, model string) bool {
	switch providerKind {
	case channel.ProviderOpenAICompatible, channel.ProviderAnthropic, channel.ProviderDeepSeek,
		channel.ProviderGroq, channel.ProviderAWSBedrock:
		return true
	case channel.ProviderGoogleVertex:
		return !vertexUsesGeminiResponses(model)
	default:
		return false
	}
}

func chatTargetNeedsAllowlistFallback(providerKind channel.ProviderKind, model string) bool {
	switch providerKind {
	case channel.ProviderAnthropic, channel.ProviderGemini, channel.ProviderAWSBedrock:
		return true
	case channel.ProviderGoogleVertex:
		return schemas.IsAnthropicModelFamily(nil, model) || vertexUsesGeminiResponses(model)
	default:
		return false
	}
}

func vertexUsesGeminiResponses(model string) bool {
	return schemas.IsGeminiModelFamily(nil, model) || schemas.IsGemmaModelFamily(nil, model) || schemas.IsAllDigitsASCII(model)
}

func targetUsesChatFallback(providerKind channel.ProviderKind, model string) bool {
	return providerKind == channel.ProviderOpenAICompatible || providerKind == channel.ProviderGroq ||
		providerKind == channel.ProviderDeepSeek || providerKind == channel.ProviderGoogleVertex && vertexUsesChatFallback(model)
}

func chatFallbackToolCompatibility(request *schemas.BifrostResponsesRequest) (bool, bool) {
	if request == nil || request.Params == nil {
		return false, false
	}
	converted := (&schemas.BifrostResponsesRequest{Params: request.Params}).ToChatRequest()
	choice := request.Params.ToolChoice
	mode, selectedFunction := responsesToolChoiceMode(choice)
	toolsChanged := !chatFallbackPreservesAllTools(request.Params.Tools, converted.Params.Tools)

	switch mode {
	case "none":
		if len(converted.Params.Tools) == 0 {
			return false, toolsChanged
		}
		return chatToolChoiceMode(converted.Params.ToolChoice) != "none", toolsChanged
	case "function":
		if selectedFunction == "" || !responsesFunctionSelectedUniquely(request.Params.Tools, selectedFunction) ||
			!chatFunctionSelectedUniquely(converted.Params.Tools, selectedFunction) ||
			chatSelectedFunction(converted.Params.ToolChoice) != selectedFunction {
			return true, false
		}
		return false, toolsChanged
	}

	if toolsChanged {
		return true, false
	}
	if choice == nil {
		return converted.Params.ToolChoice != nil, false
	}
	convertedMode := chatToolChoiceMode(converted.Params.ToolChoice)
	if len(request.Params.Tools) == 0 && (mode == "auto" || mode == "none") && converted.Params.ToolChoice == nil {
		return false, false
	}
	return convertedMode != mode, false
}

func chatFallbackPreservesAllTools(source []schemas.ResponsesTool, target []schemas.ChatTool) bool {
	if len(source) != len(target) {
		return false
	}
	for index, tool := range source {
		if tool.Type != schemas.ResponsesToolTypeFunction || tool.Name == nil || target[index].Function == nil ||
			target[index].Type != schemas.ChatToolTypeFunction || target[index].Function.Name != *tool.Name {
			return false
		}
	}
	return true
}

func responsesToolChoiceMode(choice *schemas.ResponsesToolChoice) (string, string) {
	if choice == nil {
		return "", ""
	}
	if choice.ResponsesToolChoiceStr != nil {
		return *choice.ResponsesToolChoiceStr, ""
	}
	value := choice.ResponsesToolChoiceStruct
	if value == nil {
		return "", ""
	}
	if value.Type == schemas.ResponsesToolChoiceTypeFunction && value.Name != nil {
		return "function", *value.Name
	}
	return string(value.Type), ""
}

func chatToolChoiceMode(choice *schemas.ChatToolChoice) string {
	if choice == nil {
		return ""
	}
	if choice.ChatToolChoiceStr != nil {
		return *choice.ChatToolChoiceStr
	}
	if choice.ChatToolChoiceStruct != nil {
		return string(choice.ChatToolChoiceStruct.Type)
	}
	return ""
}

func chatSelectedFunction(choice *schemas.ChatToolChoice) string {
	if choice == nil || choice.ChatToolChoiceStruct == nil ||
		choice.ChatToolChoiceStruct.Type != schemas.ChatToolChoiceTypeFunction || choice.ChatToolChoiceStruct.Function == nil {
		return ""
	}
	return choice.ChatToolChoiceStruct.Function.Name
}

func responsesFunctionSelectedUniquely(tools []schemas.ResponsesTool, name string) bool {
	count := 0
	for _, tool := range tools {
		if tool.Type == schemas.ResponsesToolTypeFunction && tool.Name != nil && *tool.Name == name {
			count++
		}
	}
	return count == 1
}

func chatFunctionSelectedUniquely(tools []schemas.ChatTool, name string) bool {
	count := 0
	for _, tool := range tools {
		if tool.Type == schemas.ChatToolTypeFunction && tool.Function != nil && tool.Function.Name == name {
			count++
		}
	}
	return count == 1
}

func chatFallbackPreservesToolHistory(request *schemas.BifrostResponsesRequest) bool {
	if request == nil {
		return true
	}
	for _, message := range request.Input {
		if message.Type == nil {
			continue
		}
		switch *message.Type {
		case schemas.ResponsesMessageTypeMessage,
			schemas.ResponsesMessageTypeReasoning,
			schemas.ResponsesMessageTypeFunctionCall,
			schemas.ResponsesMessageTypeFunctionCallOutput,
			schemas.ResponsesMessageTypeItemReference,
			schemas.ResponsesMessageTypeRefusal:
			continue
		case schemas.ResponsesMessageTypeFileSearchCall,
			schemas.ResponsesMessageTypeComputerCall,
			schemas.ResponsesMessageTypeComputerCallOutput,
			schemas.ResponsesMessageTypeWebSearchCall,
			schemas.ResponsesMessageTypeWebFetchCall,
			schemas.ResponsesMessageTypeToolSearchCall,
			schemas.ResponsesMessageTypeToolSearchOutput,
			schemas.ResponsesMessageTypeCodeInterpreterCall,
			schemas.ResponsesMessageTypeLocalShellCall,
			schemas.ResponsesMessageTypeLocalShellCallOutput,
			schemas.ResponsesMessageTypeMCPCall,
			schemas.ResponsesMessageTypeCustomToolCall,
			schemas.ResponsesMessageTypeCustomToolCallOutput,
			schemas.ResponsesMessageTypeImageGenerationCall,
			schemas.ResponsesMessageTypeMCPListTools,
			schemas.ResponsesMessageTypeMCPApprovalRequest,
			schemas.ResponsesMessageTypeMCPApprovalResponses,
			schemas.ResponsesMessageTypeAdditionalTools,
			schemas.ResponsesMessageTypeAdvisorCall:
			return false
		default:
			return false
		}
	}
	return true
}

func convertedTargetPreservesToolHistory(
	providerKind channel.ProviderKind,
	model string,
	request *schemas.BifrostResponsesRequest,
) bool {
	if targetUsesChatFallback(providerKind, model) {
		return chatFallbackPreservesToolHistory(request)
	}
	if providerKind == channel.ProviderAnthropic ||
		providerKind == channel.ProviderGoogleVertex && schemas.IsAnthropicModelFamily(nil, model) {
		return responsesHistorySupportedBy(request, channel.ProviderAnthropic)
	}
	if providerKind == channel.ProviderAWSBedrock {
		return responsesHistorySupportedBy(request, channel.ProviderAWSBedrock)
	}
	return true
}

func responsesHistorySupportedBy(
	request *schemas.BifrostResponsesRequest,
	providerKind channel.ProviderKind,
) bool {
	if request == nil {
		return true
	}
	for _, message := range request.Input {
		if message.Type == nil {
			continue
		}
		switch *message.Type {
		case schemas.ResponsesMessageTypeMessage,
			schemas.ResponsesMessageTypeReasoning,
			schemas.ResponsesMessageTypeFunctionCall,
			schemas.ResponsesMessageTypeFunctionCallOutput,
			schemas.ResponsesMessageTypeItemReference,
			schemas.ResponsesMessageTypeRefusal:
			continue
		}
		supported := false
		switch providerKind {
		case channel.ProviderAnthropic:
			switch *message.Type {
			case schemas.ResponsesMessageTypeComputerCall,
				schemas.ResponsesMessageTypeComputerCallOutput,
				schemas.ResponsesMessageTypeWebSearchCall,
				schemas.ResponsesMessageTypeWebFetchCall,
				schemas.ResponsesMessageTypeToolSearchCall,
				schemas.ResponsesMessageTypeCodeInterpreterCall,
				schemas.ResponsesMessageTypeMCPCall,
				schemas.ResponsesMessageTypeMCPApprovalRequest,
				schemas.ResponsesMessageTypeAdvisorCall:
				supported = true
			}
		case channel.ProviderAWSBedrock:
			supported = *message.Type == schemas.ResponsesMessageTypeWebSearchCall ||
				*message.Type == schemas.ResponsesMessageTypeCodeInterpreterCall
		}
		if !supported {
			return false
		}
	}
	return true
}
