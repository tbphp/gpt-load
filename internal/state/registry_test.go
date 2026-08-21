package state

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestKeyRegistryReplaceAndEncryptedValue(t *testing.T) {
	registry := NewCredentialRegistry()
	weight := 7
	entries := []CredentialEntry{
		{
			ID: 1, GroupID: 10, WeightManual: &weight,
			Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one",
		},
		{
			ID: 2, GroupID: 20,
			Status: CredentialStatusDisabled, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-two",
		},
	}
	if err := registry.ReplaceCredentials(entries); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}

	for _, test := range []struct {
		credentialID uint
		want         string
	}{
		{credentialID: 1, want: "cipher-one"},
		{credentialID: 2, want: "cipher-two"},
	} {
		got, ok := registry.EncryptedCredentialData(test.credentialID)
		if !ok || got != test.want {
			t.Errorf("EncryptedValue(%d) = %q, %t, want %q, true", test.credentialID, got, ok, test.want)
		}
	}

	weight = 99
	registry.mu.RLock()
	storedWeight := *registry.buckets[10][1].WeightManual
	registry.mu.RUnlock()
	if storedWeight != 7 {
		t.Fatalf("stored WeightManual = %d after caller mutation, want 7", storedWeight)
	}

	if err := registry.ReplaceCredentials([]CredentialEntry{{
		ID: 3, GroupID: 20,
		Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-three",
	}}); err != nil {
		t.Fatalf("second Replace() error = %v", err)
	}
	for _, removedID := range []uint{1, 2} {
		if got, ok := registry.EncryptedCredentialData(removedID); ok || got != "" {
			t.Errorf("EncryptedValue(%d) after replacement = %q, %t, want empty, false", removedID, got, ok)
		}
	}
	if got, ok := registry.EncryptedCredentialData(3); !ok || got != "cipher-three" {
		t.Fatalf("EncryptedValue(3) = %q, %t, want %q, true", got, ok, "cipher-three")
	}
}

func TestCredentialRegistryReplaceSecretIfMatchPreservesRuntimeState(t *testing.T) {
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{{
		ID: 1, GroupID: 10, Status: CredentialStatusActive,
		Version: 3, IdentityGeneration: 9, Fingerprint: "old-fingerprint", EncryptedValue: "old-cipher",
		CooldownUntil: time.Unix(100, 0), Blacklisted: true, FailureCount: 2, FailureGeneration: 4,
	}})
	registry.mu.Lock()
	registry.buckets[10][1].FailureGeneration = 4
	registry.mu.Unlock()

	if !registry.ReplaceCredentialSecretIfMatch(1, 3, 4, "new-fingerprint", "new-cipher") {
		t.Fatal("ReplaceCredentialSecretIfMatch() = false, want true")
	}
	got := registryEntry(t, registry, 1)
	if got.Version != 4 || got.IdentityGeneration != 9 || got.Fingerprint != "new-fingerprint" || got.EncryptedValue != "new-cipher" {
		t.Fatalf("updated entry = %#v", got)
	}
	if !got.Blacklisted || got.FailureCount != 2 || got.FailureGeneration != 4 || got.CooldownUntil != time.Unix(100, 0) {
		t.Fatalf("runtime state changed during secret replacement: %#v", got)
	}
	if registry.ReplaceCredentialSecretIfMatch(1, 3, 5, "stale", "stale") {
		t.Fatal("stale secret replacement succeeded")
	}
	if after := registryEntry(t, registry, 1); !reflect.DeepEqual(after, got) {
		t.Fatalf("stale replacement changed entry: %#v", after)
	}
}

func TestKeyRegistryRestoreRuntimeState(t *testing.T) {
	registry := NewCredentialRegistry()
	now := time.Date(2026, time.August, 1, 10, 0, 0, 0, time.UTC)
	mustReplaceKeyEntries(t, registry, []CredentialEntry{{
		ID: 1, GroupID: 10, Status: CredentialStatusActive,
		CooldownUntil: now.Add(time.Hour), Blacklisted: true,
		FailureCount: 3, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one",
	}})

	if ok := registry.RestoreRuntimeState(1, 37); !ok {
		t.Fatal("RestoreRuntimeState() = false, want true")
	}
	got := registryEntry(t, registry, 1)
	if !got.CooldownUntil.IsZero() || got.Blacklisted || got.FailureCount != 0 || got.WeightAuto != 37 {
		t.Fatalf("restored entry = %#v", got)
	}
	if got.FailureGeneration != 1 {
		t.Fatalf("FailureGeneration = %d, want 1", got.FailureGeneration)
	}

	before := registryEntry(t, registry, 1)
	for _, test := range []struct {
		credentialID uint
		weight       int
	}{
		{credentialID: 0, weight: 37},
		{credentialID: 99, weight: 37},
		{credentialID: 1, weight: 0},
		{credentialID: 1, weight: MaxWeight + 1},
	} {
		if registry.RestoreRuntimeState(test.credentialID, test.weight) {
			t.Fatalf("RestoreRuntimeState(%d, %d) = true, want false", test.credentialID, test.weight)
		}
	}
	if after := registryEntry(t, registry, 1); !reflect.DeepEqual(after, before) {
		t.Fatalf("entry after invalid restores = %#v, want %#v", after, before)
	}
}

func TestCredentialRegistryQuotaObservationDoesNotAffectCandidates(t *testing.T) {
	now := time.Date(2026, time.August, 14, 14, 0, 0, 0, time.UTC)
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{{
		ID: 1, GroupID: 10, Status: CredentialStatusActive,
		Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one",
	}})
	remaining := 0.0
	if !registry.SetCredentialQuotaObservation(1, &remaining, now.Add(7*24*time.Hour)) {
		t.Fatal("SetCredentialQuotaObservation() = false")
	}
	if got := registry.CollectCredentialCandidates([]uint{10}, nil, now); len(got) != 1 {
		t.Fatalf("exhausted quota candidates = %#v, want normal candidate", got)
	}
	if got := registry.CollectCredentialCandidates([]uint{10}, nil, now.Add(time.Hour)); len(got) != 1 {
		t.Fatalf("later quota candidates = %#v, want normal candidate", got)
	}

	remaining = 0.63
	if !registry.SetCredentialQuotaObservation(1, &remaining, now.Add(7*24*time.Hour)) {
		t.Fatal("SetCredentialQuotaObservation(available) = false")
	}
	got := registry.CollectCredentialCandidates([]uint{10}, nil, now)
	if len(got) != 1 {
		t.Fatalf("available quota candidates = %#v", got)
	}
}

func TestKeyRegistryBatchMutationsAreAllOrNothing(t *testing.T) {
	newRegistry := func(t *testing.T) *CredentialRegistry {
		t.Helper()
		registry := NewCredentialRegistry()
		mustReplaceKeyEntries(t, registry, []CredentialEntry{
			{ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one"},
			{ID: 2, GroupID: 10, Status: CredentialStatusDisabled, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-two"},
			{ID: 3, GroupID: 20, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-three"},
		})
		return registry
	}

	t.Run("update statuses", func(t *testing.T) {
		registry := newRegistry(t)
		if err := registry.UpdateGroupCredentialStatuses(10, []uint{1, 2}, CredentialStatusDisabled); err != nil {
			t.Fatalf("UpdateGroupCredentialStatuses() error = %v", err)
		}
		if got := registryEntry(t, registry, 1).Status; got != CredentialStatusDisabled {
			t.Fatalf("key 1 status = %q, want disabled", got)
		}
		if got := registryEntry(t, registry, 2).Status; got != CredentialStatusDisabled {
			t.Fatalf("key 2 status = %q, want disabled", got)
		}
	})

	for _, test := range []struct {
		name string
		ids  []uint
	}{
		{name: "missing", ids: []uint{1, 99}},
		{name: "cross group", ids: []uint{1, 3}},
		{name: "duplicate", ids: []uint{1, 1}},
		{name: "empty", ids: nil},
	} {
		t.Run("update rejects "+test.name, func(t *testing.T) {
			registry := newRegistry(t)
			before := registry.Snapshot()
			if err := registry.UpdateGroupCredentialStatuses(10, test.ids, CredentialStatusDisabled); err == nil {
				t.Fatal("UpdateGroupCredentialStatuses() error = nil, want error")
			}
			if after := registry.Snapshot(); !reflect.DeepEqual(after, before) {
				t.Fatalf("registry after rejected update = %#v, want %#v", after, before)
			}
		})
		t.Run("remove rejects "+test.name, func(t *testing.T) {
			registry := newRegistry(t)
			before := registry.Snapshot()
			if err := registry.RemoveGroupCredentials(10, test.ids); err == nil {
				t.Fatal("RemoveGroupCredentials() error = nil, want error")
			}
			if after := registry.Snapshot(); !reflect.DeepEqual(after, before) {
				t.Fatalf("registry after rejected remove = %#v, want %#v", after, before)
			}
		})
	}

	registry := newRegistry(t)
	if err := registry.RemoveGroupCredentials(10, []uint{2, 1}); err != nil {
		t.Fatalf("RemoveGroupCredentials() error = %v", err)
	}
	if _, ok := registry.EncryptedCredentialData(1); ok {
		t.Fatal("key 1 remains after batch removal")
	}
	if _, ok := registry.EncryptedCredentialData(2); ok {
		t.Fatal("key 2 remains after batch removal")
	}
	if got, ok := registry.EncryptedCredentialData(3); !ok || got != "cipher-three" {
		t.Fatalf("other-group key = %q/%t", got, ok)
	}
}

func TestKeyRegistryActiveEncryptedValueRequiresExpectedGroupAndActiveStatus(t *testing.T) {
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{
		{ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-active"},
		{ID: 2, GroupID: 10, Status: CredentialStatusDisabled, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-disabled"},
	})

	tests := []struct {
		name         string
		credentialID uint
		groupID      uint
		want         string
		wantOK       bool
	}{
		{name: "matching active key", credentialID: 1, groupID: 10, want: "cipher-active", wantOK: true},
		{name: "group mismatch", credentialID: 1, groupID: 20},
		{name: "disabled key", credentialID: 2, groupID: 10},
		{name: "missing key", credentialID: 99, groupID: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := registry.ActiveEncryptedCredentialData(tt.credentialID, tt.groupID)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("ActiveEncryptedCredentialData(%d, %d) = %q, %t, want %q, %t", tt.credentialID, tt.groupID, got, ok, tt.want, tt.wantOK)
			}
		})
	}

	mustReplaceKeyEntries(t, registry, []CredentialEntry{{
		ID: 1, GroupID: 20, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-moved",
	}})
	if got, ok := registry.ActiveEncryptedCredentialData(1, 10); got != "" || ok {
		t.Fatalf("ActiveEncryptedCredentialData(1, old group) = %q, %t, want empty, false", got, ok)
	}
	if got, ok := registry.ActiveEncryptedCredentialData(1, 20); got != "cipher-moved" || !ok {
		t.Fatalf("ActiveEncryptedCredentialData(1, new group) = %q, %t, want cipher-moved, true", got, ok)
	}
}

func TestKeyRegistryCaptureActiveKeyRefsIncludesTemporarilyUnavailableKeys(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{
		{
			ID: 21, GroupID: 2, Status: CredentialStatusActive,
			CooldownUntil: now.Add(time.Hour), Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-cooldown",
		},
		{
			ID: 12, GroupID: 1, Status: CredentialStatusDisabled,
			Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-disabled",
		},
		{
			ID: 11, GroupID: 1, Status: CredentialStatusActive,
			Blacklisted: true, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-blacklisted",
		},
		{
			ID: 22, GroupID: 2, Status: CredentialStatusActive,
			Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-available",
		},
		{
			ID: 31, GroupID: 3, Status: CredentialStatusActive,
			Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-other-group",
		},
	})

	want := []CredentialRef{
		{ID: 11, GroupID: 1, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-blacklisted"},
		{ID: 21, GroupID: 2, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-cooldown"},
		{ID: 22, GroupID: 2, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-available"},
	}
	got := registry.CaptureActiveCredentialRefs([]uint{2, 1})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CaptureActiveCredentialRefs() = %#v, want %#v", got, want)
	}
	if _, ok := registry.IncrFailure(11); !ok {
		t.Fatal("IncrFailure(11) = false, want true")
	}
	want[0].FailureGeneration = 1
	if got := registry.CaptureActiveCredentialRefs([]uint{1, 2}); !reflect.DeepEqual(got, want) {
		t.Fatalf("CaptureActiveCredentialRefs() after failure = %#v, want %#v", got, want)
	}

	got[0].EncryptedValue = "caller-mutated"
	if again := registry.CaptureActiveCredentialRefs([]uint{1, 2}); !reflect.DeepEqual(again, want) {
		t.Fatalf("CaptureActiveCredentialRefs() aliases caller result: %#v", again)
	}
}

func TestKeyRegistryActiveEncryptedValueIfMatchRejectsIdentityChanges(t *testing.T) {
	ref := CredentialRef{ID: 1, GroupID: 10, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-original"}
	newRegistry := func(t *testing.T) *CredentialRegistry {
		t.Helper()
		registry := NewCredentialRegistry()
		mustReplaceKeyEntries(t, registry, []CredentialEntry{{
			ID: 1, GroupID: 10, Status: CredentialStatusActive,
			Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-original",
		}})
		return registry
	}

	t.Run("matching active identity", func(t *testing.T) {
		registry := newRegistry(t)
		got, ok := registry.ActiveEncryptedCredentialDataIfMatch(ref)
		if !ok || got != "cipher-original" {
			t.Fatalf(
				"ActiveEncryptedCredentialDataIfMatch() = %q, %t, want cipher-original, true",
				got,
				ok,
			)
		}
	})

	t.Run("failure generation does not invalidate in-flight identity", func(t *testing.T) {
		registry := newRegistry(t)
		if _, ok := registry.IncrFailure(1); !ok {
			t.Fatal("IncrFailure(1) = false, want true")
		}
		got, ok := registry.ActiveEncryptedCredentialDataIfMatch(ref)
		if !ok || got != "cipher-original" {
			t.Fatalf("ActiveEncryptedCredentialDataIfMatch() = %q, %t, want cipher-original, true", got, ok)
		}
	})

	t.Run("different key ID", func(t *testing.T) {
		registry := newRegistry(t)
		mismatched := ref
		mismatched.ID = 2
		if got, ok := registry.ActiveEncryptedCredentialDataIfMatch(mismatched); ok || got != "" {
			t.Fatalf("ActiveEncryptedCredentialDataIfMatch(wrong ID) = %q, %t, want empty, false", got, ok)
		}
	})

	t.Run("replaced ciphertext", func(t *testing.T) {
		registry := newRegistry(t)
		if err := registry.ApplyCredentialImport(10, []CredentialEntry{{
			ID: 1, GroupID: 10, Status: CredentialStatusActive,
			Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-replaced",
		}}); err != nil {
			t.Fatalf("ApplyImport(replacement) error = %v", err)
		}
		if got, ok := registry.ActiveEncryptedCredentialDataIfMatch(ref); ok || got != "" {
			t.Fatalf("ActiveEncryptedCredentialDataIfMatch(replaced) = %q, %t, want empty, false", got, ok)
		}
	})

	t.Run("moved group", func(t *testing.T) {
		registry := newRegistry(t)
		mustReplaceKeyEntries(t, registry, []CredentialEntry{{
			ID: 1, GroupID: 20, Status: CredentialStatusActive,
			Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-original",
		}})
		if got, ok := registry.ActiveEncryptedCredentialDataIfMatch(ref); ok || got != "" {
			t.Fatalf("ActiveEncryptedCredentialDataIfMatch(moved) = %q, %t, want empty, false", got, ok)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		registry := newRegistry(t)
		if err := registry.SetCredentialStatus(1, CredentialStatusDisabled); err != nil {
			t.Fatalf("SetCredentialStatus(disabled) error = %v", err)
		}
		if got, ok := registry.ActiveEncryptedCredentialDataIfMatch(ref); ok || got != "" {
			t.Fatalf("ActiveEncryptedCredentialDataIfMatch(disabled) = %q, %t, want empty, false", got, ok)
		}
	})
}

func TestKeyRegistryActiveKeyIDs(t *testing.T) {
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{
		{ID: 9, GroupID: 20, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-nine"},
		{ID: 5, GroupID: 10, Status: CredentialStatusDisabled, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-five"},
		{ID: 3, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-three"},
		{ID: 7, GroupID: 20, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-seven"},
	})
	if ok := registry.SetCooldown(3, time.Now().Add(time.Hour)); !ok {
		t.Fatal("SetCooldown(3) = false, want true")
	}
	if ok := registry.SetBlacklisted(7); !ok {
		t.Fatal("SetBlacklisted(7) = false, want true")
	}

	want := []uint{3, 7, 9}
	got := registry.ActiveCredentialIDs()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ActiveCredentialIDs() = %v, want %v", got, want)
	}

	got[0] = 99
	if again := registry.ActiveCredentialIDs(); !reflect.DeepEqual(again, want) {
		t.Fatalf("ActiveCredentialIDs() after caller mutation = %v, want %v", again, want)
	}
}

func TestKeyRegistrySnapshotIsSortedDetachedAndCredentialFree(t *testing.T) {
	weight := 17
	now := time.Date(2026, time.July, 24, 10, 0, 0, 0, time.UTC)
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{
		{
			ID: 22, GroupID: 2, Status: CredentialStatusDisabled,
			Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-disabled",
		},
		{
			ID: 11, GroupID: 1, WeightManual: &weight, WeightAuto: 23,
			Status: CredentialStatusActive, CooldownUntil: now.Add(time.Minute),
			Blacklisted: true, FailureCount: 3, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-secret",
		},
		{
			ID: 12, GroupID: 1, Status: CredentialStatusActive,
			Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-default-weight",
		},
	})

	got := registry.Snapshot()
	if len(got) != 3 || got[0].ID != 11 || got[1].ID != 12 || got[2].ID != 22 {
		t.Fatalf("Snapshot order = %#v", got)
	}
	if got[0].WeightManual == nil || *got[0].WeightManual != 17 ||
		got[0].WeightAuto != 23 || !got[0].Blacklisted ||
		got[0].FailureCount != 3 || !got[0].CooldownUntil.Equal(now.Add(time.Minute)) {
		t.Fatalf("Snapshot runtime values = %#v", got[0])
	}
	if got[1].WeightAuto != DefaultWeight {
		t.Fatalf("default WeightAuto = %d, want %d", got[1].WeightAuto, DefaultWeight)
	}
	*got[0].WeightManual = 99
	got[0].FailureCount = 99
	again := registry.Snapshot()
	if again[0].WeightManual == nil || *again[0].WeightManual != 17 ||
		again[0].FailureCount != 3 {
		t.Fatalf("Snapshot aliases Registry: %#v", again[0])
	}

	typ := reflect.TypeOf(CredentialRuntimeView{})
	for _, forbidden := range []string{
		"EncryptedValue", "KeyHash", "Hash", "Mask", "HeaderRules",
	} {
		if _, exists := typ.FieldByName(forbidden); exists {
			t.Fatalf("CredentialRuntimeView exposes forbidden field %s", forbidden)
		}
	}
}

func TestKeyRuntimeViewDoesNotExposeFailureGeneration(t *testing.T) {
	typ := reflect.TypeOf(CredentialRuntimeView{})
	if _, exists := typ.FieldByName("FailureGeneration"); exists {
		t.Fatal("CredentialRuntimeView exposes forbidden field FailureGeneration")
	}
}

func TestKeyRegistryFailureGenerationTracksActualFailureStateChanges(t *testing.T) {
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{{
		ID: 1, GroupID: 10, Status: CredentialStatusActive,
		FailureGeneration: 99, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one",
	}})

	assertGeneration := func(want uint64) {
		t.Helper()
		if got := registryEntry(t, registry, 1).FailureGeneration; got != want {
			t.Fatalf("FailureGeneration = %d, want %d", got, want)
		}
	}
	assertGeneration(0)

	if _, ok := registry.IncrFailure(1); !ok {
		t.Fatal("IncrFailure(1) = false, want true")
	}
	assertGeneration(1)
	if !registry.SetBlacklisted(1) {
		t.Fatal("SetBlacklisted(1) = false, want true")
	}
	assertGeneration(2)
	if !registry.SetBlacklisted(1) {
		t.Fatal("SetBlacklisted(1) = false on existing key, want true")
	}
	assertGeneration(2)
	if !registry.ClearFailure(1) {
		t.Fatal("ClearFailure(1) = false, want true")
	}
	assertGeneration(3)
	if !registry.ClearFailure(1) {
		t.Fatal("ClearFailure(1) = false on existing key, want true")
	}
	assertGeneration(3)
	if !registry.Recover(1) {
		t.Fatal("Recover(1) = false, want true")
	}
	assertGeneration(4)
	if !registry.Recover(1) {
		t.Fatal("Recover(1) = false on existing key, want true")
	}
	assertGeneration(4)

	if registry.SetBlacklisted(99) || registry.ClearFailure(99) || registry.Recover(99) {
		t.Fatal("missing-key failure mutation = true, want false")
	}
	if _, ok := registry.IncrFailure(99); ok {
		t.Fatal("IncrFailure(missing key) = true, want false")
	}
	assertGeneration(4)

	if err := registry.ApplyCredentialImport(10, []CredentialEntry{{
		ID: 1, GroupID: 10, Status: CredentialStatusActive,
		FailureGeneration: 73, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-imported",
	}}); err != nil {
		t.Fatalf("ApplyImport() error = %v", err)
	}
	assertGeneration(0)

	mustReplaceKeyEntries(t, registry, []CredentialEntry{{
		ID: 1, GroupID: 10, Status: CredentialStatusActive,
		FailureGeneration: 42, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-replaced",
	}})
	assertGeneration(0)
}

func TestKeyRegistryReplaceFailurePreservesRegistry(t *testing.T) {
	invalidBatches := map[string][]CredentialEntry{
		"duplicate ids": {
			{ID: 2, GroupID: 20, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-two"},
			{ID: 2, GroupID: 20, Status: CredentialStatusDisabled, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "duplicate"},
		},
		"invalid status": {
			{ID: 3, GroupID: 30, Status: CredentialStatus("cooldown"), Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-three"},
		},
	}

	for name, batch := range invalidBatches {
		t.Run(name, func(t *testing.T) {
			registry := NewCredentialRegistry()
			if err := registry.ReplaceCredentials([]CredentialEntry{{
				ID: 1, GroupID: 10,
				Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "original-cipher",
			}}); err != nil {
				t.Fatalf("seed Replace() error = %v", err)
			}

			if err := registry.ReplaceCredentials(batch); err == nil {
				t.Fatal("invalid Replace() error = nil, want error")
			}
			if got, ok := registry.EncryptedCredentialData(1); !ok || got != "original-cipher" {
				t.Errorf("original EncryptedValue(1) = %q, %t after failed replacement, want %q, true", got, ok, "original-cipher")
			}
			for _, entry := range batch {
				if got, ok := registry.EncryptedCredentialData(entry.ID); ok || got != "" {
					t.Errorf("invalid EncryptedValue(%d) = %q, %t after failed replacement, want empty, false", entry.ID, got, ok)
				}
			}
		})
	}
}

func TestKeyRegistryApplyImportUpsertsOnlyRequestedGroup(t *testing.T) {
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{
		{ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "old-one"},
		{ID: 2, GroupID: 20, Status: CredentialStatusDisabled, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-two"},
	})

	weight := 12
	if err := registry.ApplyCredentialImport(10, []CredentialEntry{
		{ID: 1, GroupID: 10, Status: CredentialStatusDisabled, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "new-one"},
		{ID: 3, GroupID: 10, WeightManual: &weight, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-three"},
	}); err != nil {
		t.Fatalf("ApplyImport() error = %v", err)
	}

	assertEncryptedValue(t, registry, 1, "new-one", true)
	assertEncryptedValue(t, registry, 2, "cipher-two", true)
	assertEncryptedValue(t, registry, 3, "cipher-three", true)

	weight = 99
	registry.mu.RLock()
	storedWeight := *registry.buckets[10][3].WeightManual
	registry.mu.RUnlock()
	if storedWeight != 12 {
		t.Fatalf("imported WeightManual = %d after caller mutation, want 12", storedWeight)
	}
}

func TestKeyRegistryReconcileGroupUsesDBTruthAndPreservesSatisfiedRuntimeState(t *testing.T) {
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{
		{
			ID: 1, GroupID: 10, Status: CredentialStatusActive,
			Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one",
		},
		{
			ID: 2, GroupID: 10, Status: CredentialStatusActive,
			Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "stale-cipher",
		},
		{
			ID: 3, GroupID: 10, Status: CredentialStatusDisabled,
			Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "removed-from-db",
		},
		{
			ID: 4, GroupID: 20, Status: CredentialStatusActive,
			Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "other-group",
		},
	})
	if !registry.SetCooldown(1, time.Now().Add(time.Hour)) {
		t.Fatal("SetCooldown(1) = false")
	}
	if _, ok := registry.IncrFailure(1); !ok {
		t.Fatal("IncrFailure(1) = false")
	}

	wantGroup := []CredentialEntry{
		{
			ID: 1, GroupID: 10, Status: CredentialStatusActive,
			Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one",
		},
		{
			ID: 2, GroupID: 10, Status: CredentialStatusDisabled,
			Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-two",
		},
	}
	if registry.MatchesGroup(10, wantGroup) {
		t.Fatal("MatchesGroup(stale) = true, want false")
	}
	changed, err := registry.ReconcileGroup(10, wantGroup)
	if err != nil {
		t.Fatalf("ReconcileGroup() error = %v", err)
	}
	if !changed {
		t.Fatal("ReconcileGroup() changed = false, want true")
	}
	if !registry.MatchesGroup(10, wantGroup) {
		t.Fatal("MatchesGroup(reconciled) = false, want true")
	}
	if got := registryEntry(t, registry, 1); got.FailureCount != 1 ||
		got.CooldownUntil.IsZero() {
		t.Fatalf("satisfied key runtime state was reset: %#v", got)
	}
	if got := registryEntry(t, registry, 2); got.Status != CredentialStatusDisabled ||
		got.EncryptedValue != "cipher-two" || got.FailureCount != 0 {
		t.Fatalf("stale key was not rebuilt from DB truth: %#v", got)
	}
	if _, ok := registry.EncryptedCredentialData(3); ok {
		t.Fatal("key absent from DB truth remains in reconciled group")
	}
	if got, ok := registry.EncryptedCredentialData(4); !ok || got != "other-group" {
		t.Fatalf("other group changed: %q, %t", got, ok)
	}

	changed, err = registry.ReconcileGroup(10, wantGroup)
	if err != nil {
		t.Fatalf("second ReconcileGroup() error = %v", err)
	}
	if changed {
		t.Fatal("second ReconcileGroup() changed = true, want false")
	}
	if got := registryEntry(t, registry, 1); got.FailureCount != 1 {
		t.Fatalf("idempotent reconciliation reset runtime state: %#v", got)
	}
}

func TestKeyRegistryReconcileGroupRejectsCrossGroupIdentityWithoutMutation(t *testing.T) {
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{{
		ID: 1, GroupID: 20, Status: CredentialStatusActive,
		Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "other-group",
	}})

	_, err := registry.ReconcileGroup(10, []CredentialEntry{{
		ID: 1, GroupID: 10, Status: CredentialStatusActive,
		Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "attempted-move",
	}})
	if err == nil {
		t.Fatal("ReconcileGroup(cross-group key) error = nil")
	}
	if got, ok := registry.EncryptedCredentialData(1); !ok || got != "other-group" {
		t.Fatalf("failed reconciliation mutated registry: %q, %t", got, ok)
	}
}

func TestKeyRegistryRemoveKey(t *testing.T) {
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{
		{ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one"},
		{ID: 2, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-two"},
		{ID: 3, GroupID: 20, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-three"},
	})

	if removed := registry.RemoveCredential(1); !removed {
		t.Fatal("RemoveCredential(1) = false, want true")
	}
	assertEncryptedValue(t, registry, 1, "", false)
	assertEncryptedValue(t, registry, 2, "cipher-two", true)
	if removed := registry.RemoveCredential(1); removed {
		t.Fatal("second RemoveCredential(1) = true, want false")
	}

	if removed := registry.RemoveCredential(2); !removed {
		t.Fatal("RemoveCredential(2) = false, want true")
	}
	registry.mu.RLock()
	_, emptyBucketRetained := registry.buckets[10]
	registry.mu.RUnlock()
	if emptyBucketRetained {
		t.Fatal("empty group 10 bucket was retained")
	}
	assertEncryptedValue(t, registry, 3, "cipher-three", true)
}

func TestKeyRegistryRemoveGroupClearsBucketAndReverseIndexesAtomically(t *testing.T) {
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{
		{ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one"},
		{ID: 2, GroupID: 10, Status: CredentialStatusDisabled, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-two"},
		{ID: 3, GroupID: 20, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-three"},
	})

	if removed := registry.RemoveGroup(10); !removed {
		t.Fatal("RemoveGroup(10) = false, want true")
	}
	for _, id := range []uint{1, 2} {
		if value, ok := registry.EncryptedCredentialData(id); ok || value != "" {
			t.Fatalf("removed key %d = %q, %t", id, value, ok)
		}
	}
	if value, ok := registry.EncryptedCredentialData(3); !ok || value != "cipher-three" {
		t.Fatalf("other group key = %q, %t", value, ok)
	}
	if removed := registry.RemoveGroup(10); removed {
		t.Fatal("second RemoveGroup(10) = true")
	}
	if removed := registry.RemoveGroup(0); removed {
		t.Fatal("RemoveGroup(0) = true")
	}
}

func TestKeyRegistryRemoveGroupIsRaceSafeWithRuntimeReaders(t *testing.T) {
	registry := NewCredentialRegistry()
	entries := make([]CredentialEntry, 0, 100)
	for id := uint(1); id <= 100; id++ {
		entries = append(entries, CredentialEntry{
			ID: id, GroupID: 10, Status: CredentialStatusActive,
			Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: fmt.Sprintf("cipher-%d", id),
		})
	}
	mustReplaceKeyEntries(t, registry, entries)

	start := make(chan struct{})
	var readers sync.WaitGroup
	for range 8 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			<-start
			for range 100 {
				_ = registry.Snapshot()
				_ = registry.CollectCredentialCandidates([]uint{10}, nil, time.Time{})
				_ = registry.ActiveCredentialIDs()
			}
		}()
	}
	close(start)
	registry.RemoveGroup(10)
	readers.Wait()
	if got := registry.Snapshot(); len(got) != 0 {
		t.Fatalf("Snapshot after RemoveGroup = %#v", got)
	}
}

func TestKeyRegistrySetKeyStatus(t *testing.T) {
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{{
		ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one",
	}})

	if err := registry.SetCredentialStatus(1, CredentialStatusDisabled); err != nil {
		t.Fatalf("SetCredentialStatus(disabled) error = %v", err)
	}
	if got := keyStatus(t, registry, 1); got != CredentialStatusDisabled {
		t.Fatalf("key status = %q, want %q", got, CredentialStatusDisabled)
	}

	if err := registry.SetCredentialStatus(1, CredentialStatus("cooldown")); err == nil {
		t.Fatal("SetCredentialStatus(invalid) error = nil, want error")
	}
	if got := keyStatus(t, registry, 1); got != CredentialStatusDisabled {
		t.Fatalf("key status after invalid update = %q, want %q", got, CredentialStatusDisabled)
	}
	if err := registry.SetCredentialStatus(99, CredentialStatusActive); err == nil {
		t.Fatal("SetCredentialStatus(missing key) error = nil, want error")
	}
}

func TestKeyRegistryCollectCandidatesFiltersStatusAndExcluded(t *testing.T) {
	registry := NewCredentialRegistry()
	weight := 7
	mustReplaceKeyEntries(t, registry, []CredentialEntry{
		{ID: 4, GroupID: 30, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "unselected"},
		{
			ID: 3, GroupID: 20, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-three",
			CooldownUntil: time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC),
			Blacklisted:   true, FailureCount: 9,
		},
		{ID: 2, GroupID: 10, Status: CredentialStatusDisabled, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "disabled"},
		{ID: 5, GroupID: 20, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "excluded"},
		{ID: 1, GroupID: 10, WeightManual: &weight, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one"},
	})

	excluded := map[uint]bool{5: true}
	got := registry.CollectCredentialCandidates([]uint{20, 10}, func(credentialID uint) bool {
		return excluded[credentialID]
	}, time.Time{})
	if len(got) != 1 {
		t.Fatalf("CollectCandidates() length = %d, want 1: %#v", len(got), got)
	}
	if got[0].GroupID != 10 || got[0].ID != 1 {
		t.Errorf("CollectCandidates()[0] = %#v, want group 10 key 1", got[0])
	}
	if got[0].WeightManual == nil || *got[0].WeightManual != 7 {
		t.Fatalf("CollectCandidates()[0].WeightManual = %v, want 7", got[0].WeightManual)
	}

	*got[0].WeightManual = 99
	again := registry.CollectCredentialCandidates([]uint{10}, nil, time.Time{})
	if len(again) != 1 || again[0].WeightManual == nil || *again[0].WeightManual != 7 {
		t.Fatalf("CollectCandidates() after caller mutation = %#v, want isolated weight 7", again)
	}

	typ := reflect.TypeOf(CredentialMeta{})
	for _, field := range []string{"EncryptedValue", "CooldownUntil", "Blacklisted", "FailureCount"} {
		if _, ok := typ.FieldByName(field); ok {
			t.Fatalf("CredentialMeta exposes private field %s", field)
		}
	}
}

func TestKeyRegistryReservedRuntimeMutationsAreAtomic(t *testing.T) {
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{{
		ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one",
	}})

	until := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	if ok := registry.SetCooldown(1, until); !ok {
		t.Fatal("SetCooldown(1) = false, want true")
	}
	if ok := registry.SetBlacklisted(1); !ok {
		t.Fatal("SetBlacklisted(1) = false, want true")
	}
	for want := 1; want <= 2; want++ {
		got, ok := registry.IncrFailure(1)
		if !ok || got != want {
			t.Fatalf("IncrFailure(1) = %d, %t, want %d, true", got, ok, want)
		}
	}

	registry.mu.RLock()
	entry := *registry.buckets[10][1]
	registry.mu.RUnlock()
	if !entry.CooldownUntil.Equal(until) {
		t.Errorf("CooldownUntil = %v, want %v", entry.CooldownUntil, until)
	}
	if !entry.Blacklisted {
		t.Error("Blacklisted = false, want true")
	}
	if entry.FailureCount != 2 {
		t.Errorf("FailureCount = %d, want 2", entry.FailureCount)
	}

	if ok := registry.SetCooldown(99, until); ok {
		t.Error("SetCooldown(missing key) = true, want false")
	}
	if ok := registry.SetBlacklisted(99); ok {
		t.Error("SetBlacklisted(missing key) = true, want false")
	}
	if got, ok := registry.IncrFailure(99); ok || got != 0 {
		t.Errorf("IncrFailure(missing key) = %d, %t, want 0, false", got, ok)
	}
}

func TestKeyRegistryConcurrentMutationsAndCollection(t *testing.T) {
	const (
		writerCount = 6
		readerCount = 6
		operations  = 48
	)

	registry := NewCredentialRegistry()
	groupIDs := make([]uint, writerCount)
	for writer := 0; writer < writerCount; writer++ {
		groupIDs[writer] = uint(writer + 1)
	}

	start := make(chan struct{})
	errors := make(chan error, writerCount*operations*10+readerCount*writerCount*operations)
	var wg sync.WaitGroup
	for writer := 0; writer < writerCount; writer++ {
		groupID := uint(writer + 1)
		keyBase := uint((writer + 1) * 1000)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for operation := 0; operation < operations; operation++ {
				credentialID := keyBase + uint(operation)
				entry := CredentialEntry{
					ID: credentialID, GroupID: groupID, Status: CredentialStatusActive,
					Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: fmt.Sprintf("cipher-%d", credentialID),
				}
				if err := registry.ApplyCredentialImport(groupID, []CredentialEntry{entry}); err != nil {
					errors <- fmt.Errorf("ApplyImport(group %d, key %d): %w", groupID, credentialID, err)
					continue
				}
				if err := registry.SetCredentialStatus(credentialID, CredentialStatusDisabled); err != nil {
					errors <- fmt.Errorf("SetCredentialStatus(%d, disabled): %w", credentialID, err)
				}
				if ok := registry.SetAutoWeight(credentialID, operation%MaxWeight+1); !ok {
					errors <- fmt.Errorf("SetAutoWeight(%d) = false", credentialID)
				}
				if ok := registry.SetCooldown(credentialID, time.Unix(int64(operation+1), 0)); !ok {
					errors <- fmt.Errorf("SetCooldown(%d) = false", credentialID)
				}
				if ok := registry.SetBlacklisted(credentialID); !ok {
					errors <- fmt.Errorf("SetBlacklisted(%d) = false", credentialID)
				}
				if _, ok := registry.IncrFailure(credentialID); !ok {
					errors <- fmt.Errorf("IncrFailure(%d) = false", credentialID)
				}
				if ok := registry.ClearFailure(credentialID); !ok {
					errors <- fmt.Errorf("ClearFailure(%d) = false", credentialID)
				}
				if ok := registry.Recover(credentialID); !ok {
					errors <- fmt.Errorf("Recover(%d) = false", credentialID)
				}
				if err := registry.SetCredentialStatus(credentialID, CredentialStatusActive); err != nil {
					errors <- fmt.Errorf("SetCredentialStatus(%d, active): %w", credentialID, err)
				}
				if operation%3 == 0 {
					if removed := registry.RemoveCredential(credentialID); !removed {
						errors <- fmt.Errorf("RemoveCredential(%d) = false", credentialID)
					}
				} else if _, ok := registry.EncryptedCredentialData(credentialID); !ok {
					errors <- fmt.Errorf("EncryptedValue(%d) missing after import", credentialID)
				}
			}
		}()
	}

	for reader := 0; reader < readerCount; reader++ {
		wg.Add(1)
		go func(reader int) {
			defer wg.Done()
			<-start
			for operation := 0; operation < writerCount*operations; operation++ {
				writer := operation % writerCount
				credentialID := uint((writer+1)*1000 + operation/writerCount%operations)
				registry.EncryptedCredentialData(credentialID)
				registry.BlacklistedCredentials()
				candidates := registry.CollectCredentialCandidates(groupIDs, func(candidateID uint) bool {
					return candidateID%19 == uint(reader)%19
				}, time.Now())
				for index := 1; index < len(candidates); index++ {
					previous := candidates[index-1]
					current := candidates[index]
					if previous.GroupID > current.GroupID ||
						(previous.GroupID == current.GroupID && previous.ID > current.ID) {
						errors <- fmt.Errorf("CollectCandidates() returned unsorted entries: %#v then %#v", previous, current)
						break
					}
				}
				views := registry.Snapshot()
				for index := 1; index < len(views); index++ {
					previous, current := views[index-1], views[index]
					if previous.GroupID > current.GroupID ||
						(previous.GroupID == current.GroupID && previous.ID > current.ID) {
						errors <- fmt.Errorf("Snapshot() returned unstable order: %#v then %#v", previous, current)
						break
					}
				}
			}
		}(reader)
	}

	close(start)
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}

	finalWeight := 13
	finalEntries := []CredentialEntry{
		{ID: 42, GroupID: 4, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "final-forty-two"},
		{ID: 43, GroupID: 4, Status: CredentialStatusDisabled, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "final-disabled"},
		{ID: 7, GroupID: 2, WeightManual: &finalWeight, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "final-seven"},
	}
	if err := registry.ReplaceCredentials(finalEntries); err != nil {
		t.Fatalf("final Replace() error = %v", err)
	}

	got := registry.CollectCredentialCandidates([]uint{4, 2}, nil, time.Time{})
	if len(got) != 2 {
		t.Fatalf("final CollectCandidates() length = %d, want 2: %#v", len(got), got)
	}
	if got[0].GroupID != 2 || got[0].ID != 7 || got[0].WeightManual == nil || *got[0].WeightManual != 13 {
		t.Errorf("final CollectCandidates()[0] = %#v, want group 2 key 7 weight 13", got[0])
	}
	if got[1].GroupID != 4 || got[1].ID != 42 || got[1].WeightManual != nil {
		t.Errorf("final CollectCandidates()[1] = %#v, want group 4 key 42 without manual weight", got[1])
	}

	for credentialID, want := range map[uint]string{
		7:  "final-seven",
		42: "final-forty-two",
		43: "final-disabled",
	} {
		assertEncryptedValue(t, registry, credentialID, want, true)
	}
	for writer := 0; writer < writerCount; writer++ {
		assertEncryptedValue(t, registry, uint((writer+1)*1000), "", false)
	}
}

func TestValidateKeyEntriesRejectsMalformedEntries(t *testing.T) {
	tests := []struct {
		name        string
		entries     []CredentialEntry
		wantInError string
	}{
		{
			name:        "zero id",
			entries:     []CredentialEntry{{GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher"}},
			wantInError: "id is required",
		},
		{
			name:        "zero group id",
			entries:     []CredentialEntry{{ID: 1, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher"}},
			wantInError: "group id is required",
		},
		{
			name:        "invalid status",
			entries:     []CredentialEntry{{ID: 1, GroupID: 10, Status: CredentialStatus("cooldown"), Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher"}},
			wantInError: "invalid status",
		},
		{
			name:        "empty ciphertext",
			entries:     []CredentialEntry{{ID: 1, GroupID: 10, Status: CredentialStatusActive}},
			wantInError: "encrypted value is required",
		},
		{
			name: "duplicate id",
			entries: []CredentialEntry{
				{ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one"},
				{ID: 1, GroupID: 20, Status: CredentialStatusDisabled, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-two"},
			},
			wantInError: "duplicate credential id 1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateCredentialEntries(test.entries)
			if err == nil {
				t.Fatal("ValidateCredentialEntries() error = nil, want error")
			}
			if !strings.Contains(err.Error(), test.wantInError) {
				t.Fatalf("ValidateCredentialEntries() error = %q, want substring %q", err, test.wantInError)
			}
		})
	}

	if err := ValidateCredentialEntries([]CredentialEntry{
		{ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one"},
		{ID: 2, GroupID: 10, Status: CredentialStatusDisabled, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-two"},
	}); err != nil {
		t.Fatalf("ValidateCredentialEntries(valid) error = %v", err)
	}
}

func TestKeyRegistryApplyImportFailureDoesNotMutateRegistry(t *testing.T) {
	tests := []struct {
		name    string
		groupID uint
		batch   []CredentialEntry
	}{
		{
			name:    "invalid entry after valid entry",
			groupID: 10,
			batch: []CredentialEntry{
				{ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "updated-one"},
				{ID: 2, GroupID: 10, Status: CredentialStatusActive},
			},
		},
		{
			name:    "entry group differs from requested group",
			groupID: 10,
			batch: []CredentialEntry{
				{ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "updated-one"},
				{ID: 3, GroupID: 20, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-three"},
			},
		},
		{
			name: "zero requested group",
			batch: []CredentialEntry{
				{ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "updated-one"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := NewCredentialRegistry()
			mustReplaceKeyEntries(t, registry, []CredentialEntry{{
				ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "original-one",
			}})

			if err := registry.ApplyCredentialImport(test.groupID, test.batch); err == nil {
				t.Fatal("ApplyImport(invalid batch) error = nil, want error")
			}
			assertEncryptedValue(t, registry, 1, "original-one", true)
			for _, entry := range test.batch {
				if entry.ID != 1 {
					assertEncryptedValue(t, registry, entry.ID, "", false)
				}
			}
		})
	}
}

func TestKeyRegistryApplyImportRejectsExistingIDFromAnotherGroup(t *testing.T) {
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{{
		ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "original-one",
	}})

	err := registry.ApplyCredentialImport(20, []CredentialEntry{
		{ID: 2, GroupID: 20, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-two"},
		{ID: 1, GroupID: 20, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "moved-one"},
	})
	if err == nil {
		t.Fatal("ApplyImport(cross-group key id) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "already belongs to group 10") {
		t.Fatalf("ApplyImport(cross-group key id) error = %q, want existing group context", err)
	}
	assertEncryptedValue(t, registry, 1, "original-one", true)
	assertEncryptedValue(t, registry, 2, "", false)
}

func TestKeyRegistryUpdateKeyConfigAtomicallyPreservesRuntimeState(t *testing.T) {
	oldWeight := 20
	newWeight := 80
	cooldown := time.Date(2026, time.July, 24, 12, 30, 0, 0, time.UTC)
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{{
		ID: 11, GroupID: 7, WeightManual: &oldWeight, WeightAuto: 42,
		Status: CredentialStatusActive, CooldownUntil: cooldown,
		Blacklisted: true, FailureCount: 3, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-secret",
	}})

	if err := registry.UpdateCredentialConfig(11, CredentialStatusDisabled, &newWeight); err != nil {
		t.Fatalf("UpdateCredentialConfig() error = %v", err)
	}
	newWeight = 99
	registry.mu.RLock()
	got := cloneCredentialEntry(*registry.buckets[7][11])
	registry.mu.RUnlock()
	if got.Status != CredentialStatusDisabled || got.WeightManual == nil || *got.WeightManual != 80 {
		t.Fatalf("config = %#v", got)
	}
	if got.WeightAuto != 42 || !got.CooldownUntil.Equal(cooldown) ||
		!got.Blacklisted || got.FailureCount != 3 ||
		got.EncryptedValue != "cipher-secret" || got.GroupID != 7 {
		t.Fatalf("runtime fields changed = %#v", got)
	}

	if err := registry.UpdateCredentialConfig(11, CredentialStatusActive, nil); err != nil {
		t.Fatal(err)
	}
	view := registry.Snapshot()[0]
	if view.Status != CredentialStatusActive || view.WeightManual != nil ||
		view.WeightAuto != 42 || !view.Blacklisted || view.FailureCount != 3 {
		t.Fatalf("cleared manual state = %#v", view)
	}
}

func TestKeyRegistryUpdateKeyConfigRejectsInvalidInputWithoutPartialMutation(t *testing.T) {
	weight := 30
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{{
		ID: 1, GroupID: 10, WeightManual: &weight,
		Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one",
	}})
	before := registry.Snapshot()

	for _, test := range []struct {
		name         string
		credentialID uint
		status       CredentialStatus
		weight       *int
	}{
		{name: "missing", credentialID: 99, status: CredentialStatusDisabled},
		{name: "invalid status", credentialID: 1, status: CredentialStatus("cooldown")},
		{name: "negative weight", credentialID: 1, status: CredentialStatusDisabled, weight: intPointer(-1)},
		{name: "large weight", credentialID: 1, status: CredentialStatusDisabled, weight: intPointer(101)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := registry.UpdateCredentialConfig(test.credentialID, test.status, test.weight); err == nil {
				t.Fatal("UpdateCredentialConfig() error = nil")
			}
			if got := registry.Snapshot(); !reflect.DeepEqual(got, before) {
				t.Fatalf("Registry mutated:\ngot=%#v\nwant=%#v", got, before)
			}
		})
	}
}

func TestKeyRegistryUpdateKeyConfigIsRaceSafeWithRuntimeMutations(t *testing.T) {
	registry := NewCredentialRegistry()
	mustReplaceKeyEntries(t, registry, []CredentialEntry{{
		ID: 1, GroupID: 10, Status: CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher-one",
	}})
	start := make(chan struct{})
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		worker := worker
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for index := 0; index < 100; index++ {
				weight := (worker + index) % (MaxWeight + 1)
				_ = registry.UpdateCredentialConfig(1, CredentialStatusActive, &weight)
				_ = registry.SetAutoWeight(1, (index%MaxWeight)+1)
				_ = registry.SetCooldown(1, time.Unix(int64(index), 0))
				_, _ = registry.IncrFailure(1)
			}
		}()
	}
	close(start)
	workers.Wait()
	view := registry.Snapshot()[0]
	if view.GroupID != 10 || view.Status != CredentialStatusActive {
		t.Fatalf("final safe view = %#v", view)
	}
	if value, ok := registry.EncryptedCredentialData(1); !ok || value != "cipher-one" {
		t.Fatalf("credential = %q, %t", value, ok)
	}
}

func intPointer(value int) *int {
	return &value
}

func mustReplaceKeyEntries(t *testing.T, registry *CredentialRegistry, entries []CredentialEntry) {
	t.Helper()
	if err := registry.ReplaceCredentials(entries); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
}

func assertEncryptedValue(t *testing.T, registry *CredentialRegistry, credentialID uint, want string, wantOK bool) {
	t.Helper()
	got, ok := registry.EncryptedCredentialData(credentialID)
	if got != want || ok != wantOK {
		t.Errorf("EncryptedValue(%d) = %q, %t, want %q, %t", credentialID, got, ok, want, wantOK)
	}
}

func keyStatus(t *testing.T, registry *CredentialRegistry, credentialID uint) CredentialStatus {
	t.Helper()
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	groupID, ok := registry.credentialGroups[credentialID]
	if !ok {
		t.Fatalf("key %d does not exist", credentialID)
	}
	return registry.buckets[groupID][credentialID].Status
}
