package bifrost

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/contentcoding"
	"gpt-load/internal/protocol"
)

const (
	openRouterDecisionsDefaultBaseURL = "https://openrouter.ai/api/v1"
	vercelTypeSafeAPIPrefix           = "/typesafe/v1"
	vercelJevModel                    = "typesafe-ai/jev"
)

func prepareDecisions(
	spec execution.AttemptSpec,
	resolved channel.ResolvedTarget,
	provider schemas.ModelProvider,
	directKey schemas.Key,
	secrets []string,
) (preparedAttempt, *execution.AttemptResult) {
	baseURL, path, err := decisionsTarget(resolved)
	if err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid decisions target")
		return preparedAttempt{}, &failure
	}
	upstreamModel := spec.UpstreamModel
	if vercelAIGatewayHost(urlHostname(baseURL)) {
		upstreamModel = vercelJevUpstreamModel(spec.UpstreamModel)
	}
	request := &dialect.ParsedRequest{
		Method: http.MethodPost,
		Path:   "/v1/systemone",
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
		body, err := json.Marshal(map[string]any{
			"model": upstreamModel,
			"state": "ping",
			"questions": map[string]any{
				"ready": map[string]any{
					"type": "noul", "instructions": "Is the service ready?",
					"criteria": map[string]string{"true": "Ready", "false": "Not ready"},
				},
			},
		})
		if err != nil {
			failure := notSentUnaryFailure(execution.ErrorKindInternal, "encode decisions probe")
			return preparedAttempt{}, &failure
		}
		request.Body = body
	}
	if spec.Operation != execution.OperationListModels {
		request, err = dialect.NewDecisions().RewriteRequestModel(request, upstreamModel)
		if err != nil {
			failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid decisions request body")
			failure.Error.OriginHint = execution.ErrorOriginClient
			failure.Error.ScopeHint = execution.ErrorScopeRequest
			return preparedAttempt{}, &failure
		}
	}
	model := upstreamModel
	if spec.Operation == execution.OperationListModels {
		path = "/models"
		model = ""
	}
	return preparedAttempt{
		provider: provider, mode: channel.RouteNative, upstreamProtocol: protocol.Decisions,
		clientProtocol: protocol.Decisions, directKey: directKey, secrets: secrets,
		passthrough: &schemas.BifrostPassthroughRequest{
			Provider: provider, Model: model, Method: request.Method,
			Path: path, UpstreamURL: baseURL, RawQuery: safeAttemptQuery(spec),
			Body: request.Body, SafeHeaders: safePassthroughHeaders(request.Header),
		},
	}, nil
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
		return jevDecisionsTarget(baseURL)
	case channel.ProviderOpenRouter:
		if !configured {
			baseURL = openRouterDecisionsDefaultBaseURL
		}
		parsed, err := url.Parse(baseURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return "", "", fmt.Errorf("invalid OpenRouter base URL")
		}
		path := strings.TrimSuffix(parsed.Path, "/")
		if !strings.HasSuffix(path, "/v1") {
			return "", "", fmt.Errorf("OpenRouter Decisions base URL must end in /v1")
		}
		parsed.Path = strings.TrimSuffix(path, "/v1") + "/alpha"
		parsed.RawPath = ""
		return strings.TrimSuffix(parsed.String(), "/"), "/decisions", nil
	default:
		return "", "", fmt.Errorf("unsupported decisions provider")
	}
}

func jevDecisionsTarget(baseURL string) (string, string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed == nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", "", fmt.Errorf("invalid Jev base URL")
	}
	if vercelAIGatewayHost(parsed.Hostname()) {
		parsed.Path = vercelTypeSafeAPIPrefix
		parsed.RawPath = ""
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return strings.TrimRight(parsed.String(), "/"), "/systemone", nil
	}
	return baseURL, "/systemone", nil
}

func vercelAIGatewayHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "ai-gateway.vercel.sh" || strings.HasSuffix(host, ".ai-gateway.vercel.sh")
}

func urlHostname(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil {
		return ""
	}
	return parsed.Hostname()
}

func vercelJevUpstreamModel(model string) string {
	switch strings.TrimSpace(model) {
	case "", "jev", "jev-latest", "jev-preview", "jev-1.13", "jev-1.13.0", "~typesafe/jev-latest":
		return vercelJevModel
	default:
		return model
	}
}

func normalizeDecisionsAttemptResult(spec execution.AttemptSpec, result *execution.AttemptResult) {
	if result == nil || spec.ClientProtocol != protocol.Decisions {
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
		body, err = contentcoding.DecodeLimited(encoding, result.Body, execution.UnaryResponseBodyLimit(protocol.Decisions))
	}
	if err != nil {
		return
	}
	if normalized, usageErr := dialect.NewDecisions().ExtractUsage(body); usageErr == nil {
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
	result.UpstreamProtocol = protocol.Decisions
}
