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

func TestClientSnapshot(t *testing.T) {
	if fmt.Sprintf("%x", sha256.Sum256(ClientJSON())) != ClientSHA256 {
		t.Fatal("client snapshot digest mismatch")
	}
	levels := ReasoningLevels("gpt-6.1-sol")
	found := false
	for _, level := range levels {
		if level == "ultra" {
			found = true
		}
	}
	if !found {
		t.Fatal("official ultra capability missing")
	}
	levels[0] = "modified"
	if ReasoningLevels("gpt-6.1-sol")[0] == "modified" {
		t.Fatal("caller changed shared contract")
	}
	if ReasoningLevels("unknown-model") != nil {
		t.Fatal("unknown model inherited levels")
	}
}
