package cpa

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func prepareConvertedFidelity(spec execution.AttemptSpec, providerKind channel.ProviderKind) (execution.AttemptSpec, *execution.ErrorEvidence) {
	if spec.RouteMode != execution.RouteConverted ||
		(spec.Operation != execution.OperationChatCompletion && spec.Operation != execution.OperationResponsesCreate &&
			!countTokensOperation(spec.Operation)) {
		return spec, nil
	}
	var toolFailure *execution.ErrorEvidence
	spec, toolFailure = prepareConvertedToolConstraints(spec, providerKind)
	if toolFailure != nil {
		return spec, toolFailure
	}
	if (providerKind == channel.ProviderCodex || providerKind == channel.ProviderGrok) && spec.ClientProtocol == protocol.Anthropic {
		// 两个渠道共用 Codex 转换器；先映射角色，避免 CPA 将 system 降为 user 提醒并移动位置。
		for index, message := range gjson.GetBytes(spec.Body, "messages").Array() {
			if message.Get("role").String() != "system" {
				continue
			}
			body, err := sjson.SetBytes(spec.Body, "messages."+strconv.Itoa(index)+".role", "developer")
			if err != nil {
				return spec, notSentEvidence(execution.ErrorKindInvalidRequest,
					"cannot preserve subscription system instructions", "invalid_subscription_instructions")
			}
			spec.Body = body
		}
		return spec, nil
	}
	if countTokensOperation(spec.Operation) ||
		dialect.CountMidConversationSystemMessages(spec.ClientProtocol, spec.Body) == 0 {
		return spec, nil
	}
	lossy := false
	switch providerKind {
	case channel.ProviderClaude, channel.ProviderAntigravity:
		lossy = true
	}
	if !lossy {
		return spec, nil
	}
	return spec, notSentEvidence(execution.ErrorKindConversionUnsupported,
		"conversion cannot preserve mid-conversation system instructions", execution.ErrorCodeCriticalSemanticLoss)
}

func prepareConvertedToolConstraints(
	spec execution.AttemptSpec,
	providerKind channel.ProviderKind,
) (execution.AttemptSpec, *execution.ErrorEvidence) {
	if spec.RouteMode != execution.RouteConverted ||
		(spec.Operation != execution.OperationChatCompletion && spec.Operation != execution.OperationResponsesCreate &&
			!countTokensOperation(spec.Operation)) {
		return spec, nil
	}
	switch providerKind {
	case channel.ProviderClaude, channel.ProviderAntigravity, channel.ProviderCodex, channel.ProviderGrok:
	default:
		return spec, nil
	}

	var body []byte
	var present, valid bool
	switch spec.ClientProtocol {
	case protocol.OpenAIResponses:
		// Codex/Grok 原生接收 Responses；保留结构化白名单和完整工具定义，避免破坏缓存形态。
		if providerKind == channel.ProviderCodex || providerKind == channel.ProviderGrok {
			return spec, nil
		}
		body, present, valid = prepareResponsesFunctionAllowlist(spec.Body)
	case protocol.OpenAICompletions:
		body, present, valid = prepareChatFunctionAllowlist(spec.Body)
	case protocol.Gemini:
		body, present, valid = prepareGeminiFunctionAllowlist(spec.Body, providerKind)
	default:
		return spec, nil
	}
	if !present {
		return spec, nil
	}
	if !valid || spec.ClientProtocol == protocol.OpenAIResponses && !subscriptionResponsesToolHistorySupported(spec.Body, providerKind) {
		return spec, notSentEvidence(
			execution.ErrorKindConversionUnsupported,
			"subscription conversion cannot preserve requested tools or tool choice",
			execution.ErrorCodeCriticalSemanticLoss,
		)
	}
	if providerKind == channel.ProviderAntigravity && strings.Contains(spec.UpstreamModel, "claude") &&
		gjson.GetBytes(body, "tool_choice").String() == "required" {
		return spec, notSentEvidence(
			execution.ErrorKindConversionUnsupported,
			"subscription conversion cannot preserve requested tools or tool choice",
			execution.ErrorCodeCriticalSemanticLoss,
		)
	}
	spec.Body = body
	return spec, nil
}

func prepareResponsesFunctionAllowlist(body []byte) ([]byte, bool, bool) {
	choice := gjson.GetBytes(body, "tool_choice")
	if !choice.IsObject() || choice.Get("type").String() != "allowed_tools" {
		return body, false, true
	}
	mode := choice.Get("mode").String()
	allowed := choice.Get("tools")
	if (mode != "auto" && mode != "required") || !allowed.IsArray() || len(allowed.Array()) == 0 {
		return body, true, false
	}
	names, valid := responseAllowedFunctionNames(allowed)
	if !valid {
		return body, true, false
	}
	filtered, valid := filterRawFunctionTools(gjson.GetBytes(body, "tools"), names, "name")
	if !valid {
		return body, true, false
	}
	prepared, err := setRawJSONArray(body, "tools", filtered)
	if err != nil {
		return body, true, false
	}
	prepared, err = sjson.SetBytes(prepared, "tool_choice", mode)
	return prepared, true, err == nil
}

func prepareChatFunctionAllowlist(body []byte) ([]byte, bool, bool) {
	choice := gjson.GetBytes(body, "tool_choice")
	if !choice.IsObject() || choice.Get("type").String() != "allowed_tools" {
		return body, false, true
	}
	allowed := choice.Get("allowed_tools")
	mode := allowed.Get("mode").String()
	tools := allowed.Get("tools")
	if !allowed.IsObject() || (mode != "auto" && mode != "required") || !tools.IsArray() || len(tools.Array()) == 0 {
		return body, true, false
	}
	names := make(map[string]struct{}, len(tools.Array()))
	for _, tool := range tools.Array() {
		name := tool.Get("function.name").String()
		if !tool.IsObject() || tool.Get("type").String() != "function" || strings.TrimSpace(name) == "" {
			return body, true, false
		}
		if _, duplicate := names[name]; duplicate {
			return body, true, false
		}
		names[name] = struct{}{}
	}
	filtered, valid := filterRawFunctionTools(gjson.GetBytes(body, "tools"), names, "function.name")
	if !valid {
		return body, true, false
	}
	prepared, err := setRawJSONArray(body, "tools", filtered)
	if err != nil {
		return body, true, false
	}
	prepared, err = sjson.SetBytes(prepared, "tool_choice", mode)
	return prepared, true, err == nil
}

func prepareGeminiFunctionAllowlist(body []byte, providerKind channel.ProviderKind) ([]byte, bool, bool) {
	config := gjson.GetBytes(body, "toolConfig.functionCallingConfig")
	if providerKind == channel.ProviderClaude && gjson.GetBytes(body, "tool_config").Exists() {
		config = gjson.GetBytes(body, "tool_config.function_calling_config")
	}
	allowed := config.Get("allowedFunctionNames")
	if providerKind == channel.ProviderClaude && !allowed.Exists() {
		allowed = config.Get("allowed_function_names")
	}
	if !allowed.Exists() || !allowed.IsArray() || len(allowed.Array()) == 0 {
		return body, false, true
	}
	mode := config.Get("mode").String()
	if mode != "AUTO" && mode != "ANY" {
		return body, true, false
	}
	names := make(map[string]struct{}, len(allowed.Array()))
	for _, value := range allowed.Array() {
		name := value.String()
		if value.Type != gjson.String || strings.TrimSpace(name) == "" {
			return body, true, false
		}
		if _, duplicate := names[name]; duplicate {
			return body, true, false
		}
		names[name] = struct{}{}
	}

	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return body, true, false
	}
	counts := make(map[string]int, len(names))
	filteredGroups := make([]json.RawMessage, 0, len(tools.Array()))
	for _, group := range tools.Array() {
		if !group.IsObject() {
			return body, true, false
		}
		declarations := group.Get("functionDeclarations")
		if !declarations.Exists() {
			filteredGroups = append(filteredGroups, json.RawMessage(group.Raw))
			continue
		}
		if !declarations.IsArray() {
			return body, true, false
		}
		selected := make([]json.RawMessage, 0, len(declarations.Array()))
		for _, declaration := range declarations.Array() {
			name := declaration.Get("name").String()
			if _, keep := names[name]; keep {
				counts[name]++
				selected = append(selected, json.RawMessage(declaration.Raw))
			}
		}
		if len(selected) == 0 {
			updated, err := sjson.DeleteBytes([]byte(group.Raw), "functionDeclarations")
			if err != nil {
				return body, true, false
			}
			if objectHasFields(updated) {
				filteredGroups = append(filteredGroups, json.RawMessage(updated))
			}
			continue
		}
		updated, err := setRawJSONArray([]byte(group.Raw), "functionDeclarations", selected)
		if err != nil {
			return body, true, false
		}
		filteredGroups = append(filteredGroups, json.RawMessage(updated))
	}
	for name := range names {
		if counts[name] != 1 {
			return body, true, false
		}
	}
	prepared, err := setRawJSONArray(body, "tools", filteredGroups)
	return prepared, true, err == nil
}

func responseAllowedFunctionNames(allowed gjson.Result) (map[string]struct{}, bool) {
	names := make(map[string]struct{}, len(allowed.Array()))
	for _, tool := range allowed.Array() {
		name := tool.Get("name").String()
		if !tool.IsObject() || tool.Get("type").String() != "function" || strings.TrimSpace(name) == "" || tool.Get("server_label").Exists() {
			return nil, false
		}
		if _, duplicate := names[name]; duplicate {
			return nil, false
		}
		names[name] = struct{}{}
	}
	return names, true
}

func filterRawFunctionTools(tools gjson.Result, names map[string]struct{}, namePath string) ([]json.RawMessage, bool) {
	if !tools.IsArray() {
		return nil, false
	}
	counts := make(map[string]int, len(names))
	filtered := make([]json.RawMessage, 0, len(names))
	for _, tool := range tools.Array() {
		if !tool.IsObject() || tool.Get("type").String() != "function" {
			continue
		}
		name := tool.Get(namePath).String()
		if _, keep := names[name]; keep {
			counts[name]++
			filtered = append(filtered, json.RawMessage(tool.Raw))
		}
	}
	for name := range names {
		if counts[name] != 1 {
			return nil, false
		}
	}
	return filtered, true
}

func setRawJSONArray(body []byte, path string, values []json.RawMessage) ([]byte, error) {
	var array bytes.Buffer
	array.WriteByte('[')
	for index, value := range values {
		if index > 0 {
			array.WriteByte(',')
		}
		array.Write(value)
	}
	array.WriteByte(']')
	return sjson.SetRawBytes(body, path, array.Bytes())
}

func objectHasFields(body []byte) bool {
	hasFields := false
	gjson.ParseBytes(body).ForEach(func(_, _ gjson.Result) bool {
		hasFields = true
		return false
	})
	return hasFields
}

func subscriptionResponsesToolHistorySupported(body []byte, providerKind channel.ProviderKind) bool {
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return true
	}
	for _, item := range input.Array() {
		kind := item.Get("type").String()
		switch kind {
		case "", "message", "reasoning", "function_call", "function_call_output", "refusal":
			continue
		case "custom_tool_call", "custom_tool_call_output":
			if providerKind == channel.ProviderClaude || providerKind == channel.ProviderAntigravity {
				continue
			}
			return false
		case "web_search_call":
			if providerKind == channel.ProviderClaude {
				continue
			}
			return false
		case "file_search_call", "computer_call", "computer_call_output", "web_fetch_call",
			"tool_search_call", "tool_search_output", "code_interpreter_call", "local_shell_call", "local_shell_call_output",
			"mcp_call", "image_generation_call", "mcp_list_tools",
			"mcp_approval_request", "mcp_approval_responses", "additional_tools", "advisor_call":
			return false
		default:
			return false
		}
	}
	return true
}
