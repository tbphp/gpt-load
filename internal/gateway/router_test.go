package gateway

import (
	"net/http"
	"testing"

	"gpt-load/internal/protocol"
)

func TestDetermineRouteUsesOnlyTheGlobalStaticTable(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		path    string
		headers http.Header
		want    route
		wantOK  bool
	}{
		{name: "OpenAI chat", method: http.MethodPost, path: "/v1/chat/completions", want: route{Protocol: protocol.OpenAIChatCompletions, Kind: endpointForward}, wantOK: true},
		{name: "Anthropic chat", method: http.MethodPost, path: "/v1/messages", want: route{Protocol: protocol.Anthropic, Kind: endpointForward}, wantOK: true},
		{name: "Gemini generate", method: http.MethodPost, path: "/v1beta/models/gemini-2.5-pro:generateContent", want: route{Protocol: protocol.Gemini, Kind: endpointForward}, wantOK: true},
		{name: "Gemini stream", method: http.MethodPost, path: "/v1beta/models/gemini-2.5-pro:streamGenerateContent", want: route{Protocol: protocol.Gemini, Kind: endpointForward}, wantOK: true},
		{name: "Gemini models", method: http.MethodGet, path: "/v1beta/models", want: route{Protocol: protocol.Gemini, Kind: endpointModels}, wantOK: true},
		{name: "OpenAI models", method: http.MethodGet, path: "/v1/models", want: route{Protocol: protocol.OpenAIChatCompletions, Kind: endpointModels}, wantOK: true},
		{name: "Anthropic models", method: http.MethodGet, path: "/v1/models", headers: http.Header{"Anthropic-Version": {"2023-06-01"}}, want: route{Protocol: protocol.Anthropic, Kind: endpointModels}, wantOK: true},
		{name: "responses create", method: http.MethodPost, path: "/v1/responses", want: route{Protocol: protocol.OpenAIResponses, Kind: endpointForward}, wantOK: true},
		{name: "responses compact", method: http.MethodPost, path: "/v1/responses/compact", want: route{Protocol: protocol.OpenAIResponses, Kind: endpointForward}, wantOK: true},
		{name: "responses retrieve", method: http.MethodGet, path: "/v1/responses/resp_123", want: route{Protocol: protocol.OpenAIResponses, Kind: endpointForward}, wantOK: true},
		{name: "responses delete", method: http.MethodDelete, path: "/v1/responses/resp_123", want: route{Protocol: protocol.OpenAIResponses, Kind: endpointForward}, wantOK: true},
		{name: "responses head", method: http.MethodHead, path: "/v1/responses/resp_123", want: route{Protocol: protocol.OpenAIResponses, Kind: endpointForward}, wantOK: true},
		{name: "responses custom ordinary method", method: http.MethodPatch, path: "/v1/responses/vendor-extension", want: route{Protocol: protocol.OpenAIResponses, Kind: endpointForward}, wantOK: true},
		{name: "responses prefix collision", method: http.MethodPost, path: "/v1/responsesx", wantOK: false},
		{name: "responses parent dot segment", method: http.MethodGet, path: "/v1/responses/../models", wantOK: false},
		{name: "responses current dot segment", method: http.MethodGet, path: "/v1/responses/./resp_123", wantOK: false},
		{name: "responses options rejected locally", method: http.MethodOptions, path: "/v1/responses", wantOK: false},
		{name: "responses connect rejected locally", method: http.MethodConnect, path: "/v1/responses/resp_123", wantOK: false},
		{name: "responses trace rejected locally", method: http.MethodTrace, path: "/v1/responses/resp_123", wantOK: false},
		{name: "wrong method", method: http.MethodGet, path: "/v1/chat/completions", wantOK: false},
		{name: "empty Gemini model", method: http.MethodPost, path: "/v1beta/models/:generateContent", wantOK: false},
		{name: "Gemini nested model", method: http.MethodPost, path: "/v1beta/models/vendor/model:generateContent", wantOK: false},
		{name: "unknown", method: http.MethodPost, path: "/unknown", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := determineRoute(tt.method, tt.path, tt.headers)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("determineRoute() = (%#v, %t), want (%#v, %t)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestDetermineRouteIgnoresAuthenticationAndUserAgent(t *testing.T) {
	headers := http.Header{
		"Authorization": {"Bearer openai-looking"},
		"X-Api-Key":     {"anthropic-looking"},
		"User-Agent":    {"claude-cli"},
	}
	got, ok := determineRoute(http.MethodPost, "/v1/messages", headers)
	if !ok || got.Protocol != protocol.Anthropic {
		t.Fatalf("route = (%#v, %t), want Anthropic from path", got, ok)
	}
}
