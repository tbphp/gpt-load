package dialect

import (
	"net/http"
	"testing"

	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

func TestOpenAIResponsesProtocolAndRequestMetadata(t *testing.T) {
	selected := NewOpenAIResponses()
	if got := selected.Protocol(); got != protocol.OpenAIResponses {
		t.Fatalf("Protocol() = %q, want %q", got, protocol.OpenAIResponses)
	}

	tests := []struct {
		name        string
		request     *ParsedRequest
		wantModel   string
		wantStream  bool
		wantObserve bool
		wantErr     bool
	}{
		{name: "empty create", request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses"}, wantObserve: true},
		{name: "create stream", request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Body: []byte(`{"model":"gpt-5","stream":true}`)}, wantModel: "gpt-5", wantStream: true, wantObserve: true},
		{name: "compact", request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses/compact", Body: []byte(`{"model":"gpt-5"}`)}, wantModel: "gpt-5", wantObserve: true},
		{name: "input tokens", request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses/input_tokens", Body: []byte(`{"model":"gpt-5"}`)}, wantModel: "gpt-5"},
		{name: "retrieve stream", request: &ParsedRequest{Method: http.MethodGet, Path: "/v1/responses/resp_123", RawQuery: "stream=true"}, wantStream: true},
		{name: "encoded stream", request: &ParsedRequest{Method: http.MethodGet, Path: "/v1/responses/resp_123", RawQuery: "%73tream=%74rue"}, wantStream: true},
		{name: "nil", wantErr: true},
		{name: "non object", request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Body: []byte(`[]`)}, wantErr: true},
		{name: "blank model", request: &ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Body: []byte(`{"model":""}`)}, wantErr: true},
		{name: "duplicate stream query", request: &ParsedRequest{Method: http.MethodGet, Path: "/v1/responses/resp_123", RawQuery: "stream=true&stream=false"}, wantErr: true},
		{name: "invalid stream query", request: &ParsedRequest{Method: http.MethodGet, Path: "/v1/responses/resp_123", RawQuery: "stream=1"}, wantErr: true},
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
			if err != nil || metadata.Stream != test.wantStream || metadata.ObserveUsage != test.wantObserve {
				t.Fatalf("InspectRequest() = %#v, %v", metadata, err)
			}
			if test.wantModel == "" {
				if metadata.Model != nil {
					t.Fatalf("model = %v, want nil", metadata.Model)
				}
			} else if metadata.Model == nil || *metadata.Model != test.wantModel {
				t.Fatalf("model = %v, want %q", metadata.Model, test.wantModel)
			}
		})
	}
}

func TestOpenAIResponsesRequestMarksUnsupportedPricingModes(t *testing.T) {
	selected := NewOpenAIResponses()
	for _, body := range []string{
		`{"model":"gpt-5","service_tier":"priority"}`,
		`{"model":"gpt-5","speed":"fast"}`,
		`{"model":"gpt-5","reasoning":{"mode":"pro"}}`,
	} {
		metadata, err := selected.InspectRequest(&ParsedRequest{Method: http.MethodPost, Path: "/v1/responses", Body: []byte(body)})
		if err != nil || !metadata.UsageDiagnostics.Has(usage.DiagnosticUnsupportedBillableDetail) {
			t.Fatalf("InspectRequest(%s) = %#v, %v", body, metadata, err)
		}
	}
}
