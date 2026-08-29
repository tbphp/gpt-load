package channel

import (
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestOpenAIEmbeddingsNativeRouteIsLimitedToSupportedAPIKeyChannels(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	supported := map[ID]struct{}{
		OpenAI: {}, OpenRouter: {}, OpenAICompatible: {},
	}
	for _, descriptor := range registry.List() {
		definition, ok := registry.lookup(descriptor.ID)
		if !ok {
			t.Fatalf("lookup(%q) missing", descriptor.ID)
		}
		_, want := supported[descriptor.ID]
		for _, operation := range []execution.Operation{
			execution.OperationEmbeddingsCreate,
			execution.OperationProbe,
		} {
			mode, ok := definition.modes[protocol.OpenAIEmbeddings][operation]
			if want {
				if !ok || mode != RouteNative {
					t.Errorf("%q embeddings %q route = %q, %t; want native", descriptor.ID, operation, mode, ok)
				}
				continue
			}
			if ok {
				t.Errorf("%q unexpectedly advertises embeddings %q route %q", descriptor.ID, operation, mode)
			}
		}
	}
}

func TestValidProtocolOperationOpenAIEmbeddingsMatrix(t *testing.T) {
	t.Parallel()

	if !validProtocolOperation(protocol.OpenAIEmbeddings, execution.OperationEmbeddingsCreate) {
		t.Fatal("openai-embeddings/embeddings_create must be valid")
	}
	if !validProtocolOperation(protocol.OpenAIEmbeddings, execution.OperationProbe) {
		t.Fatal("openai-embeddings/probe must be valid")
	}
	for _, operation := range []execution.Operation{
		execution.OperationChatCompletion,
		execution.OperationResponsesCreate,
		execution.OperationImagesGenerate,
		execution.OperationListModels,
	} {
		if validProtocolOperation(protocol.OpenAIEmbeddings, operation) {
			t.Errorf("openai-embeddings/%s must be invalid", operation)
		}
	}
	if validProtocolOperation(protocol.OpenAICompletions, execution.OperationEmbeddingsCreate) {
		t.Fatal("openai-completions accepted embeddings_create")
	}
}
