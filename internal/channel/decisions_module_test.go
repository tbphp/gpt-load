package channel

import (
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestDecisionsNativeCapabilitiesAreExplicit(t *testing.T) {
	registry := NewRegistry()
	for _, channelID := range []ID{Jev, OpenRouter} {
		target, err := registry.Resolve(channelID, nil)
		if err != nil {
			t.Fatalf("Resolve(%q) error = %v", channelID, err)
		}
		for _, operation := range []execution.Operation{execution.OperationDecisionsCreate, execution.OperationProbe} {
			if mode, ok := target.Mode(protocol.Decisions, operation); !ok || mode != RouteNative {
				t.Fatalf("%s %s mode = %q, %t", channelID, operation, mode, ok)
			}
		}
	}
	jev, err := registry.Resolve(Jev, nil)
	if err != nil {
		t.Fatalf("Resolve(Jev) error = %v", err)
	}
	if mode, ok := jev.Mode(protocol.Decisions, execution.OperationListModels); !ok || mode != RouteNative {
		t.Fatalf("Jev ListModels mode = %q, %t", mode, ok)
	}
	openRouter, err := registry.Resolve(OpenRouter, nil)
	if err != nil {
		t.Fatalf("Resolve(OpenRouter) error = %v", err)
	}
	if _, ok := openRouter.Mode(protocol.Decisions, execution.OperationListModels); ok {
		t.Fatal("OpenRouter unexpectedly exposes Decisions model discovery")
	}
	descriptor, ok := registry.Get(Jev)
	if !ok || !descriptor.Capabilities.ModelDiscovery {
		t.Fatalf("Jev model discovery capability = %#v, %t", descriptor.Capabilities, ok)
	}
	baseURL, ok := registry.FixedBaseURL(Jev)
	if jev.ProviderKind != ProviderJev || !ok || baseURL != "https://api.typesafe.ai/v1" {
		t.Fatalf("Jev provider = %q base URL = %q, %t", jev.ProviderKind, baseURL, ok)
	}
}
