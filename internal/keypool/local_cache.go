package keypool

import (
	"gpt-load/internal/models"
	"sync"
	"time"
)

// LocalKeyCache 本地密钥缓存，提供高性能的密钥访问
type LocalKeyCache struct {
	mu       sync.RWMutex
	cache    map[uint]*CacheEntry
	lru      *LRUList
	maxSize  int
	ttl      time.Duration
	stats    *CacheStats
}

// CacheEntry 缓存条目
type CacheEntry struct {
	Key       *models.APIKey
	ExpiresAt time.Time
	AccessAt  time.Time
	HitCount  int64
	prev      *CacheEntry
	next      *CacheEntry
}

// LRUList LRU链表
type LRUList struct {
	head *CacheEntry
	tail *CacheEntry
	size int
}

// CacheStats 缓存统计
type CacheStats struct {
	Hits        int64
	Misses      int64
	Evictions   int64
	Size        int
	HitRate     float64
	LastUpdated time.Time
}

// NewLocalKeyCache 创建本地密钥缓存
func NewLocalKeyCache(maxSize int, ttl time.Duration) *LocalKeyCache {
	cache := &LocalKeyCache{
		cache:   make(map[uint]*CacheEntry),
		lru:     NewLRUList(),
		maxSize: maxSize,
		ttl:     ttl,
		stats:   &CacheStats{},
	}
	
	// 启动后台清理任务
	go cache.cleanupLoop()
	
	return cache
}

// NewLRUList 创建LRU链表
func NewLRUList() *LRUList {
	head := &CacheEntry{}
	tail := &CacheEntry{}
	head.next = tail
	tail.prev = head
	
	return &LRUList{
		head: head,
		tail: tail,
		size: 0,
	}
}

// Get 获取缓存的密钥
func (c *LocalKeyCache) Get(keyID uint) *models.APIKey {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	entry, exists := c.cache[keyID]
	if !exists {
		c.stats.Misses++
		c.updateStats()
		return nil
	}
	
	// 检查是否过期
	if time.Now().After(entry.ExpiresAt) {
		c.removeEntry(keyID)
		c.stats.Misses++
		c.updateStats()
		return nil
	}
	
	// 更新访问信息
	entry.AccessAt = time.Now()
	entry.HitCount++
	c.stats.Hits++
	
	// 移动到LRU链表头部
	c.lru.MoveToFront(entry)
	
	c.updateStats()
	return entry.Key
}

// Set 设置缓存的密钥
func (c *LocalKeyCache) Set(keyID uint, key *models.APIKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	now := time.Now()
	
	// 检查是否已存在
	if entry, exists := c.cache[keyID]; exists {
		// 更新现有条目
		entry.Key = key
		entry.ExpiresAt = now.Add(c.ttl)
		entry.AccessAt = now
		c.lru.MoveToFront(entry)
		return
	}
	
	// 检查是否需要驱逐
	if len(c.cache) >= c.maxSize {
		c.evictLRU()
	}
	
	// 创建新条目
	entry := &CacheEntry{
		Key:       key,
		ExpiresAt: now.Add(c.ttl),
		AccessAt:  now,
		HitCount:  0,
	}
	
	c.cache[keyID] = entry
	c.lru.AddToFront(entry)
	c.stats.Size = len(c.cache)
}

// Remove 移除缓存的密钥
func (c *LocalKeyCache) Remove(keyID uint) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	c.removeEntry(keyID)
}

// removeEntry 内部移除条目方法
func (c *LocalKeyCache) removeEntry(keyID uint) {
	if entry, exists := c.cache[keyID]; exists {
		delete(c.cache, keyID)
		c.lru.Remove(entry)
		c.stats.Size = len(c.cache)
	}
}

// evictLRU 驱逐最少使用的条目
func (c *LocalKeyCache) evictLRU() {
	if c.lru.size == 0 {
		return
	}
	
	// 获取最后一个条目
	lastEntry := c.lru.tail.prev
	if lastEntry == c.lru.head {
		return
	}
	
	// 找到对应的keyID
	var keyIDToRemove uint
	for keyID, entry := range c.cache {
		if entry == lastEntry {
			keyIDToRemove = keyID
			break
		}
	}
	
	if keyIDToRemove != 0 {
		c.removeEntry(keyIDToRemove)
		c.stats.Evictions++
	}
}

// cleanupLoop 后台清理过期条目
func (c *LocalKeyCache) cleanupLoop() {
	ticker := time.NewTicker(c.ttl / 4) // 每1/4 TTL时间清理一次
	defer ticker.Stop()
	
	for range ticker.C {
		c.cleanupExpired()
	}
}

// cleanupExpired 清理过期条目
func (c *LocalKeyCache) cleanupExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	now := time.Now()
	expiredKeys := make([]uint, 0)
	
	for keyID, entry := range c.cache {
		if now.After(entry.ExpiresAt) {
			expiredKeys = append(expiredKeys, keyID)
		}
	}
	
	for _, keyID := range expiredKeys {
		c.removeEntry(keyID)
	}
}

// updateStats 更新统计信息
func (c *LocalKeyCache) updateStats() {
	total := c.stats.Hits + c.stats.Misses
	if total > 0 {
		c.stats.HitRate = float64(c.stats.Hits) / float64(total)
	}
	c.stats.LastUpdated = time.Now()
}

// GetStats 获取缓存统计
func (c *LocalKeyCache) GetStats() *CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	// 返回统计信息的副本
	return &CacheStats{
		Hits:        c.stats.Hits,
		Misses:      c.stats.Misses,
		Evictions:   c.stats.Evictions,
		Size:        c.stats.Size,
		HitRate:     c.stats.HitRate,
		LastUpdated: c.stats.LastUpdated,
	}
}

// Clear 清空缓存
func (c *LocalKeyCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	c.cache = make(map[uint]*CacheEntry)
	c.lru = NewLRUList()
	c.stats = &CacheStats{}
}

// --- LRU链表操作 ---

// AddToFront 添加到链表头部
func (l *LRUList) AddToFront(entry *CacheEntry) {
	entry.prev = l.head
	entry.next = l.head.next
	l.head.next.prev = entry
	l.head.next = entry
	l.size++
}

// Remove 从链表中移除
func (l *LRUList) Remove(entry *CacheEntry) {
	if entry.prev != nil {
		entry.prev.next = entry.next
	}
	if entry.next != nil {
		entry.next.prev = entry.prev
	}
	entry.prev = nil
	entry.next = nil
	l.size--
}

// MoveToFront 移动到链表头部
func (l *LRUList) MoveToFront(entry *CacheEntry) {
	l.Remove(entry)
	l.AddToFront(entry)
}

// AdaptiveCache 自适应缓存，根据访问模式动态调整
type AdaptiveCache struct {
	*LocalKeyCache
	adaptiveConfig *AdaptiveCacheConfig
	lastAdaptTime  time.Time
}

// AdaptiveCacheConfig 自适应缓存配置
type AdaptiveCacheConfig struct {
	MinSize         int           `json:"min_size"`
	MaxSize         int           `json:"max_size"`
	AdaptInterval   time.Duration `json:"adapt_interval"`
	HitRateTarget   float64       `json:"hit_rate_target"`
	SizeAdjustStep  int           `json:"size_adjust_step"`
}

// NewAdaptiveCache 创建自适应缓存
func NewAdaptiveCache(config *AdaptiveCacheConfig, ttl time.Duration) *AdaptiveCache {
	if config == nil {
		config = &AdaptiveCacheConfig{
			MinSize:        100,
			MaxSize:        1000,
			AdaptInterval:  5 * time.Minute,
			HitRateTarget:  0.8,
			SizeAdjustStep: 50,
		}
	}
	
	cache := &AdaptiveCache{
		LocalKeyCache:  NewLocalKeyCache(config.MinSize, ttl),
		adaptiveConfig: config,
		lastAdaptTime:  time.Now(),
	}
	
	// 启动自适应调整
	go cache.adaptiveLoop()
	
	return cache
}

// adaptiveLoop 自适应调整循环
func (a *AdaptiveCache) adaptiveLoop() {
	ticker := time.NewTicker(a.adaptiveConfig.AdaptInterval)
	defer ticker.Stop()
	
	for range ticker.C {
		a.adaptSize()
	}
}

// adaptSize 自适应调整缓存大小
func (a *AdaptiveCache) adaptSize() {
	stats := a.GetStats()
	
	// 如果命中率低于目标，增加缓存大小
	if stats.HitRate < a.adaptiveConfig.HitRateTarget {
		newSize := a.maxSize + a.adaptiveConfig.SizeAdjustStep
		if newSize <= a.adaptiveConfig.MaxSize {
			a.maxSize = newSize
		}
	} else if stats.HitRate > a.adaptiveConfig.HitRateTarget+0.1 {
		// 如果命中率明显高于目标，可以减少缓存大小
		newSize := a.maxSize - a.adaptiveConfig.SizeAdjustStep
		if newSize >= a.adaptiveConfig.MinSize {
			a.maxSize = newSize
			// 如果当前缓存大小超过新的最大值，需要驱逐一些条目
			a.mu.Lock()
			for len(a.cache) > a.maxSize {
				a.evictLRU()
			}
			a.mu.Unlock()
		}
	}
	
	a.lastAdaptTime = time.Now()
}
