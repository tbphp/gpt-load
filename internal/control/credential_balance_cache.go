package control

import (
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"
)

// credentialBalanceTTL bounds how long a fetched balance is reused before the
// console triggers another upstream query. Balances change slowly and a group
// can hold many credentials, so caching keeps a page view from fanning out into
// one upstream call per key.
const credentialBalanceTTL = 5 * time.Minute

type cachedCredentialBalance struct {
	response  CredentialBalanceResponse
	fetchedAt time.Time
}

// balanceCache holds the most recent balance per credential.
type balanceCache struct {
	mu      sync.RWMutex
	ttl     time.Duration
	now     func() time.Time
	entries map[uint]cachedCredentialBalance
}

func newBalanceCache(ttl time.Duration, now func() time.Time) *balanceCache {
	if ttl <= 0 {
		ttl = credentialBalanceTTL
	}
	if now == nil {
		now = time.Now
	}
	return &balanceCache{
		ttl:     ttl,
		now:     now,
		entries: make(map[uint]cachedCredentialBalance),
	}
}

func (c *balanceCache) get(credentialID uint) (cachedCredentialBalance, bool) {
	if c == nil {
		return cachedCredentialBalance{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[credentialID]
	return entry, ok
}

// fresh reports a cached balance that is still within its TTL.
func (c *balanceCache) fresh(credentialID uint) (cachedCredentialBalance, bool) {
	entry, ok := c.get(credentialID)
	if !ok {
		return cachedCredentialBalance{}, false
	}
	if c.now().Sub(entry.fetchedAt) > c.ttl {
		return cachedCredentialBalance{}, false
	}
	return entry, true
}

func (c *balanceCache) put(credentialID uint, response CredentialBalanceResponse) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[credentialID] = cachedCredentialBalance{
		response:  response,
		fetchedAt: c.now(),
	}
}

// decimalAccumulator sums decimal strings without the drift float64 would
// introduce on money values, preserving the widest precision it was given.
type decimalAccumulator struct {
	value    *big.Rat
	decimals int
	seen     bool
}

func (a *decimalAccumulator) add(raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	parsed, ok := new(big.Rat).SetString(raw)
	if !ok {
		return
	}
	if a.value == nil {
		a.value = new(big.Rat)
	}
	a.value.Add(a.value, parsed)
	a.seen = true
	if dot := strings.IndexByte(raw, '.'); dot >= 0 {
		if places := len(raw) - dot - 1; places > a.decimals {
			a.decimals = places
		}
	}
}

func (a *decimalAccumulator) string() string {
	if !a.seen || a.value == nil {
		return "0"
	}
	return a.value.FloatString(a.decimals)
}

// sumBalanceEntries adds same-currency entries together, returning currencies in
// a stable order.
func sumBalanceEntries(entries []CredentialBalanceEntry) []CredentialBalanceEntry {
	if len(entries) == 0 {
		return []CredentialBalanceEntry{}
	}
	type bucket struct {
		currency string
		total    decimalAccumulator
		granted  decimalAccumulator
		toppedUp decimalAccumulator
	}
	buckets := make(map[string]*bucket, len(entries))
	order := make([]string, 0, len(entries))
	for _, entry := range entries {
		current, ok := buckets[entry.Currency]
		if !ok {
			current = &bucket{currency: entry.Currency}
			buckets[entry.Currency] = current
			order = append(order, entry.Currency)
		}
		current.total.add(entry.Total)
		current.granted.add(entry.Granted)
		current.toppedUp.add(entry.ToppedUp)
	}
	sort.Strings(order)
	result := make([]CredentialBalanceEntry, 0, len(order))
	for _, currency := range order {
		current := buckets[currency]
		result = append(result, CredentialBalanceEntry{
			Currency: current.currency,
			Total:    current.total.string(),
			Granted:  current.granted.string(),
			ToppedUp: current.toppedUp.string(),
		})
	}
	return result
}
