package store

import (
	"errors"
	"time"
)

// ErrNotFound is the error returned when a key is not found in the store.
var ErrNotFound = errors.New("store: key not found")

// Message is the struct for received pub/sub messages.
type Message struct {
	Channel string
	Payload []byte
}

// Subscription represents an active subscription to a pub/sub channel.
type Subscription interface {
	Channel() <-chan *Message
	Close() error
}

// Store is a generic key-value store interface.
type Store interface {
	Set(key string, value []byte, ttl time.Duration) error
	Get(key string) ([]byte, error)
	Delete(key string) error
	Del(keys ...string) error
	Exists(key string) (bool, error)
	SetNX(key string, value []byte, ttl time.Duration) (bool, error)
	HSet(key string, values map[string]any) error
	HGetAll(key string) (map[string]string, error)
	HIncrBy(key, field string, incr int64) (int64, error)
	LPush(key string, values ...any) error
	LRem(key string, count int64, value any) error
	Rotate(key string) (string, error)
	LLen(key string) (int64, error)
	SAdd(key string, members ...any) error
	SPopN(key string, count int64) ([]string, error)
	Close() error
	Publish(channel string, message []byte) error
	Subscribe(channel string) (Subscription, error)
	Clear() error
}

// Pipeliner defines an interface for executing a batch of commands.
type Pipeliner interface {
	HSet(key string, values map[string]any)
	Exec() error
}

// RedisPipeliner is an optional interface that a Store can implement to provide pipelining.
type RedisPipeliner interface {
	Pipeline() Pipeliner
}
