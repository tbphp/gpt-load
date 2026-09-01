package channel

import (
	"encoding/json"
	"reflect"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestGPTLoadChannelContract(t *testing.T) {
	t.Parallel()

	const gptLoad = ID("gpt_load")
	registry := NewRegistry()
	descriptor, ok := registry.Get(gptLoad)
	if !ok {
		t.Fatal("GPT-Load channel is not registered")
	}
	if descriptor.Name != "GPT-Load" || descriptor.Mark != "GL" || descriptor.Icon != "gpt-load" {
		t.Fatalf("GPT-Load identity = %#v", descriptor)
	}
	wantSearchTerms := []string{"gpt-load", "gptload", "gpt load", "gl", "gpt-load v2"}
	if !reflect.DeepEqual(descriptor.SearchTerms, wantSearchTerms) {
		t.Fatalf("GPT-Load search terms = %#v, want %#v", descriptor.SearchTerms, wantSearchTerms)
	}
	if descriptor.Connection.Type != "api_key" || descriptor.Connection.CredentialInput != "batch_text" {
		t.Fatalf("GPT-Load connection = %#v", descriptor.Connection)
	}
	if len(descriptor.ParamFields) != 1 {
		t.Fatalf("GPT-Load params = %#v", descriptor.ParamFields)
	}
	if field := descriptor.ParamFields[0]; field.Key != "base_url" || field.InputKind != InputURL || !field.Required || field.Sensitive {
		t.Fatalf("GPT-Load base_url field = %#v", field)
	}
	if len(descriptor.CredentialFields) != 1 {
		t.Fatalf("GPT-Load credentials = %#v", descriptor.CredentialFields)
	}
	if field := descriptor.CredentialFields[0]; field.Key != "api_key" || field.Label != "AccessKey" ||
		field.InputKind != InputSecret || !field.Required || !field.Sensitive {
		t.Fatalf("GPT-Load AccessKey field = %#v", field)
	}
	if !descriptor.Capabilities.ModelDiscovery || !descriptor.Capabilities.OutboundProxy {
		t.Fatalf("GPT-Load capabilities = %#v", descriptor.Capabilities)
	}

	target, err := registry.Resolve(gptLoad, json.RawMessage(`{"base_url":"HTTPS://GPT-LOAD.EXAMPLE:443/"}`))
	if err != nil {
		t.Fatalf("Resolve(GPT-Load) error = %v", err)
	}
	if target.ProviderKind != ProviderMultiProtocolGateway || target.CatalogProviderID != "" {
		t.Fatalf("GPT-Load target = %#v", target)
	}
	if got := string(target.TargetConfig); got != `{"base_url":"https://gpt-load.example"}` {
		t.Fatalf("GPT-Load target config = %s", got)
	}
	if !target.SupportsResponsesLifecycle() {
		t.Fatal("GPT-Load must advertise the complete Responses lifecycle")
	}

	wantRoutes := map[protocol.Protocol]map[execution.Operation]RouteMode{
		protocol.OpenAICompletions: {
			execution.OperationChatCompletion: RouteNative,
			execution.OperationListModels:     RouteNative,
			execution.OperationProbe:          RouteNative,
		},
		protocol.OpenAIResponses: {
			execution.OperationResponsesCreate:      RouteNative,
			execution.OperationResponsesRetrieve:    RouteNative,
			execution.OperationResponsesDelete:      RouteNative,
			execution.OperationResponsesCancel:      RouteNative,
			execution.OperationResponsesInputItems:  RouteNative,
			execution.OperationResponsesCompact:     RouteNative,
			execution.OperationResponsesInputTokens: RouteNative,
			execution.OperationResponsesPassthrough: RouteNative,
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
			execution.OperationCountTokens:    RouteNative,
			execution.OperationListModels:     RouteNative,
		},
		protocol.Gemini: {
			execution.OperationChatCompletion: RouteNative,
			execution.OperationCountTokens:    RouteNative,
			execution.OperationListModels:     RouteNative,
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
		t.Fatalf("GPT-Load routes = %#v, want %#v", gotRoutes, wantRoutes)
	}
}
