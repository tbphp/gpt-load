package channel

import (
	"reflect"
	"testing"

	"gpt-load/internal/channel/modules"
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestGrokModuleDeclaresCompleteSubscriptionContract(t *testing.T) {
	t.Parallel()

	definitions, err := compileBuiltInModules([]spec.Module{modules.Grok()})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := newRegistry(definitions)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, ok := registry.Get(Grok)
	if !ok {
		t.Fatal("Grok descriptor is missing")
	}
	if descriptor.Connection.Type != string(spec.ConnectionSubscription) ||
		!reflect.DeepEqual(descriptor.Connection.AuthorizationMethods, []AuthorizationMethod{
			AuthorizationDeviceOAuth,
			AuthorizationOAuthFile,
		}) {
		t.Fatalf("Grok connection = %#v", descriptor.Connection)
	}
	target, err := registry.Resolve(Grok, nil)
	if err != nil {
		t.Fatal(err)
	}
	if target.ProviderKind != ProviderGrok || target.CatalogProviderID != "" {
		t.Fatalf("Grok target = %#v", target)
	}
	bindings, ok := registry.CapabilityBindings(Grok)
	if !ok || bindings.SubscriptionDriver != modules.GrokSubscriptionDriver ||
		bindings.ModelDiscovery != modules.GrokModelDiscovery ||
		bindings.QuotaObservation != modules.GrokQuotaObservation {
		t.Fatalf("Grok capabilities = %#v, %t", bindings, ok)
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
			t.Fatalf("Grok %q/%q mode = %q, %t; want %q", test.clientProtocol, test.operation, mode, exists, test.mode)
		}
	}
	if _, exists := target.Mode(protocol.OpenAIResponses, execution.OperationResponsesCompact); exists {
		t.Fatal("Grok unexpectedly declares Responses Compact")
	}
}
