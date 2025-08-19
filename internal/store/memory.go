package store

import (
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"
)

// memoryStoreItem holds the value and expiration timestamp for a key.
type memoryStoreItem struct {
	value     []byte
	expiresAt int64 // Unix-nano timestamp. 0 for no expiry.
}

// zsetMember represents a member in a sorted set.
type zsetMember struct {
	Member string
	Score  float64
}

// zset represents a sorted set in memory.
type zset struct {
	members map[string]float64 // member -> score
	sorted  []zsetMember       // sorted by score
	dirty   bool               // needs re-sorting
}

// MemoryStore is an in-memory key-value store that is safe for concurrent use.
type MemoryStore struct {
	mu            sync.RWMutex
	data          map[string]any
	muSubscribers sync.RWMutex
	subscribers   map[string]map[chan *Message]struct{}
}

// NewMemoryStore creates and returns a new MemoryStore instance.
func NewMemoryStore() *MemoryStore {
	s := &MemoryStore{
		data:        make(map[string]any),
		subscribers: make(map[string]map[chan *Message]struct{}),
	}
	return s
}

// Close cleans up resources.
func (s *MemoryStore) Close() error {
	return nil
}

// Set stores a key-value pair.
func (s *MemoryStore) Set(key string, value []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var expiresAt int64
	if ttl > 0 {
		expiresAt = time.Now().UnixNano() + ttl.Nanoseconds()
	}

	s.data[key] = memoryStoreItem{
		value:     value,
		expiresAt: expiresAt,
	}
	return nil
}

// Get retrieves a value by its key.
func (s *MemoryStore) Get(key string) ([]byte, error) {
	s.mu.RLock()
	rawItem, exists := s.data[key]
	s.mu.RUnlock()

	if !exists {
		return nil, ErrNotFound
	}

	item, ok := rawItem.(memoryStoreItem)
	if !ok {
		return nil, fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}

	if item.expiresAt > 0 && time.Now().UnixNano() > item.expiresAt {
		s.mu.Lock()
		delete(s.data, key)
		s.mu.Unlock()
		return nil, ErrNotFound
	}

	return item.value, nil
}

// Delete removes a value by its key.
func (s *MemoryStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

// Del removes multiple values by their keys.
func (s *MemoryStore) Del(keys ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range keys {
		delete(s.data, key)
	}
	return nil
}

// Exists checks if a key exists.
func (s *MemoryStore) Exists(key string) (bool, error) {
	s.mu.RLock()
	rawItem, exists := s.data[key]
	s.mu.RUnlock()

	if !exists {
		return false, nil
	}

	if item, ok := rawItem.(memoryStoreItem); ok {
		if item.expiresAt > 0 && time.Now().UnixNano() > item.expiresAt {
			s.mu.Lock()
			delete(s.data, key)
			s.mu.Unlock()
			return false, nil
		}
	}

	return true, nil
}

// SetNX sets a key-value pair if the key does not already exist.
func (s *MemoryStore) SetNX(key string, value []byte, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rawItem, exists := s.data[key]
	if exists {
		if item, ok := rawItem.(memoryStoreItem); ok {
			if item.expiresAt == 0 || time.Now().UnixNano() < item.expiresAt {
				return false, nil
			}
		} else {
			// Key exists but is not a simple K/V item, treat as existing
			return false, nil
		}
	}

	// Key does not exist or is expired, so we can set it.
	var expiresAt int64
	if ttl > 0 {
		expiresAt = time.Now().UnixNano() + ttl.Nanoseconds()
	}
	s.data[key] = memoryStoreItem{
		value:     value,
		expiresAt: expiresAt,
	}
	return true, nil
}

// --- HASH operations ---

func (s *MemoryStore) HSet(key string, values map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var hash map[string]string
	rawHash, exists := s.data[key]
	if !exists {
		hash = make(map[string]string)
		s.data[key] = hash
	} else {
		var ok bool
		hash, ok = rawHash.(map[string]string)
		if !ok {
			return fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
		}
	}

	for field, value := range values {
		hash[field] = fmt.Sprint(value)
	}
	return nil
}

func (s *MemoryStore) HGetAll(key string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rawHash, exists := s.data[key]
	if !exists {
		return make(map[string]string), nil
	}

	hash, ok := rawHash.(map[string]string)
	if !ok {
		return nil, fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}

	result := make(map[string]string, len(hash))
	for k, v := range hash {
		result[k] = v
	}

	return result, nil
}

func (s *MemoryStore) HIncrBy(key, field string, incr int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var hash map[string]string
	rawHash, exists := s.data[key]
	if !exists {
		hash = make(map[string]string)
		s.data[key] = hash
	} else {
		var ok bool
		hash, ok = rawHash.(map[string]string)
		if !ok {
			return 0, fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
		}
	}

	currentVal, _ := strconv.ParseInt(hash[field], 10, 64)
	newVal := currentVal + incr
	hash[field] = strconv.FormatInt(newVal, 10)

	return newVal, nil
}

// --- LIST operations ---

func (s *MemoryStore) LPush(key string, values ...any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var list []string
	rawList, exists := s.data[key]
	if !exists {
		list = make([]string, 0)
	} else {
		var ok bool
		list, ok = rawList.([]string)
		if !ok {
			return fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
		}
	}

	strValues := make([]string, len(values))
	for i, v := range values {
		strValues[i] = fmt.Sprint(v)
	}

	s.data[key] = append(strValues, list...) // Prepend
	return nil
}

func (s *MemoryStore) LRem(key string, count int64, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rawList, exists := s.data[key]
	if !exists {
		return nil
	}

	list, ok := rawList.([]string)
	if !ok {
		return fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}

	strValue := fmt.Sprint(value)
	newList := make([]string, 0, len(list))

	if count != 0 {
		return fmt.Errorf("LRem with non-zero count is not implemented in MemoryStore")
	}

	for _, item := range list {
		if item != strValue {
			newList = append(newList, item)
		}
	}
	s.data[key] = newList
	return nil
}

func (s *MemoryStore) Rotate(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rawList, exists := s.data[key]
	if !exists {
		return "", ErrNotFound
	}

	list, ok := rawList.([]string)
	if !ok {
		return "", fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}

	if len(list) == 0 {
		return "", ErrNotFound
	}

	lastIndex := len(list) - 1
	item := list[lastIndex]

	// "LPUSH"
	newList := append([]string{item}, list[:lastIndex]...)
	s.data[key] = newList

	return item, nil
}

// --- SET operations ---

// SAdd adds members to a set.
func (s *MemoryStore) SAdd(key string, members ...any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var set map[string]struct{}
	rawSet, exists := s.data[key]
	if !exists {
		set = make(map[string]struct{})
		s.data[key] = set
	} else {
		var ok bool
		set, ok = rawSet.(map[string]struct{})
		if !ok {
			return fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
		}
	}

	for _, member := range members {
		set[fmt.Sprint(member)] = struct{}{}
	}
	return nil
}

// SRem removes members from a set.
func (s *MemoryStore) SRem(key string, members ...any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rawSet, exists := s.data[key]
	if !exists {
		return nil
	}

	set, ok := rawSet.(map[string]struct{})
	if !ok {
		return fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}

	for _, member := range members {
		delete(set, fmt.Sprint(member))
	}
	return nil
}

// SMembers returns all members of a set.
func (s *MemoryStore) SMembers(key string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rawSet, exists := s.data[key]
	if !exists {
		return []string{}, nil
	}

	set, ok := rawSet.(map[string]struct{})
	if !ok {
		return nil, fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}

	members := make([]string, 0, len(set))
	for member := range set {
		members = append(members, member)
	}
	return members, nil
}

// --- ZSET operations ---

// ZAdd adds members to a sorted set.
func (s *MemoryStore) ZAdd(key string, members ...ZMember) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var zs *zset
	rawZset, exists := s.data[key]
	if !exists {
		zs = &zset{
			members: make(map[string]float64),
			sorted:  make([]zsetMember, 0),
			dirty:   false,
		}
		s.data[key] = zs
	} else {
		var ok bool
		zs, ok = rawZset.(*zset)
		if !ok {
			return fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
		}
	}

	for _, member := range members {
		memberStr := fmt.Sprint(member.Member)
		oldScore, existed := zs.members[memberStr]
		zs.members[memberStr] = member.Score

		if !existed || oldScore != member.Score {
			zs.dirty = true
		}
	}

	return nil
}

// ZRem removes members from a sorted set.
func (s *MemoryStore) ZRem(key string, members ...any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rawZset, exists := s.data[key]
	if !exists {
		return nil
	}

	zs, ok := rawZset.(*zset)
	if !ok {
		return fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}

	for _, member := range members {
		memberStr := fmt.Sprint(member)
		if _, existed := zs.members[memberStr]; existed {
			delete(zs.members, memberStr)
			zs.dirty = true
		}
	}

	return nil
}

// ZRangeByScore returns members with scores between min and max.
func (s *MemoryStore) ZRangeByScore(key string, min, max float64) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rawZset, exists := s.data[key]
	if !exists {
		return []string{}, nil
	}

	zs, ok := rawZset.(*zset)
	if !ok {
		return nil, fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}

	s.ensureSorted(zs)

	var result []string
	for _, member := range zs.sorted {
		if member.Score >= min && member.Score <= max {
			result = append(result, member.Member)
		}
	}

	return result, nil
}

// ZRemRangeByScore removes members with scores between min and max.
func (s *MemoryStore) ZRemRangeByScore(key string, min, max float64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rawZset, exists := s.data[key]
	if !exists {
		return 0, nil
	}

	zs, ok := rawZset.(*zset)
	if !ok {
		return 0, fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}

	var removed int64
	for member, score := range zs.members {
		if score >= min && score <= max {
			delete(zs.members, member)
			removed++
		}
	}

	if removed > 0 {
		zs.dirty = true
	}

	return removed, nil
}

// ZCard returns the number of members in a sorted set.
func (s *MemoryStore) ZCard(key string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rawZset, exists := s.data[key]
	if !exists {
		return 0, nil
	}

	zs, ok := rawZset.(*zset)
	if !ok {
		return 0, fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}

	return int64(len(zs.members)), nil
}

// ensureSorted ensures the sorted slice is up to date.
func (s *MemoryStore) ensureSorted(zs *zset) {
	if !zs.dirty {
		return
	}

	zs.sorted = zs.sorted[:0] // clear but keep capacity
	for member, score := range zs.members {
		zs.sorted = append(zs.sorted, zsetMember{
			Member: member,
			Score:  score,
		})
	}

	sort.Slice(zs.sorted, func(i, j int) bool {
		return zs.sorted[i].Score < zs.sorted[j].Score
	})

	zs.dirty = false
}

// SPopN randomly removes and returns the given number of members from a set.
func (s *MemoryStore) SPopN(key string, count int64) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rawSet, exists := s.data[key]
	if !exists {
		return []string{}, nil
	}

	set, ok := rawSet.(map[string]struct{})
	if !ok {
		return nil, fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}

	if count > int64(len(set)) {
		count = int64(len(set))
	}

	popped := make([]string, 0, count)
	for member := range set {
		if int64(len(popped)) >= count {
			break
		}
		popped = append(popped, member)
		delete(set, member)
	}

	return popped, nil
}

// --- Pub/Sub operations ---

// memorySubscription implements the Subscription interface for the in-memory store.
type memorySubscription struct {
	store   *MemoryStore
	channel string
	msgChan chan *Message
}

// Channel returns the message channel for the subscription.
func (ms *memorySubscription) Channel() <-chan *Message {
	return ms.msgChan
}

// Close removes the subscription from the store.
func (ms *memorySubscription) Close() error {
	ms.store.muSubscribers.Lock()
	defer ms.store.muSubscribers.Unlock()

	if subs, ok := ms.store.subscribers[ms.channel]; ok {
		delete(subs, ms.msgChan)
		if len(subs) == 0 {
			delete(ms.store.subscribers, ms.channel)
		}
	}
	close(ms.msgChan)
	return nil
}

// Publish sends a message to all subscribers of a channel.
func (s *MemoryStore) Publish(channel string, message []byte) error {
	s.muSubscribers.RLock()
	defer s.muSubscribers.RUnlock()

	msg := &Message{
		Channel: channel,
		Payload: message,
	}

	if subs, ok := s.subscribers[channel]; ok {
		for subCh := range subs {
			go func(c chan *Message) {
				select {
				case c <- msg:
				case <-time.After(1 * time.Second):
				}
			}(subCh)
		}
	}
	return nil
}

// Subscribe listens for messages on a given channel.
func (s *MemoryStore) Subscribe(channel string) (Subscription, error) {
	s.muSubscribers.Lock()
	defer s.muSubscribers.Unlock()

	msgChan := make(chan *Message, 10) // Buffered channel

	if _, ok := s.subscribers[channel]; !ok {
		s.subscribers[channel] = make(map[chan *Message]struct{})
	}
	s.subscribers[channel][msgChan] = struct{}{}

	sub := &memorySubscription{
		store:   s,
		channel: channel,
		msgChan: msgChan,
	}

	return sub, nil
}
