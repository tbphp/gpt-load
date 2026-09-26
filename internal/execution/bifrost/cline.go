package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/maximhq/bifrost/core/providers/openai"
	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/contentcoding"
	"gpt-load/internal/platform/httpheader"
	"gpt-load/internal/protocol"
)

func (r *Runtime) prepareCline(
	spec execution.AttemptSpec,
	provider schemas.ModelProvider,
	key schemas.Key,
	secrets []string,
	stream bool,
	rawQuery string,
) (preparedAttempt, *execution.AttemptResult) {
	prepared := preparedAttempt{
		provider: provider, mode: channel.RouteMode(spec.RouteMode),
		upstreamProtocol: protocol.OpenAICompletions, clientProtocol: spec.ClientProtocol,
		directKey: key, secrets: secrets,
	}
	if spec.Operation == execution.OperationListModels {
		target, err := resolveTypedTargetURL(r.fixedConfig.targetBaseURL, "/models", rawQuery)
		if err != nil {
			failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid Cline model-list target")
			return preparedAttempt{}, &failure
		}
		prepared.listModelsRequest = &schemas.BifrostListModelsRequest{Provider: provider}
		prepared.typedURL = target
		return prepared, nil
	}
	if spec.Operation == execution.OperationProbe {
		spec.ClientProtocol = protocol.OpenAICompletions
		spec.Operation = execution.OperationChatCompletion
		spec.Body = []byte(`{"messages":[{"role":"user","content":"ping"}],"max_tokens":1}`)
	}
	if spec.ClientProtocol != protocol.OpenAICompletions {
		request, err := buildConvertedResponsesRequest(spec, provider)
		if err != nil {
			var classified interface{ ConversionCode() string }
			if errors.As(err, &classified) {
				failure := notSentConversionFailure(classified.ConversionCode(), err.Error())
				return preparedAttempt{}, &failure
			}
			failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, err.Error())
			return preparedAttempt{}, &failure
		}
		prepared.responsesRequest = request
		var failure *execution.AttemptResult
		prepared, failure = finishConvertedPreparation(spec, channel.ProviderCline, prepared)
		if failure != nil {
			return preparedAttempt{}, failure
		}
		body, err := clineConvertedRequest(prepared.responsesRequest)
		if err != nil {
			failure := notSentConversionFailure(execution.ErrorCodeCriticalSemanticLoss, "cannot preserve Cline Chat request")
			return preparedAttempt{}, &failure
		}
		spec.Body = body
		spec.ClientProtocol = protocol.OpenAICompletions
		spec.Operation = execution.OperationChatCompletion
	}
	body, headers, err := sanitizeNativePassthroughRequest(spec, stream)
	if err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid Cline request")
		return preparedAttempt{}, &failure
	}
	object, err := decodeNativeJSONObject(body)
	if err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid Cline request")
		return preparedAttempt{}, &failure
	}
	// Cline 默认 stream=true；必须显式传递客户端选择的模式。
	object["stream"] = json.RawMessage("false")
	if stream {
		object["stream"] = json.RawMessage("true")
	}
	if clineUsesMaxCompletionTokens(spec.UpstreamModel) && object["max_tokens"] != nil {
		if object["max_completion_tokens"] == nil {
			object["max_completion_tokens"] = object["max_tokens"]
		}
		delete(object, "max_tokens")
	}
	body, err = encodeNativeJSONObject(object)
	if err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "encode Cline request")
		return preparedAttempt{}, &failure
	}
	safeHeaders := safePassthroughHeaders(headers)
	safeHeaders["Content-Type"] = "application/json"
	safeHeaders["Accept-Encoding"] = "identity"
	prepared.cline = true
	prepared.passthrough = &schemas.BifrostPassthroughRequest{
		Provider: provider, Model: spec.UpstreamModel,
		Method: http.MethodPost, Path: "/chat/completions", RawQuery: rawQuery,
		UpstreamURL: r.fixedConfig.targetBaseURL, Body: body, SafeHeaders: safeHeaders,
	}
	return prepared, nil
}

func (r *Runtime) executeCline(parent context.Context, spec execution.AttemptSpec, prepared preparedAttempt) execution.AttemptResult {
	wireSpec := spec
	wireSpec.ClientProtocol = protocol.OpenAICompletions
	wireSpec.ClientModel = spec.UpstreamModel
	result := r.executePassthrough(parent, wireSpec, prepared)
	if prepared.mode == channel.RouteConverted {
		result.AppliedReasoning = inspectWireAppliedReasoning(prepared.passthrough.Body)
	}
	if !result.ResponseStarted {
		return result
	}
	if len(result.Body) == 0 {
		if result.StatusCode >= http.StatusOK && result.StatusCode < http.StatusMultipleChoices {
			return invalidClineResponse(result.Header)
		}
		return result
	}
	encoding, err := contentcoding.ParseContentEncoding(result.Header.Values("Content-Encoding"))
	if err != nil {
		return invalidClineResponse(result.Header)
	}
	body, err := contentcoding.DecodeLimited(encoding, result.Body, r.unaryResponseBodyLimit(spec))
	if err != nil {
		return invalidClineResponse(result.Header)
	}
	status, body, err := normalizeClineResponse(result.StatusCode, body)
	if err != nil {
		return invalidClineResponse(result.Header)
	}
	result.StatusCode = status
	result.Body = body
	result.Header = result.Header.Clone()
	httpheader.StripRepresentationMetadata(result.Header)
	result.Header.Del("Content-Encoding")
	result.Header.Set("Content-Type", "application/json")
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		result.Body = redactSecrets(body, prepared.secrets)
		result.Error = passthroughHTTPError(status, result.Header, result.Body, prepared.secrets)
		return result
	}
	result.Model = openAIResponseModel(body, spec.UpstreamModel)
	result.Usage, err = usageEvidenceFromPassthrough(openai.ExtractOpenAIPassthroughUsage(
		http.MethodPost, openAIChatPath, prepared.passthrough.Body, body,
	))
	if err != nil {
		return invalidClineResponse(result.Header)
	}
	if prepared.mode == channel.RouteConverted && spec.Operation != execution.OperationProbe {
		var chat schemas.BifrostChatResponse
		if err := json.Unmarshal(body, &chat); err != nil {
			return invalidClineResponse(result.Header)
		}
		ctx := schemas.NewBifrostContext(parent, schemas.NoDeadline)
		defer ctx.Cancel()
		response := chat.ToBifrostResponsesResponse()
		response.Store = schemas.Ptr(false)
		body, _, err = encodeConvertedResponsesResponse(spec.ClientProtocol, ctx, response)
		if err != nil {
			return invalidClineResponse(result.Header)
		}
		result.Body = body
	}
	if needsClientModelAlias(spec) {
		result.Body, err = rewriteClientResponseModel(spec.ClientProtocol, body, spec.ClientModel)
		if err != nil {
			return invalidClineResponse(result.Header)
		}
	}
	return result
}

func clineUsesMaxCompletionTokens(model string) bool {
	name, ok := strings.CutPrefix(strings.ToLower(model), "openai/")
	if !ok {
		return false
	}
	for _, family := range []string{"gpt-5", "o1", "o3", "o4"} {
		if name == family || strings.HasPrefix(name, family+"-") || strings.HasPrefix(name, family+".") {
			return true
		}
	}
	return false
}

func clineConvertedRequest(request *schemas.BifrostResponsesRequest) ([]byte, error) {
	chat := request.ToChatRequest()
	preserveClineToolTurnReasoning(chat.Input)
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	defer ctx.Cancel()
	ctx.SetValue(schemas.BifrostContextKeyIsCustomProvider, true)
	wire := openai.ToOpenAIChatRequest(ctx, chat)
	if wire == nil {
		return nil, fmt.Errorf("missing Chat request")
	}
	// Cline 是聚合网关，不能套用直连 DeepSeek 的禁用思考或字段删除策略。
	if !clineUsesMaxCompletionTokens(request.Model) {
		wire.MaxTokens = wire.MaxCompletionTokens
		wire.MaxCompletionTokens = nil
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	object, err := decodeNativeJSONObject(body)
	if err != nil {
		return nil, err
	}
	if request.Params != nil && request.Params.Reasoning != nil {
		reasoning := request.Params.Reasoning
		value := map[string]any{}
		if reasoning.Effort != nil {
			value["effort"] = *reasoning.Effort
		}
		if reasoning.MaxTokens != nil {
			value["max_tokens"] = *reasoning.MaxTokens
		}
		if len(value) > 0 {
			object["reasoning"], err = json.Marshal(value)
			if err != nil {
				return nil, err
			}
			delete(object, "reasoning_effort")
		}
	}
	return encodeNativeJSONObject(object)
}

// 仅把同一助手回合已有的完整思考附到工具调用上，不从摘要或密文补造原文。
func preserveClineToolTurnReasoning(messages []schemas.ChatMessage) {
	for start := 0; start < len(messages); {
		if messages[start].Role != schemas.ChatMessageRoleAssistant {
			start++
			continue
		}
		end := start
		var reasoning []string
		for end < len(messages) && messages[end].Role == schemas.ChatMessageRoleAssistant {
			if assistant := messages[end].ChatAssistantMessage; assistant != nil && assistant.Reasoning != nil && *assistant.Reasoning != "" {
				reasoning = append(reasoning, *assistant.Reasoning)
			}
			end++
		}
		if text := strings.Join(reasoning, "\n"); text != "" {
			for index := start; index < end; index++ {
				assistant := messages[index].ChatAssistantMessage
				if assistant != nil && len(assistant.ToolCalls) > 0 && assistant.Reasoning == nil {
					assistant.Reasoning = schemas.Ptr(text)
				}
			}
		}
		start = end
	}
}

// normalizeClineResponse 同时接受文档中的标准响应与线上历史包装，不重建完成对象。
func normalizeClineResponse(status int, body []byte) (int, []byte, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil || object == nil {
		if status < http.StatusOK || status >= http.StatusMultipleChoices {
			normalized, err := normalizeClineError(nil)
			return status, normalized, err
		}
		return status, nil, fmt.Errorf("invalid Cline response object")
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices ||
		bytes.Equal(bytes.TrimSpace(object["success"]), []byte("false")) || clineErrorPresent(object["error"]) {
		if status >= http.StatusOK && status < http.StatusMultipleChoices {
			status = http.StatusBadGateway
		}
		normalized, err := normalizeClineError(object)
		return status, normalized, err
	}
	if bytes.Equal(bytes.TrimSpace(object["success"]), []byte("true")) && object["choices"] == nil {
		body = object["data"]
	}
	var completion struct {
		ID      string `json:"id"`
		Choices []struct {
			Message map[string]json.RawMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &completion); err != nil || completion.ID == "" || len(completion.Choices) == 0 {
		return status, nil, fmt.Errorf("invalid Cline completion")
	}
	for _, choice := range completion.Choices {
		if choice.Message == nil {
			return status, nil, fmt.Errorf("invalid Cline completion message")
		}
	}
	return status, bytes.Clone(body), nil
}

func clineErrorPresent(value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) != 0 && !bytes.Equal(trimmed, []byte("null"))
}

func normalizeClineError(object map[string]json.RawMessage) ([]byte, error) {
	var message string
	var detail map[string]json.RawMessage
	if json.Unmarshal(object["error"], &detail) == nil && detail != nil {
		return json.Marshal(map[string]json.RawMessage{"error": object["error"]})
	}
	if json.Unmarshal(object["error"], &message) != nil || message == "" {
		if json.Unmarshal(object["message"], &message) != nil || message == "" {
			message = "Cline request failed."
		}
	}
	detail = map[string]json.RawMessage{"type": json.RawMessage(`"upstream_error"`)}
	encodedMessage, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}
	detail["message"] = encodedMessage
	if code := object["code"]; clineErrorPresent(code) {
		detail["code"] = code
	}
	return json.Marshal(map[string]any{"error": detail})
}

func invalidClineResponse(headers http.Header) execution.AttemptResult {
	result := startedUnaryFailure(http.StatusBadGateway, headers, execution.ErrorKindProvider, "Cline returned an invalid Chat Completions response")
	result.Error.Code = "invalid_upstream_response"
	result.Error.OriginHint = execution.ErrorOriginUpstream
	result.Error.ScopeHint = execution.ErrorScopeGroup
	return result
}
