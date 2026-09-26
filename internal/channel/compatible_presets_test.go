package channel

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestCompatiblePresetsReuseExistingRoutesAndCredentials(t *testing.T) {
	registry := NewRegistry()
	for _, test := range []struct {
		id         ID
		baseURL    string
		embeddings bool
		rerank     bool
	}{
		{id: "cerebras", baseURL: "https://api.cerebras.ai/v1"},
		{id: "mistral", baseURL: "https://api.mistral.ai/v1", embeddings: true},
		{id: "nebius", baseURL: "https://api.tokenfactory.nebius.com/v1", embeddings: true},
		{id: "parasail", baseURL: "https://api.parasail.io/v1"},
		{id: "wafer", baseURL: "https://pass.wafer.ai/v1"},
		{id: "huggingface", baseURL: "https://router.huggingface.co/v1"},
		{id: "cohere", baseURL: "https://api.cohere.ai/v2", rerank: true},
	} {
		t.Run(string(test.id), func(t *testing.T) {
			descriptor, ok := registry.Get(test.id)
			if !ok {
				t.Fatal("channel is not registered")
			}
			if descriptor.Connection.Type != "api_key" || descriptor.Connection.CredentialInput != "batch_text" ||
				len(descriptor.CredentialFields) != 1 || descriptor.CredentialFields[0].Key != "api_key" ||
				!descriptor.CredentialFields[0].Required || !descriptor.CredentialFields[0].Sensitive {
				t.Fatalf("unexpected credential contract: %+v / %+v", descriptor.Connection, descriptor.CredentialFields)
			}
			for _, params := range []json.RawMessage{json.RawMessage(`{}`), json.RawMessage(`{"base_url":"https://relay.example/custom/v1"}`)} {
				resolved, err := registry.Resolve(test.id, params)
				if err != nil {
					t.Fatal(err)
				}
				var target struct {
					BaseURL string `json:"base_url"`
				}
				if err := json.Unmarshal(resolved.TargetConfig, &target); err != nil {
					t.Fatal(err)
				}
				wantURL := test.baseURL
				if string(params) != `{}` {
					wantURL = "https://relay.example/custom/v1"
				}
				if resolved.ProviderKind != ProviderOpenAICompatible || target.BaseURL != wantURL {
					t.Fatalf("target = %s / %s, want existing compatible adapter at %s", resolved.ProviderKind, target.BaseURL, wantURL)
				}
			}

			allowed := map[protocol.Protocol]bool{protocol.Rerank: true}
			if !test.rerank {
				allowed = map[protocol.Protocol]bool{
					protocol.OpenAICompletions: true, protocol.OpenAIResponses: true,
					protocol.Anthropic: true, protocol.Gemini: true,
				}
				if test.embeddings {
					allowed[protocol.OpenAIEmbeddings] = true
				}
			}
			if len(descriptor.ClientProtocols) != len(allowed) {
				t.Fatalf("protocols = %v", descriptor.ClientProtocols)
			}
			for _, route := range descriptor.Routes {
				if !allowed[route.ClientProtocol] {
					t.Errorf("unexpected protocol %s", route.ClientProtocol)
				}
				if test.rerank && (route.ClientProtocol != protocol.Rerank ||
					(route.Operation != execution.OperationRerank && route.Operation != execution.OperationProbe) || route.RouteMode != RouteNative) {
					t.Errorf("unexpected Cohere route %+v", route)
				}
				if route.ClientProtocol == protocol.OpenAIResponses && route.RouteMode != RouteConverted {
					t.Errorf("Responses must use existing Chat conversion: %+v", route)
				}
			}
		})
	}
}
