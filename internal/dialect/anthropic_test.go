package dialect

import (
	"testing"

	"gpt-load/internal/protocol"
)

func TestAnthropicProtocol(t *testing.T) {
	if got := NewAnthropic().Protocol(); got != protocol.Anthropic {
		t.Fatalf("Protocol() = %q, want %q", got, protocol.Anthropic)
	}
}

func TestAnthropicInspectRequest(t *testing.T) {
	selected := NewAnthropic()
	tests := []struct {
		name       string
		request    *ParsedRequest
		wantModel  string
		wantStream bool
		wantErr    bool
	}{
		{name: "non-stream", request: &ParsedRequest{Body: []byte(`{"model":"claude-3-5-sonnet","stream":false}`)}, wantModel: "claude-3-5-sonnet"},
		{name: "stream", request: &ParsedRequest{Body: []byte(`{"model":"claude-3-5-sonnet","stream":true}`)}, wantModel: "claude-3-5-sonnet", wantStream: true},
		{name: "nil", wantErr: true},
		{name: "invalid JSON", request: &ParsedRequest{Body: []byte("{")}, wantErr: true},
		{name: "missing", request: &ParsedRequest{Body: []byte(`{}`)}, wantErr: true},
		{name: "blank", request: &ParsedRequest{Body: []byte(`{"model":"  "}`)}, wantErr: true},
		{name: "boundary whitespace", request: &ParsedRequest{Body: []byte(`{"model":" claude-3 "}`)}, wantErr: true},
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
