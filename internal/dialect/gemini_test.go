package dialect

import (
	"testing"

	"gpt-load/internal/protocol"
)

func TestGeminiProtocol(t *testing.T) {
	if got := NewGemini().Protocol(); got != protocol.Gemini {
		t.Fatalf("Protocol() = %q, want %q", got, protocol.Gemini)
	}
}

func TestGeminiInspectRequest(t *testing.T) {
	selected := NewGemini()
	tests := []struct {
		name       string
		request    *ParsedRequest
		wantModel  string
		wantStream bool
		wantErr    bool
	}{
		{name: "generate", request: &ParsedRequest{Path: "/v1beta/models/gemini-2.5-pro:generateContent", Body: []byte("{")}, wantModel: "gemini-2.5-pro"},
		{name: "stream", request: &ParsedRequest{Path: "/v1beta/models/gemini-2.5-pro:streamGenerateContent"}, wantModel: "gemini-2.5-pro", wantStream: true},
		{name: "nil", wantErr: true},
		{name: "wrong prefix", request: &ParsedRequest{Path: "/models/gemini:generateContent"}, wantErr: true},
		{name: "wrong suffix", request: &ParsedRequest{Path: "/v1beta/models/gemini:embedContent"}, wantErr: true},
		{name: "empty model", request: &ParsedRequest{Path: "/v1beta/models/:generateContent"}, wantErr: true},
		{name: "nested slash", request: &ParsedRequest{Path: "/v1beta/models/vendor/gemini:generateContent"}, wantErr: true},
		{name: "boundary whitespace", request: &ParsedRequest{Path: "/v1beta/models/ gemini :generateContent"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata, err := selected.InspectRequest(test.request)
			if test.wantErr {
				if err == nil {
					t.Fatalf("InspectRequest() = %#v, nil", metadata)
				}
				return
			}
			if err != nil || metadata.Model == nil || *metadata.Model != test.wantModel ||
				metadata.Stream != test.wantStream || !metadata.ObserveUsage {
				t.Fatalf("InspectRequest() = %#v, %v", metadata, err)
			}
		})
	}
}
