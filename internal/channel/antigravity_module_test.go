package channel

import (
	"reflect"
	"testing"

	"gpt-load/internal/channel/modules"
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestAntigravityModuleDeclaresSubscriptionContract(t *testing.T) {
	t.Parallel()

	definitions, err := compileBuiltInModules([]spec.Module{modules.Antigravity()})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := newRegistry(definitions)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, ok := registry.Get(Antigravity)
	if !ok {
		t.Fatal("Antigravity descriptor is missing")
	}
	if descriptor.Connection.Type != string(spec.ConnectionSubscription) ||
		!reflect.DeepEqual(descriptor.Connection.AuthorizationMethods, []AuthorizationMethod{
			AuthorizationBrowserOAuth,
			AuthorizationOAuthFile,
		}) {
		t.Fatalf("Antigravity connection = %#v", descriptor.Connection)
	}
	if !reflect.DeepEqual(descriptor.Notices, []NoticeDescriptor{{
		ID: NoticeAntigravityOAuthRisk, Tone: NoticeToneWarning,
	}}) {
		t.Fatalf("Antigravity notices = %#v", descriptor.Notices)
	}
	target, err := registry.Resolve(Antigravity, nil)
	if err != nil {
		t.Fatal(err)
	}
	if target.ProviderKind != ProviderAntigravity || target.CatalogProviderID != "" {
		t.Fatalf("Antigravity target = %#v", target)
	}
	bindings, ok := registry.CapabilityBindings(Antigravity)
	if !ok {
		t.Fatal("Antigravity capability bindings are missing")
	}
	if bindings.SubscriptionDriver != modules.AntigravitySubscriptionDriver ||
		bindings.ModelDiscovery != modules.AntigravityModelDiscovery ||
		bindings.QuotaObservation != modules.AntigravityQuotaObservation ||
		bindings.ResetCreditAction != "" {
		t.Fatalf("Antigravity capabilities = %#v", bindings)
	}
	policy, ok := registry.SchedulingPolicy(Antigravity)
	if !ok || policy.QuotaPriority {
		t.Fatal("Antigravity unexpectedly enables quota-priority scheduling")
	}
	for _, test := range []struct {
		clientProtocol protocol.Protocol
		operation      execution.Operation
		mode           RouteMode
	}{
		{protocol.Gemini, execution.OperationChatCompletion, RouteNative},
		{protocol.Gemini, execution.OperationCountTokens, RouteNative},
		{protocol.Anthropic, execution.OperationChatCompletion, RouteConverted},
		{protocol.Anthropic, execution.OperationCountTokens, RouteConverted},
		{protocol.OpenAICompletions, execution.OperationChatCompletion, RouteConverted},
		{protocol.OpenAIResponses, execution.OperationResponsesCreate, RouteConverted},
		{protocol.OpenAIResponses, execution.OperationResponsesInputTokens, RouteConverted},
	} {
		if mode, exists := target.Mode(test.clientProtocol, test.operation); !exists || mode != test.mode {
			t.Fatalf("Antigravity %q/%q mode = %q, %t; want %q", test.clientProtocol, test.operation, mode, exists, test.mode)
		}
	}
	if _, exists := target.Mode(protocol.OpenAIResponses, execution.OperationResponsesCompact); exists {
		t.Fatal("Antigravity unexpectedly declares responses compact")
	}
}
