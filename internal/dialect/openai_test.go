package dialect

import (
	"testing"

	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

func TestParsedRequestCarriesIngressInputs(t *testing.T) {
	request := ParsedRequest{Method: "POST", Path: "/v1/chat/completions", RawQuery: "trace=true", Body: []byte(`{"model":"gpt-4o"}`)}
	if request.Method != "POST" || request.Path != "/v1/chat/completions" ||
		request.RawQuery != "trace=true" || len(request.Body) == 0 {
		t.Fatalf("ParsedRequest = %#v", request)
	}
}

func TestOpenAIProtocol(t *testing.T) {
	if got := NewOpenAI().Protocol(); got != protocol.OpenAICompletions {
		t.Fatalf("Protocol() = %q, want %q", got, protocol.OpenAICompletions)
	}
}

func TestOpenAIInspectRequest(t *testing.T) {
	selected := NewOpenAI()
	tests := []struct {
		name       string
		request    *ParsedRequest
		wantModel  string
		wantStream bool
		wantErr    bool
	}{
		{name: "non-stream", request: &ParsedRequest{Body: []byte(`{"model":"gpt-4o","messages":[]}`)}, wantModel: "gpt-4o"},
		{name: "stream", request: &ParsedRequest{Body: []byte(`{"model":"gpt-4o-mini","stream":true}`)}, wantModel: "gpt-4o-mini", wantStream: true},
		{name: "nil", wantErr: true},
		{name: "invalid JSON", request: &ParsedRequest{Body: []byte(`{"model":`)}, wantErr: true},
		{name: "missing model", request: &ParsedRequest{Body: []byte(`{"stream":true}`)}, wantErr: true},
		{name: "blank model", request: &ParsedRequest{Body: []byte(`{"model":"  "}`)}, wantErr: true},
		{name: "model boundary whitespace", request: &ParsedRequest{Body: []byte(`{"model":" gpt-4o "}`)}, wantErr: true},
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

func TestOpenAIInspectRequestSelectsSupportedPricingModes(t *testing.T) {
	selected := NewOpenAI()
	for _, test := range []struct {
		body        string
		mode        pricing.Mode
		unsupported bool
	}{
		{body: `{"model":"gpt-5","service_tier":"priority"}`, mode: pricing.ModeFast},
		{body: `{"model":"gpt-5","service_tier":"fast"}`, mode: pricing.ModeFast},
		{body: `{"model":"gpt-5","service_tier":"default"}`, mode: pricing.ModeStandard},
		{body: `{"model":"gpt-5"}`},
		{body: `{"model":"gpt-5","service_tier":"auto"}`},
		{body: `{"model":"gpt-5","service_tier":"flex"}`, unsupported: true},
		{body: `{"model":"gpt-5","speed":"fast"}`, unsupported: true},
		{body: `{"model":"gpt-5","reasoning":{"mode":"pro"}}`, unsupported: true},
	} {
		metadata, err := selected.InspectRequest(&ParsedRequest{Body: []byte(test.body)})
		if err != nil || metadata.PricingMode != test.mode ||
			metadata.UsageDiagnostics.Has(usage.DiagnosticUnsupportedBillableDetail) != test.unsupported {
			t.Fatalf("InspectRequest(%s) = %#v, %v", test.body, metadata, err)
		}
	}
}
