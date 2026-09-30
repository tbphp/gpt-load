package ratelimit

import (
	"maps"
	"sync"
)

// Concurrency 只记录进程内尚未完成的业务，独立于配置快照。
type Concurrency struct {
	mu         sync.Mutex
	global     int64
	accessKeys map[uint]int64
	groups     map[uint]int64
}

type ConcurrencySnapshot struct {
	Global     int64
	AccessKeys map[uint]int64
	Groups     map[uint]int64
}

func NewConcurrency() *Concurrency {
	return &Concurrency{accessKeys: make(map[uint]int64), groups: make(map[uint]int64)}
}

// TryAcquireRequest 同时取得全局和访问密钥名额；调用方在业务收尾后释放。
func (c *Concurrency) TryAcquireRequest(key uint, globalLimit, keyLimit int64) (func(), bool) {
	c.mu.Lock()
	if (globalLimit > 0 && c.global >= globalLimit) || (keyLimit > 0 && c.accessKeys[key] >= keyLimit) {
		c.mu.Unlock()
		return nil, false
	}
	c.global++
	c.accessKeys[key]++
	c.mu.Unlock()
	return sync.OnceFunc(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.global--
		c.accessKeys[key]--
		if c.accessKeys[key] == 0 {
			delete(c.accessKeys, key)
		}
	}), true
}

func (c *Concurrency) TryAcquireGroup(group uint, limit int64) (func(), bool) {
	c.mu.Lock()
	if limit > 0 && c.groups[group] >= limit {
		c.mu.Unlock()
		return nil, false
	}
	c.groups[group]++
	c.mu.Unlock()
	return sync.OnceFunc(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.groups[group]--
		if c.groups[group] == 0 {
			delete(c.groups, group)
		}
	}), true
}

func (c *Concurrency) Snapshot() ConcurrencySnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return ConcurrencySnapshot{Global: c.global, AccessKeys: maps.Clone(c.accessKeys), Groups: maps.Clone(c.groups)}
}

func (c *Concurrency) AccessKeyCurrent(id uint) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.accessKeys[id]
}
