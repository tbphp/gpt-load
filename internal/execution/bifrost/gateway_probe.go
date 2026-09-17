package bifrost

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

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
			validGatewayProbeTypedItems(response.Output)
	case protocol.Anthropic:
		return response.Type == "message" && response.Content != nil && validGatewayProbeTypedItems(response.Content)
	case protocol.Gemini:
		return len(response.Candidates) > 0 && validGatewayProbeCandidates(response.Candidates)
	default:
		return false
	}
}

// 仅校验协议结构，不要求生成文本非空，保留低输出预算下的合法响应。
func validGatewayProbeTypedItems(items []json.RawMessage) bool {
	for _, item := range items {
		var output struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(item, &output) != nil || strings.TrimSpace(output.Type) == "" {
			return false
		}
	}
	return true
}

func validGatewayProbeCandidates(items []json.RawMessage) bool {
	for _, item := range items {
		var candidate struct {
			Content *struct {
				Parts []map[string]json.RawMessage `json:"parts"`
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
				if len(part) == 0 {
					return false
				}
			}
		}
	}
	return true
}
