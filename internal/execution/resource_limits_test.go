package execution

import (
	"testing"

	"gpt-load/internal/protocol"
)

func TestProtocolResourceLimits(t *testing.T) {
	if got := UnaryResponseBodyLimit(protocol.OpenAICompletions); got != 32<<20 {
		t.Fatalf("default unary response body limit = %d", got)
	}
	if got := UnaryResponseBodyLimit(protocol.OpenAIImages); got != 64<<20 {
		t.Fatalf("Images unary response body limit = %d", got)
	}
	if got := SSEEventLimit(protocol.OpenAICompletions); got != 10<<20 {
		t.Fatalf("default SSE event limit = %d", got)
	}
	if got := SSEEventLimit(protocol.OpenAIImages); got != 32<<20 {
		t.Fatalf("Images SSE event limit = %d", got)
	}
}
