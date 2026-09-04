package channel

import (
	"encoding/json"
	"reflect"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestSub2APIChannelContract(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	descriptor, ok := registry.Get(Sub2API)
	if !ok {
		t.Fatal("Sub2API channel is not registered")
	}
	if descriptor.Name != "Sub2API" || descriptor.Mark != "S2A" || descriptor.Icon != "sub2api" {
		t.Fatalf("Sub2API identity = %#v", descriptor)
	}
	if !reflect.DeepEqual(descriptor.SearchTerms, []string{"sub 2 api", "s2a"}) {
		t.Fatalf("Sub2API search terms = %#v", descriptor.SearchTerms)
	}
	if descriptor.Connection.Type != "api_key" || descriptor.Connection.CredentialInput != "batch_text" {
		t.Fatalf("Sub2API connection = %#v", descriptor.Connection)
	}
	if len(descriptor.ParamFields) != 1 {
		t.Fatalf("Sub2API params = %#v", descriptor.ParamFields)
	}
	if field := descriptor.ParamFields[0]; field.Key != "base_url" || field.InputKind != InputURL || !field.Required || field.Sensitive {
		t.Fatalf("Sub2API base_url field = %#v", field)
	}
	if len(descriptor.CredentialFields) != 1 {
		t.Fatalf("Sub2API credentials = %#v", descriptor.CredentialFields)
	}
	if field := descriptor.CredentialFields[0]; field.Key != "api_key" || field.InputKind != InputSecret || !field.Required || !field.Sensitive {
		t.Fatalf("Sub2API api_key field = %#v", field)
	}
	if !descriptor.Capabilities.ModelDiscovery || !descriptor.Capabilities.OutboundProxy {
		t.Fatalf("Sub2API capabilities = %#v", descriptor.Capabilities)
	}

	target, err := registry.Resolve(Sub2API, json.RawMessage(`{"base_url":"HTTPS://SUB2API.EXAMPLE:443/team-a/"}`))
	if err != nil {
		t.Fatalf("Resolve(Sub2API) error = %v", err)
	}
	if target.ProviderKind != ProviderMultiProtocolGateway || target.CatalogProviderID != "" {
		t.Fatalf("Sub2API target = %#v", target)
	}
	if got := string(target.TargetConfig); got != `{"base_url":"https://sub2api.example/team-a"}` {
		t.Fatalf("Sub2API target config = %s", got)
	}
	if target.SupportsResponsesLifecycle() {
		t.Fatal("Sub2API must not advertise a complete Responses lifecycle")
	}

	wantRoutes := map[protocol.Protocol]map[execution.Operation]RouteMode{
		protocol.OpenAICompletions: {
			execution.OperationChatCompletion: RouteNative,
			execution.OperationListModels:     RouteNative,
			execution.OperationProbe:          RouteNative,
		},
		protocol.OpenAIResponses: {
			execution.OperationResponsesCreate:  RouteNative,
			execution.OperationResponsesCompact: RouteNative,
		},
		protocol.OpenAIImages: {
			execution.OperationImagesGenerate: RouteNative,
			execution.OperationImagesEdit:     RouteNative,
		},
		protocol.Anthropic: {
			execution.OperationChatCompletion: RouteNative,
			execution.OperationCountTokens:    RouteNative,
		},
		protocol.Gemini: {
			execution.OperationChatCompletion: RouteNative,
			execution.OperationCountTokens:    RouteNative,
		},
	}
	gotRoutes := make(map[protocol.Protocol]map[execution.Operation]RouteMode)
	for _, route := range descriptor.Routes {
		if gotRoutes[route.ClientProtocol] == nil {
			gotRoutes[route.ClientProtocol] = make(map[execution.Operation]RouteMode)
		}
		gotRoutes[route.ClientProtocol][route.Operation] = route.RouteMode
	}
	if !reflect.DeepEqual(gotRoutes, wantRoutes) {
		t.Fatalf("Sub2API routes = %#v, want %#v", gotRoutes, wantRoutes)
	}
}
