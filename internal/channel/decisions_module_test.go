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
	baseURL, ok := registry.FixedBaseURL(Jev)
	if jev.ProviderKind != ProviderJev || !ok || baseURL != "https://api.typesafe.ai/v1" {
		t.Fatalf("Jev provider = %q base URL = %q, %t", jev.ProviderKind, baseURL, ok)
	}
}
