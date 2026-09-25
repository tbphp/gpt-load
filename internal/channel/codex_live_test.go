package channel

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestCodexDeclaresOnlyNativeLiveCall(t *testing.T) {
	registry := NewRegistry()
	target, err := registry.Resolve(Codex, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	mode, ok := target.Mode(protocol.Protocol("codex-live"), execution.Operation("live_call"))
	if !ok || mode != RouteNative {
		t.Fatalf("Codex live route = %q, %v, want native", mode, ok)
	}
	for _, other := range []ID{OpenAI, Claude, Antigravity} {
		resolved, err := registry.Resolve(other, json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := resolved.Mode(protocol.Protocol("codex-live"), execution.Operation("live_call")); exists {
			t.Fatalf("%s unexpectedly supports Codex live", other)
		}
	}
}
