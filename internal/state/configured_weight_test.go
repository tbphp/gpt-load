package state

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRuntimeCheckpointIgnoresLegacyAutomaticWeight(t *testing.T) {
	registry := NewCredentialRegistry()
	weight := 80
	mustReplaceKeyEntries(t, registry, []CredentialEntry{{
		ID: 1, GroupID: 10, Status: CredentialStatusActive, WeightManual: &weight,
		Version: 1, IdentityGeneration: 1, Fingerprint: "fixture", EncryptedValue: "fixture",
	}})
	var checkpoints []CredentialRuntimeCheckpoint
	if err := json.Unmarshal([]byte(`[{"id":1,"group_id":10,"weight_auto":7,"blacklisted":true,"failure_count":3}]`), &checkpoints); err != nil {
		t.Fatal(err)
	}
	if registry.RestoreRuntimeCheckpoint(checkpoints) != 1 {
		t.Fatal("legacy checkpoint did not restore")
	}
	entry := registryEntry(t, registry, 1)
	if !entry.Blacklisted || entry.FailureCount != 3 || entry.WeightManual == nil || *entry.WeightManual != 80 {
		t.Fatalf("restored state = %#v, want health retained and configured weight 80", entry)
	}
	payload, err := json.Marshal(registry.CaptureRuntimeCheckpoint())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "weight_auto") {
		t.Fatal("new checkpoint still persists automatic weight")
	}
}
