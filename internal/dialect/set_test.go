package dialect

import (
	"net/http"
	"testing"

	"gpt-load/internal/protocol"
)

func TestNewSetIndexesDialectsByProtocol(t *testing.T) {
	openAI := NewOpenAI(http.DefaultClient)
	got := NewSet(openAI)
	if len(got) != 1 || got[protocol.OpenAI] != openAI {
		t.Fatalf("NewSet(OpenAI) = %#v, want OpenAI indexed", got)
	}
}

func TestNewSetAllowsAnEmptyRuntimeSet(t *testing.T) {
	if got := NewSet(); len(got) != 0 {
		t.Fatalf("NewSet() = %#v, want empty", got)
	}
}
