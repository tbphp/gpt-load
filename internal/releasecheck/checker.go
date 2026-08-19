package releasecheck

import (
	"context"
	"sync"
	"time"

	"gpt-load/internal/platform/version"
)

const (
	successCacheDuration = 6 * time.Hour
	failureRetryDuration = 30 * time.Minute
)

type releaseFetcher interface {
	Fetch(context.Context) ([]Release, error)
}

// Checker maintains the current public update result.
type Checker struct {
	fetcher releaseFetcher
	current string

	checkMu     sync.Mutex
	mu          sync.RWMutex
	update      *Update
	nextCheckAt time.Time
	now         func() time.Time
	wait        func(context.Context, time.Duration) bool
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
		wait:    waitForNextCheck,
	}
}

// Snapshot returns the currently confirmed update, if any.
func (checker *Checker) Snapshot() *Update {
	if checker == nil {
		return nil
	}
	checker.mu.RLock()
	defer checker.mu.RUnlock()
	if checker.update == nil {
		return nil
	}
	result := *checker.update
	return &result
}

func (checker *Checker) check(ctx context.Context) time.Duration {
	if checker == nil || checker.fetcher == nil {
		return failureRetryDuration
	}
	checker.checkMu.Lock()
	defer checker.checkMu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	now := checker.currentTime()
	checker.mu.RLock()
	nextCheckAt := checker.nextCheckAt
	checker.mu.RUnlock()
	if now.Before(nextCheckAt) {
		return nextCheckAt.Sub(now)
	}

	releases, err := checker.fetcher.Fetch(ctx)
	if ctx.Err() != nil {
		return 0
	}
	delay := successCacheDuration
	var update *Update
	if err != nil {
		delay = failureRetryDuration
	} else {
		update = SelectUpdate(checker.current, releases)
	}
	checkedAt := checker.currentTime()
	checker.mu.Lock()
	checker.update = update
	checker.nextCheckAt = checkedAt.Add(delay)
	checker.mu.Unlock()
	return delay
}

// Run checks immediately, then waits according to the result cache policy.
func (checker *Checker) Run(ctx context.Context) {
	if checker == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	wait := checker.wait
	if wait == nil {
		wait = waitForNextCheck
	}
	for ctx.Err() == nil {
		delay := checker.check(ctx)
		if ctx.Err() != nil {
			return
		}
		if delay <= 0 {
			delay = failureRetryDuration
		}
		if !wait(ctx, delay) {
			return
		}
	}
}

func (checker *Checker) currentTime() time.Time {
	if checker.now == nil {
		return time.Now().UTC()
	}
	return checker.now().UTC()
}

func waitForNextCheck(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
