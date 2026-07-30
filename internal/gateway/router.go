package gateway

import (
	"net/http"
	"strings"

	"gpt-load/internal/protocol"
)

const (
	openAICompletionsPath    = "/v1/chat/completions"
	anthropicMessagesPath    = "/v1/messages"
	geminiModelsPath         = "/v1beta/models"
	geminiGenerationPattern  = geminiModelsPath + "/:model_action"
	modelsPath               = "/v1/models"
	openAIResponsesPath      = "/v1/responses"
	responsesResourcePattern = openAIResponsesPath + "/*resource_path"
)

type endpointKind uint8

const (
	endpointForward endpointKind = iota + 1
	endpointModels
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
			name:          "data.gemini.generate",
			methods:       []string{http.MethodPost},
			path:          geminiGenerationPattern,
			pathValidator: validateGeminiGenerationRequest,
			resolve:       staticRoute(protocol.Gemini, endpointForward),
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

func staticRoute(selectedProtocol protocol.Protocol, kind endpointKind) func(*http.Request) route {
	return func(*http.Request) route {
		return route{Protocol: selectedProtocol, Kind: kind}
	}
}

func resolveModelListRoute(request *http.Request) route {
	if request != nil &&
		strings.TrimSpace(request.Header.Get("anthropic-version")) != "" {
		return route{Protocol: protocol.Anthropic, Kind: endpointModels}
	}
	return route{Protocol: protocol.OpenAICompletions, Kind: endpointModels}
}

func validateGeminiGenerationRequest(request *http.Request) bool {
	return request != nil &&
		request.URL != nil &&
		geminiGenerationPath(request.URL.Path)
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

func geminiGenerationPath(path string) bool {
	const prefix = geminiModelsPath + "/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	modelAndAction := strings.TrimPrefix(path, prefix)
	if strings.Contains(modelAndAction, "/") {
		return false
	}
	for _, suffix := range []string{":generateContent", ":streamGenerateContent"} {
		if model := strings.TrimSuffix(modelAndAction, suffix); model != modelAndAction {
			return model != ""
		}
	}
	return false
}
