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
	const wantDigest = "4fb788d9792b871c7e2ea00d073756fb3464841370b85dcbfbbb9a904496dea8"
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
		execution.OperationListModels,
		execution.OperationProbe,
	}
}
