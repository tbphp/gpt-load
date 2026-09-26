package channel

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestOpenCodePresetsResolveNativeGatewayRoutes(t *testing.T) {
	registry := NewRegistry()
	for _, test := range []struct {
		id      ID
		baseURL string
	}{
		{"opencode_go", "https://opencode.ai/zen/go"},
		{"opencode_zen", "https://opencode.ai/zen"},
	} {
		t.Run(string(test.id), func(t *testing.T) {
			for _, override := range []string{"", "https://relay.example/team"} {
				params := json.RawMessage(`{}`)
				wantURL := test.baseURL
				if override != "" {
					params = json.RawMessage(`{"base_url":"` + override + `/"}`)
					wantURL = override
				}
				target, err := registry.Resolve(test.id, params)
				if err != nil {
					t.Fatal(err)
				}
				if target.ProviderKind != ProviderMultiProtocolGateway || string(target.TargetConfig) != `{"base_url":"`+wantURL+`"}` {
					t.Fatalf("unexpected gateway target: %+v", target)
				}
				for _, p := range []protocol.Protocol{protocol.OpenAICompletions, protocol.OpenAIResponses, protocol.Anthropic} {
					operation := execution.OperationChatCompletion
					if p == protocol.OpenAIResponses {
						operation = execution.OperationResponsesCreate
					}
					if mode, ok := target.Mode(p, operation); !ok || mode != RouteNative {
						t.Errorf("%s must use a native route, got %s / %t", p, mode, ok)
					}
				}
				if _, ok := target.Mode(protocol.Gemini, execution.OperationChatCompletion); ok {
					t.Error("Gemini must not be advertised without its upstream path adaptation")
				}
				if _, ok := target.Mode(protocol.OpenAIResponses, execution.OperationResponsesRetrieve); ok {
					t.Error("Responses resource operations must not be advertised")
				}
			}
			descriptor, _ := registry.Get(test.id)
			for _, route := range descriptor.Routes {
				if route.RouteMode != RouteNative {
					t.Errorf("unexpected conversion route: %+v", route)
				}
			}
		})
	}
}
