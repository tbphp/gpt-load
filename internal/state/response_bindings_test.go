package state

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestResponseBindingsPreserveIdentityAndOriginalExpiry(t *testing.T) {
	now := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	bindings := NewResponseBindings()
	bindings.now = func() time.Time { return now }
	bindings.ttl = time.Hour
	ref := CredentialRef{ID: 1, GroupID: 2, IdentityGeneration: 3, Version: 1}
	if !bindings.Record(1, "opaque/id", ref) {
		t.Fatal("record failed")
	}
	now = now.Add(30 * time.Minute)
	ref.Version++
	if !bindings.Record(1, "opaque/id", ref) {
		t.Fatal("normal token refresh changed ownership")
	}
	ref.IdentityGeneration++
	if bindings.Record(1, "opaque/id", ref) {
		t.Fatal("another identity replaced existing ownership")
	}
	if saved, ok := bindings.Lookup(1, "opaque/id"); !ok || saved.IdentityGeneration != 3 {
		t.Fatal("ownership changed after conflict")
	}
	if _, ok := bindings.Lookup(2, "opaque/id"); ok {
		t.Fatal("binding crossed AccessKeys")
	}
	now = now.Add(30 * time.Minute)
	if _, ok := bindings.Lookup(1, "opaque/id"); ok {
		t.Fatal("duplicate observation extended the original expiry")
	}
}

func TestResponseBindingsEvictOldestAndBoundIDMemory(t *testing.T) {
	bindings := NewResponseBindings()
	bindings.capacity = 2
	ref := CredentialRef{ID: 1, GroupID: 1, IdentityGeneration: 1}
	for _, id := range []string{"old", "new", "newest"} {
		if !bindings.Record(1, id, ref) {
			t.Fatal("record failed")
		}
	}
	if _, ok := bindings.Lookup(1, "old"); ok {
		t.Fatal("capacity retained oldest entry")
	}
	for _, id := range []string{"new", "newest"} {
		if _, ok := bindings.Lookup(1, id); !ok {
			t.Fatalf("recent binding %q was lost", id)
		}
	}
	largeID := strings.Repeat("x", maxResponseBindingIDBytes)
	if !bindings.Record(1, largeID, ref) {
		t.Fatal("opaque ID within memory budget was rejected")
	}
	if _, ok := bindings.Lookup(1, "newest"); ok {
		t.Fatal("ID byte budget did not evict old entries")
	}
	if bindings.Record(2, largeID+"x", ref) {
		t.Fatal("oversized record exceeded ID memory budget")
	}
}

func TestResponseBindingsConcurrentRegistrationIsConsistent(t *testing.T) {
	bindings := NewResponseBindings()
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			for range 100 {
				if !bindings.Record(1, "same-response", CredentialRef{ID: 1, GroupID: 1, IdentityGeneration: 1}) {
					t.Error("idempotent concurrent record failed")
				}
				if _, ok := bindings.Lookup(1, "same-response"); !ok {
					t.Error("registered response was lost")
				}
			}
		})
	}
	workers.Wait()
}

func TestResponseBindingsCheckpointKeepsExpiryAndCapacity(t *testing.T) {
	now := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	original := NewResponseBindings()
	original.now = func() time.Time { return now }
	original.ttl = time.Hour
	ref := CredentialRef{ID: 1, GroupID: 1, IdentityGeneration: 1}
	original.Record(1, "expired", ref)
	now = now.Add(30 * time.Minute)
	original.Record(1, "retained", ref)
	checkpoint := original.CaptureCheckpoint()
	now = now.Add(30 * time.Minute)
	restored := NewResponseBindings()
	restored.capacity = 1
	restored.now = func() time.Time { return now }
	if err := restored.RestoreCheckpoint(checkpoint); err != nil {
		t.Fatal(err)
	}
	if _, ok := restored.Lookup(1, "expired"); ok {
		t.Fatal("expired checkpoint binding was restored")
	}
	if binding, ok := restored.Lookup(1, "retained"); !ok || !binding.ExpiresAt.Equal(checkpoint[1].ExpiresAt) {
		t.Fatal("restore lost or extended retained binding")
	}
	now = now.Add(30 * time.Minute)
	if _, ok := restored.Lookup(1, "retained"); ok {
		t.Fatal("restart renewed response TTL")
	}
}

func TestResponseBindingsRejectConflictingCheckpoint(t *testing.T) {
	bindings := NewResponseBindings()
	first := ResponseBinding{AccessKeyID: 1, ResponseID: "same", CredentialID: 1, GroupID: 1,
		IdentityGeneration: 1, ExpiresAt: time.Now().Add(time.Hour)}
	conflict := first
	conflict.CredentialID = 2
	if err := bindings.RestoreCheckpoint([]ResponseBinding{first, conflict}); err == nil {
		t.Fatal("ambiguous checkpoint was accepted")
	}
	if _, ok := bindings.Lookup(1, "same"); ok {
		t.Fatal("conflicting checkpoint left routable ownership")
	}
}
