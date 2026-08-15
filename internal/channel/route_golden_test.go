package channel

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

// TestBuiltInRouteGolden freezes the committed b6cf9481 route behavior before
// the code-owned channel modules replace the implicit matrix builder. Any
// deliberate product behavior change must update the relevant row explicitly.
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
	const wantDigest = "de6804498975ff2904629369f01ee1a502ecd18ff5cec390cb0c26f7b9a73bd1"
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
		execution.OperationResponsesPassthrough,
		execution.OperationListModels,
		execution.OperationProbe,
	}
}
