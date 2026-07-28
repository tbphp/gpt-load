package protocol

import "testing"

func TestProtocolKnownAndDataPlaneEnabled(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value   Protocol
		known   bool
		enabled bool
	}{
		{value: OpenAI, known: true, enabled: true},
		{value: Anthropic, known: true, enabled: true},
		{value: Gemini, known: true, enabled: true},
		{value: OpenAIResponse, known: true, enabled: false},
		{value: Protocol("unknown"), known: false, enabled: false},
		{value: "", known: false, enabled: false},
	}
	for _, tt := range tests {
		if got := tt.value.Valid(); got != tt.known {
			t.Errorf("Protocol(%q).Valid() = %t, want %t", tt.value, got, tt.known)
		}
		if got := tt.value.DataPlaneEnabled(); got != tt.enabled {
			t.Errorf(
				"Protocol(%q).DataPlaneEnabled() = %t, want %t",
				tt.value,
				got,
				tt.enabled,
			)
		}
	}
}
