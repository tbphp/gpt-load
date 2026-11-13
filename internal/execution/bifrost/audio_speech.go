package bifrost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/contentcoding"
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
	stream bool,
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
	var options struct {
		Stream       bool   `json:"stream"`
		StreamFormat string `json:"stream_format"`
	}
	if err := json.Unmarshal(request.Body, &options); err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid audio speech streaming options")
		return preparedAttempt{}, &failure
	}
	// 只接受所选渠道的原生开关，不在 OpenAI 与 Mistral 格式之间转换。
	upstreamStream := false
	switch resolved.ProviderKind {
	case channel.ProviderOpenAI:
		upstreamStream = options.StreamFormat == "sse"
	case channel.ProviderOpenAICompatible:
		upstreamStream = options.Stream
	}
	if stream != upstreamStream {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "audio speech streaming requires the channel's native streaming option")
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
// OpenAI and OpenRouter bases follow their SDKs and omit /v1;
// OpenAI-compatible bases such as Mistral already include that prefix.
func audioSpeechTarget(resolved channel.ResolvedTarget, clientPath string) (string, string, error) {
	baseURL, configured, err := targetBaseURL(resolved.TargetConfig)
	if err != nil {
		return "", "", err
	}
	if resolved.ProviderKind == channel.ProviderOpenRouter || resolved.ProviderKind == channel.ProviderOpenAI {
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

// audioSpeechAudio reports whether a speech response carries raw audio, whose
// leading bytes are samples that may match gzip or zstd magic.
func audioSpeechAudio(spec execution.AttemptSpec, headers http.Header) bool {
	if spec.ClientProtocol != protocol.AudioSpeech {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(headers.Get("Content-Type"))
	return err == nil && strings.HasPrefix(mediaType, "audio/")
}

// encodedAudio reports whether an audio body that starts with gzip or zstd
// magic really is compressed. The SDK drops Content-Encoding from passthrough
// headers but keeps the raw body, so only a body that decodes counts.
func encodedAudio(body []byte, limit int64) bool {
	encoding := contentcoding.Gzip
	if body[0] == 0x28 {
		encoding = contentcoding.Zstd
	}
	_, err := contentcoding.DecodeLimited(encoding, body, limit)
	return err == nil || errors.Is(err, contentcoding.ErrDecodedBodyTooLarge)
}

// audioSpeechRequestShape 检查原生语音路径；流式开关由所选渠道单独验证。
func audioSpeechRequestShape(spec execution.AttemptSpec) bool {
	return spec.RouteMode == execution.RouteNative && spec.Operation == execution.OperationAudioSpeech &&
		spec.Method == http.MethodPost && spec.Path == "/v1/audio/speech"
}
