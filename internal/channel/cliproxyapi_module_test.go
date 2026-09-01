package channel

import (
	"encoding/json"
	"reflect"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestCLIProxyAPIChannelContract(t *testing.T) {
	t.Parallel()

	const cliProxyAPI = ID("cliproxyapi")
	registry := NewRegistry()
	descriptor, ok := registry.Get(cliProxyAPI)
	if !ok {
		t.Fatal("CLIProxyAPI channel is not registered")
	}
	if descriptor.Name != "CLIProxyAPI" || descriptor.Mark != "CPA" || descriptor.Icon != "cpa" {
		t.Fatalf("CLIProxyAPI identity = %#v", descriptor)
	}
	if !reflect.DeepEqual(descriptor.SearchTerms, []string{"cpa", "cli proxy api"}) {
		t.Fatalf("CLIProxyAPI search terms = %#v", descriptor.SearchTerms)
	}
	if descriptor.Connection.Type != "api_key" || descriptor.Connection.CredentialInput != "batch_text" {
		t.Fatalf("CLIProxyAPI connection = %#v", descriptor.Connection)
	}
	if len(descriptor.ParamFields) != 1 {
		t.Fatalf("CLIProxyAPI params = %#v", descriptor.ParamFields)
	}
	if field := descriptor.ParamFields[0]; field.Key != "base_url" || field.InputKind != InputURL || !field.Required || field.Sensitive {
		t.Fatalf("CLIProxyAPI base_url field = %#v", field)
	}
	if len(descriptor.CredentialFields) != 1 {
		t.Fatalf("CLIProxyAPI credentials = %#v", descriptor.CredentialFields)
	}
	if field := descriptor.CredentialFields[0]; field.Key != "api_key" || field.InputKind != InputSecret || !field.Required || !field.Sensitive {
		t.Fatalf("CLIProxyAPI api_key field = %#v", field)
	}
	if !descriptor.Capabilities.ModelDiscovery || !descriptor.Capabilities.OutboundProxy {
		t.Fatalf("CLIProxyAPI capabilities = %#v", descriptor.Capabilities)
	}

	target, err := registry.Resolve(cliProxyAPI, json.RawMessage(`{"base_url":"HTTPS://CPA.EXAMPLE:443/team-a/"}`))
	if err != nil {
		t.Fatalf("Resolve(CLIProxyAPI) error = %v", err)
	}
	if target.ProviderKind != ProviderMultiProtocolGateway || target.CatalogProviderID != "" {
		t.Fatalf("CLIProxyAPI target = %#v", target)
	}
	if got := string(target.TargetConfig); got != `{"base_url":"https://cpa.example/team-a"}` {
		t.Fatalf("CLIProxyAPI target config = %s", got)
	}
	if target.SupportsResponsesLifecycle() {
		t.Fatal("CLIProxyAPI must not advertise a complete Responses lifecycle")
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
		t.Fatalf("CLIProxyAPI routes = %#v, want %#v", gotRoutes, wantRoutes)
	}
}
