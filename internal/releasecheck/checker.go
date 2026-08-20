package releasecheck

import (
	"context"
	"errors"
	"sync"
	"time"

	"gpt-load/internal/platform/version"
)

const (
	successCacheDuration = 6 * time.Hour
	failureCacheDuration = 30 * time.Minute
)

type releaseFetcher interface {
	Fetch(context.Context) ([]Release, error)
}

// Checker maintains the current public update result.
type Checker struct {
	fetcher releaseFetcher
	current string

	mu        sync.Mutex
	update    *Update
	cachedErr error
	expiresAt time.Time
	now       func() time.Time
}

// NewChecker creates the process-local release checker.
func NewChecker(client *Client) *Checker {
	return newChecker(client, version.Version)
}

func newChecker(fetcher releaseFetcher, current string) *Checker {
	return &Checker{
		fetcher: fetcher,
		current: current,
		now:     time.Now,
	}
}

// Check returns the cached result or synchronously refreshes it from GitHub.
// When force is true, it ignores an unexpired cache and fetches GitHub again.
func (checker *Checker) Check(ctx context.Context, force bool) (*Update, error) {
	if checker == nil || checker.fetcher == nil {
		return nil, errors.New("check GitHub releases: checker is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	checker.mu.Lock()
	defer checker.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	now := checker.currentTime()
	if !force && now.Before(checker.expiresAt) {
		return cloneUpdate(checker.update), checker.cachedErr
	}

	releases, err := checker.fetcher.Fetch(ctx)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		checker.update = nil
		checker.cachedErr = err
		checker.expiresAt = checker.currentTime().Add(failureCacheDuration)
		return nil, err
	}

	checker.update = SelectUpdate(checker.current, releases)
	checker.cachedErr = nil
	checker.expiresAt = checker.currentTime().Add(successCacheDuration)
	return cloneUpdate(checker.update), nil
}

func (checker *Checker) currentTime() time.Time {
	if checker.now == nil {
		return time.Now().UTC()
	}
	return checker.now().UTC()
}

func cloneUpdate(update *Update) *Update {
	if update == nil {
		return nil
	}
	result := *update
	return &result
}
