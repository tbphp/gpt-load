package gateway

import (
	"net/http"
	"strings"

	"gpt-load/internal/protocol"
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

func determineRoute(method, path string, headers http.Header) (route, bool) {
	switch {
	case method == http.MethodPost && path == "/v1/chat/completions":
		return route{Protocol: protocol.OpenAIChatCompletions, Kind: endpointForward}, true
	case method == http.MethodPost && path == "/v1/messages":
		return route{Protocol: protocol.Anthropic, Kind: endpointForward}, true
	case method == http.MethodPost && geminiGenerationPath(path):
		return route{Protocol: protocol.Gemini, Kind: endpointForward}, true
	case responsesPath(path) && !locallyRejectedForwardMethod(method):
		return route{Protocol: protocol.OpenAIResponses, Kind: endpointForward}, true
	case method == http.MethodGet && path == "/v1beta/models":
		return route{Protocol: protocol.Gemini, Kind: endpointModels}, true
	case method == http.MethodGet && path == "/v1/models":
		if strings.TrimSpace(headers.Get("anthropic-version")) != "" {
			return route{Protocol: protocol.Anthropic, Kind: endpointModels}, true
		}
		return route{Protocol: protocol.OpenAIChatCompletions, Kind: endpointModels}, true
	default:
		return route{}, false
	}
}

func responsesPath(path string) bool {
	const prefix = "/v1/responses"
	if path == prefix {
		return true
	}
	if !strings.HasPrefix(path, prefix+"/") {
		return false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(path, prefix+"/"), "/") {
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
	const prefix = "/v1beta/models/"
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
