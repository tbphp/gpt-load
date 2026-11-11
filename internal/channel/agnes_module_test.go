package channel

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestAgnesChannelDeclaresVideoRoutes(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	descriptor, ok := registry.Get(Agnes)
	if !ok {
		t.Fatal("Agnes channel is not registered")
	}
	if descriptor.Name != "Agnes AI" || descriptor.Mark != "AG" || descriptor.Icon != "agnes" {
		t.Fatalf("Agnes identity = %#v", descriptor)
	}
	if descriptor.Connection.Type != "api_key" || descriptor.Connection.CredentialInput != "batch_text" {
		t.Fatalf("Agnes connection = %#v", descriptor.Connection)
	}
	target, err := registry.Resolve(Agnes, json.RawMessage(`{"base_url":"https://apihub.agnes-ai.com/v1"}`))
	if err != nil {
		t.Fatalf("Resolve(Agnes) error = %v", err)
	}
	if target.ProviderKind != ProviderOpenAICompatible {
		t.Fatalf("Agnes provider = %q", target.ProviderKind)
	}
	for _, operation := range []execution.Operation{
		execution.OperationVideosCreate,
		execution.OperationVideosRetrieve,
		execution.OperationVideosContent,
	} {
		mode, ok := target.Mode(protocol.OpenAIVideos, operation)
		if !ok || mode != RouteNative {
			t.Fatalf("Agnes route %q = %q, %t", operation, mode, ok)
		}
	}
	if _, ok := target.Mode(protocol.OpenAIVideos, execution.OperationVideosDelete); ok {
		t.Fatal("Agnes must not advertise unsupported video cancel")
	}
}
