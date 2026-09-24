package channel

import (
	"reflect"
	"testing"

	"gpt-load/internal/channel/modules"
	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestMirasimModuleDeclaresOAuthFileAndModelWire(t *testing.T) {
	t.Parallel()

	definitions, err := compileBuiltInModules([]spec.Module{modules.Mirasim()})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := newRegistry(definitions)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, ok := registry.Get(Mirasim)
	if !ok {
		t.Fatal("Mirasim descriptor is missing")
	}
	if descriptor.Connection.Type != string(spec.ConnectionSubscription) ||
		!reflect.DeepEqual(descriptor.Connection.AuthorizationMethods, []AuthorizationMethod{
			AuthorizationBrowserOAuth,
			AuthorizationOAuthFile,
		}) {
		t.Fatalf("Mirasim connection = %#v", descriptor.Connection)
	}
	target, err := registry.Resolve(Mirasim, nil)
	if err != nil {
		t.Fatal(err)
	}
	if target.ProviderKind != ProviderMirasim || target.CatalogProviderID != "" {
		t.Fatalf("Mirasim target = %#v", target)
	}
	if mode, ok := target.ModeForModel(protocol.Anthropic, execution.OperationChatCompletion, "claude-sonnet-5"); !ok || mode != RouteNative {
		t.Fatalf("claude anthropic mode = %q, %t", mode, ok)
	}
	if mode, ok := target.ModeForModel(protocol.OpenAIResponses, execution.OperationResponsesCreate, "gpt-5.6"); !ok || mode != RouteNative {
		t.Fatalf("gpt responses mode = %q, %t", mode, ok)
	}
	if mode, ok := target.ModeForModel(protocol.Anthropic, execution.OperationChatCompletion, "gpt-5.6"); !ok || mode != RouteConverted {
		t.Fatalf("gpt anthropic mode = %q, %t", mode, ok)
	}
	if mode, ok := target.ModeForModel(protocol.OpenAIResponses, execution.OperationResponsesCreate, "claude-sonnet-5"); !ok || mode != RouteConverted {
		t.Fatalf("claude responses mode = %q, %t", mode, ok)
	}
}
