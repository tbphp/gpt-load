package channel

import (
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestGeminiEmbeddingsNativeRouteIsLimitedToSupportedChannels(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	supported := map[ID]struct{}{Gemini: {}, GPTLoad: {}, NewAPI: {}}
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
			mode, ok := definition.modes[protocol.GeminiEmbeddings][operation]
			if want {
				if !ok || mode != RouteNative {
					t.Errorf("%q gemini-embeddings %q route = %q, %t; want native", descriptor.ID, operation, mode, ok)
				}
				continue
			}
			if ok {
				t.Errorf("%q unexpectedly advertises gemini-embeddings %q route %q", descriptor.ID, operation, mode)
			}
		}
	}
}

func TestValidProtocolOperationGeminiEmbeddingsMatrix(t *testing.T) {
	t.Parallel()

	for _, operation := range []execution.Operation{
		execution.OperationEmbeddingsCreate,
		execution.OperationProbe,
	} {
		if !validProtocolOperation(protocol.GeminiEmbeddings, operation) {
			t.Errorf("gemini-embeddings/%s must be valid", operation)
		}
	}
	for _, operation := range []execution.Operation{
		execution.OperationChatCompletion,
		execution.OperationCountTokens,
		execution.OperationListModels,
		execution.OperationImagesGenerate,
	} {
		if validProtocolOperation(protocol.GeminiEmbeddings, operation) {
			t.Errorf("gemini-embeddings/%s must be invalid", operation)
		}
	}
	if validProtocolOperation(protocol.Gemini, execution.OperationEmbeddingsCreate) {
		t.Fatal("gemini accepted embeddings_create")
	}
}
