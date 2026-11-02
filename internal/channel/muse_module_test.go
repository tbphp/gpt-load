package channel

import (
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestMuseChannelExposesSubscriptionAndNativeResponses(t *testing.T) {
	registry := NewRegistry()
	descriptor, ok := registry.Get(Muse)
	if !ok {
		t.Fatal("Muse Code subscription channel is missing")
	}
	if descriptor.Name != "Muse Code" || descriptor.Connection.Type != "subscription" || len(descriptor.Connection.AuthorizationMethods) != 2 {
		t.Fatal("invalid Muse Code connection contract")
	}
	target, err := registry.Resolve(Muse, nil)
	if err != nil {
		t.Fatal(err)
	}
	if target.ProviderKind != ProviderMuse {
		t.Fatal("wrong Muse Code provider")
	}
	if mode, ok := target.Mode(protocol.OpenAIResponses, execution.OperationResponsesCreate); !ok || mode != RouteNative {
		t.Fatal("Muse Code must support native Responses")
	}
	for _, proto := range []protocol.Protocol{protocol.OpenAICompletions, protocol.Anthropic, protocol.Gemini} {
		if mode, ok := target.Mode(proto, execution.OperationChatCompletion); !ok || mode != RouteConverted {
			t.Fatalf("missing conversion for %s", proto)
		}
	}
}
