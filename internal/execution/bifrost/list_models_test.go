package bifrost

import (
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestSanitizeListModelsResponsePreservesOpenRouterEmbeddingModels(t *testing.T) {
	t.Parallel()

	response := &schemas.BifrostListModelsResponse{Data: []schemas.Model{
		{ID: "openrouter/text"},
		{ID: "openrouter/embedding-singular", Architecture: &schemas.Architecture{OutputModalities: []string{" Embedding "}}},
		{ID: "openrouter/embedding-plural", Architecture: &schemas.Architecture{OutputModalities: []string{"EMBEDDINGS"}}},
		{ID: "openrouter/mixed", Architecture: &schemas.Architecture{OutputModalities: []string{"text", "embeddings"}}},
		{ID: "openrouter/unknown", Architecture: &schemas.Architecture{}},
	}}

	got := sanitizeListModelsResponse(schemas.OpenRouter, response)
	gotIDs := make([]string, 0, len(got.Data))
	for _, model := range got.Data {
		gotIDs = append(gotIDs, model.ID)
	}
	if want := []string{"text", "embedding-singular", "embedding-plural", "mixed", "unknown"}; !reflect.DeepEqual(gotIDs, want) {
		t.Fatalf("sanitized OpenRouter models = %v, want %v", gotIDs, want)
	}
	originalIDs := make([]string, 0, len(response.Data))
	for _, model := range response.Data {
		originalIDs = append(originalIDs, model.ID)
	}
	if want := []string{
		"openrouter/text",
		"openrouter/embedding-singular",
		"openrouter/embedding-plural",
		"openrouter/mixed",
		"openrouter/unknown",
	}; !reflect.DeepEqual(originalIDs, want) {
		t.Fatalf("sanitize mutated source response: %#v", response.Data)
	}

	other := sanitizeListModelsResponse(schemas.OpenAI, &schemas.BifrostListModelsResponse{Data: []schemas.Model{{
		ID: "openai/embedding-only", Architecture: &schemas.Architecture{OutputModalities: []string{"embeddings"}},
	}}})
	if len(other.Data) != 1 || other.Data[0].ID != "embedding-only" {
		t.Fatalf("non-OpenRouter embedding model was filtered: %#v", other.Data)
	}
}
