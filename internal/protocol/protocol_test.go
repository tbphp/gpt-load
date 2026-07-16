package protocol

import "testing"

func TestProtocolValid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		protocol Protocol
		want     bool
	}{
		{protocol: OpenAI, want: true},
		{protocol: Anthropic, want: true},
		{protocol: Gemini, want: true},
		{protocol: OpenAIResponse, want: true},
		{protocol: Protocol("unknown"), want: false},
		{protocol: "", want: false},
	}
	for _, tt := range tests {
		if got := tt.protocol.Valid(); got != tt.want {
			t.Errorf("Protocol(%q).Valid() = %t, want %t", tt.protocol, got, tt.want)
		}
	}
}
