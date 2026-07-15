package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisKeyPrefix is the prefix for all Redis keys used by GPT-Load.
const RedisKeyPrefix = "gpt-load:"

// RedisStore is a Redis-backed key-value store.
type RedisStore struct {
	client *redis.Client
}

// NewRedisStore creates a new RedisStore instance.
func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{client: client}
}

func (s *RedisStore) prefixKey(key string) string {
	return RedisKeyPrefix + key
}

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
	value, err := s.client.Get(context.Background(), s.prefixKey(key)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return value, nil
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
	count, err := s.client.Exists(context.Background(), s.prefixKey(key)).Result()
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// SetNX sets a key-value pair if it does not already exist.
func (s *RedisStore) SetNX(key string, value []byte, ttl time.Duration) (bool, error) {
	return s.client.SetNX(context.Background(), s.prefixKey(key), value, ttl).Result()
}

// Close closes the Redis client connection.
func (s *RedisStore) Close() error {
	return s.client.Close()
}

// HSet writes fields to a Redis hash.
func (s *RedisStore) HSet(key string, values map[string]any) error {
	return s.client.HSet(context.Background(), s.prefixKey(key), values).Err()
}

// HGetAll returns all fields in a Redis hash.
func (s *RedisStore) HGetAll(key string) (map[string]string, error) {
	return s.client.HGetAll(context.Background(), s.prefixKey(key)).Result()
}

// HIncrBy increments an integer Redis hash field.
func (s *RedisStore) HIncrBy(key, field string, incr int64) (int64, error) {
	return s.client.HIncrBy(context.Background(), s.prefixKey(key), field, incr).Result()
}

// LPush prepends values to a Redis list.
func (s *RedisStore) LPush(key string, values ...any) error {
	return s.client.LPush(context.Background(), s.prefixKey(key), values...).Err()
}

// LRem removes matching values from a Redis list.
func (s *RedisStore) LRem(key string, count int64, value any) error {
	return s.client.LRem(context.Background(), s.prefixKey(key), count, value).Err()
}

// Rotate moves the final Redis list item to the front and returns it.
func (s *RedisStore) Rotate(key string) (string, error) {
	prefixedKey := s.prefixKey(key)
	value, err := s.client.RPopLPush(context.Background(), prefixedKey, prefixedKey).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", ErrNotFound
		}
		return "", err
	}
	return value, nil
}

// LLen returns the length of a Redis list.
func (s *RedisStore) LLen(key string) (int64, error) {
	return s.client.LLen(context.Background(), s.prefixKey(key)).Result()
}

// SAdd adds members to a Redis set.
func (s *RedisStore) SAdd(key string, members ...any) error {
	return s.client.SAdd(context.Background(), s.prefixKey(key), members...).Err()
}

// SPopN removes and returns up to count Redis set members.
func (s *RedisStore) SPopN(key string, count int64) ([]string, error) {
	return s.client.SPopN(context.Background(), s.prefixKey(key), count).Result()
}

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
	return &redisPipeliner{pipe: s.client.Pipeline(), store: s}
}

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
			for redisMessage := range rs.pubsub.Channel() {
				rs.msgChan <- &Message{
					Channel: redisMessage.Channel,
					Payload: []byte(redisMessage.Payload),
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

// Publish sends a message to a Redis channel.
func (s *RedisStore) Publish(channel string, message []byte) error {
	return s.client.Publish(context.Background(), s.prefixKey(channel), message).Err()
}

// Subscribe listens for messages on a Redis channel.
func (s *RedisStore) Subscribe(channel string) (Subscription, error) {
	prefixedChannel := s.prefixKey(channel)
	pubsub := s.client.Subscribe(context.Background(), prefixedChannel)
	if _, err := pubsub.Receive(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to subscribe to channel %s: %w", channel, err)
	}
	return &redisSubscription{pubsub: pubsub}, nil
}

// Clear removes all GPT-Load-prefixed keys from the current Redis database.
func (s *RedisStore) Clear() error {
	ctx := context.Background()
	var cursor uint64
	var allKeys []string

	for {
		keys, nextCursor, err := s.client.Scan(ctx, cursor, RedisKeyPrefix+"*", 10000).Result()
		if err != nil {
			return fmt.Errorf("failed to scan keys: %w", err)
		}
		allKeys = append(allKeys, keys...)
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	const batchSize = 1000
	for start := 0; start < len(allKeys); start += batchSize {
		end := start + batchSize
		if end > len(allKeys) {
			end = len(allKeys)
		}
		if err := s.client.Del(ctx, allKeys[start:end]...).Err(); err != nil {
			return fmt.Errorf("failed to delete keys: %w", err)
		}
	}
	return nil
}
