package cpa

import (
	"testing"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestAdapterValidatesAllDeclaredMirasimRoutes(t *testing.T) {
	registry := channel.NewRegistry()
	adapter := NewAdapter(nil, registry)
	descriptor, ok := registry.Get(channel.Mirasim)
	if !ok {
		t.Fatal("Mirasim channel is missing")
	}
	for _, route := range descriptor.Routes {
		if err := adapter.ValidateRouteCapability(channel.ProviderMirasim, route); err != nil {
			t.Fatalf("ValidateRouteCapability(%#v) error = %v", route, err)
		}
		for _, mode := range route.PossibleModes {
			candidate := route
			candidate.RouteMode = mode
			if err := adapter.ValidateRouteCapability(channel.ProviderMirasim, candidate); err != nil {
				t.Fatalf("ValidateRouteCapability(%#v) error = %v", candidate, err)
			}
		}
	}
	if err := adapter.ValidateRouteCapability(channel.ProviderMirasim, channel.RouteDescriptor{
		ClientProtocol: protocol.OpenAIResponses,
		Operation:      execution.OperationResponsesCompact,
		RouteMode:      execution.RouteNative,
	}); err == nil {
		t.Fatal("compact route was accepted")
	}
}

func TestMirasimUpstreamProtocolUsesObservedPath(t *testing.T) {
	if got := mirasimUpstreamProtocol("/v1/messages"); got != protocol.Anthropic {
		t.Fatalf("messages protocol = %q", got)
	}
	if got := mirasimUpstreamProtocol("/v1/responses"); got != protocol.OpenAIResponses {
		t.Fatalf("responses protocol = %q", got)
	}
}
