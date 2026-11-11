package bifrost

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func prepareVideos(
	spec execution.AttemptSpec,
	resolved channel.ResolvedTarget,
	provider schemas.ModelProvider,
	directKey schemas.Key,
	secrets []string,
) (preparedAttempt, *execution.AttemptResult) {
	headers := safePassthroughHeaders(spec.Header)
	headers["Accept-Encoding"] = "identity"
	var (
		body     []byte
		path     string
		upstream string
		rawQuery string
		method   = spec.Method
		failure  *execution.AttemptResult
	)
	if spec.Operation == execution.OperationVideosCreate {
		body, headers, failure = videoCreateRequest(spec, headers)
		if failure != nil {
			return preparedAttempt{}, failure
		}
		path, upstream, failure = videoCreateTarget(spec, resolved)
		if failure != nil {
			return preparedAttempt{}, failure
		}
		method = http.MethodPost
	} else {
		path, upstream, rawQuery, failure = videoResourceTarget(spec, resolved)
		if failure != nil {
			return preparedAttempt{}, failure
		}
		body = nil
	}
	return preparedAttempt{
		provider: provider, mode: channel.RouteNative, upstreamProtocol: protocol.OpenAIVideos,
		clientProtocol: protocol.OpenAIVideos, directKey: directKey, secrets: secrets,
		passthrough: &schemas.BifrostPassthroughRequest{
			Provider: provider, Model: spec.UpstreamModel, Method: method,
			Path: path, UpstreamURL: upstream, RawQuery: rawQuery,
			Body: body, SafeHeaders: headers,
		},
	}, nil
}

func videoCreateRequest(
	spec execution.AttemptSpec,
	headers map[string]string,
) ([]byte, map[string]string, *execution.AttemptResult) {
	body, sanitizedHeaders, err := sanitizeNativePassthroughRequest(spec, false)
	if err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid video request body")
		failure.Error.OriginHint = execution.ErrorOriginClient
		failure.Error.ScopeHint = execution.ErrorScopeRequest
		failure.Error.ReplaySafety = execution.ReplaySafetyUnknown
		return nil, nil, &failure
	}
	if sanitizedHeaders != nil {
		headers = safePassthroughHeaders(sanitizedHeaders)
	}
	if headers == nil {
		headers = make(map[string]string, 2)
	}
	headers["Content-Type"] = "application/json"
	headers["Accept-Encoding"] = "identity"
	return body, headers, nil
}

func videoCreateTarget(
	spec execution.AttemptSpec,
	resolved channel.ResolvedTarget,
) (path, upstream string, failure *execution.AttemptResult) {
	switch resolved.ProviderKind {
	case channel.ProviderOpenAI:
		return "/videos", "", nil
	case channel.ProviderOpenAICompatible:
		baseURL, configured, err := targetBaseURL(resolved.TargetConfig)
		if err != nil || !configured {
			return videoCreateTargetFailure("invalid video target")
		}
		return "/videos", baseURL, nil
	case channel.ProviderMultiProtocolGateway:
		baseURL, configured, err := targetBaseURL(resolved.TargetConfig)
		if err != nil || !configured {
			return videoCreateTargetFailure("invalid video target")
		}
		return "/v1/videos", baseURL, nil
	default:
		return videoCreateTargetFailure("video route is not implemented for this provider")
	}
}

func videoResourceTarget(
	spec execution.AttemptSpec,
	resolved channel.ResolvedTarget,
) (path, upstream, rawQuery string, failure *execution.AttemptResult) {
	if spec.Method == http.MethodHead {
		return videoTargetFailure("video resources do not support HEAD")
	}
	remainder, ok := strings.CutPrefix(spec.Path, "/v1/videos/")
	if !ok || remainder == "" {
		return videoTargetFailure("invalid video resource path")
	}
	parts := strings.Split(remainder, "/")
	resourceID := parts[0]
	suffix := ""
	if len(parts) == 2 {
		suffix = parts[1]
	}
	if len(parts) > 2 || resourceID == "" || resourceID == "." || resourceID == ".." ||
		strings.ContainsAny(resourceID, "\\?#") {
		return videoTargetFailure("invalid video resource path")
	}
	if decoded, err := url.PathUnescape(resourceID); err == nil {
		resourceID = decoded
	}
	if resolved.ChannelID == channel.Agnes {
		return agnesVideoResourceTarget(spec, resolved, resourceID, suffix)
	}
	switch resolved.ProviderKind {
	case channel.ProviderOpenAI:
		return videoOpenAIPath(resourceID, suffix), "", "", nil
	case channel.ProviderOpenAICompatible:
		baseURL, configured, err := targetBaseURL(resolved.TargetConfig)
		if err != nil || !configured {
			return videoTargetFailure("invalid video target")
		}
		return videoOpenAIPath(resourceID, suffix), baseURL, "", nil
	case channel.ProviderMultiProtocolGateway:
		baseURL, configured, err := targetBaseURL(resolved.TargetConfig)
		if err != nil || !configured {
			return videoTargetFailure("invalid video target")
		}
		return "/v1" + videoOpenAIPath(resourceID, suffix), baseURL, "", nil
	default:
		return videoTargetFailure("video route is not implemented for this provider")
	}
}

func agnesVideoResourceTarget(
	spec execution.AttemptSpec,
	resolved channel.ResolvedTarget,
	resourceID string,
	suffix string,
) (path, upstream, rawQuery string, failure *execution.AttemptResult) {
	baseURL, configured, err := targetBaseURL(resolved.TargetConfig)
	if err != nil || !configured {
		return videoTargetFailure("invalid Agnes video target")
	}
	if spec.Operation == execution.OperationVideosRetrieve {
		origin, originErr := agnesOrigin(baseURL)
		if originErr != nil {
			return videoTargetFailure("invalid Agnes origin")
		}
		query := url.Values{}
		query.Set("video_id", resourceID)
		if spec.UpstreamModel != "" {
			query.Set("model_name", spec.UpstreamModel)
		}
		return "/agnesapi", origin, query.Encode(), nil
	}
	return videoOpenAIPath(resourceID, suffix), baseURL, "", nil
}

func videoOpenAIPath(resourceID, suffix string) string {
	path := "/videos/" + url.PathEscape(resourceID)
	if suffix == "content" {
		path += "/content"
	}
	return path
}

func agnesOrigin(baseURL string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" {
		return "", err
	}
	path := strings.TrimRight(parsed.Path, "/")
	path = strings.TrimSuffix(path, "/v1")
	parsed.Path = path
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func videoTargetFailure(summary string) (path, upstream, rawQuery string, failure *execution.AttemptResult) {
	value := notSentUnaryFailure(execution.ErrorKindInvalidRequest, summary)
	return "", "", "", &value
}

func videoCreateTargetFailure(summary string) (path, upstream string, failure *execution.AttemptResult) {
	value := notSentUnaryFailure(execution.ErrorKindInvalidRequest, summary)
	return "", "", &value
}
