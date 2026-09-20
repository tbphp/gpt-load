package gateway

import (
	"fmt"
	"testing"
	"time"
)

func TestAutoTaskCacheIsolationExpiryAndCapacity(t *testing.T) {
	var cache autoTaskCache
	now := time.Unix(1000, 0)
	key := autoTaskKey{accessKeyID: 1, entryID: "auto", revision: 1, fingerprint: "task"}
	preset := autoTaskPreset{presetID: "high", prewarm: true}
	cache.record(key, preset, now)
	for _, other := range []autoTaskKey{
		{accessKeyID: 2, entryID: "auto", revision: 1, fingerprint: "task"},
		{accessKeyID: 1, entryID: "other", revision: 1, fingerprint: "task"},
		{accessKeyID: 1, entryID: "auto", revision: 2, fingerprint: "task"},
		{accessKeyID: 1, entryID: "auto", revision: 1, fingerprint: "new task"},
	} {
		if _, found := cache.lookup(other, now); found {
			t.Fatalf("cross-boundary cache hit: %#v", other)
		}
	}
	if got, found := cache.lookup(key, now.Add(autoTaskCacheTTL-time.Second)); !found || got != preset {
		t.Fatalf("cached preset=%#v found=%t", got, found)
	}
	if _, found := cache.lookup(key, now.Add(autoTaskCacheTTL)); found {
		t.Fatal("lookup extended expired decision")
	}
	cache.record(key, preset, now)
	for index := range autoTaskCacheCapacity {
		other := key
		other.fingerprint = fmt.Sprint(index)
		cache.record(other, preset, now)
	}
	if _, found := cache.lookup(key, now); found {
		t.Fatal("oldest entry was not evicted")
	}
	if len(cache.entries) != autoTaskCacheCapacity {
		t.Fatalf("cache size=%d", len(cache.entries))
	}
}
