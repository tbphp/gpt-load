package channel

import (
	"encoding/json"
	"reflect"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestNewAPIChannelContract(t *testing.T) {
	t.Parallel()

	const newAPI = ID("newapi")
	registry := NewRegistry()
	descriptor, ok := registry.Get(newAPI)
	if !ok {
		t.Fatal("New API channel is not registered")
	}
	if descriptor.Name != "New API" || descriptor.Mark != "NA" || descriptor.Icon != "new-api" {
		t.Fatalf("New API identity = %#v", descriptor)
	}
	if descriptor.Connection.Type != "api_key" || descriptor.Connection.CredentialInput != "batch_text" {
		t.Fatalf("New API connection = %#v", descriptor.Connection)
	}
	if len(descriptor.ParamFields) != 1 {
		t.Fatalf("New API params = %#v", descriptor.ParamFields)
	}
	if field := descriptor.ParamFields[0]; field.Key != "base_url" || field.InputKind != InputURL || !field.Required || field.Sensitive {
		t.Fatalf("New API base_url field = %#v", field)
	}
	if len(descriptor.CredentialFields) != 1 {
		t.Fatalf("New API credentials = %#v", descriptor.CredentialFields)
	}
	if field := descriptor.CredentialFields[0]; field.Key != "api_key" || field.InputKind != InputSecret || !field.Required || !field.Sensitive {
		t.Fatalf("New API api_key field = %#v", field)
	}
	if !descriptor.Capabilities.ModelDiscovery || !descriptor.Capabilities.OutboundProxy {
		t.Fatalf("New API capabilities = %#v", descriptor.Capabilities)
	}

	target, err := registry.Resolve(newAPI, json.RawMessage(`{"base_url":"HTTPS://RELAY.EXAMPLE:443/team-a/"}`))
	if err != nil {
		t.Fatalf("Resolve(New API) error = %v", err)
	}
	if target.ProviderKind != ProviderMultiProtocolGateway || target.CatalogProviderID != "" {
		t.Fatalf("New API target = %#v", target)
	}
	if got := string(target.TargetConfig); got != `{"base_url":"https://relay.example/team-a"}` {
		t.Fatalf("New API target config = %s", got)
	}
	if target.SupportsResponsesLifecycle() {
		t.Fatal("New API must not advertise a complete Responses lifecycle")
	}
	if got := target.ResponsesStoreCompatibility(
		protocol.OpenAIResponses,
		execution.OperationResponsesCreate,
	); got != ResponsesStoreCompatibilityStateless {
		t.Fatalf("New API Responses create store compatibility = %q, want stateless", got)
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
		protocol.OpenAIEmbeddings: {
			execution.OperationEmbeddingsCreate: RouteNative,
			execution.OperationProbe:            RouteNative,
		},
		protocol.Anthropic: {
			execution.OperationChatCompletion: RouteNative,
		},
		protocol.Gemini: {
			execution.OperationChatCompletion: RouteNative,
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
		t.Fatalf("New API routes = %#v, want %#v", gotRoutes, wantRoutes)
	}
}
