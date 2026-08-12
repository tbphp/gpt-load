package affinity

import (
	"testing"
	"time"
)

func TestCacheLearnsAndRefreshesSuccessfulTarget(t *testing.T) {
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	cache := newCache(2, time.Hour, func() time.Time { return now })
	key := Key("one")
	target := Target{GroupID: 1, CredentialID: 11, IdentityGeneration: 101}

	miss := cache.Lookup(key)
	if miss.Found() {
		t.Fatalf("initial Lookup() = %#v, want miss", miss)
	}
	if !cache.RecordSuccess(key, miss, target) {
		t.Fatal("RecordSuccess() = false, want insert")
	}
	first := cache.Lookup(key)
	if !first.Found() || first.Target != target {
		t.Fatalf("Lookup() = %#v, want target %#v", first, target)
	}

	now = now.Add(50 * time.Minute)
	if !cache.RecordSuccess(key, first, target) {
		t.Fatal("RecordSuccess() = false, want TTL refresh")
	}
	now = now.Add(20 * time.Minute)
	if refreshed := cache.Lookup(key); !refreshed.Found() || refreshed.Target != target {
		t.Fatalf("Lookup() after refreshed TTL = %#v, want hit", refreshed)
	}
}

func TestCacheLookupDoesNotRefreshTTL(t *testing.T) {
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	cache := newCache(2, time.Hour, func() time.Time { return now })
	key := Key("one")
	target := Target{GroupID: 1, CredentialID: 11, IdentityGeneration: 101}
	cache.RecordSuccess(key, Observation{}, target)

	now = now.Add(50 * time.Minute)
	if !cache.Lookup(key).Found() {
		t.Fatal("Lookup() before expiry = miss, want hit")
	}
	now = now.Add(11 * time.Minute)
	if got := cache.Lookup(key); got.Found() {
		t.Fatalf("Lookup() after original expiry = %#v, want miss", got)
	}
}

func TestCacheFirstSuccessWinsAndFallbackUsesCompareAndSwap(t *testing.T) {
	cache := newCache(4, time.Hour, time.Now)
	key := Key("conversation")
	first := Target{GroupID: 1, CredentialID: 11, IdentityGeneration: 101}
	second := Target{GroupID: 1, CredentialID: 12, IdentityGeneration: 102}

	missOne := cache.Lookup(key)
	missTwo := cache.Lookup(key)
	if !cache.RecordSuccess(key, missOne, first) {
		t.Fatal("first miss success did not create mapping")
	}
	if cache.RecordSuccess(key, missTwo, second) {
		t.Fatal("second concurrent miss overwrote first-success mapping")
	}

	observedFirst := cache.Lookup(key)
	if !cache.RecordSuccess(key, observedFirst, second) {
		t.Fatal("fallback success did not switch observed mapping")
	}
	if cache.RecordSuccess(key, observedFirst, first) {
		t.Fatal("stale success overwrote newer fallback mapping")
	}
	got := cache.Lookup(key)
	if !got.Found() || got.Target != second {
		t.Fatalf("Lookup() = %#v, want fallback target %#v", got, second)
	}
}

func TestCacheEvictsLeastRecentlyUsedEntry(t *testing.T) {
	cache := newCache(2, time.Hour, time.Now)
	target := Target{GroupID: 1, CredentialID: 11, IdentityGeneration: 101}
	for _, key := range []Key{"one", "two"} {
		cache.RecordSuccess(key, Observation{}, target)
	}
	cache.Lookup("one")
	cache.RecordSuccess("three", Observation{}, target)

	if cache.Lookup("two").Found() {
		t.Fatal("least recently used entry remained cached")
	}
	if !cache.Lookup("one").Found() || !cache.Lookup("three").Found() || cache.entryCount() != 2 {
		t.Fatalf("cache state invalid after eviction; entries=%d", cache.entryCount())
	}
}

func TestCacheRejectsInvalidInputs(t *testing.T) {
	cache := newCache(2, time.Hour, time.Now)
	validTarget := Target{GroupID: 1, CredentialID: 11, IdentityGeneration: 101}
	for _, test := range []struct {
		key    Key
		target Target
	}{
		{target: validTarget},
		{key: "key"},
		{key: "key", target: Target{GroupID: 1, CredentialID: 11}},
	} {
		if cache.RecordSuccess(test.key, Observation{}, test.target) {
			t.Fatalf("RecordSuccess(%q, %#v) = true, want false", test.key, test.target)
		}
	}
	if cache.Lookup("").Found() || cache.entryCount() != 0 {
		t.Fatalf("invalid inputs changed cache; entries=%d", cache.entryCount())
	}
}
