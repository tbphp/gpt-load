package bifrost

import (
	"bytes"
	"fmt"
	"net/http"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

// prepareAudioSpeech rewrites the speech model and builds the passthrough
// attempt. Each channel keeps its own voice fields and response format.
func prepareAudioSpeech(
	spec execution.AttemptSpec,
	resolved channel.ResolvedTarget,
	provider schemas.ModelProvider,
	directKey schemas.Key,
	secrets []string,
) (preparedAttempt, *execution.AttemptResult) {
	request, err := dialect.NewAudioSpeech().RewriteRequestModel(&dialect.ParsedRequest{
		Method: spec.Method, Path: spec.Path, RawQuery: spec.RawQuery,
		Header: spec.Header.Clone(), Body: bytes.Clone(spec.Body),
	}, spec.UpstreamModel)
	if err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid audio speech request body")
		failure.Error.OriginHint = execution.ErrorOriginClient
		failure.Error.ScopeHint = execution.ErrorScopeRequest
		return preparedAttempt{}, &failure
	}
	upstreamURL, path, err := audioSpeechTarget(resolved, spec.Path)
	if err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid audio speech target")
		return preparedAttempt{}, &failure
	}
	return preparedAttempt{
		provider: provider, mode: channel.RouteNative, upstreamProtocol: protocol.AudioSpeech,
		clientProtocol: protocol.AudioSpeech, directKey: directKey, secrets: secrets,
		passthrough: &schemas.BifrostPassthroughRequest{
			Provider: provider, Model: spec.UpstreamModel, Method: spec.Method,
			Path: path, UpstreamURL: upstreamURL, RawQuery: safeAttemptQuery(spec),
			Body: request.Body, SafeHeaders: safePassthroughHeaders(request.Header),
		},
	}, nil
}

// audioSpeechTarget maps the speech path onto the channel base URL.
// OpenRouter bases follow its SDK and omit /v1; OpenAI-compatible bases such
// as Mistral already end in /v1.
func audioSpeechTarget(resolved channel.ResolvedTarget, clientPath string) (string, string, error) {
	baseURL, configured, err := targetBaseURL(resolved.TargetConfig)
	if err != nil {
		return "", "", err
	}
	if resolved.ProviderKind == channel.ProviderOpenRouter {
		if !configured {
			baseURL, _, err = sdkDefaultBaseURL(resolved.ProviderKind)
			if err != nil {
				return "", "", err
			}
		}
		return baseURL, clientPath, nil
	}
	if !configured {
		return "", "", fmt.Errorf("audio speech base URL is required")
	}
	path, err := dialect.MistralUpstreamPath(clientPath)
	if err != nil {
		return "", "", err
	}
	return baseURL, path, nil
}

// audioSpeechRequestShape reports whether the attempt is a unary native
// speech request.
func audioSpeechRequestShape(spec execution.AttemptSpec, stream bool) bool {
	return !stream && spec.RouteMode == execution.RouteNative && spec.Operation == execution.OperationAudioSpeech &&
		spec.Method == http.MethodPost && spec.Path == "/v1/audio/speech"
}
