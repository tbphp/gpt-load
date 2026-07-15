package store_test

import (
	"bytes"
	"errors"
	"testing"

	"gpt-load/internal/storage/store"
)

func TestNewStoreWithoutRedisUsesMemory(t *testing.T) {
	t.Parallel()

	got, err := store.NewStore("")
	if err != nil {
		t.Fatalf("NewStore(\"\") error = %v", err)
	}
	t.Cleanup(func() {
		if err := got.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	if _, ok := got.(*store.MemoryStore); !ok {
		t.Fatalf("NewStore(\"\") type = %T, want *store.MemoryStore", got)
	}
}

func TestNewStoreRejectsMalformedRedisDSN(t *testing.T) {
	t.Parallel()

	if _, err := store.NewStore("not-a-redis-url"); err == nil {
		t.Fatal("NewStore(malformed DSN) error = nil, want a parsing error")
	}
}

func TestMemoryStoreKeyValueAndSetNX(t *testing.T) {
	t.Parallel()

	s := store.NewMemoryStore()
	if err := s.Set("key", []byte("first"), 0); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	got, err := s.Get("key")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !bytes.Equal(got, []byte("first")) {
		t.Fatalf("Get() = %q, want %q", got, "first")
	}

	set, err := s.SetNX("key", []byte("second"), 0)
	if err != nil {
		t.Fatalf("SetNX(existing) error = %v", err)
	}
	if set {
		t.Fatal("SetNX(existing) = true, want false")
	}

	set, err = s.SetNX("new-key", []byte("new"), 0)
	if err != nil {
		t.Fatalf("SetNX(new) error = %v", err)
	}
	if !set {
		t.Fatal("SetNX(new) = false, want true")
	}

	if err := s.Delete("key"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := s.Get("key"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Get(deleted) error = %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreHashListAndSetOperations(t *testing.T) {
	t.Parallel()

	s := store.NewMemoryStore()

	if err := s.HSet("hash", map[string]any{"count": 1, "state": "ready"}); err != nil {
		t.Fatalf("HSet() error = %v", err)
	}
	count, err := s.HIncrBy("hash", "count", 2)
	if err != nil {
		t.Fatalf("HIncrBy() error = %v", err)
	}
	if count != 3 {
		t.Fatalf("HIncrBy() = %d, want 3", count)
	}

	if err := s.LPush("list", "one"); err != nil {
		t.Fatalf("LPush() error = %v", err)
	}
	rotated, err := s.Rotate("list")
	if err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}
	if rotated != "one" {
		t.Fatalf("Rotate() = %q, want %q", rotated, "one")
	}

	if err := s.SAdd("set", "a", "b"); err != nil {
		t.Fatalf("SAdd() error = %v", err)
	}
	popped, err := s.SPopN("set", 2)
	if err != nil {
		t.Fatalf("SPopN() error = %v", err)
	}
	if len(popped) != 2 {
		t.Fatalf("len(SPopN()) = %d, want 2", len(popped))
	}
}
