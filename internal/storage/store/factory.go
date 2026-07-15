package store

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// NewStore creates a Redis store when redisDSN is configured, otherwise an in-memory store.
func NewStore(redisDSN string) (Store, error) {
	if redisDSN != "" {
		opts, err := redis.ParseURL(redisDSN)
		if err != nil {
			return nil, fmt.Errorf("parse Redis DSN: %w", err)
		}

		client := redis.NewClient(opts)
		if err := client.Ping(context.Background()).Err(); err != nil {
			_ = client.Close()
			return nil, fmt.Errorf("connect to Redis: %w", err)
		}

		logrus.Debug("Successfully connected to Redis.")
		return NewRedisStore(client), nil
	}

	logrus.Info("Redis DSN not configured, falling back to in-memory store.")
	return NewMemoryStore(), nil
}
