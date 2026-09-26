package bifrost

import (
	"bytes"
	"net/http"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

// prepareMistral rewrites a native Mistral HTTP request and builds the
// passthrough attempt. Realtime transcription is not prepared here.
func prepareMistral(
	spec execution.AttemptSpec,
	resolved channel.ResolvedTarget,
	provider schemas.ModelProvider,
	directKey schemas.Key,
	secrets []string,
) (preparedAttempt, *execution.AttemptResult) {
	request := &dialect.ParsedRequest{
		Method: spec.Method, Path: spec.Path, RawQuery: spec.RawQuery,
		Header: spec.Header.Clone(), Body: bytes.Clone(spec.Body),
	}
	if spec.UpstreamModel != "" {
		rewritten, err := dialect.NewMistral().RewriteRequestModel(request, spec.UpstreamModel)
		if err != nil {
			failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid mistral request body")
			failure.Error.OriginHint = execution.ErrorOriginClient
			failure.Error.ScopeHint = execution.ErrorScopeRequest
			return preparedAttempt{}, &failure
		}
		request = rewritten
	} else if _, err := dialect.NewMistral().InspectRequest(request); err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid mistral request")
		failure.Error.OriginHint = execution.ErrorOriginClient
		failure.Error.ScopeHint = execution.ErrorScopeRequest
		return preparedAttempt{}, &failure
	}
	baseURL, configured, err := targetBaseURL(resolved.TargetConfig)
	if err != nil || !configured {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid mistral target")
		return preparedAttempt{}, &failure
	}
	upstreamURL, path, err := dialect.MistralUpstreamTarget(baseURL, spec.Path)
	if err != nil {
		failure := notSentUnaryFailure(execution.ErrorKindInvalidRequest, "invalid mistral request path")
		return preparedAttempt{}, &failure
	}
	return preparedAttempt{
		provider: provider, mode: channel.RouteNative, upstreamProtocol: protocol.Mistral,
		clientProtocol: protocol.Mistral, directKey: directKey, secrets: secrets,
		passthrough: &schemas.BifrostPassthroughRequest{
			Provider: provider, Model: spec.UpstreamModel, Method: spec.Method,
			Path: path, UpstreamURL: upstreamURL, RawQuery: safeAttemptQuery(spec),
			Body: request.Body, SafeHeaders: safePassthroughHeaders(request.Header),
		},
	}, nil
}

// mistralRequestShape reports whether the attempt matches the native HTTP
// shape of its Mistral operation. Realtime transcription is rejected because
// the gateway copies its WebSocket frames outside Bifrost.
func mistralRequestShape(spec execution.AttemptSpec, stream bool) bool {
	if spec.RouteMode != execution.RouteNative {
		return false
	}
	switch spec.Operation {
	case execution.OperationMistralOCR:
		return !stream && spec.Method == http.MethodPost && spec.Path == "/v1/ocr"
	case execution.OperationMistralFIM:
		return !stream && spec.Method == http.MethodPost && spec.Path == "/v1/fim/completions"
	case execution.OperationMistralAudioTranscription:
		return !stream && spec.Method == http.MethodPost && spec.Path == "/v1/audio/transcriptions"
	case execution.OperationMistralAudioSpeech:
		return !stream && spec.Method == http.MethodPost && spec.Path == "/v1/audio/speech"
	case execution.OperationMistralModeration:
		return !stream && spec.Method == http.MethodPost && spec.Path == "/v1/moderations"
	case execution.OperationMistralChatModeration:
		return !stream && spec.Method == http.MethodPost && spec.Path == "/v1/chat/moderations"
	case execution.OperationMistralClassification:
		return !stream && spec.Method == http.MethodPost && spec.Path == "/v1/classifications"
	case execution.OperationMistralFiles:
		return !stream && mistralResourceMethod(spec.Method) && mistralSubtree(spec.Path, "/v1/files")
	case execution.OperationMistralBatch:
		return !stream && mistralResourceMethod(spec.Method) && mistralSubtree(spec.Path, "/v1/batch")
	case execution.OperationMistralAgents:
		return mistralResourceMethod(spec.Method) && mistralSubtree(spec.Path, "/v1/agents") &&
			(!stream || spec.Method == http.MethodPost)
	case execution.OperationMistralConversations:
		return mistralResourceMethod(spec.Method) && mistralSubtree(spec.Path, "/v1/conversations") &&
			(!stream || spec.Method == http.MethodPost)
	case execution.OperationMistralVoices:
		return !stream && mistralVoiceMethod(spec.Method) &&
			(mistralSubtree(spec.Path, "/v1/audio/voices") || mistralSubtree(spec.Path, "/v2/audio/voices"))
	default:
		return false
	}
}

// mistralVoiceMethod reports whether the method is valid for a voice path.
func mistralVoiceMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodHead:
		return true
	default:
		return false
	}
}

// mistralResourceMethod reports whether the method is valid for files,
// batch, agents, and conversations.
func mistralResourceMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead:
		return true
	default:
		return false
	}
}

// mistralSubtree reports whether path is root or a safe child of root.
func mistralSubtree(path, root string) bool {
	if path == root {
		return true
	}
	if len(path) <= len(root)+1 || path[:len(root)] != root || path[len(root)] != '/' {
		return false
	}
	rest := path[len(root)+1:]
	if rest == "" {
		return false
	}
	for _, segment := range splitPath(rest) {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// splitPath returns the slash-separated segments of path, including empty
// segments.
func splitPath(path string) []string {
	parts := make([]string, 0, 4)
	start := 0
	for index := 0; index <= len(path); index++ {
		if index == len(path) || path[index] == '/' {
			parts = append(parts, path[start:index])
			start = index + 1
		}
	}
	return parts
}
