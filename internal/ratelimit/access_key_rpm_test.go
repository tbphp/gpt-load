package ratelimit

import (
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *fakeClock) current() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *fakeClock) set(value time.Time) {
	clock.mu.Lock()
	clock.now = value
	clock.mu.Unlock()
}

func TestAccessKeyRPMAllowsExactSlidingWindow(t *testing.T) {
	base := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: base}
	limiter := NewAccessKeyRPM()
	limiter.now = clock.current

	for request := 1; request <= 3; request++ {
		if got := limiter.Allow(7, 3); !got.Allowed {
			t.Fatalf("request %d rejected: %#v", request, got)
		}
	}
	got := limiter.Allow(7, 3)
	if got.Allowed || got.RetryAfter != time.Minute {
		t.Fatalf("fourth request = %#v, want rejected for 60s", got)
	}
}

func TestAccessKeyRPMExpiresAtExactSixtySecondBoundary(t *testing.T) {
	base := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: base}
	limiter := NewAccessKeyRPM()
	limiter.now = clock.current
	_ = limiter.Allow(7, 1)

	clock.set(base.Add(time.Minute))
	if got := limiter.Allow(7, 1); !got.Allowed {
		t.Fatalf("request at exact boundary = %#v, want allowed", got)
	}
}

func TestAccessKeyRPMRejectDoesNotExtendWindow(t *testing.T) {
	base := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: base}
	limiter := NewAccessKeyRPM()
	limiter.now = clock.current
	_ = limiter.Allow(7, 1)

	clock.set(base.Add(30 * time.Second))
	firstReject := limiter.Allow(7, 1)
	clock.set(base.Add(59 * time.Second))
	secondReject := limiter.Allow(7, 1)
	if firstReject.Allowed || secondReject.Allowed || secondReject.RetryAfter != time.Second {
		t.Fatalf("reject decisions = %#v / %#v", firstReject, secondReject)
	}
}

func TestAccessKeyRPMComputesRetryAfterAfterLimitDecrease(t *testing.T) {
	base := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: base}
	limiter := NewAccessKeyRPM()
	limiter.now = clock.current
	for offset := 0; offset < 5; offset++ {
		clock.set(base.Add(time.Duration(offset) * time.Second))
		if !limiter.Allow(7, 5).Allowed {
			t.Fatal("warmup request rejected")
		}
	}
	clock.set(base.Add(10 * time.Second))
	got := limiter.Allow(7, 3)
	if got.Allowed || got.RetryAfter != 52*time.Second {
		t.Fatalf("decreased limit decision = %#v, want 52s", got)
	}
}

func TestAccessKeyRPMZeroClearsObservedWindow(t *testing.T) {
	limiter := NewAccessKeyRPM()
	if !limiter.Allow(7, 1).Allowed {
		t.Fatal("initial request rejected")
	}
	if !limiter.Allow(7, 0).Allowed {
		t.Fatal("zero limit request rejected")
	}
	if got := limiter.Allow(7, 1); !got.Allowed {
		t.Fatalf("request after zero limit = %#v, want allowed", got)
	}
}

func TestAccessKeyRPMConservativeZeroTransitionWithoutTraffic(t *testing.T) {
	base := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: base}
	limiter := NewAccessKeyRPM()
	limiter.now = clock.current
	_ = limiter.Allow(7, 1)

	clock.set(base.Add(30 * time.Second))
	if got := limiter.Allow(7, 1); got.Allowed {
		t.Fatalf("request after unobserved zero transition = %#v, want rejected", got)
	}
}

func TestAccessKeyRPMRemovesStaleEntriesOpportunistically(t *testing.T) {
	base := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: base}
	limiter := NewAccessKeyRPM()
	limiter.now = clock.current
	_ = limiter.Allow(7, 1)

	clock.set(base.Add(2 * time.Minute))
	_ = limiter.Allow(8, 1)
	if _, exists := limiter.windows[7]; exists {
		t.Fatal("stale access key window still retained")
	}
}

func TestAccessKeyRPMConcurrentLimit(t *testing.T) {
	limiter := NewAccessKeyRPM()
	start := make(chan struct{})
	var group sync.WaitGroup
	allowed := 0
	var count sync.Mutex

	for range 64 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			if limiter.Allow(7, 7).Allowed {
				count.Lock()
				allowed++
				count.Unlock()
			}
		}()
	}
	close(start)
	group.Wait()
	if allowed != 7 {
		t.Fatalf("allowed = %d, want 7", allowed)
	}
}
