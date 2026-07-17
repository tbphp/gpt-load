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
		{name: "OpenAI chat", method: http.MethodPost, path: "/v1/chat/completions", want: route{Protocol: protocol.OpenAI, Kind: endpointChat}, wantOK: true},
		{name: "Anthropic chat", method: http.MethodPost, path: "/v1/messages", want: route{Protocol: protocol.Anthropic, Kind: endpointChat}, wantOK: true},
		{name: "Gemini generate", method: http.MethodPost, path: "/v1beta/models/gemini-2.5-pro:generateContent", want: route{Protocol: protocol.Gemini, Kind: endpointChat}, wantOK: true},
		{name: "Gemini stream", method: http.MethodPost, path: "/v1beta/models/gemini-2.5-pro:streamGenerateContent", want: route{Protocol: protocol.Gemini, Kind: endpointChat}, wantOK: true},
		{name: "Gemini models", method: http.MethodGet, path: "/v1beta/models", want: route{Protocol: protocol.Gemini, Kind: endpointModels}, wantOK: true},
		{name: "OpenAI models", method: http.MethodGet, path: "/v1/models", want: route{Protocol: protocol.OpenAI, Kind: endpointModels}, wantOK: true},
		{name: "Anthropic models", method: http.MethodGet, path: "/v1/models", headers: http.Header{"Anthropic-Version": {"2023-06-01"}}, want: route{Protocol: protocol.Anthropic, Kind: endpointModels}, wantOK: true},
		{name: "responses reserved", method: http.MethodPost, path: "/v1/responses", wantOK: false},
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
