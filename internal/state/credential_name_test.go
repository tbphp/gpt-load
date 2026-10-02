package state

import (
	"reflect"
	"testing"
)

func TestCredentialNameRecoveryPreservesRuntime(t *testing.T) {
	registry := NewCredentialRegistry()
	entry := CredentialEntry{ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1, Status: CredentialStatusActive, Fingerprint: "name-fingerprint", EncryptedValue: "cipher"}
	if err := registry.ReplaceCredentials([]CredentialEntry{entry}); err != nil {
		t.Fatal(err)
	}
	if !registry.SetBlacklisted(entry.ID) {
		t.Fatal("blacklist credential")
	}
	before := registry.Snapshot()
	entry.Name = "生产账号"
	changed, err := registry.ReconcileGroup(entry.GroupID, []CredentialEntry{entry})
	if err != nil || !changed {
		t.Fatalf("reconcile alias = %t, %v", changed, err)
	}
	if !reflect.DeepEqual(before, registry.Snapshot()) {
		t.Fatal("alias recovery changed credential health")
	}
	ref, ok := registry.CredentialRef(entry.ID)
	if !ok || ref.Name != entry.Name {
		t.Fatal("recovered alias is missing")
	}
	changed, err = registry.ReconcileGroup(entry.GroupID, []CredentialEntry{entry})
	if err != nil || changed {
		t.Fatalf("repeated alias recovery = %t, %v", changed, err)
	}
}
