package codexrecovery

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// InvalidLineageTTL bounds how long rejected upstream-bound state is
	// remembered for one client session.
	InvalidLineageTTL = time.Hour
	// InvalidLineageCapacity bounds the number of sessions retained in memory.
	InvalidLineageCapacity = 10_000
	invalidLineageMaxItems = 64
)

type invalidLineageEntry struct {
	digests   map[string]struct{}
	itemIDs   map[string]struct{}
	expiresAt time.Time
}

// InvalidLineage remembers encrypted-content digests and history item IDs that
// one upstream has explicitly rejected for a client session. Client history
// commonly carries those items into every later turn, so matching state is
// removed before the next upstream attempt instead of being rejected again.
type InvalidLineage struct {
	mu      sync.Mutex
	entries map[string]invalidLineageEntry
}

// NewInvalidLineage creates an empty session lineage cache.
func NewInvalidLineage() *InvalidLineage {
	return &InvalidLineage{entries: make(map[string]invalidLineageEntry)}
}

// InvalidLineageKey scopes a session lineage to one routing group. The same
// item can be valid for the account that created it and invalid for another
// group's account.
func InvalidLineageKey(groupID uint, sessionKey string) string {
	sessionKey = strings.TrimSpace(sessionKey)
	if groupID == 0 || sessionKey == "" {
		return ""
	}
	return strconv.FormatUint(uint64(groupID), 10) + "\x00" + sessionKey
}

// Lookup returns a copy of the rejected digests for key.
func (lineage *InvalidLineage) Lookup(key string, now time.Time) map[string]struct{} {
	return lineage.lookupSet(key, now, false)
}

// LookupItemIDs returns a copy of the rejected history item IDs for key.
func (lineage *InvalidLineage) LookupItemIDs(key string, now time.Time) map[string]struct{} {
	return lineage.lookupSet(key, now, true)
}

func (lineage *InvalidLineage) lookupSet(key string, now time.Time, itemIDs bool) map[string]struct{} {
	if lineage == nil || key == "" {
		return nil
	}
	lineage.mu.Lock()
	defer lineage.mu.Unlock()
	entry, exists := lineage.entries[key]
	if !exists {
		return nil
	}
	if !now.Before(entry.expiresAt) {
		delete(lineage.entries, key)
		return nil
	}
	set := entry.digests
	if itemIDs {
		set = entry.itemIDs
	}
	copied := make(map[string]struct{}, len(set))
	for digest := range set {
		copied[digest] = struct{}{}
	}
	return copied
}

// Mark records rejected digests for key and returns the number of new digests.
func (lineage *InvalidLineage) Mark(key string, digests []string, now time.Time) int {
	return lineage.markSet(key, digests, now, false)
}

// MarkItemIDs records rejected history item IDs for key and returns the number
// of new IDs.
func (lineage *InvalidLineage) MarkItemIDs(key string, itemIDs []string, now time.Time) int {
	return lineage.markSet(key, itemIDs, now, true)
}

func (lineage *InvalidLineage) markSet(key string, values []string, now time.Time, itemIDs bool) int {
	if lineage == nil || key == "" || len(values) == 0 {
		return 0
	}
	lineage.mu.Lock()
	defer lineage.mu.Unlock()

	lineage.pruneExpiredLocked(now)
	entry, exists := lineage.entries[key]
	if !exists || !now.Before(entry.expiresAt) {
		if !exists && len(lineage.entries) >= InvalidLineageCapacity {
			lineage.evictOldestLocked()
		}
		entry = invalidLineageEntry{
			digests: make(map[string]struct{}, 4),
			itemIDs: make(map[string]struct{}, 4),
		}
	}
	set := entry.digests
	if itemIDs {
		set = entry.itemIDs
	}
	if set == nil {
		set = make(map[string]struct{}, len(values))
	}
	added := 0
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, duplicate := set[value]; duplicate {
			continue
		}
		if len(set) >= invalidLineageMaxItems {
			continue
		}
		set[value] = struct{}{}
		added++
	}
	if itemIDs {
		entry.itemIDs = set
	} else {
		entry.digests = set
	}
	entry.expiresAt = now.Add(InvalidLineageTTL)
	lineage.entries[key] = entry
	return added
}

func (lineage *InvalidLineage) pruneExpiredLocked(now time.Time) {
	for key, entry := range lineage.entries {
		if !now.Before(entry.expiresAt) {
			delete(lineage.entries, key)
		}
	}
}

func (lineage *InvalidLineage) evictOldestLocked() {
	oldestKey := ""
	var oldestExpiry time.Time
	for key, entry := range lineage.entries {
		if oldestKey == "" || entry.expiresAt.Before(oldestExpiry) {
			oldestKey = key
			oldestExpiry = entry.expiresAt
		}
	}
	if oldestKey != "" {
		delete(lineage.entries, oldestKey)
	}
}

// EncryptedContentDigest returns the stable SHA-256 digest of one opaque value.
func EncryptedContentDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// EncryptedContentDigests collects every opaque encrypted_content value carried
// by a Responses input. Only digests are retained by InvalidLineage.
func EncryptedContentDigests(body []byte) []string {
	object, ok := decodeJSONObject(body)
	if !ok {
		return nil
	}
	items, _ := responseInputItems(object["input"])
	if items == nil {
		return nil
	}
	seen := make(map[string]struct{})
	digests := make([]string, 0, 4)
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			if encrypted, isString := typed["encrypted_content"].(string); isString && encrypted != "" {
				digest := EncryptedContentDigest(encrypted)
				if _, duplicate := seen[digest]; !duplicate {
					seen[digest] = struct{}{}
					digests = append(digests, digest)
				}
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(items)
	return digests
}

// HistoryItemIDs collects IDs from replayable Responses input items and item
// references. Only these IDs can be removed safely before a later attempt.
func HistoryItemIDs(body []byte) []string {
	object, ok := decodeJSONObject(body)
	if !ok {
		return nil
	}
	items, _ := responseInputItems(object["input"])
	if items == nil {
		return nil
	}
	seen := make(map[string]struct{})
	ids := make([]string, 0, 4)
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		itemType, _ := record["type"].(string)
		if itemType != "item_reference" && !replayableHistoryItem(record) {
			continue
		}
		id, _ := record["id"].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}
