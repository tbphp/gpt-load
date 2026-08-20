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

func TestCheckerCheckRunsOnlyOnDemandAndCachesSuccess(t *testing.T) {
	base := time.Date(2026, time.August, 20, 8, 0, 0, 0, time.UTC)
	now := base
	fetcher := &sequenceFetcher{results: []fetchResult{{
		releases: []Release{testRelease("v2.0.1", "2026-08-20T07:00:00Z")},
	}}}
	checker := newChecker(fetcher, "v2.0.0")
	checker.now = func() time.Time { return now }

	if calls := fetcher.callCount(); calls != 0 {
		t.Fatalf("constructor fetch calls = %d, want 0", calls)
	}
	first, err := checker.Check(t.Context())
	if err != nil || first == nil || first.Version != "v2.0.1" || fetcher.callCount() != 1 {
		t.Fatalf("first Check() = %#v, %v, calls=%d", first, err, fetcher.callCount())
	}
	first.Version = "mutated"
	now = base.Add(2 * time.Hour)
	second, err := checker.Check(t.Context())
	if err != nil || second == nil || second.Version != "v2.0.1" || fetcher.callCount() != 1 {
		t.Fatalf("cached Check() = %#v, %v, calls=%d", second, err, fetcher.callCount())
	}
}

func TestCheckerCachesSuccessForSixHoursAndFailureForThirtyMinutes(t *testing.T) {
	base := time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)
	now := base
	offlineErr := errors.New("offline")
	fetcher := &sequenceFetcher{results: []fetchResult{
		{releases: []Release{testRelease("v2.0.1", "2026-08-19T11:00:00Z")}},
		{err: offlineErr},
		{releases: []Release{testRelease("v2.0.2", "2026-08-19T19:00:00Z")}},
	}}
	checker := newChecker(fetcher, "v2.0.0")
	checker.now = func() time.Time { return now }

	update, err := checker.Check(t.Context())
	if err != nil || update == nil || update.Version != "v2.0.1" {
		t.Fatalf("first Check() = %#v, %v", update, err)
	}

	now = base.Add(2 * time.Hour)
	update, err = checker.Check(t.Context())
	if err != nil || update == nil || update.Version != "v2.0.1" || fetcher.callCount() != 1 {
		t.Fatalf("cached Check() = %#v, %v, calls=%d", update, err, fetcher.callCount())
	}

	now = base.Add(6 * time.Hour)
	update, err = checker.Check(t.Context())
	if update != nil || !errors.Is(err, offlineErr) || fetcher.callCount() != 2 {
		t.Fatalf("failed Check() = %#v, %v, calls=%d", update, err, fetcher.callCount())
	}

	now = base.Add(6*time.Hour + 15*time.Minute)
	update, err = checker.Check(t.Context())
	if update != nil || !errors.Is(err, offlineErr) || fetcher.callCount() != 2 {
		t.Fatalf("cached failure Check() = %#v, %v, calls=%d", update, err, fetcher.callCount())
	}

	now = base.Add(6*time.Hour + 30*time.Minute)
	update, err = checker.Check(t.Context())
	if err != nil || update == nil || update.Version != "v2.0.2" || fetcher.callCount() != 3 {
		t.Fatalf("recovered Check() = %#v, %v, calls=%d", update, err, fetcher.callCount())
	}
}

func TestCheckerCachesSuccessfulNoUpdateResult(t *testing.T) {
	fetcher := &sequenceFetcher{results: []fetchResult{{
		releases: []Release{testRelease("v2.0.0-beta.8", "2026-08-19T11:00:00Z")},
	}}}
	checker := newChecker(fetcher, "v2.0.0")
	update, err := checker.Check(t.Context())
	if err != nil || update != nil || fetcher.callCount() != 1 {
		t.Fatalf("Check() = %#v, %v, calls=%d", update, err, fetcher.callCount())
	}
	update, err = checker.Check(t.Context())
	if err != nil || update != nil || fetcher.callCount() != 1 {
		t.Fatalf("cached Check() = %#v, %v, calls=%d", update, err, fetcher.callCount())
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
	done := make(chan error, 2)
	go func() {
		_, err := checker.Check(t.Context())
		done <- err
	}()
	<-fetcher.started
	go func() {
		_, err := checker.Check(t.Context())
		done <- err
	}()
	time.Sleep(25 * time.Millisecond)
	if calls := fetcher.calls.Load(); calls != 1 {
		close(fetcher.release)
		t.Fatalf("concurrent fetch calls = %d, want 1", calls)
	}
	close(fetcher.release)
	if firstErr, secondErr := <-done, <-done; firstErr != nil || secondErr != nil {
		t.Fatalf("concurrent Check errors = %v, %v", firstErr, secondErr)
	}
	if calls := fetcher.calls.Load(); calls != 1 {
		t.Fatalf("completed fetch calls = %d, want 1", calls)
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

func TestCheckerCheckCancelsActiveFetch(t *testing.T) {
	fetcher := blockingFetcher{started: make(chan struct{})}
	checker := newChecker(fetcher, "v2.0.0")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := checker.Check(ctx)
		done <- err
	}()
	select {
	case <-fetcher.started:
	case <-time.After(time.Second):
		t.Fatal("Run did not start an immediate release check")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Check() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Check did not return after cancellation")
	}
}
