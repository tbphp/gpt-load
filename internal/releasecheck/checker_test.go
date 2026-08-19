package releasecheck

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fetchResult struct {
	releases []Release
	err      error
}

type sequenceFetcher struct {
	mu      sync.Mutex
	results []fetchResult
	calls   int
}

func (fetcher *sequenceFetcher) Fetch(context.Context) ([]Release, error) {
	fetcher.mu.Lock()
	defer fetcher.mu.Unlock()
	index := fetcher.calls
	fetcher.calls++
	if index >= len(fetcher.results) {
		return nil, errors.New("unexpected fetch")
	}
	return fetcher.results[index].releases, fetcher.results[index].err
}

func (fetcher *sequenceFetcher) callCount() int {
	fetcher.mu.Lock()
	defer fetcher.mu.Unlock()
	return fetcher.calls
}

func TestCheckerCachesSuccessForSixHoursAndFailureForThirtyMinutes(t *testing.T) {
	base := time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)
	now := base
	fetcher := &sequenceFetcher{results: []fetchResult{
		{releases: []Release{testRelease("v2.0.1", "2026-08-19T11:00:00Z")}},
		{err: errors.New("offline")},
		{releases: []Release{testRelease("v2.0.2", "2026-08-19T19:00:00Z")}},
	}}
	checker := newChecker(fetcher, "v2.0.0")
	checker.now = func() time.Time { return now }

	if delay := checker.check(t.Context()); delay != successCacheDuration {
		t.Fatalf("first delay = %v, want %v", delay, successCacheDuration)
	}
	if update := checker.Snapshot(); update == nil || update.Version != "v2.0.1" {
		t.Fatalf("first Snapshot() = %#v", update)
	}

	now = base.Add(2 * time.Hour)
	if delay := checker.check(t.Context()); delay != 4*time.Hour || fetcher.callCount() != 1 {
		t.Fatalf("cached delay/calls = %v/%d, want 4h/1", delay, fetcher.callCount())
	}

	now = base.Add(6 * time.Hour)
	if delay := checker.check(t.Context()); delay != failureRetryDuration {
		t.Fatalf("failure delay = %v, want %v", delay, failureRetryDuration)
	}
	if update := checker.Snapshot(); update != nil {
		t.Fatalf("Snapshot() after failure = %#v, want nil", update)
	}

	now = base.Add(6*time.Hour + 15*time.Minute)
	if delay := checker.check(t.Context()); delay != 15*time.Minute || fetcher.callCount() != 2 {
		t.Fatalf("failure cache delay/calls = %v/%d, want 15m/2", delay, fetcher.callCount())
	}

	now = base.Add(6*time.Hour + 30*time.Minute)
	if delay := checker.check(t.Context()); delay != successCacheDuration {
		t.Fatalf("recovery delay = %v, want %v", delay, successCacheDuration)
	}
	if update := checker.Snapshot(); update == nil || update.Version != "v2.0.2" || fetcher.callCount() != 3 {
		t.Fatalf("recovered Snapshot/calls = %#v/%d", update, fetcher.callCount())
	}
}

func TestCheckerCachesSuccessfulNoUpdateResult(t *testing.T) {
	fetcher := &sequenceFetcher{results: []fetchResult{{
		releases: []Release{testRelease("v2.0.0-beta.8", "2026-08-19T11:00:00Z")},
	}}}
	checker := newChecker(fetcher, "v2.0.0")
	if delay := checker.check(t.Context()); delay != successCacheDuration ||
		checker.Snapshot() != nil || fetcher.callCount() != 1 {
		t.Fatalf("delay/update/calls = %v/%#v/%d", delay, checker.Snapshot(), fetcher.callCount())
	}
}

type concurrentFetcher struct {
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (fetcher *concurrentFetcher) Fetch(context.Context) ([]Release, error) {
	if fetcher.calls.Add(1) == 1 {
		close(fetcher.started)
	}
	<-fetcher.release
	return nil, nil
}

func TestCheckerCoalescesConcurrentChecks(t *testing.T) {
	fetcher := &concurrentFetcher{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	checker := newChecker(fetcher, "v2.0.0")
	done := make(chan struct{}, 2)
	go func() {
		checker.check(t.Context())
		done <- struct{}{}
	}()
	<-fetcher.started
	go func() {
		checker.check(t.Context())
		done <- struct{}{}
	}()
	time.Sleep(25 * time.Millisecond)
	if calls := fetcher.calls.Load(); calls != 1 {
		close(fetcher.release)
		t.Fatalf("concurrent fetch calls = %d, want 1", calls)
	}
	close(fetcher.release)
	<-done
	<-done
	if calls := fetcher.calls.Load(); calls != 1 {
		t.Fatalf("completed fetch calls = %d, want 1", calls)
	}
}

func TestCheckerRunChecksImmediatelyAndUsesResultDelay(t *testing.T) {
	fetcher := &sequenceFetcher{results: []fetchResult{{}}}
	checker := newChecker(fetcher, "v2.0.0")
	var gotDelay time.Duration
	checker.wait = func(_ context.Context, delay time.Duration) bool {
		gotDelay = delay
		return false
	}

	checker.Run(t.Context())
	if fetcher.callCount() != 1 || gotDelay != successCacheDuration {
		t.Fatalf("Run calls/delay = %d/%v", fetcher.callCount(), gotDelay)
	}
}

type blockingFetcher struct {
	started chan struct{}
}

func (fetcher blockingFetcher) Fetch(ctx context.Context) ([]Release, error) {
	close(fetcher.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestCheckerRunCancelsActiveFetch(t *testing.T) {
	fetcher := blockingFetcher{started: make(chan struct{})}
	checker := newChecker(fetcher, "v2.0.0")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		checker.Run(ctx)
		close(done)
	}()
	select {
	case <-fetcher.started:
	case <-time.After(time.Second):
		t.Fatal("Run did not start an immediate release check")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}
