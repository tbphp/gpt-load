package channel

import (
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestVertexEmbeddingsRoutesExcludeUnsupportedTargets(t *testing.T) {
	t.Parallel()
	target, err := NewRegistry().Resolve(GoogleVertex, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		model     string
		supported bool
	}{
		{"gemini-embedding-2", true},
		{"google/gemini-embedding-2", true},
		{"publishers/google/models/gemini-embedding-2", true},
		{"text-embedding-005", false},
		{"text-multilingual-embedding-002", false},
		{"claude-3-5-sonnet@20240620", false},
		{"123456789", false},
		{"google/123456789", false},
		{"publishers/google/models/123456789", false},
		{"", false},
	} {
		t.Run(test.model, func(t *testing.T) {
			for _, route := range []struct {
				protocol  protocol.Protocol
				operation execution.Operation
				mode      RouteMode
			}{
				{protocol.GeminiEmbeddings, execution.OperationEmbeddingsCreate, RouteNative},
				{protocol.GeminiEmbeddings, execution.OperationProbe, RouteNative},
				{protocol.OpenAIEmbeddings, execution.OperationEmbeddingsCreate, RouteConverted},
			} {
				mode, supported := target.ModeForModel(route.protocol, route.operation, test.model)
				if supported != test.supported || supported && mode != route.mode {
					t.Errorf("%s/%s route = %q, %t; want %q, %t", route.protocol, route.operation, mode, supported, route.mode, test.supported)
				}
			}
		})
	}
}
