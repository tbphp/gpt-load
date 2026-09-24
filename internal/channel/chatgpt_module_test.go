package channel

import (
	"reflect"
	"testing"

	"gpt-load/internal/channel/modules"
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestChatGPTModuleDeclaresFileImportSubscriptionContract(t *testing.T) {
	t.Parallel()

	definitions, err := compileBuiltInModules([]spec.Module{modules.ChatGPT()})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := newRegistry(definitions)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, ok := registry.Get(ChatGPT)
	if !ok {
		t.Fatal("ChatGPT descriptor is missing")
	}
	if descriptor.Connection.Type != string(spec.ConnectionSubscription) ||
		!reflect.DeepEqual(descriptor.Connection.AuthorizationMethods, []AuthorizationMethod{
			AuthorizationOAuthFile,
		}) {
		t.Fatalf("ChatGPT connection = %#v", descriptor.Connection)
	}
	target, err := registry.Resolve(ChatGPT, nil)
	if err != nil {
		t.Fatal(err)
	}
	if target.ProviderKind != ProviderChatGPT || target.CatalogProviderID != "" {
		t.Fatalf("ChatGPT target = %#v", target)
	}
	bindings, ok := registry.CapabilityBindings(ChatGPT)
	if !ok || bindings.SubscriptionDriver != modules.ChatGPTSubscriptionDriver ||
		bindings.ModelDiscovery != "" || bindings.QuotaObservation != "" {
		t.Fatalf("ChatGPT capabilities = %#v, %t", bindings, ok)
	}
	for _, test := range []struct {
		clientProtocol protocol.Protocol
		operation      execution.Operation
		mode           RouteMode
	}{
		{protocol.OpenAIResponses, execution.OperationResponsesCreate, RouteNative},
		{protocol.OpenAIResponses, execution.OperationResponsesInputTokens, RouteNative},
		{protocol.OpenAICompletions, execution.OperationChatCompletion, RouteConverted},
		{protocol.Anthropic, execution.OperationChatCompletion, RouteConverted},
		{protocol.Anthropic, execution.OperationCountTokens, RouteConverted},
		{protocol.Gemini, execution.OperationChatCompletion, RouteConverted},
		{protocol.Gemini, execution.OperationCountTokens, RouteConverted},
	} {
		if mode, exists := target.Mode(test.clientProtocol, test.operation); !exists || mode != test.mode {
			t.Fatalf("ChatGPT %q/%q mode = %q, %t; want %q", test.clientProtocol, test.operation, mode, exists, test.mode)
		}
	}
	if _, exists := target.Mode(protocol.OpenAIResponses, execution.OperationResponsesCompact); exists {
		t.Fatal("ChatGPT unexpectedly declares Responses Compact")
	}
}
