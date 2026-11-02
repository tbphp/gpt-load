package channel

import (
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestDevinSubscriptionUsesExplicitConvertedRoutes(t *testing.T) {
	registry := NewRegistry()
	descriptor, ok := registry.Get(ID("devin"))
	if !ok {
		t.Fatal("Devin subscription channel is missing")
	}
	if descriptor.Name != "Devin" || descriptor.Connection.Type != "subscription" || len(descriptor.Connection.AuthorizationMethods) != 2 {
		t.Fatal("invalid Devin authorization contract")
	}
	target, err := registry.Resolve(ID("devin"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if target.ProviderKind != ProviderKind("devin") {
		t.Fatal("wrong Devin provider")
	}
	for _, proto := range []protocol.Protocol{protocol.OpenAICompletions, protocol.Anthropic, protocol.Gemini, protocol.OpenAIResponses} {
		operation := execution.OperationChatCompletion
		if proto == protocol.OpenAIResponses {
			operation = execution.OperationResponsesCreate
		}
		if mode, ok := target.Mode(proto, operation); !ok || mode != RouteConverted {
			t.Fatalf("missing explicit Devin conversion for %s", proto)
		}
		if _, ok := target.Mode(proto, execution.OperationCountTokens); ok {
			t.Fatal("Devin must not advertise its byte-length token estimate as token counting")
		}
	}
}
