package protocol

import "testing"

func TestOpenAICompletionsCanonicalProtocolIsCleanBreak(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   Protocol
		enabled bool
	}{
		{
			name:    "canonical value",
			value:   Protocol("openai-completions"),
			enabled: true,
		},
		{
			name:    "replaced value",
			value:   Protocol("openai-chat-completions"),
			enabled: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.value.Valid(); got != tt.enabled {
				t.Errorf("Protocol(%q).Valid() = %t, want %t", tt.value, got, tt.enabled)
			}
			if got := tt.value.DataPlaneEnabled(); got != tt.enabled {
				t.Errorf(
					"Protocol(%q).DataPlaneEnabled() = %t, want %t",
					tt.value,
					got,
					tt.enabled,
				)
			}
		})
	}
}

func TestProtocolKnownAndDataPlaneEnabled(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value   Protocol
		known   bool
		enabled bool
	}{
		{value: OpenAICompletions, known: true, enabled: true},
		{value: OpenAIResponses, known: true, enabled: true},
		{value: Anthropic, known: true, enabled: true},
		{value: Gemini, known: true, enabled: true},
		{value: Protocol("openai"), known: false, enabled: false},
		{value: Protocol("openai-response"), known: false, enabled: false},
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

func TestDataPlaneProtocolsReturnsCanonicalOrderAndIndependentCopies(t *testing.T) {
	t.Parallel()

	first := DataPlaneProtocols()
	want := []Protocol{
		OpenAICompletions,
		OpenAIResponses,
		Anthropic,
		Gemini,
	}
	if len(first) != len(want) {
		t.Fatalf("DataPlaneProtocols() = %#v, want %#v", first, want)
	}
	for index := range want {
		if first[index] != want[index] {
			t.Fatalf("DataPlaneProtocols()[%d] = %q, want %q", index, first[index], want[index])
		}
	}

	first[0] = Gemini
	second := DataPlaneProtocols()
	if second[0] != OpenAICompletions {
		t.Fatalf("DataPlaneProtocols() shared mutable storage: %#v", second)
	}
}
