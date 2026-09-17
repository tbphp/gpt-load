package dialect

import (
	"net/http"
	"testing"

	"gpt-load/internal/execution"
)

func TestCodexSearchRequestMetadata(t *testing.T) {
	selected := NewOpenAIResponses()
	for _, test := range []struct {
		name, body string
		wantErr    bool
	}{
		{name: "native", body: `{"id":"session","model":"gpt-5","commands":{"search_query":[{"q":"test"}]},"service_tier":"priority","reasoning":{"effort":"high"}}`},
		{name: "missing model", body: `{"id":"session"}`, wantErr: true},
		{name: "missing id", body: `{"model":"gpt-5"}`, wantErr: true},
		{name: "invalid id", body: `{"id":42,"model":"gpt-5"}`, wantErr: true},
		{name: "streaming", body: `{"id":"session","model":"gpt-5","stream":true}`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			metadata, err := selected.InspectRequest(&ParsedRequest{Method: http.MethodPost, Path: "/v1/alpha/search", Body: []byte(test.body)})
			if test.wantErr {
				if err == nil {
					t.Fatal("invalid search request was accepted")
				}
				return
			}
			if err != nil || metadata.Model == nil || *metadata.Model != "gpt-5" ||
				metadata.Operation != execution.OperationWebSearch || metadata.RouteRequirement != execution.RouteRequirementNative ||
				metadata.Stream || metadata.ObserveUsage || string(metadata.AffinityPrefix) != "codex-search\x00session" {
				t.Fatalf("search metadata = %#v, error = %v", metadata, err)
			}
		})
	}
}
