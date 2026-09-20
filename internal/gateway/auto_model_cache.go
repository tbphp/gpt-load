package gateway

import (
	"container/list"
	"sync"
	"time"
)

const (
	autoTaskCacheCapacity = 1024
	autoTaskCacheTTL      = 5 * time.Minute
)

type autoTaskKey struct {
	accessKeyID uint
	entryID     string
	revision    uint64
	fingerprint string
}

type autoTaskPreset struct {
	presetID string
	prewarm  bool
}

type autoTaskCacheEntry struct {
	key       autoTaskKey
	preset    autoTaskPreset
	expiresAt time.Time
}

// 只记住预设标识，不保存提示词或调度目标；配置变更后自然不再命中。
type autoTaskCache struct {
	mu      sync.Mutex
	entries map[autoTaskKey]*list.Element
	recent  list.List
}

func (cache *autoTaskCache) lookup(key autoTaskKey, now time.Time) (autoTaskPreset, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	element := cache.entries[key]
	if element == nil {
		return autoTaskPreset{}, false
	}
	entry := element.Value.(autoTaskCacheEntry)
	if !now.Before(entry.expiresAt) {
		delete(cache.entries, key)
		cache.recent.Remove(element)
		return autoTaskPreset{}, false
	}
	cache.recent.MoveToFront(element)
	return entry.preset, true
}

func (cache *autoTaskCache) record(key autoTaskKey, preset autoTaskPreset, now time.Time) {
	if key.fingerprint == "" {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.entries == nil {
		cache.entries = make(map[autoTaskKey]*list.Element)
	}
	entry := autoTaskCacheEntry{key: key, preset: preset, expiresAt: now.Add(autoTaskCacheTTL)}
	if element := cache.entries[key]; element != nil {
		element.Value = entry
		cache.recent.MoveToFront(element)
		return
	}
	if len(cache.entries) >= autoTaskCacheCapacity {
		oldest := cache.recent.Back()
		delete(cache.entries, oldest.Value.(autoTaskCacheEntry).key)
		cache.recent.Remove(oldest)
	}
	cache.entries[key] = cache.recent.PushFront(entry)
}

func (cache *autoTaskCache) consumePrewarm(key autoTaskKey, presetID string) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if element := cache.entries[key]; element != nil {
		entry := element.Value.(autoTaskCacheEntry)
		if entry.preset.presetID == presetID {
			entry.preset.prewarm = false
			element.Value = entry
		}
	}
}
