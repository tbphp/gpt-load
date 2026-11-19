package bifrost

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/contentcoding"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

func prepareDecisions(
	spec execution.AttemptSpec,
	resolved channel.ResolvedTarget,
	provider schemas.ModelProvider,
	directKey schemas.Key,
	secrets []string,
) (preparedAttempt, *execution.AttemptResult) {
	clientPath, wire := decisionsWire(spec.ClientProtocol)
	request := &dialect.ParsedRequest{
		Method: http.MethodPost,
		Path:   clientPath,
		Header: spec.Header.Clone(),
		Body:   spec.Body,
	}
	if spec.Operation == execution.OperationListModels {
		if resolved.ProviderKind != channel.ProviderJev {
			failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "unsupported decisions model-list provider")
			return preparedAttempt{}, &failure
		}
		request.Method = http.MethodGet
		request.Path = "/v1/models"
		request.Body = nil
	} else if spec.Operation == execution.OperationProbe {
		body, err := json.Marshal(decisionsProbeBody(spec.ClientProtocol, spec.UpstreamModel))
		if err != nil {
			failure := notSentUnaryFailure(execution.ErrorKindInternal, "encode decisions probe")
			return preparedAttempt{}, &failure
		}
		request.Body = body
	}
	if spec.Operation != execution.OperationListModels {
		var err error
		request, err = wire.RewriteRequestModel(request, spec.UpstreamModel)
		if err != nil {
			failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid decisions request body")
			failure.Error.OriginHint = execution.ErrorOriginClient
			failure.Error.ScopeHint = execution.ErrorScopeRequest
			return preparedAttempt{}, &failure
		}
	}
	baseURL, path, err := decisionsTarget(resolved)
	if err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid decisions target")
		return preparedAttempt{}, &failure
	}
	model := spec.UpstreamModel
	if spec.Operation == execution.OperationListModels {
		path = "/models"
		model = ""
	}
	return preparedAttempt{
		provider: provider, mode: channel.RouteNative, upstreamProtocol: spec.ClientProtocol,
		clientProtocol: spec.ClientProtocol, directKey: directKey, secrets: secrets,
		passthrough: &schemas.BifrostPassthroughRequest{
			Provider: provider, Model: model, Method: request.Method,
			Path: path, UpstreamURL: baseURL, RawQuery: safeAttemptQuery(spec),
			Body: request.Body, SafeHeaders: safePassthroughHeaders(request.Header),
		},
	}, nil
}

// decisionsWireFormat 是执行层需要的决策方言能力：改写请求模型与读取响应用量。
type decisionsWireFormat interface {
	RewriteRequestModel(*dialect.ParsedRequest, string) (*dialect.ParsedRequest, error)
	ExtractUsage([]byte) (usage.Result, error)
}

// decisionsWire 返回决策类协议在客户端侧的入口路径与对应方言。
func decisionsWire(clientProtocol protocol.Protocol) (string, decisionsWireFormat) {
	if clientProtocol == protocol.GeminiDecisions {
		return "/v1beta/decisions", dialect.NewGeminiDecisions()
	}
	return "/v1/systemone", dialect.NewDecisions()
}

// decisionsProbeBody 构造只含一道 noul 题的最小探测请求；Gemini 用 input 字段且 criteria 可省略。
func decisionsProbeBody(clientProtocol protocol.Protocol, model string) map[string]any {
	question := map[string]any{"type": "noul", "instructions": "Is the service ready?"}
	if clientProtocol == protocol.GeminiDecisions {
		return map[string]any{"model": model, "input": "ping", "questions": map[string]any{"ready": question}}
	}
	question["criteria"] = map[string]string{"true": "Ready", "false": "Not ready"}
	return map[string]any{"model": model, "state": "ping", "questions": map[string]any{"ready": question}}
}

func decisionsTarget(resolved channel.ResolvedTarget) (string, string, error) {
	baseURL, configured, err := targetBaseURL(resolved.TargetConfig)
	if err != nil {
		return "", "", err
	}
	switch resolved.ProviderKind {
	case channel.ProviderJev:
		if !configured {
			return "", "", fmt.Errorf("Jev base URL is required")
		}
		return baseURL, "/systemone", nil
	case channel.ProviderOpenRouter:
		if !configured {
			baseURL, _, err = sdkDefaultBaseURL(resolved.ProviderKind)
			if err != nil {
				return "", "", err
			}
		}
		return baseURL, "/alpha/decisions", nil
	case channel.ProviderGemini:
		// Gemini 透传沿用渠道 base URL（已含 /v1beta），与原生 embeddings 一致只拼接资源路径。
		if !configured {
			baseURL, _, err = sdkDefaultBaseURL(resolved.ProviderKind)
			if err != nil {
				return "", "", err
			}
		}
		return baseURL, "/decisions", nil
	default:
		return "", "", fmt.Errorf("unsupported decisions provider")
	}
}

func normalizeDecisionsAttemptResult(spec execution.AttemptSpec, result *execution.AttemptResult) {
	if result == nil || (spec.ClientProtocol != protocol.Decisions && spec.ClientProtocol != protocol.GeminiDecisions) {
		return
	}
	if result.Error != nil {
		if result.Error.ReplaySafety == "" {
			result.Error.ReplaySafety = execution.ReplaySafetyUnknown
		}
	}
	if !result.ResponseStarted || len(result.Body) == 0 {
		return
	}
	encoding, err := contentcoding.ParseContentEncoding(result.Header.Values("Content-Encoding"))
	var body []byte
	if err == nil {
		body, err = contentcoding.DecodeLimited(encoding, result.Body, execution.UnaryResponseBodyLimit(spec.ClientProtocol))
	}
	if err != nil {
		return
	}
	_, wire := decisionsWire(spec.ClientProtocol)
	if normalized, usageErr := wire.ExtractUsage(body); usageErr == nil {
		var root map[string]json.RawMessage
		_ = json.Unmarshal(body, &root)
		result.Usage = &execution.UsageEvidence{
			Normalized: normalized,
			Raw:        bytes.Clone(root["usage"]),
		}
	}
	if result.Error != nil || result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices ||
		spec.Operation != execution.OperationProbe {
		return
	}
	var response struct {
		Answers map[string]json.RawMessage `json:"answers"`
	}
	if json.Unmarshal(body, &response) == nil {
		if ready, exists := response.Answers["ready"]; exists {
			var answer struct {
				Noul *float64 `json:"noul"`
			}
			if json.Unmarshal(ready, &answer) == nil && answer.Noul != nil &&
				*answer.Noul >= 0 && *answer.Noul <= 1 {
				return
			}
		}
	}
	knownUsage := result.Usage
	*result = startedUnaryFailure(result.StatusCode, result.Header, execution.ErrorKindInternal, "upstream returned an invalid decisions probe response")
	result.Usage = knownUsage
	result.UpstreamProtocol = spec.ClientProtocol
}
