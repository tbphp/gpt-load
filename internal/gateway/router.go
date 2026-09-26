package gateway

import (
	"net/http"
	"strings"

	"gpt-load/internal/protocol"
)

const (
	openAICompletionsPath       = "/v1/chat/completions"
	anthropicMessagesPath       = "/v1/messages"
	geminiModelsPath            = "/v1beta/models"
	geminiGenerationPattern     = geminiModelsPath + "/:model_action"
	modelsPath                  = "/v1/models"
	openAIResponsesPath         = "/v1/responses"
	responsesResourcePattern    = openAIResponsesPath + "/*resource_path"
	openAIImagesGenerationsPath = "/v1/images/generations"
	openAIImagesEditsPath       = "/v1/images/edits"
	openAIEmbeddingsPath        = "/v1/embeddings"
	decisionsPath               = "/v1/systemone"
)

type endpointKind uint8

const (
	endpointForward endpointKind = iota + 1
	endpointModels
	endpointUsage
	endpointLiveCreate
	endpointLiveSideband
	endpointLiveHangup
	endpointMistralRealtime
)

type route struct {
	Protocol protocol.Protocol
	Kind     endpointKind
}

type dataPlaneEndpoint struct {
	name            string
	methods         []string
	path            string
	pathValidator   func(*http.Request) bool
	rejectAfterAuth func(*http.Request) bool
	resolve         func(*http.Request) route
}

func dataPlaneEndpointCatalog() []dataPlaneEndpoint {
	return []dataPlaneEndpoint{
		{name: "data.usage", methods: []string{http.MethodGet}, path: "/v1/user/balance", resolve: staticRoute("", endpointUsage)},
		{name: "data.usage.unversioned", methods: []string{http.MethodGet}, path: "/user/balance", resolve: staticRoute("", endpointUsage)},
		{
			name:    "data.openai.completions",
			methods: []string{http.MethodPost},
			path:    openAICompletionsPath,
			resolve: staticRoute(protocol.OpenAICompletions, endpointForward),
		},
		{
			name:    "data.anthropic.messages",
			methods: []string{http.MethodPost},
			path:    anthropicMessagesPath,
			resolve: staticRoute(protocol.Anthropic, endpointForward),
		},
		{
			name:    "data.anthropic.count_tokens",
			methods: []string{http.MethodPost},
			path:    anthropicMessagesPath + "/count_tokens",
			resolve: staticRoute(protocol.Anthropic, endpointForward),
		},
		{
			name:          "data.gemini.generate",
			methods:       []string{http.MethodPost},
			path:          geminiGenerationPattern,
			pathValidator: validateGeminiRequest,
			resolve:       resolveGeminiModelActionRoute,
		},
		{
			name:    "data.gemini.models",
			methods: []string{http.MethodGet},
			path:    geminiModelsPath,
			resolve: staticRoute(protocol.Gemini, endpointModels),
		},
		{
			name:    "data.models",
			methods: []string{http.MethodGet},
			path:    modelsPath,
			resolve: resolveModelListRoute,
		},
		{
			name:            "data.openai.responses",
			methods:         responsesRegisteredMethods(),
			path:            openAIResponsesPath,
			rejectAfterAuth: rejectResponsesAfterAuthentication,
			resolve:         staticRoute(protocol.OpenAIResponses, endpointForward),
		},
		{
			name:            "data.openai.responses.resource",
			methods:         responsesRegisteredMethods(),
			path:            responsesResourcePattern,
			rejectAfterAuth: rejectResponsesAfterAuthentication,
			resolve:         staticRoute(protocol.OpenAIResponses, endpointForward),
		},
		{
			name:    "data.openai.images.generations",
			methods: []string{http.MethodPost},
			path:    openAIImagesGenerationsPath,
			resolve: staticRoute(protocol.OpenAIImages, endpointForward),
		},
		{
			name:    "data.openai.images.edits",
			methods: []string{http.MethodPost},
			path:    openAIImagesEditsPath,
			resolve: staticRoute(protocol.OpenAIImages, endpointForward),
		},
		{
			name:    "data.openai.embeddings",
			methods: []string{http.MethodPost},
			path:    openAIEmbeddingsPath,
			resolve: staticRoute(protocol.OpenAIEmbeddings, endpointForward),
		},
		{name: "data.rerank", methods: []string{http.MethodPost}, path: "/v1/rerank", resolve: staticRoute(protocol.Rerank, endpointForward)},
		{name: "data.decisions", methods: []string{http.MethodPost}, path: decisionsPath, resolve: staticRoute(protocol.Decisions, endpointForward)},
		{name: "data.mistral.ocr", methods: []string{http.MethodPost}, path: "/v1/ocr", resolve: staticRoute(protocol.Mistral, endpointForward)},
		{name: "data.mistral.fim", methods: []string{http.MethodPost}, path: "/v1/fim/completions", resolve: staticRoute(protocol.Mistral, endpointForward)},
		{name: "data.mistral.audio.transcriptions", methods: []string{http.MethodPost}, path: "/v1/audio/transcriptions", resolve: staticRoute(protocol.Mistral, endpointForward)},
		{name: "data.mistral.realtime", methods: []string{http.MethodGet}, path: "/v1/audio/transcriptions/realtime", resolve: staticRoute(protocol.Mistral, endpointMistralRealtime)},
		{name: "data.mistral.audio.speech", methods: []string{http.MethodPost}, path: "/v1/audio/speech", resolve: staticRoute(protocol.Mistral, endpointForward)},
		{name: "data.mistral.voices", methods: mistralVoiceMethods(), path: "/v1/audio/voices", resolve: staticRoute(protocol.Mistral, endpointForward)},
		{name: "data.mistral.voices.resource", methods: mistralVoiceMethods(), path: "/v1/audio/voices/*resource_path", pathValidator: mistralResourcePath, resolve: staticRoute(protocol.Mistral, endpointForward)},
		{name: "data.mistral.voices.v2", methods: []string{http.MethodGet, http.MethodHead}, path: "/v2/audio/voices", resolve: staticRoute(protocol.Mistral, endpointForward)},
		{name: "data.mistral.voices.v2.resource", methods: []string{http.MethodGet, http.MethodHead}, path: "/v2/audio/voices/*resource_path", pathValidator: mistralResourcePath, resolve: staticRoute(protocol.Mistral, endpointForward)},
		{name: "data.mistral.moderations", methods: []string{http.MethodPost}, path: "/v1/moderations", resolve: staticRoute(protocol.Mistral, endpointForward)},
		{name: "data.mistral.chat.moderations", methods: []string{http.MethodPost}, path: "/v1/chat/moderations", resolve: staticRoute(protocol.Mistral, endpointForward)},
		{name: "data.mistral.classifications", methods: []string{http.MethodPost}, path: "/v1/classifications", resolve: staticRoute(protocol.Mistral, endpointForward)},
		{name: "data.mistral.files", methods: mistralResourceMethods(), path: "/v1/files", resolve: staticRoute(protocol.Mistral, endpointForward)},
		{name: "data.mistral.files.resource", methods: mistralResourceMethods(), path: "/v1/files/*resource_path", pathValidator: mistralResourcePath, resolve: staticRoute(protocol.Mistral, endpointForward)},
		{name: "data.mistral.batch", methods: mistralResourceMethods(), path: "/v1/batch", resolve: staticRoute(protocol.Mistral, endpointForward)},
		{name: "data.mistral.batch.resource", methods: mistralResourceMethods(), path: "/v1/batch/*resource_path", pathValidator: mistralResourcePath, resolve: staticRoute(protocol.Mistral, endpointForward)},
		{name: "data.mistral.agents", methods: mistralResourceMethods(), path: "/v1/agents", resolve: staticRoute(protocol.Mistral, endpointForward)},
		{name: "data.mistral.agents.resource", methods: mistralResourceMethods(), path: "/v1/agents/*resource_path", pathValidator: mistralResourcePath, resolve: staticRoute(protocol.Mistral, endpointForward)},
		{name: "data.mistral.conversations", methods: mistralResourceMethods(), path: "/v1/conversations", resolve: staticRoute(protocol.Mistral, endpointForward)},
		{name: "data.mistral.conversations.resource", methods: mistralResourceMethods(), path: "/v1/conversations/*resource_path", pathValidator: mistralResourcePath, resolve: staticRoute(protocol.Mistral, endpointForward)},
		{name: "data.codex.search", methods: []string{http.MethodPost}, path: "/v1/alpha/search", resolve: staticRoute(protocol.OpenAIResponses, endpointForward)},
		{name: "data.codex.live", methods: []string{http.MethodPost}, path: "/v1/live", resolve: staticRoute(protocol.CodexLive, endpointLiveCreate)},
		{name: "data.codex.live.sideband", methods: []string{http.MethodGet}, path: "/v1/live/:call_id", resolve: staticRoute(protocol.CodexLive, endpointLiveSideband)},
		{name: "data.codex.live.legacy.hangup", methods: []string{http.MethodPost}, path: "/v1/live/:call_id/hangup", resolve: staticRoute(protocol.CodexLive, endpointLiveHangup)},
		{name: "data.codex.live.calls", methods: []string{http.MethodPost}, path: "/v1/realtime/calls", resolve: staticRoute(protocol.CodexLive, endpointLiveCreate)},
		{name: "data.codex.live.realtime", methods: []string{http.MethodGet}, path: "/v1/realtime", resolve: staticRoute(protocol.CodexLive, endpointLiveSideband)},
		{name: "data.codex.live.call.sideband", methods: []string{http.MethodGet}, path: "/v1/realtime/calls/:call_id", resolve: staticRoute(protocol.CodexLive, endpointLiveSideband)},
		{name: "data.codex.live.hangup", methods: []string{http.MethodPost}, path: "/v1/realtime/calls/:call_id/hangup", resolve: staticRoute(protocol.CodexLive, endpointLiveHangup)},
	}
}

func responsesRegisteredMethods() []string {
	return []string{
		http.MethodGet,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodHead,
		http.MethodOptions,
		http.MethodDelete,
		http.MethodConnect,
		http.MethodTrace,
	}
}

func mistralVoiceMethods() []string {
	return []string{
		http.MethodGet,
		http.MethodPost,
		http.MethodPatch,
		http.MethodDelete,
		http.MethodHead,
	}
}

func mistralResourceMethods() []string {
	return []string{
		http.MethodGet,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
		http.MethodHead,
	}
}

func mistralResourcePath(request *http.Request) bool {
	if request == nil || request.URL == nil {
		return false
	}
	path := request.URL.Path
	for _, root := range []string{"/v1/files", "/v1/batch", "/v1/agents", "/v1/conversations", "/v1/audio/voices", "/v2/audio/voices"} {
		if path == root {
			return true
		}
		prefix := root + "/"
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		rest := strings.TrimPrefix(path, prefix)
		if rest == "" || strings.Contains(rest, "//") {
			return false
		}
		for _, segment := range strings.Split(rest, "/") {
			if segment == "" || segment == "." || segment == ".." {
				return false
			}
		}
		return true
	}
	return false
}

func staticRoute(selectedProtocol protocol.Protocol, kind endpointKind) func(*http.Request) route {
	return func(*http.Request) route {
		return route{Protocol: selectedProtocol, Kind: kind}
	}
}

// resolveGeminiModelActionRoute 按动作后缀区分 Gemini 生成类请求和原生 embedding 请求。
func resolveGeminiModelActionRoute(request *http.Request) route {
	if request != nil && request.URL != nil && geminiEmbeddingsRequestPath(request.URL.Path) {
		return route{Protocol: protocol.GeminiEmbeddings, Kind: endpointForward}
	}
	return route{Protocol: protocol.Gemini, Kind: endpointForward}
}

func resolveModelListRoute(request *http.Request) route {
	if request != nil &&
		strings.TrimSpace(request.Header.Get("anthropic-version")) != "" {
		return route{Protocol: protocol.Anthropic, Kind: endpointModels}
	}
	return route{Protocol: protocol.OpenAICompletions, Kind: endpointModels}
}

func validateGeminiRequest(request *http.Request) bool {
	return request != nil &&
		request.URL != nil &&
		geminiRequestPath(request.URL.Path)
}

func rejectResponsesAfterAuthentication(request *http.Request) bool {
	return request == nil ||
		request.URL == nil ||
		!responsesPath(request.URL.Path) ||
		locallyRejectedForwardMethod(request.Method)
}

func responsesPath(path string) bool {
	if path == openAIResponsesPath {
		return true
	}
	if !strings.HasPrefix(path, openAIResponsesPath+"/") {
		return false
	}
	for _, segment := range strings.Split(
		strings.TrimPrefix(path, openAIResponsesPath+"/"),
		"/",
	) {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func locallyRejectedForwardMethod(method string) bool {
	switch method {
	case http.MethodOptions, http.MethodConnect, http.MethodTrace:
		return true
	default:
		return false
	}
}

func geminiRequestPath(path string) bool {
	return geminiModelActionPath(path, ":generateContent", ":streamGenerateContent", ":countTokens") ||
		geminiEmbeddingsRequestPath(path)
}

func geminiEmbeddingsRequestPath(path string) bool {
	return geminiModelActionPath(path, ":embedContent", ":batchEmbedContents")
}

func geminiModelActionPath(path string, suffixes ...string) bool {
	const prefix = geminiModelsPath + "/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	modelAndAction := strings.TrimPrefix(path, prefix)
	if strings.Contains(modelAndAction, "/") {
		return false
	}
	for _, suffix := range suffixes {
		if model := strings.TrimSuffix(modelAndAction, suffix); model != modelAndAction {
			return model != ""
		}
	}
	return false
}
