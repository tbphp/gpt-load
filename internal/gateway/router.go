package gateway

import (
	"net/http"
	"strings"

	"gpt-load/internal/protocol"
)

type endpointKind uint8

const (
	endpointChat endpointKind = iota + 1
	endpointModels
)

type route struct {
	Protocol protocol.Protocol
	Kind     endpointKind
}

func determineRoute(method, path string, headers http.Header) (route, bool) {
	switch {
	case method == http.MethodPost && path == "/v1/chat/completions":
		return route{Protocol: protocol.OpenAI, Kind: endpointChat}, true
	case method == http.MethodPost && path == "/v1/messages":
		return route{Protocol: protocol.Anthropic, Kind: endpointChat}, true
	case method == http.MethodPost && geminiGenerationPath(path):
		return route{Protocol: protocol.Gemini, Kind: endpointChat}, true
	case method == http.MethodGet && path == "/v1beta/models":
		return route{Protocol: protocol.Gemini, Kind: endpointModels}, true
	case method == http.MethodGet && path == "/v1/models":
		if strings.TrimSpace(headers.Get("anthropic-version")) != "" {
			return route{Protocol: protocol.Anthropic, Kind: endpointModels}, true
		}
		return route{Protocol: protocol.OpenAI, Kind: endpointModels}, true
	default:
		return route{}, false
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
