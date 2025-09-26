package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisKeyPrefix is the prefix for all Redis keys used by GPT-Load
const RedisKeyPrefix = "gpt-load:"

// RedisStore is a Redis-backed key-value store.
type RedisStore struct {
	client *redis.Client
}

// NewRedisStore creates a new RedisStore instance.
func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{client: client}
}

// prefixKey adds the application prefix to a key
func (s *RedisStore) prefixKey(key string) string {
	return RedisKeyPrefix + key
}

// prefixKeys adds the application prefix to multiple keys
func (s *RedisStore) prefixKeys(keys []string) []string {
	prefixed := make([]string, len(keys))
	for i, key := range keys {
		prefixed[i] = s.prefixKey(key)
	}
	return prefixed
}

// Set stores a key-value pair in Redis.
func (s *RedisStore) Set(key string, value []byte, ttl time.Duration) error {
	return s.client.Set(context.Background(), s.prefixKey(key), value, ttl).Err()
}

// Get retrieves a value from Redis.
func (s *RedisStore) Get(key string) ([]byte, error) {
	val, err := s.client.Get(context.Background(), s.prefixKey(key)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return val, nil
}

// Delete removes a value from Redis.
func (s *RedisStore) Delete(key string) error {
	return s.client.Del(context.Background(), s.prefixKey(key)).Err()
}

// Del removes multiple values from Redis.
func (s *RedisStore) Del(keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return s.client.Del(context.Background(), s.prefixKeys(keys)...).Err()
}

// Exists checks if a key exists in Redis.
func (s *RedisStore) Exists(key string) (bool, error) {
	val, err := s.client.Exists(context.Background(), s.prefixKey(key)).Result()
	if err != nil {
		return false, err
	}
	return val > 0, nil
}

// SetNX sets a key-value pair in Redis if the key does not already exist.
func (s *RedisStore) SetNX(key string, value []byte, ttl time.Duration) (bool, error) {
	return s.client.SetNX(context.Background(), s.prefixKey(key), value, ttl).Result()
}

// Close closes the Redis client connection.
func (s *RedisStore) Close() error {
	return s.client.Close()
}

// --- HASH operations ---

func (s *RedisStore) HSet(key string, values map[string]any) error {
	return s.client.HSet(context.Background(), s.prefixKey(key), values).Err()
}

func (s *RedisStore) HGetAll(key string) (map[string]string, error) {
	return s.client.HGetAll(context.Background(), s.prefixKey(key)).Result()
}

func (s *RedisStore) HIncrBy(key, field string, incr int64) (int64, error) {
	return s.client.HIncrBy(context.Background(), s.prefixKey(key), field, incr).Result()
}

// --- LIST operations ---

func (s *RedisStore) LPush(key string, values ...any) error {
	return s.client.LPush(context.Background(), s.prefixKey(key), values...).Err()
}

func (s *RedisStore) LRem(key string, count int64, value any) error {
	return s.client.LRem(context.Background(), s.prefixKey(key), count, value).Err()
}

func (s *RedisStore) Rotate(key string) (string, error) {
	prefixedKey := s.prefixKey(key)
	val, err := s.client.RPopLPush(context.Background(), prefixedKey, prefixedKey).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", ErrNotFound
		}
		return "", err
	}
	return val, nil
}

// LLen returns the length of a list.
func (s *RedisStore) LLen(key string) (int64, error) {
	return s.client.LLen(context.Background(), s.prefixKey(key)).Result()
}

// --- SET operations ---

func (s *RedisStore) SAdd(key string, members ...any) error {
	return s.client.SAdd(context.Background(), s.prefixKey(key), members...).Err()
}

func (s *RedisStore) SPopN(key string, count int64) ([]string, error) {
	return s.client.SPopN(context.Background(), s.prefixKey(key), count).Result()
}

// --- Pipeliner implementation ---

type redisPipeliner struct {
	pipe  redis.Pipeliner
	store *RedisStore
}

// HSet adds an HSET command to the pipeline.
func (p *redisPipeliner) HSet(key string, values map[string]any) {
	p.pipe.HSet(context.Background(), p.store.prefixKey(key), values)
}

// Exec executes all commands in the pipeline.
func (p *redisPipeliner) Exec() error {
	_, err := p.pipe.Exec(context.Background())
	return err
}

// Pipeline creates a new pipeline.
func (s *RedisStore) Pipeline() Pipeliner {
	return &redisPipeliner{
		pipe:  s.client.Pipeline(),
		store: s,
	}
}

// --- Pub/Sub operations ---

// redisSubscription wraps the redis.PubSub to implement the Subscription interface.
type redisSubscription struct {
	pubsub  *redis.PubSub
	msgChan chan *Message
	once    sync.Once
}

// Channel returns a channel that receives messages from the subscription.
func (rs *redisSubscription) Channel() <-chan *Message {
	rs.once.Do(func() {
		rs.msgChan = make(chan *Message, 10)
		go func() {
			defer close(rs.msgChan)
			for redisMsg := range rs.pubsub.Channel() {
				rs.msgChan <- &Message{
					Channel: redisMsg.Channel,
					Payload: []byte(redisMsg.Payload),
				}
			}
		}()
	})
	return rs.msgChan
}

// Close closes the subscription.
func (rs *redisSubscription) Close() error {
	return rs.pubsub.Close()
}

// Publish sends a message to a given channel.
func (s *RedisStore) Publish(channel string, message []byte) error {
	return s.client.Publish(context.Background(), s.prefixKey(channel), message).Err()
}

// Subscribe listens for messages on a given channel.
func (s *RedisStore) Subscribe(channel string) (Subscription, error) {
	prefixedChannel := s.prefixKey(channel)
	pubsub := s.client.Subscribe(context.Background(), prefixedChannel)

	_, err := pubsub.Receive(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to channel %s: %w", channel, err)
	}

	return &redisSubscription{pubsub: pubsub}, nil
}

// Clear clears all keys with the GPT-Load prefix in the current Redis database.
// This method only removes keys that belong to GPT-Load, preserving other applications' data.
func (s *RedisStore) Clear() error {
	ctx := context.Background()

	// Use SCAN to iterate through all keys with our prefix
	var cursor uint64
	var allKeys []string

	for {
		// Scan for keys with our prefix, 1000 at a time
		keys, nextCursor, err := s.client.Scan(ctx, cursor, RedisKeyPrefix+"*", 10000).Result()
		if err != nil {
			return fmt.Errorf("failed to scan keys: %w", err)
		}

		allKeys = append(allKeys, keys...)
		cursor = nextCursor

		// When cursor is 0, we've completed the full iteration
		if cursor == 0 {
			break
		}
	}

	// If no keys found, return early
	if len(allKeys) == 0 {
		return nil
	}

	// Delete keys in batches to avoid overwhelming Redis
	const batchSize = 1000
	for i := 0; i < len(allKeys); i += batchSize {
		end := i + batchSize
		if end > len(allKeys) {
			end = len(allKeys)
		}

		batch := allKeys[i:end]
		if err := s.client.Del(ctx, batch...).Err(); err != nil {
			return fmt.Errorf("failed to delete keys: %w", err)
		}
	}

	return nil
}
