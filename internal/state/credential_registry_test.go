package state

import "testing"

func TestCredentialRegistryKeepsIdentityAndHealthGenerationsIndependent(t *testing.T) {
	t.Parallel()

	registry := NewCredentialRegistry()
	entry := CredentialEntry{
		ID: 11, GroupID: 7, Version: 42, IdentityGeneration: 99,
		Fingerprint: "fingerprint", Status: CredentialStatusActive,
		WeightAuto: DefaultWeight, EncryptedValue: "encrypted-data",
	}
	if err := ValidateCredentialEntries([]CredentialEntry{entry}); err != nil {
		t.Fatalf("ValidateCredentialEntries() error = %v", err)
	}
	if err := registry.ReplaceCredentials([]CredentialEntry{entry}); err != nil {
		t.Fatalf("ReplaceCredentials() error = %v", err)
	}

	refs := registry.CaptureActiveCredentialRefs([]uint{7})
	if len(refs) != 1 {
		t.Fatalf("CaptureActiveCredentialRefs() = %#v", refs)
	}
	ref := refs[0]
	if ref.Version != 42 || ref.IdentityGeneration != 99 || ref.Fingerprint != "fingerprint" {
		t.Fatalf("credential ref = %#v", ref)
	}

	if _, ok := registry.IncrFailure(11); !ok {
		t.Fatal("IncrFailure() = false")
	}
	if got, ok := registry.ActiveEncryptedCredentialDataIfMatch(ref); !ok || got != "encrypted-data" {
		t.Fatalf("health generation invalidated credential identity: %q, %t", got, ok)
	}

	changed := ref
	changed.Fingerprint = "other-fingerprint"
	if _, ok := registry.ActiveEncryptedCredentialDataIfMatch(changed); ok {
		t.Fatal("mismatched full fingerprint was accepted")
	}
	changed = ref
	changed.IdentityGeneration++
	if _, ok := registry.ActiveEncryptedCredentialDataIfMatch(changed); ok {
		t.Fatal("mismatched identity generation was accepted")
	}
	changed = ref
	changed.Version++
	if _, ok := registry.ActiveEncryptedCredentialDataIfMatch(changed); ok {
		t.Fatal("mismatched version was accepted")
	}
}

func TestValidateCredentialEntriesRequiresDurableIdentityEvidence(t *testing.T) {
	t.Parallel()

	base := CredentialEntry{
		ID: 1, GroupID: 2, Version: 3, IdentityGeneration: 4,
		Fingerprint: "fingerprint", Status: CredentialStatusActive,
		WeightAuto: DefaultWeight, EncryptedValue: "encrypted",
	}
	tests := []struct {
		name   string
		mutate func(*CredentialEntry)
	}{
		{name: "version", mutate: func(entry *CredentialEntry) { entry.Version = 0 }},
		{name: "identity generation", mutate: func(entry *CredentialEntry) { entry.IdentityGeneration = 0 }},
		{name: "fingerprint", mutate: func(entry *CredentialEntry) { entry.Fingerprint = "  " }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := base
			test.mutate(&entry)
			if err := ValidateCredentialEntries([]CredentialEntry{entry}); err == nil {
				t.Fatal("ValidateCredentialEntries() error = nil")
			}
		})
	}
}
