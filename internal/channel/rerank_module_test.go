package channel

import (
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestRerankNativeCapabilitiesAreExplicit(t *testing.T) {
	registry := NewRegistry()
	for _, descriptor := range registry.List() {
		definition, ok := registry.lookup(descriptor.ID)
		if !ok {
			t.Fatal("missing definition")
		}
		want := descriptor.ID == OpenAICompatible || descriptor.ID == NewAPI || descriptor.ID == GPTLoad
		for _, operation := range []execution.Operation{execution.OperationRerank, execution.OperationProbe} {
			mode, present := definition.modes[protocol.Rerank][operation]
			if present != want || present && mode != RouteNative {
				t.Errorf("%s/%s: mode=%s present=%t want=%t", descriptor.ID, operation, mode, present, want)
			}
		}
	}
	for _, operation := range []execution.Operation{execution.OperationChatCompletion, execution.OperationEmbeddingsCreate, execution.OperationListModels} {
		if validProtocolOperation(protocol.Rerank, operation) {
			t.Errorf("invalid rerank operation %s", operation)
		}
	}
}
