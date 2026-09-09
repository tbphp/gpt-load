package bifrost

import (
	"encoding/json"
	"net/http"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/contentcoding"
	"gpt-load/internal/protocol"
)

func prepareRerank(
	spec execution.AttemptSpec,
	resolved channel.ResolvedTarget,
	provider schemas.ModelProvider,
	directKey schemas.Key,
	secrets []string,
) (preparedAttempt, *execution.AttemptResult) {
	request := &dialect.ParsedRequest{Method: http.MethodPost, Path: "/v1/rerank", Header: spec.Header.Clone(), Body: spec.Body}
	if spec.Operation == execution.OperationProbe {
		body, err := json.Marshal(map[string]any{
			"model": spec.UpstreamModel, "query": "ping", "documents": []string{"ping"}, "top_n": 1,
		})
		if err != nil {
			failure := notSentUnaryFailure(execution.ErrorKindInternal, "encode rerank probe")
			return preparedAttempt{}, &failure
		}
		request.Body = body
	}
	request, err := dialect.NewRerank().RewriteRequestModel(request, spec.UpstreamModel)
	if err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid rerank request body")
		failure.Error.OriginHint = execution.ErrorOriginClient
		failure.Error.ScopeHint = execution.ErrorScopeRequest
		return preparedAttempt{}, &failure
	}
	baseURL, configured, err := targetBaseURL(resolved.TargetConfig)
	if err != nil || !configured {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid rerank target")
		return preparedAttempt{}, &failure
	}
	path := "/rerank"
	if resolved.ProviderKind == channel.ProviderMultiProtocolGateway {
		path = "/v1/rerank"
	}
	return preparedAttempt{
		provider: provider, mode: channel.RouteNative, upstreamProtocol: protocol.Rerank,
		clientProtocol: protocol.Rerank, directKey: directKey, secrets: secrets,
		passthrough: &schemas.BifrostPassthroughRequest{
			Provider: provider, Model: spec.UpstreamModel, Method: http.MethodPost,
			Path: path, UpstreamURL: baseURL, RawQuery: safeAttemptQuery(spec),
			Body: request.Body, SafeHeaders: safePassthroughHeaders(request.Header),
		},
	}, nil
}

func normalizeRerankAttemptResult(spec execution.AttemptSpec, result *execution.AttemptResult) {
	if result == nil || spec.ClientProtocol != protocol.Rerank {
		return
	}
	if result.Error != nil {
		if result.Error.ReplaySafety == "" {
			result.Error.ReplaySafety = execution.ReplaySafetyUnknown
		}
		return
	}
	if spec.Operation != execution.OperationProbe || result.StatusCode < 200 || result.StatusCode >= 300 {
		return
	}
	encoding, err := contentcoding.ParseContentEncoding(result.Header.Values("Content-Encoding"))
	var body []byte
	if err == nil {
		body, err = contentcoding.DecodeLimited(encoding, result.Body, execution.UnaryResponseBodyLimit(protocol.Rerank))
	}
	var response struct {
		Results []struct {
			Index *int     `json:"index"`
			Score *float64 `json:"relevance_score"`
		} `json:"results"`
	}
	if err != nil || json.Unmarshal(body, &response) != nil || len(response.Results) != 1 ||
		response.Results[0].Index == nil || *response.Results[0].Index != 0 || response.Results[0].Score == nil {
		*result = startedUnaryFailure(result.StatusCode, result.Header, execution.ErrorKindInternal, "upstream returned an invalid rerank probe response")
		result.UpstreamProtocol = protocol.Rerank
	}
}
