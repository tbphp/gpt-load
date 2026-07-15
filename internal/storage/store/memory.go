package store

import (
	"fmt"
	"strconv"
	"sync"
	"time"
)

type memoryStoreItem struct {
	value     []byte
	expiresAt int64
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
	return &MemoryStore{
		data:        make(map[string]any),
		subscribers: make(map[string]map[chan *Message]struct{}),
	}
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

	s.data[key] = memoryStoreItem{value: value, expiresAt: expiresAt}
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
			return false, nil
		}
	}

	var expiresAt int64
	if ttl > 0 {
		expiresAt = time.Now().UnixNano() + ttl.Nanoseconds()
	}
	s.data[key] = memoryStoreItem{value: value, expiresAt: expiresAt}
	return true, nil
}

// HSet writes fields to a hash.
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

// HGetAll returns all fields in a hash.
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

// HIncrBy increments an integer hash field.
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

	currentValue, _ := strconv.ParseInt(hash[field], 10, 64)
	newValue := currentValue + incr
	hash[field] = strconv.FormatInt(newValue, 10)
	return newValue, nil
}

// LPush prepends values to a list.
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

	stringValues := make([]string, len(values))
	for i, value := range values {
		stringValues[i] = fmt.Sprint(value)
	}
	s.data[key] = append(stringValues, list...)
	return nil
}

// LRem removes all matching list values when count is zero.
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
	if count != 0 {
		return fmt.Errorf("LRem with non-zero count is not implemented in MemoryStore")
	}

	stringValue := fmt.Sprint(value)
	newList := make([]string, 0, len(list))
	for _, item := range list {
		if item != stringValue {
			newList = append(newList, item)
		}
	}
	s.data[key] = newList
	return nil
}

// Rotate moves the final list item to the front and returns it.
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
	s.data[key] = append([]string{item}, list[:lastIndex]...)
	return item, nil
}

// LLen returns the length of a list.
func (s *MemoryStore) LLen(key string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rawList, exists := s.data[key]
	if !exists {
		return 0, nil
	}
	list, ok := rawList.([]string)
	if !ok {
		return 0, fmt.Errorf("type mismatch: key '%s' holds a different data type", key)
	}
	return int64(len(list)), nil
}

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

// SPopN removes and returns up to count members from a set.
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

	if subscribers, ok := ms.store.subscribers[ms.channel]; ok {
		delete(subscribers, ms.msgChan)
		if len(subscribers) == 0 {
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

	msg := &Message{Channel: channel, Payload: message}
	if subscribers, ok := s.subscribers[channel]; ok {
		for subscriberChannel := range subscribers {
			go func(ch chan *Message) {
				select {
				case ch <- msg:
				case <-time.After(time.Second):
				}
			}(subscriberChannel)
		}
	}
	return nil
}

// Subscribe listens for messages on a given channel.
func (s *MemoryStore) Subscribe(channel string) (Subscription, error) {
	s.muSubscribers.Lock()
	defer s.muSubscribers.Unlock()

	messageChannel := make(chan *Message, 10)
	if _, ok := s.subscribers[channel]; !ok {
		s.subscribers[channel] = make(map[chan *Message]struct{})
	}
	s.subscribers[channel][messageChannel] = struct{}{}
	return &memorySubscription{store: s, channel: channel, msgChan: messageChannel}, nil
}

// Clear clears all data.
func (s *MemoryStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = make(map[string]any)
	return nil
}
