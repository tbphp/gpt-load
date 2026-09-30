package modelcatalog

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestSnapshot(t *testing.T) {
	if fmt.Sprintf("%x", sha256.Sum256(JSON())) != SHA256 {
		t.Fatal("snapshot digest mismatch")
	}
	for _, m := range []string{"gpt-6.1-sol", "gpt-6-sol", "gpt-6-astra", "gpt-6-luna"} {
		if !SupportsReasoningUpdates(m) {
			t.Errorf("missing %s", m)
		}
	}
	if SupportsReasoningUpdates("unknown") || SupportsReasoningUpdates("gpt-5.5") {
		t.Fatal("unsupported update capability")
	}
}
