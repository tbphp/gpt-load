package bifrost

import (
	"net/http"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func zhipuMediaCreateOperation(operation execution.Operation) bool {
	switch operation {
	case execution.OperationZhipuImagesGenerate,
		execution.OperationZhipuImagesCreateAsync,
		execution.OperationZhipuVideosCreate:
		return true
	default:
		return false
	}
}

func prepareZhipuMedia(
	spec execution.AttemptSpec,
	resolved channel.ResolvedTarget,
	provider schemas.ModelProvider,
	directKey schemas.Key,
	secrets []string,
) (preparedAttempt, *execution.AttemptResult) {
	if resolved.ProviderKind != channel.ProviderOpenAICompatible {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "Zhipu media route is not implemented for this provider")
		return preparedAttempt{}, &failure
	}
	baseURL, configured, err := targetBaseURL(resolved.TargetConfig)
	if err != nil || !configured {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid Zhipu media target")
		return preparedAttempt{}, &failure
	}
	headers := safePassthroughHeaders(spec.Header)
	headers["Accept-Encoding"] = "identity"
	method := spec.Method
	path := ""
	var body []byte
	switch spec.Operation {
	case execution.OperationZhipuImagesGenerate:
		path = "/images/generations"
		method = http.MethodPost
	case execution.OperationZhipuImagesCreateAsync:
		path = "/async/images/generations"
		method = http.MethodPost
	case execution.OperationZhipuVideosCreate:
		path = "/videos/generations"
		method = http.MethodPost
	case execution.OperationZhipuMediaRetrieve, execution.OperationZhipuMediaCancel:
		remainder, ok := strings.CutPrefix(spec.Path, "/v1/async-result/")
		if !ok || remainder == "" || strings.Contains(remainder, "/") {
			failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid Zhipu async result path")
			return preparedAttempt{}, &failure
		}
		path = "/async-result/" + remainder
		method = http.MethodGet
		if spec.Operation == execution.OperationZhipuMediaCancel {
			method = http.MethodDelete
		}
	default:
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "unsupported Zhipu media operation")
		return preparedAttempt{}, &failure
	}
	if zhipuMediaCreateOperation(spec.Operation) {
		var sanitizedHeaders http.Header
		body, sanitizedHeaders, err = sanitizeNativePassthroughRequest(spec, false)
		if err != nil {
			failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid Zhipu media request body")
			return preparedAttempt{}, &failure
		}
		headers = safePassthroughHeaders(sanitizedHeaders)
		if headers == nil {
			headers = make(map[string]string, 2)
		}
		headers["Content-Type"] = "application/json"
		headers["Accept-Encoding"] = "identity"
	}
	return preparedAttempt{
		provider: provider, mode: channel.RouteNative, upstreamProtocol: protocol.ZhipuMedia,
		clientProtocol: protocol.ZhipuMedia, directKey: directKey, secrets: secrets,
		passthrough: &schemas.BifrostPassthroughRequest{
			Provider: provider, Model: spec.UpstreamModel, Method: method,
			Path: path, UpstreamURL: baseURL, RawQuery: "",
			Body: body, SafeHeaders: headers,
		},
	}, nil
}
