package affinity

import (
	"container/list"
	"sync"
	"time"
)

const (
	DefaultTTL      = time.Hour
	DefaultCapacity = 10_000
)

// Target is the exact Credential identity remembered as a soft preference.
type Target struct {
	GroupID            uint
	CredentialID       uint
	IdentityGeneration uint64
}

func (target Target) Valid() bool {
	return target.GroupID != 0 && target.CredentialID != 0 && target.IdentityGeneration != 0
}

// Observation is a versioned cache lookup used for conditional success updates.
type Observation struct {
	Target  Target
	key     Key
	version uint64
	found   bool
}

func (observation Observation) Found() bool {
	return observation.found
}

type cacheEntry struct {
	key       Key
	target    Target
	version   uint64
	expiresAt time.Time
}

// Cache is a bounded, process-local soft-affinity cache.
type Cache struct {
	mu          sync.Mutex
	entries     map[Key]*list.Element
	recent      list.List
	capacity    int
	ttl         time.Duration
	now         func() time.Time
	nextVersion uint64
}

func NewCache() *Cache {
	return newCache(DefaultCapacity, DefaultTTL, time.Now)
}

func newCache(capacity int, ttl time.Duration, now func() time.Time) *Cache {
	return &Cache{
		entries:  make(map[Key]*list.Element),
		capacity: capacity,
		ttl:      ttl,
		now:      now,
	}
}

func (cache *Cache) Lookup(key Key) Observation {
	if cache == nil || !key.Valid() || cache.now == nil {
		return Observation{}
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	element := cache.currentElementLocked(key, cache.now())
	if element == nil {
		return Observation{key: key}
	}
	cache.recent.MoveToFront(element)
	entry := element.Value.(*cacheEntry)
	return Observation{
		Target: entry.target, key: key, version: entry.version, found: true,
	}
}

// RecordSuccess conditionally learns the successful target. It returns true
// when this call inserted, refreshed, or changed the current mapping.
func (cache *Cache) RecordSuccess(key Key, observed Observation, target Target) bool {
	if cache == nil || !key.Valid() || !target.Valid() || cache.capacity <= 0 ||
		cache.ttl <= 0 || cache.now == nil {
		return false
	}
	if observed.key.Valid() && observed.key != key {
		return false
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := cache.now()
	element := cache.currentElementLocked(key, now)
	if observed.found {
		if element != nil {
			current := element.Value.(*cacheEntry)
			if current.version != observed.version || current.target != observed.Target {
				return false
			}
		} else {
			return cache.insertLocked(key, target, now)
		}
	} else if element != nil {
		return false
	}

	if element == nil {
		return cache.insertLocked(key, target, now)
	}
	cache.nextVersion++
	entry := element.Value.(*cacheEntry)
	entry.target = target
	entry.version = cache.nextVersion
	entry.expiresAt = now.Add(cache.ttl)
	cache.recent.MoveToFront(element)
	return true
}

func (cache *Cache) entryCount() int {
	if cache == nil {
		return 0
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return len(cache.entries)
}

func (cache *Cache) currentElementLocked(key Key, now time.Time) *list.Element {
	element := cache.entries[key]
	if element == nil {
		return nil
	}
	entry := element.Value.(*cacheEntry)
	if !now.Before(entry.expiresAt) {
		cache.removeLocked(element)
		return nil
	}
	return element
}

func (cache *Cache) insertLocked(key Key, target Target, now time.Time) bool {
	for len(cache.entries) >= cache.capacity {
		oldest := cache.recent.Back()
		if oldest == nil {
			break
		}
		cache.removeLocked(oldest)
	}
	cache.nextVersion++
	entry := &cacheEntry{
		key: key, target: target, version: cache.nextVersion,
		expiresAt: now.Add(cache.ttl),
	}
	cache.entries[key] = cache.recent.PushFront(entry)
	return true
}

func (cache *Cache) removeLocked(element *list.Element) {
	entry := element.Value.(*cacheEntry)
	delete(cache.entries, entry.key)
	cache.recent.Remove(element)
}
