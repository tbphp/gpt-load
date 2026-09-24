package channel

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

// TestBuiltInRouteGolden freezes the explicit built-in route contract. It is
// based on the pre-module behavior with the intentionally unreachable
// OpenAI Responses model-list routes removed.
func TestBuiltInRouteGolden(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	got := make([]string, 0)
	for _, descriptor := range registry.List() {
		definition, ok := registry.lookup(descriptor.ID)
		if !ok {
			t.Fatalf("lookup(%q) missing", descriptor.ID)
		}
		for _, clientProtocol := range protocol.DataPlaneProtocols() {
			for _, operation := range allGoldenOperations() {
				mode, exists := definition.modes[clientProtocol][operation]
				if !exists {
					continue
				}
				got = append(got, fmt.Sprintf(
					"%s|%s|%s|%s",
					descriptor.ID,
					clientProtocol,
					operation,
					mode,
				))
			}
		}
	}

	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(got, "\n"))))
	const wantDigest = "5523f4fbbf1b074afc36087eba2abb9e527126b3be6b5d930c914b74a0f3bde4"
	if digest != wantDigest {
		t.Fatalf("built-in routes changed: digest = %s, want %s\n%s", digest, wantDigest, strings.Join(got, "\n"))
	}
}

func allGoldenOperations() []execution.Operation {
	return []execution.Operation{
		execution.OperationChatCompletion,
		execution.OperationResponsesCreate,
		execution.OperationResponsesRetrieve,
		execution.OperationResponsesDelete,
		execution.OperationResponsesCancel,
		execution.OperationResponsesInputItems,
		execution.OperationResponsesCompact,
		execution.OperationResponsesInputTokens,
		execution.OperationCountTokens,
		execution.OperationResponsesPassthrough,
		execution.OperationImagesGenerate,
		execution.OperationImagesEdit,
		execution.OperationEmbeddingsCreate,
		execution.OperationLiveCall,
		execution.OperationRerank,
		execution.OperationDecisionsCreate,
		execution.OperationListModels,
		execution.OperationProbe,
	}
}
