package bifrost

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/maximhq/bifrost/core/providers/anthropic"
	"github.com/maximhq/bifrost/core/providers/gemini"
	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/contentcoding"
	"gpt-load/internal/protocol"
)

// 原生网关 Probe 复用透传，避开 SDK typed request 在取消时的 Model 读写竞争。
func prepareGatewayProtocolProbe(
	spec execution.AttemptSpec,
	resolved channel.ResolvedTarget,
	provider schemas.ModelProvider,
	directKey schemas.Key,
	secrets []string,
) (preparedAttempt, *execution.AttemptResult) {
	var path string
	var payload map[string]any
	switch spec.ClientProtocol {
	case protocol.OpenAIResponses:
		path = "/v1/responses"
		payload = map[string]any{"model": spec.UpstreamModel, "input": "ping", "max_output_tokens": 16, "store": false, "stream": false}
	case protocol.Anthropic:
		path = "/v1/messages"
		payload = map[string]any{
			"model": spec.UpstreamModel, "max_tokens": 1, "stream": false,
			"messages": []map[string]string{{"role": "user", "content": "ping"}},
		}
	case protocol.Gemini:
		path = "/v1beta/models/" + url.PathEscape(spec.UpstreamModel) + ":generateContent"
		payload = map[string]any{
			"contents":         []any{map[string]any{"role": "user", "parts": []map[string]string{{"text": "ping"}}}},
			"generationConfig": map[string]int{"maxOutputTokens": 1},
		}
	default:
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "unsupported multi-protocol gateway probe protocol")
		return preparedAttempt{}, &failure
	}
	body, err := json.Marshal(payload)
	if err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInternal, "encode gateway protocol probe")
		return preparedAttempt{}, &failure
	}
	baseURL, configured, err := targetBaseURL(resolved.TargetConfig)
	if err != nil || !configured {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid multi-protocol gateway probe target")
		return preparedAttempt{}, &failure
	}
	headers := safePassthroughHeaders(spec.Header)
	headers["Content-Type"] = "application/json"
	return preparedAttempt{
		provider: provider, mode: channel.RouteNative, upstreamProtocol: spec.ClientProtocol,
		clientProtocol: spec.ClientProtocol, directKey: directKey, secrets: secrets,
		passthrough: &schemas.BifrostPassthroughRequest{
			Provider: provider, Model: spec.UpstreamModel, Method: http.MethodPost,
			Path: path, UpstreamURL: baseURL, Body: body, SafeHeaders: headers,
		},
	}, nil
}

func normalizeGatewayProtocolProbeResult(spec execution.AttemptSpec, result *execution.AttemptResult) {
	if result == nil || spec.Operation != execution.OperationProbe || result.Error != nil ||
		result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
		return
	}
	switch spec.ClientProtocol {
	case protocol.OpenAIResponses, protocol.Anthropic, protocol.Gemini:
	default:
		return
	}
	encoding, err := contentcoding.ParseContentEncoding(result.Header.Values("Content-Encoding"))
	var body []byte
	if err == nil {
		body, err = contentcoding.DecodeLimited(encoding, result.Body, execution.UnaryResponseBodyLimit(spec.ClientProtocol))
	}
	if err != nil || !validGatewayProtocolProbeResponse(spec.ClientProtocol, body) {
		*result = startedUnaryFailure(result.StatusCode, result.Header, execution.ErrorKindProvider, "upstream returned an invalid protocol probe response")
		result.Error.OriginHint = execution.ErrorOriginUpstream
		result.UpstreamProtocol = spec.ClientProtocol
	}
}

func validGatewayProtocolProbeResponse(selected protocol.Protocol, body []byte) bool {
	var response struct {
		Object     string            `json:"object"`
		Status     string            `json:"status"`
		Type       string            `json:"type"`
		Output     []json.RawMessage `json:"output"`
		Content    []json.RawMessage `json:"content"`
		Candidates []json.RawMessage `json:"candidates"`
		Error      json.RawMessage   `json:"error"`
	}
	if json.Unmarshal(body, &response) != nil ||
		(len(response.Error) > 0 && !bytes.Equal(bytes.TrimSpace(response.Error), []byte("null"))) {
		return false
	}
	switch selected {
	case protocol.OpenAIResponses:
		return response.Object == "response" && response.Output != nil &&
			(response.Status == "completed" || response.Status == "incomplete") &&
			validGatewayProbeTypedItems(protocol.OpenAIResponses, response.Output)
	case protocol.Anthropic:
		return response.Type == "message" && response.Content != nil && validGatewayProbeTypedItems(protocol.Anthropic, response.Content)
	case protocol.Gemini:
		return len(response.Candidates) > 0 && validGatewayProbeCandidates(response.Candidates)
	default:
		return false
	}
}

// 仅校验协议结构，不要求生成文本非空，保留低输出预算下的合法响应。
func validGatewayProbeTypedItems(selected protocol.Protocol, items []json.RawMessage) bool {
	for _, item := range items {
		fields, err := decodeNativeJSONObject(item)
		if err != nil {
			return false
		}
		if selected == protocol.Anthropic {
			if !validGatewayProbeAnthropicItem(item, fields) {
				return false
			}
		} else if !validGatewayProbeResponsesItem(item, fields) {
			return false
		}
	}
	return true
}

func validGatewayProbeAnthropicItem(item json.RawMessage, fields map[string]json.RawMessage) bool {
	var block anthropic.AnthropicContentBlock
	if json.Unmarshal(item, &block) != nil {
		return false
	}
	switch block.Type {
	case anthropic.AnthropicContentBlockTypeText:
		return block.Text != nil
	case anthropic.AnthropicContentBlockTypeThinking:
		return block.Thinking != nil && block.Signature != nil
	case anthropic.AnthropicContentBlockTypeRedactedThinking:
		return block.Data != nil
	case anthropic.AnthropicContentBlockTypeToolUse, anthropic.AnthropicContentBlockTypeServerToolUse, anthropic.AnthropicContentBlockTypeMCPToolUse:
		_, err := decodeNativeJSONObject(block.Input)
		return err == nil && block.ID != nil && block.Name != nil &&
			(block.Type != anthropic.AnthropicContentBlockTypeMCPToolUse || block.ServerName != nil)
	case anthropic.AnthropicContentBlockTypeToolResult, anthropic.AnthropicContentBlockTypeWebSearchToolResult,
		anthropic.AnthropicContentBlockTypeWebFetchToolResult, anthropic.AnthropicContentBlockTypeCodeExecutionToolResult,
		anthropic.AnthropicContentBlockTypeBashCodeExecutionToolResult, anthropic.AnthropicContentBlockTypeTextEditorCodeExecutionToolResult,
		anthropic.AnthropicContentBlockTypeToolSearchToolResult, anthropic.AnthropicContentBlockTypeMCPToolResult,
		anthropic.AnthropicContentBlockTypeAdvisorToolResult:
		return block.ToolUseID != nil && block.Content != nil
	case anthropic.AnthropicContentBlockTypeCompaction:
		return gatewayProbeStringFields(fields, "content")
	case anthropic.AnthropicContentBlockTypeContainerUpload:
		return block.FileID != nil
	case anthropic.AnthropicContentBlockTypeFallback:
		return block.From != nil && block.To != nil
	default:
		return false
	}
}

func validGatewayProbeResponsesItem(item json.RawMessage, fields map[string]json.RawMessage) bool {
	// 只解析所需投影，不把 SDK 的跨协议中间模型当作原生工具响应合同。
	var output struct {
		Type schemas.ResponsesMessageType `json:"type"`
	}
	if json.Unmarshal(item, &output) != nil {
		return false
	}
	switch output.Type {
	case schemas.ResponsesMessageTypeMessage:
		return validGatewayProbeResponseContent(fields["content"], false)
	case schemas.ResponsesMessageTypeReasoning:
		return validGatewayProbeResponseContent(fields["summary"], true)
	case schemas.ResponsesMessageTypeFunctionCall:
		return gatewayProbeStringFields(fields, "call_id", "name", "arguments")
	case schemas.ResponsesMessageTypeCustomToolCall:
		return gatewayProbeStringFields(fields, "call_id", "name", "input")
	case schemas.ResponsesMessageTypeMCPCall, schemas.ResponsesMessageTypeMCPApprovalRequest:
		return gatewayProbeStringFields(fields, "name", "arguments", "server_label")
	case schemas.ResponsesMessageTypeCompaction:
		return gatewayProbeStringFields(fields, "encrypted_content")
	case schemas.ResponsesMessageTypeImageGenerationCall:
		return gatewayProbeStringFields(fields, "id", "status", "result")
	case schemas.ResponsesMessageTypeFileSearchCall:
		return gatewayProbeStringFields(fields, "id", "status") && gatewayProbeStringArray(fields["queries"])
	case schemas.ResponsesMessageTypeComputerCall, schemas.ResponsesMessageTypeWebSearchCall, schemas.ResponsesMessageTypeWebFetchCall:
		action, err := decodeNativeJSONObject(fields["action"])
		return err == nil && gatewayProbeStringFields(fields, "id", "status") && gatewayProbeStringFields(action, "type") &&
			(output.Type != schemas.ResponsesMessageTypeComputerCall || gatewayProbeStringFields(fields, "call_id") && gatewayProbeArrayFields(fields, "pending_safety_checks"))
	case schemas.ResponsesMessageTypeLocalShellCall:
		action, err := decodeNativeJSONObject(fields["action"])
		return err == nil && gatewayProbeStringFields(fields, "id", "call_id", "status") &&
			gatewayProbeStringFields(action, "type") && gatewayProbeStringArray(action["command"]) && gatewayProbeStringObject(action["env"])
	case schemas.ResponsesMessageTypeCodeInterpreterCall:
		return gatewayProbeStringFields(fields, "id", "status", "container_id") &&
			(gatewayProbeNullField(fields, "code") || gatewayProbeStringFields(fields, "code")) &&
			(gatewayProbeNullField(fields, "outputs") || validGatewayProbeCodeOutputs(fields["outputs"]))
	case schemas.ResponsesMessageTypeMCPListTools:
		return gatewayProbeStringFields(fields, "id", "server_label") && validGatewayProbeMCPTools(fields["tools"])
	case schemas.ResponsesMessageTypeToolSearchCall:
		return gatewayProbeStringFields(fields, "id", "call_id", "status", "execution") &&
			len(fields["arguments"]) > 0 && !gatewayProbeNullField(fields, "arguments")
	case schemas.ResponsesMessageTypeToolSearchOutput:
		return gatewayProbeStringFields(fields, "id", "call_id", "status", "execution") && gatewayProbeArrayFields(fields, "tools")
	case schemas.ResponsesMessageTypeAdvisorCall:
		switch {
		case jsonStringEquals(fields["result_type"], "advisor_result"):
			return gatewayProbeStringFields(fields, "advisor_text")
		case jsonStringEquals(fields["result_type"], "advisor_redacted_result"):
			return gatewayProbeStringFields(fields, "advisor_encrypted_content")
		case jsonStringEquals(fields["result_type"], "advisor_tool_result_error"):
			return gatewayProbeStringFields(fields, "advisor_error_code")
		}
		return false
	default:
		return false
	}
}

func validGatewayProbeResponseContent(raw json.RawMessage, summary bool) bool {
	var blocks []json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil || blocks == nil {
		return false
	}
	for _, rawBlock := range blocks {
		fields, err := decodeNativeJSONObject(rawBlock)
		var block schemas.ResponsesMessageContentBlock
		if err != nil || json.Unmarshal(rawBlock, &block) != nil {
			return false
		}
		if summary {
			if !jsonStringEquals(fields["type"], "summary_text") || !gatewayProbeStringFields(fields, "text") {
				return false
			}
			continue
		}
		var valid bool
		switch block.Type {
		case schemas.ResponsesOutputMessageContentTypeText, schemas.ResponsesOutputMessageContentTypeReasoning:
			valid = block.Text != nil
		case schemas.ResponsesOutputMessageContentTypeRefusal:
			valid = gatewayProbeStringFields(fields, "refusal")
		case schemas.ResponsesOutputMessageContentTypeRenderedContent:
			valid = gatewayProbeStringFields(fields, "rendered_content")
		case schemas.ResponsesOutputMessageContentTypeCompaction:
			valid = gatewayProbeStringFields(fields, "summary")
		case schemas.ResponsesOutputMessageContentTypeFallback:
			valid = gatewayProbeStringFields(fields, "from_model", "to_model")
		}
		if !valid {
			return false
		}
	}
	return true
}

func gatewayProbeStringFields(fields map[string]json.RawMessage, names ...string) bool {
	for _, name := range names {
		var value *string
		if json.Unmarshal(fields[name], &value) != nil || value == nil {
			return false
		}
	}
	return true
}

func gatewayProbeArrayFields(fields map[string]json.RawMessage, names ...string) bool {
	for _, name := range names {
		var value []json.RawMessage
		if json.Unmarshal(fields[name], &value) != nil || value == nil {
			return false
		}
	}
	return true
}

func gatewayProbeStringArray(raw json.RawMessage) bool {
	var values []*string
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return false
	}
	for _, value := range values {
		if value == nil {
			return false
		}
	}
	return true
}

func gatewayProbeStringObject(raw json.RawMessage) bool {
	var values map[string]*string
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return false
	}
	for _, value := range values {
		if value == nil {
			return false
		}
	}
	return true
}

func gatewayProbeNullField(fields map[string]json.RawMessage, name string) bool {
	return bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null"))
}

func validGatewayProbeCodeOutputs(raw json.RawMessage) bool {
	var outputs []json.RawMessage
	if json.Unmarshal(raw, &outputs) != nil || outputs == nil {
		return false
	}
	for _, output := range outputs {
		fields, err := decodeNativeJSONObject(output)
		if err != nil {
			return false
		}
		switch {
		case jsonStringEquals(fields["type"], "logs"):
			if !gatewayProbeStringFields(fields, "logs") {
				return false
			}
		case jsonStringEquals(fields["type"], "image"):
			if !gatewayProbeStringFields(fields, "url") {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func validGatewayProbeMCPTools(raw json.RawMessage) bool {
	var tools []json.RawMessage
	if json.Unmarshal(raw, &tools) != nil || tools == nil {
		return false
	}
	for _, tool := range tools {
		fields, err := decodeNativeJSONObject(tool)
		if err != nil || !gatewayProbeStringFields(fields, "name") ||
			len(fields["input_schema"]) == 0 || gatewayProbeNullField(fields, "input_schema") {
			return false
		}
	}
	return true
}

func validGatewayProbeCandidates(items []json.RawMessage) bool {
	for _, item := range items {
		var candidate struct {
			Content *struct {
				Parts []json.RawMessage `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		}
		if json.Unmarshal(item, &candidate) != nil {
			return false
		}
		hasContent := candidate.Content != nil && candidate.Content.Parts != nil
		if !hasContent && strings.TrimSpace(candidate.FinishReason) == "" {
			return false
		}
		if candidate.Content != nil {
			for _, part := range candidate.Content.Parts {
				if !validGatewayProbeGeminiPart(part) {
					return false
				}
			}
		}
	}
	return true
}

func validGatewayProbeGeminiPart(raw json.RawMessage) bool {
	fields, err := decodeNativeJSONObject(raw)
	var part gemini.Part
	if err != nil || json.Unmarshal(raw, &part) != nil {
		return false
	}
	payloadCount := 0
	for _, name := range []string{"text", "inlineData", "fileData", "functionCall", "functionResponse", "executableCode", "codeExecutionResult", "toolCall", "toolResponse"} {
		if _, exists := fields[name]; !exists {
			continue
		}
		var valid bool
		switch name {
		case "text":
			valid = gatewayProbeStringFields(fields, name)
		case "inlineData":
			valid = part.InlineData != nil && part.InlineData.MIMEType != "" && part.InlineData.Data != ""
		case "fileData":
			valid = part.FileData != nil && part.FileData.FileURI != ""
		case "functionCall":
			valid = part.FunctionCall != nil && part.FunctionCall.Name != ""
			if valid && len(part.FunctionCall.Args) > 0 {
				_, err := decodeNativeJSONObject(part.FunctionCall.Args)
				valid = err == nil
			}
		case "functionResponse":
			valid = part.FunctionResponse != nil && part.FunctionResponse.Name != ""
			if valid {
				_, err := decodeNativeJSONObject(part.FunctionResponse.Response)
				valid = err == nil
			}
		case "executableCode":
			nested, err := decodeNativeJSONObject(fields[name])
			valid = err == nil && gatewayProbeStringFields(nested, "code", "language")
		case "codeExecutionResult":
			valid = part.CodeExecutionResult != nil && part.CodeExecutionResult.Outcome != ""
		case "toolCall":
			valid = part.ToolCall != nil && part.ToolCall.ToolType != "" && part.ToolCall.Args != nil
		case "toolResponse":
			valid = part.ToolResponse != nil && part.ToolResponse.ToolType != "" && part.ToolResponse.Response != nil
		}
		if !valid {
			return false
		}
		payloadCount++
	}
	return payloadCount == 1
}
