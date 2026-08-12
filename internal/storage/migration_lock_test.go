package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPostgresMigrationLockHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := acquirePostgresMigrationLock(ctx, nil, time.Millisecond)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("acquirePostgresMigrationLock() error = %v, want context canceled", err)
	}
}
