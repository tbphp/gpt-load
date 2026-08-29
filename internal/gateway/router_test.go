package gateway

import (
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"gpt-load/internal/protocol"
)

func TestDataPlaneEndpointCatalogDeclaresCompleteHTTPRoutes(t *testing.T) {
	registeredResponsesMethods := []string{
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
	want := []struct {
		name    string
		methods []string
		path    string
	}{
		{
			name:    "data.openai.completions",
			methods: []string{http.MethodPost},
			path:    "/v1/chat/completions",
		},
		{
			name:    "data.anthropic.messages",
			methods: []string{http.MethodPost},
			path:    "/v1/messages",
		},
		{
			name:    "data.anthropic.count_tokens",
			methods: []string{http.MethodPost},
			path:    "/v1/messages/count_tokens",
		},
		{
			name:    "data.gemini.generate",
			methods: []string{http.MethodPost},
			path:    "/v1beta/models/:model_action",
		},
		{
			name:    "data.gemini.models",
			methods: []string{http.MethodGet},
			path:    "/v1beta/models",
		},
		{
			name:    "data.models",
			methods: []string{http.MethodGet},
			path:    "/v1/models",
		},
		{
			name:    "data.openai.responses",
			methods: registeredResponsesMethods,
			path:    "/v1/responses",
		},
		{
			name:    "data.openai.responses.resource",
			methods: registeredResponsesMethods,
			path:    "/v1/responses/*resource_path",
		},
		{
			name:    "data.openai.images.generations",
			methods: []string{http.MethodPost},
			path:    "/v1/images/generations",
		},
		{
			name:    "data.openai.images.edits",
			methods: []string{http.MethodPost},
			path:    "/v1/images/edits",
		},
		{
			name:    "data.openai.embeddings",
			methods: []string{http.MethodPost},
			path:    "/v1/embeddings",
		},
	}

	catalog := dataPlaneEndpointCatalog()
	if len(catalog) != len(want) {
		t.Fatalf("catalog length = %d, want %d", len(catalog), len(want))
	}
	for index, expected := range want {
		got := catalog[index]
		if got.name != expected.name ||
			got.path != expected.path ||
			!reflect.DeepEqual(got.methods, expected.methods) {
			t.Fatalf(
				"catalog[%d] = %q %v %q, want %q %v %q",
				index,
				got.name,
				got.methods,
				got.path,
				expected.name,
				expected.methods,
				expected.path,
			)
		}
	}

	module := (&Handler{}).HTTPModule()
	if len(module.Routes) != len(want) {
		t.Fatalf("HTTP module routes = %d, want %d", len(module.Routes), len(want))
	}
	for index, expected := range want {
		got := module.Routes[index]
		if got.Name != expected.name ||
			got.Path != expected.path ||
			!reflect.DeepEqual(got.Methods, expected.methods) {
			t.Fatalf(
				"HTTP module route[%d] = %q %v %q, want %q %v %q",
				index,
				got.Name,
				got.Methods,
				got.Path,
				expected.name,
				expected.methods,
				expected.path,
			)
		}
	}
}

func TestDataPlaneEndpointCatalogResolvesProtocolAndKind(t *testing.T) {
	tests := []struct {
		name      string
		endpoint  string
		method    string
		path      string
		headers   http.Header
		want      route
		validPath bool
	}{
		{
			name: "OpenAI chat", endpoint: "data.openai.completions",
			method: http.MethodPost, path: "/v1/chat/completions",
			want:      route{Protocol: protocol.OpenAICompletions, Kind: endpointForward},
			validPath: true,
		},
		{
			name: "Anthropic chat", endpoint: "data.anthropic.messages",
			method: http.MethodPost, path: "/v1/messages",
			want:      route{Protocol: protocol.Anthropic, Kind: endpointForward},
			validPath: true,
		},
		{
			name: "Anthropic count tokens", endpoint: "data.anthropic.count_tokens",
			method: http.MethodPost, path: "/v1/messages/count_tokens",
			want:      route{Protocol: protocol.Anthropic, Kind: endpointForward},
			validPath: true,
		},
		{
			name: "Gemini generate", endpoint: "data.gemini.generate",
			method: http.MethodPost, path: "/v1beta/models/gemini-2.5-pro:generateContent",
			want:      route{Protocol: protocol.Gemini, Kind: endpointForward},
			validPath: true,
		},
		{
			name: "Gemini stream", endpoint: "data.gemini.generate",
			method: http.MethodPost, path: "/v1beta/models/gemini-2.5-pro:streamGenerateContent",
			want:      route{Protocol: protocol.Gemini, Kind: endpointForward},
			validPath: true,
		},
		{
			name: "Gemini count tokens", endpoint: "data.gemini.generate",
			method: http.MethodPost, path: "/v1beta/models/gemini-2.5-pro:countTokens",
			want:      route{Protocol: protocol.Gemini, Kind: endpointForward},
			validPath: true,
		},
		{
			name: "Gemini models", endpoint: "data.gemini.models",
			method: http.MethodGet, path: "/v1beta/models",
			want:      route{Protocol: protocol.Gemini, Kind: endpointModels},
			validPath: true,
		},
		{
			name: "OpenAI models", endpoint: "data.models",
			method: http.MethodGet, path: "/v1/models",
			want:      route{Protocol: protocol.OpenAICompletions, Kind: endpointModels},
			validPath: true,
		},
		{
			name: "Anthropic models", endpoint: "data.models",
			method: http.MethodGet, path: "/v1/models",
			headers:   http.Header{"Anthropic-Version": {"2023-06-01"}},
			want:      route{Protocol: protocol.Anthropic, Kind: endpointModels},
			validPath: true,
		},
		{
			name: "Responses root", endpoint: "data.openai.responses",
			method: http.MethodPost, path: "/v1/responses",
			want:      route{Protocol: protocol.OpenAIResponses, Kind: endpointForward},
			validPath: true,
		},
		{
			name: "Responses nested extension", endpoint: "data.openai.responses.resource",
			method: http.MethodPatch, path: "/v1/responses/vendor-extension/nested",
			want:      route{Protocol: protocol.OpenAIResponses, Kind: endpointForward},
			validPath: true,
		},
		{
			name: "Images generation", endpoint: "data.openai.images.generations",
			method: http.MethodPost, path: "/v1/images/generations",
			want:      route{Protocol: protocol.OpenAIImages, Kind: endpointForward},
			validPath: true,
		},
		{
			name: "Images edit", endpoint: "data.openai.images.edits",
			method: http.MethodPost, path: "/v1/images/edits",
			want:      route{Protocol: protocol.OpenAIImages, Kind: endpointForward},
			validPath: true,
		},
		{
			name: "OpenAI embeddings", endpoint: "data.openai.embeddings",
			method: http.MethodPost, path: "/v1/embeddings",
			want:      route{Protocol: protocol.OpenAIEmbeddings, Kind: endpointForward},
			validPath: true,
		},
	}

	catalog := dataPlaneEndpointCatalog()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			endpoint := findDataPlaneEndpointForTest(t, catalog, test.endpoint)
			request, err := http.NewRequest(test.method, test.path, nil)
			if err != nil {
				t.Fatalf("NewRequest() error = %v", err)
			}
			request.Header = test.headers.Clone()
			if endpoint.pathValidator != nil {
				gotValid := endpoint.pathValidator(request)
				if gotValid != test.validPath {
					t.Fatalf("path validator = %t, want %t", gotValid, test.validPath)
				}
			}
			if got := endpoint.resolve(request); got != test.want {
				t.Fatalf("resolve() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestDataPlaneEndpointCatalogRejectsMalformedPreAuthPaths(t *testing.T) {
	catalog := dataPlaneEndpointCatalog()
	tests := []struct {
		name     string
		endpoint string
		path     string
	}{
		{
			name: "Gemini missing action", endpoint: "data.gemini.generate",
			path: "/v1beta/models/missing-action",
		},
		{
			name: "Gemini empty model", endpoint: "data.gemini.generate",
			path: "/v1beta/models/:generateContent",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			endpoint := findDataPlaneEndpointForTest(t, catalog, test.endpoint)
			if endpoint.pathValidator == nil {
				t.Fatal("path validator = nil")
			}
			request := &http.Request{URL: mustURLForRouteTest(t, test.path)}
			if endpoint.pathValidator(request) {
				t.Fatalf("path validator accepted %q", test.path)
			}
		})
	}
}

func TestDataPlaneEndpointCatalogRejectsResponsesLocallyAfterAuthentication(t *testing.T) {
	catalog := dataPlaneEndpointCatalog()
	for _, test := range []struct {
		name     string
		endpoint string
		method   string
		path     string
		rejected bool
	}{
		{
			name: "valid resource", endpoint: "data.openai.responses.resource",
			method: http.MethodGet, path: "/v1/responses/resp_123",
		},
		{
			name: "parent segment", endpoint: "data.openai.responses.resource",
			method: http.MethodGet, path: "/v1/responses/../models", rejected: true,
		},
		{
			name: "current segment", endpoint: "data.openai.responses.resource",
			method: http.MethodGet, path: "/v1/responses/./resp_123", rejected: true,
		},
		{
			name: "options", endpoint: "data.openai.responses",
			method: http.MethodOptions, path: "/v1/responses", rejected: true,
		},
		{
			name: "connect", endpoint: "data.openai.responses.resource",
			method: http.MethodConnect, path: "/v1/responses/resp_123", rejected: true,
		},
		{
			name: "trace", endpoint: "data.openai.responses.resource",
			method: http.MethodTrace, path: "/v1/responses/resp_123", rejected: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			endpoint := findDataPlaneEndpointForTest(t, catalog, test.endpoint)
			if endpoint.rejectAfterAuth == nil {
				t.Fatal("rejectAfterAuth = nil")
			}
			request := &http.Request{
				Method: test.method,
				URL:    mustURLForRouteTest(t, test.path),
			}
			if got := endpoint.rejectAfterAuth(request); got != test.rejected {
				t.Fatalf("rejectAfterAuth() = %t, want %t", got, test.rejected)
			}
		})
	}
}

func TestDataPlaneEndpointResolutionIgnoresAuthenticationAndUserAgent(t *testing.T) {
	headers := http.Header{
		"Authorization": {"Bearer openai-looking"},
		"X-Api-Key":     {"anthropic-looking"},
		"User-Agent":    {"claude-cli"},
	}
	endpoint := findDataPlaneEndpointForTest(
		t,
		dataPlaneEndpointCatalog(),
		"data.anthropic.messages",
	)
	request, err := http.NewRequest(http.MethodPost, "/v1/messages", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	request.Header = headers

	got := endpoint.resolve(request)
	if got.Protocol != protocol.Anthropic {
		t.Fatalf("route = %#v, want Anthropic from catalog", got)
	}
}

func findDataPlaneEndpointForTest(
	t *testing.T,
	catalog []dataPlaneEndpoint,
	name string,
) dataPlaneEndpoint {
	t.Helper()
	for _, endpoint := range catalog {
		if endpoint.name == name {
			return endpoint
		}
	}
	t.Fatalf("endpoint %q not found", name)
	return dataPlaneEndpoint{}
}

func mustURLForRouteTest(t *testing.T, path string) *url.URL {
	t.Helper()
	got, err := url.Parse(path)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	return got
}
