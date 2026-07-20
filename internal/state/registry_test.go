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
	registry := NewKeyRegistry()
	weight := 7
	entries := []KeyEntry{
		{
			ID: 1, GroupID: 10, WeightManual: &weight,
			Status: KeyStatusActive, EncryptedValue: "cipher-one",
		},
		{
			ID: 2, GroupID: 20,
			Status: KeyStatusDisabled, EncryptedValue: "cipher-two",
		},
	}
	if err := registry.Replace(entries); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}

	for _, test := range []struct {
		keyID uint
		want  string
	}{
		{keyID: 1, want: "cipher-one"},
		{keyID: 2, want: "cipher-two"},
	} {
		got, ok := registry.EncryptedValue(test.keyID)
		if !ok || got != test.want {
			t.Errorf("EncryptedValue(%d) = %q, %t, want %q, true", test.keyID, got, ok, test.want)
		}
	}

	weight = 99
	registry.mu.RLock()
	storedWeight := *registry.buckets[10][1].WeightManual
	registry.mu.RUnlock()
	if storedWeight != 7 {
		t.Fatalf("stored WeightManual = %d after caller mutation, want 7", storedWeight)
	}

	if err := registry.Replace([]KeyEntry{{
		ID: 3, GroupID: 20,
		Status: KeyStatusActive, EncryptedValue: "cipher-three",
	}}); err != nil {
		t.Fatalf("second Replace() error = %v", err)
	}
	for _, removedID := range []uint{1, 2} {
		if got, ok := registry.EncryptedValue(removedID); ok || got != "" {
			t.Errorf("EncryptedValue(%d) after replacement = %q, %t, want empty, false", removedID, got, ok)
		}
	}
	if got, ok := registry.EncryptedValue(3); !ok || got != "cipher-three" {
		t.Fatalf("EncryptedValue(3) = %q, %t, want %q, true", got, ok, "cipher-three")
	}
}

func TestKeyRegistryActiveEncryptedValueRequiresExpectedGroupAndActiveStatus(t *testing.T) {
	registry := NewKeyRegistry()
	mustReplaceKeyEntries(t, registry, []KeyEntry{
		{ID: 1, GroupID: 10, Status: KeyStatusActive, EncryptedValue: "cipher-active"},
		{ID: 2, GroupID: 10, Status: KeyStatusDisabled, EncryptedValue: "cipher-disabled"},
	})

	tests := []struct {
		name    string
		keyID   uint
		groupID uint
		want    string
		wantOK  bool
	}{
		{name: "matching active key", keyID: 1, groupID: 10, want: "cipher-active", wantOK: true},
		{name: "group mismatch", keyID: 1, groupID: 20},
		{name: "disabled key", keyID: 2, groupID: 10},
		{name: "missing key", keyID: 99, groupID: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := registry.ActiveEncryptedValue(tt.keyID, tt.groupID)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("ActiveEncryptedValue(%d, %d) = %q, %t, want %q, %t", tt.keyID, tt.groupID, got, ok, tt.want, tt.wantOK)
			}
		})
	}

	mustReplaceKeyEntries(t, registry, []KeyEntry{{
		ID: 1, GroupID: 20, Status: KeyStatusActive, EncryptedValue: "cipher-moved",
	}})
	if got, ok := registry.ActiveEncryptedValue(1, 10); got != "" || ok {
		t.Fatalf("ActiveEncryptedValue(1, old group) = %q, %t, want empty, false", got, ok)
	}
	if got, ok := registry.ActiveEncryptedValue(1, 20); got != "cipher-moved" || !ok {
		t.Fatalf("ActiveEncryptedValue(1, new group) = %q, %t, want cipher-moved, true", got, ok)
	}
}

func TestKeyRegistryReplaceFailurePreservesRegistry(t *testing.T) {
	invalidBatches := map[string][]KeyEntry{
		"duplicate ids": {
			{ID: 2, GroupID: 20, Status: KeyStatusActive, EncryptedValue: "cipher-two"},
			{ID: 2, GroupID: 20, Status: KeyStatusDisabled, EncryptedValue: "duplicate"},
		},
		"invalid status": {
			{ID: 3, GroupID: 30, Status: KeyStatus("cooldown"), EncryptedValue: "cipher-three"},
		},
	}

	for name, batch := range invalidBatches {
		t.Run(name, func(t *testing.T) {
			registry := NewKeyRegistry()
			if err := registry.Replace([]KeyEntry{{
				ID: 1, GroupID: 10,
				Status: KeyStatusActive, EncryptedValue: "original-cipher",
			}}); err != nil {
				t.Fatalf("seed Replace() error = %v", err)
			}

			if err := registry.Replace(batch); err == nil {
				t.Fatal("invalid Replace() error = nil, want error")
			}
			if got, ok := registry.EncryptedValue(1); !ok || got != "original-cipher" {
				t.Errorf("original EncryptedValue(1) = %q, %t after failed replacement, want %q, true", got, ok, "original-cipher")
			}
			for _, entry := range batch {
				if got, ok := registry.EncryptedValue(entry.ID); ok || got != "" {
					t.Errorf("invalid EncryptedValue(%d) = %q, %t after failed replacement, want empty, false", entry.ID, got, ok)
				}
			}
		})
	}
}

func TestKeyRegistryApplyImportUpsertsOnlyRequestedGroup(t *testing.T) {
	registry := NewKeyRegistry()
	mustReplaceKeyEntries(t, registry, []KeyEntry{
		{ID: 1, GroupID: 10, Status: KeyStatusActive, EncryptedValue: "old-one"},
		{ID: 2, GroupID: 20, Status: KeyStatusDisabled, EncryptedValue: "cipher-two"},
	})

	weight := 12
	if err := registry.ApplyImport(10, []KeyEntry{
		{ID: 1, GroupID: 10, Status: KeyStatusDisabled, EncryptedValue: "new-one"},
		{ID: 3, GroupID: 10, WeightManual: &weight, Status: KeyStatusActive, EncryptedValue: "cipher-three"},
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

func TestKeyRegistryRemoveKey(t *testing.T) {
	registry := NewKeyRegistry()
	mustReplaceKeyEntries(t, registry, []KeyEntry{
		{ID: 1, GroupID: 10, Status: KeyStatusActive, EncryptedValue: "cipher-one"},
		{ID: 2, GroupID: 10, Status: KeyStatusActive, EncryptedValue: "cipher-two"},
		{ID: 3, GroupID: 20, Status: KeyStatusActive, EncryptedValue: "cipher-three"},
	})

	if removed := registry.RemoveKey(1); !removed {
		t.Fatal("RemoveKey(1) = false, want true")
	}
	assertEncryptedValue(t, registry, 1, "", false)
	assertEncryptedValue(t, registry, 2, "cipher-two", true)
	if removed := registry.RemoveKey(1); removed {
		t.Fatal("second RemoveKey(1) = true, want false")
	}

	if removed := registry.RemoveKey(2); !removed {
		t.Fatal("RemoveKey(2) = false, want true")
	}
	registry.mu.RLock()
	_, emptyBucketRetained := registry.buckets[10]
	registry.mu.RUnlock()
	if emptyBucketRetained {
		t.Fatal("empty group 10 bucket was retained")
	}
	assertEncryptedValue(t, registry, 3, "cipher-three", true)
}

func TestKeyRegistrySetKeyStatus(t *testing.T) {
	registry := NewKeyRegistry()
	mustReplaceKeyEntries(t, registry, []KeyEntry{{
		ID: 1, GroupID: 10, Status: KeyStatusActive, EncryptedValue: "cipher-one",
	}})

	if err := registry.SetKeyStatus(1, KeyStatusDisabled); err != nil {
		t.Fatalf("SetKeyStatus(disabled) error = %v", err)
	}
	if got := keyStatus(t, registry, 1); got != KeyStatusDisabled {
		t.Fatalf("key status = %q, want %q", got, KeyStatusDisabled)
	}

	if err := registry.SetKeyStatus(1, KeyStatus("cooldown")); err == nil {
		t.Fatal("SetKeyStatus(invalid) error = nil, want error")
	}
	if got := keyStatus(t, registry, 1); got != KeyStatusDisabled {
		t.Fatalf("key status after invalid update = %q, want %q", got, KeyStatusDisabled)
	}
	if err := registry.SetKeyStatus(99, KeyStatusActive); err == nil {
		t.Fatal("SetKeyStatus(missing key) error = nil, want error")
	}
}

func TestKeyRegistryCollectCandidatesFiltersStatusAndExcluded(t *testing.T) {
	registry := NewKeyRegistry()
	weight := 7
	mustReplaceKeyEntries(t, registry, []KeyEntry{
		{ID: 4, GroupID: 30, Status: KeyStatusActive, EncryptedValue: "unselected"},
		{
			ID: 3, GroupID: 20, Status: KeyStatusActive, EncryptedValue: "cipher-three",
			CooldownUntil: time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC),
			Blacklisted:   true, FailureCount: 9,
		},
		{ID: 2, GroupID: 10, Status: KeyStatusDisabled, EncryptedValue: "disabled"},
		{ID: 5, GroupID: 20, Status: KeyStatusActive, EncryptedValue: "excluded"},
		{ID: 1, GroupID: 10, WeightManual: &weight, Status: KeyStatusActive, EncryptedValue: "cipher-one"},
	})

	excluded := map[uint]bool{5: true}
	got := registry.CollectCandidates([]uint{20, 10}, func(keyID uint) bool {
		return excluded[keyID]
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
	again := registry.CollectCandidates([]uint{10}, nil, time.Time{})
	if len(again) != 1 || again[0].WeightManual == nil || *again[0].WeightManual != 7 {
		t.Fatalf("CollectCandidates() after caller mutation = %#v, want isolated weight 7", again)
	}

	typ := reflect.TypeOf(KeyMeta{})
	for _, field := range []string{"EncryptedValue", "CooldownUntil", "Blacklisted", "FailureCount"} {
		if _, ok := typ.FieldByName(field); ok {
			t.Fatalf("KeyMeta exposes private field %s", field)
		}
	}
}

func TestKeyRegistryReservedRuntimeMutationsAreAtomic(t *testing.T) {
	registry := NewKeyRegistry()
	mustReplaceKeyEntries(t, registry, []KeyEntry{{
		ID: 1, GroupID: 10, Status: KeyStatusActive, EncryptedValue: "cipher-one",
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

	registry := NewKeyRegistry()
	groupIDs := make([]uint, writerCount)
	for writer := 0; writer < writerCount; writer++ {
		groupIDs[writer] = uint(writer + 1)
	}

	start := make(chan struct{})
	errors := make(chan error, writerCount*operations*6+readerCount*writerCount*operations)
	var wg sync.WaitGroup
	for writer := 0; writer < writerCount; writer++ {
		groupID := uint(writer + 1)
		keyBase := uint((writer + 1) * 1000)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for operation := 0; operation < operations; operation++ {
				keyID := keyBase + uint(operation)
				entry := KeyEntry{
					ID: keyID, GroupID: groupID, Status: KeyStatusActive,
					EncryptedValue: fmt.Sprintf("cipher-%d", keyID),
				}
				if err := registry.ApplyImport(groupID, []KeyEntry{entry}); err != nil {
					errors <- fmt.Errorf("ApplyImport(group %d, key %d): %w", groupID, keyID, err)
					continue
				}
				if err := registry.SetKeyStatus(keyID, KeyStatusDisabled); err != nil {
					errors <- fmt.Errorf("SetKeyStatus(%d, disabled): %w", keyID, err)
				}
				if ok := registry.SetCooldown(keyID, time.Unix(int64(operation+1), 0)); !ok {
					errors <- fmt.Errorf("SetCooldown(%d) = false", keyID)
				}
				if ok := registry.SetBlacklisted(keyID); !ok {
					errors <- fmt.Errorf("SetBlacklisted(%d) = false", keyID)
				}
				if _, ok := registry.IncrFailure(keyID); !ok {
					errors <- fmt.Errorf("IncrFailure(%d) = false", keyID)
				}
				if err := registry.SetKeyStatus(keyID, KeyStatusActive); err != nil {
					errors <- fmt.Errorf("SetKeyStatus(%d, active): %w", keyID, err)
				}
				if operation%3 == 0 {
					if removed := registry.RemoveKey(keyID); !removed {
						errors <- fmt.Errorf("RemoveKey(%d) = false", keyID)
					}
				} else if _, ok := registry.EncryptedValue(keyID); !ok {
					errors <- fmt.Errorf("EncryptedValue(%d) missing after import", keyID)
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
				keyID := uint((writer+1)*1000 + operation/writerCount%operations)
				registry.EncryptedValue(keyID)
				candidates := registry.CollectCandidates(groupIDs, func(candidateID uint) bool {
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
	finalEntries := []KeyEntry{
		{ID: 42, GroupID: 4, Status: KeyStatusActive, EncryptedValue: "final-forty-two"},
		{ID: 43, GroupID: 4, Status: KeyStatusDisabled, EncryptedValue: "final-disabled"},
		{ID: 7, GroupID: 2, WeightManual: &finalWeight, Status: KeyStatusActive, EncryptedValue: "final-seven"},
	}
	if err := registry.Replace(finalEntries); err != nil {
		t.Fatalf("final Replace() error = %v", err)
	}

	got := registry.CollectCandidates([]uint{4, 2}, nil, time.Time{})
	if len(got) != 2 {
		t.Fatalf("final CollectCandidates() length = %d, want 2: %#v", len(got), got)
	}
	if got[0].GroupID != 2 || got[0].ID != 7 || got[0].WeightManual == nil || *got[0].WeightManual != 13 {
		t.Errorf("final CollectCandidates()[0] = %#v, want group 2 key 7 weight 13", got[0])
	}
	if got[1].GroupID != 4 || got[1].ID != 42 || got[1].WeightManual != nil {
		t.Errorf("final CollectCandidates()[1] = %#v, want group 4 key 42 without manual weight", got[1])
	}

	for keyID, want := range map[uint]string{
		7:  "final-seven",
		42: "final-forty-two",
		43: "final-disabled",
	} {
		assertEncryptedValue(t, registry, keyID, want, true)
	}
	for writer := 0; writer < writerCount; writer++ {
		assertEncryptedValue(t, registry, uint((writer+1)*1000), "", false)
	}
}

func TestValidateKeyEntriesRejectsMalformedEntries(t *testing.T) {
	tests := []struct {
		name        string
		entries     []KeyEntry
		wantInError string
	}{
		{
			name:        "zero id",
			entries:     []KeyEntry{{GroupID: 10, Status: KeyStatusActive, EncryptedValue: "cipher"}},
			wantInError: "id is required",
		},
		{
			name:        "zero group id",
			entries:     []KeyEntry{{ID: 1, Status: KeyStatusActive, EncryptedValue: "cipher"}},
			wantInError: "group id is required",
		},
		{
			name:        "invalid status",
			entries:     []KeyEntry{{ID: 1, GroupID: 10, Status: KeyStatus("cooldown"), EncryptedValue: "cipher"}},
			wantInError: "invalid status",
		},
		{
			name:        "empty ciphertext",
			entries:     []KeyEntry{{ID: 1, GroupID: 10, Status: KeyStatusActive}},
			wantInError: "encrypted value is required",
		},
		{
			name: "duplicate id",
			entries: []KeyEntry{
				{ID: 1, GroupID: 10, Status: KeyStatusActive, EncryptedValue: "cipher-one"},
				{ID: 1, GroupID: 20, Status: KeyStatusDisabled, EncryptedValue: "cipher-two"},
			},
			wantInError: "duplicate key id 1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateKeyEntries(test.entries)
			if err == nil {
				t.Fatal("ValidateKeyEntries() error = nil, want error")
			}
			if !strings.Contains(err.Error(), test.wantInError) {
				t.Fatalf("ValidateKeyEntries() error = %q, want substring %q", err, test.wantInError)
			}
		})
	}

	if err := ValidateKeyEntries([]KeyEntry{
		{ID: 1, GroupID: 10, Status: KeyStatusActive, EncryptedValue: "cipher-one"},
		{ID: 2, GroupID: 10, Status: KeyStatusDisabled, EncryptedValue: "cipher-two"},
	}); err != nil {
		t.Fatalf("ValidateKeyEntries(valid) error = %v", err)
	}
}

func TestKeyRegistryApplyImportFailureDoesNotMutateRegistry(t *testing.T) {
	tests := []struct {
		name    string
		groupID uint
		batch   []KeyEntry
	}{
		{
			name:    "invalid entry after valid entry",
			groupID: 10,
			batch: []KeyEntry{
				{ID: 1, GroupID: 10, Status: KeyStatusActive, EncryptedValue: "updated-one"},
				{ID: 2, GroupID: 10, Status: KeyStatusActive},
			},
		},
		{
			name:    "entry group differs from requested group",
			groupID: 10,
			batch: []KeyEntry{
				{ID: 1, GroupID: 10, Status: KeyStatusActive, EncryptedValue: "updated-one"},
				{ID: 3, GroupID: 20, Status: KeyStatusActive, EncryptedValue: "cipher-three"},
			},
		},
		{
			name: "zero requested group",
			batch: []KeyEntry{
				{ID: 1, GroupID: 10, Status: KeyStatusActive, EncryptedValue: "updated-one"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := NewKeyRegistry()
			mustReplaceKeyEntries(t, registry, []KeyEntry{{
				ID: 1, GroupID: 10, Status: KeyStatusActive, EncryptedValue: "original-one",
			}})

			if err := registry.ApplyImport(test.groupID, test.batch); err == nil {
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
	registry := NewKeyRegistry()
	mustReplaceKeyEntries(t, registry, []KeyEntry{{
		ID: 1, GroupID: 10, Status: KeyStatusActive, EncryptedValue: "original-one",
	}})

	err := registry.ApplyImport(20, []KeyEntry{
		{ID: 2, GroupID: 20, Status: KeyStatusActive, EncryptedValue: "cipher-two"},
		{ID: 1, GroupID: 20, Status: KeyStatusActive, EncryptedValue: "moved-one"},
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

func mustReplaceKeyEntries(t *testing.T, registry *KeyRegistry, entries []KeyEntry) {
	t.Helper()
	if err := registry.Replace(entries); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
}

func assertEncryptedValue(t *testing.T, registry *KeyRegistry, keyID uint, want string, wantOK bool) {
	t.Helper()
	got, ok := registry.EncryptedValue(keyID)
	if got != want || ok != wantOK {
		t.Errorf("EncryptedValue(%d) = %q, %t, want %q, %t", keyID, got, ok, want, wantOK)
	}
}

func keyStatus(t *testing.T, registry *KeyRegistry, keyID uint) KeyStatus {
	t.Helper()
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	groupID, ok := registry.keyGroups[keyID]
	if !ok {
		t.Fatalf("key %d does not exist", keyID)
	}
	return registry.buckets[groupID][keyID].Status
}
