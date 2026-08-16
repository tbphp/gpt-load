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
	if target.ProviderKind != ProviderClaude || target.CatalogProviderID != "anthropic" {
		t.Fatalf("Claude target = %#v", target)
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
