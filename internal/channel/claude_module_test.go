package channel

import (
	"reflect"
	"testing"

	"gpt-load/internal/channel/modules"
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestClaudeModuleDeclaresSubscriptionContract(t *testing.T) {
	t.Parallel()

	definitions, err := compileBuiltInModules([]spec.Module{modules.Claude()})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := newRegistry(definitions)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, ok := registry.Get(Claude)
	if !ok {
		t.Fatal("Claude descriptor is missing")
	}
	if descriptor.Connection.Type != string(spec.ConnectionSubscription) ||
		!reflect.DeepEqual(descriptor.Connection.AuthorizationMethods, []AuthorizationMethod{
			AuthorizationBrowserOAuth,
			AuthorizationOAuthFile,
		}) {
		t.Fatalf("Claude connection = %#v", descriptor.Connection)
	}
	if !reflect.DeepEqual(descriptor.Notices, []NoticeDescriptor{{
		ID: NoticeClaudeOAuthRisk, Tone: NoticeToneWarning,
	}}) {
		t.Fatalf("Claude notices = %#v", descriptor.Notices)
	}
	target, err := registry.Resolve(Claude, nil)
	if err != nil {
		t.Fatal(err)
	}
	if target.ProviderKind != ProviderClaude || target.CatalogProviderID != "" {
		t.Fatalf("Claude target = %#v", target)
	}
	bindings, ok := registry.CapabilityBindings(Claude)
	if !ok {
		t.Fatal("Claude capability bindings are missing")
	}
	if bindings.SubscriptionDriver != modules.ClaudeSubscriptionDriver ||
		bindings.ModelDiscovery != modules.ClaudeModelDiscovery ||
		bindings.QuotaObservation != modules.ClaudeQuotaObservation ||
		bindings.ResetCreditAction != "" {
		t.Fatalf("Claude capabilities = %#v", bindings)
	}
	policy, ok := registry.SchedulingPolicy(Claude)
	if !ok || !policy.QuotaPriority {
		t.Fatal("Claude quota-priority scheduling is disabled")
	}
	wantRoutes := map[protocol.Protocol]RouteMode{
		protocol.Anthropic:         RouteNative,
		protocol.OpenAICompletions: RouteConverted,
		protocol.OpenAIResponses:   RouteConverted,
		protocol.Gemini:            RouteConverted,
	}
	for clientProtocol, wantMode := range wantRoutes {
		operation := execution.OperationChatCompletion
		if clientProtocol == protocol.OpenAIResponses {
			operation = execution.OperationResponsesCreate
		}
		if got, exists := target.Mode(clientProtocol, operation); !exists || got != wantMode {
			t.Fatalf("Claude %q/%q mode = %q, %t; want %q", clientProtocol, operation, got, exists, wantMode)
		}
	}
	if _, exists := target.Mode(protocol.OpenAIResponses, execution.OperationResponsesCompact); exists {
		t.Fatal("Claude unexpectedly declares responses compact")
	}
}

func TestBuiltInCountTokensRouteMatrix(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	countTokens := execution.OperationCountTokens
	tests := []struct {
		channelID      ID
		clientProtocol protocol.Protocol
		operation      execution.Operation
		wantMode       RouteMode
		want           bool
	}{
		{Claude, protocol.Anthropic, countTokens, RouteNative, true},
		{Claude, protocol.Gemini, countTokens, RouteConverted, true},
		{Claude, protocol.OpenAIResponses, execution.OperationResponsesInputTokens, RouteConverted, true},
		{Anthropic, protocol.Anthropic, countTokens, RouteNative, true},
		{Anthropic, protocol.Gemini, countTokens, RouteConverted, true},
		{Anthropic, protocol.OpenAIResponses, execution.OperationResponsesInputTokens, RouteConverted, true},
		{Gemini, protocol.Gemini, countTokens, RouteNative, true},
		{Gemini, protocol.Anthropic, countTokens, RouteConverted, true},
		{Gemini, protocol.OpenAIResponses, execution.OperationResponsesInputTokens, RouteConverted, true},
		{OpenAI, protocol.OpenAIResponses, execution.OperationResponsesInputTokens, RouteNative, true},
		{OpenAI, protocol.Anthropic, countTokens, RouteConverted, true},
		{OpenAI, protocol.Gemini, countTokens, RouteConverted, true},
		{Codex, protocol.OpenAIResponses, execution.OperationResponsesInputTokens, RouteNative, true},
		{Codex, protocol.Anthropic, countTokens, RouteConverted, true},
		{Codex, protocol.Gemini, countTokens, RouteConverted, true},
	}
	for _, test := range tests {
		t.Run(string(test.channelID)+"/"+string(test.clientProtocol)+"/"+string(test.operation), func(t *testing.T) {
			target, err := registry.Resolve(test.channelID, nil)
			if err != nil {
				t.Fatalf("Resolve(%q) error = %v", test.channelID, err)
			}
			gotMode, got := target.Mode(test.clientProtocol, test.operation)
			if got != test.want || gotMode != test.wantMode {
				t.Fatalf("Mode() = %q, %t; want %q, %t", gotMode, got, test.wantMode, test.want)
			}
		})
	}
}
