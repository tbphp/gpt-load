package store

import (
	"testing"
	"time"
)

func TestDeleteIfExpiredDoesNotDeleteReplacement(t *testing.T) {
	t.Parallel()

	s := NewMemoryStore()
	s.data["key"] = memoryStoreItem{
		value:     []byte("replacement"),
		expiresAt: time.Now().Add(time.Minute).UnixNano(),
	}

	s.deleteIfExpired("key")

	item, exists := s.data["key"]
	if !exists {
		t.Fatal("deleteIfExpired() deleted a live replacement")
	}
	if got := string(item.(memoryStoreItem).value); got != "replacement" {
		t.Fatalf("replacement value = %q", got)
	}
}

func TestDeleteIfExpiredRemovesCurrentExpiredValue(t *testing.T) {
	t.Parallel()

	s := NewMemoryStore()
	s.data["key"] = memoryStoreItem{
		value:     []byte("expired"),
		expiresAt: time.Now().Add(-time.Minute).UnixNano(),
	}

	s.deleteIfExpired("key")

	if _, exists := s.data["key"]; exists {
		t.Fatal("deleteIfExpired() retained an expired value")
	}
}
