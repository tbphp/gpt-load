package control

import (
	"sync"
	"testing"
	"time"
)

func TestAuthFailureLimiterLocksOnFifthFailure(t *testing.T) {
	current := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
	limiter := newAuthFailureLimiter()
	limiter.now = func() time.Time { return current }

	for attempt := 1; attempt < authFailureLimit; attempt++ {
		decision := limiter.evaluate("192.0.2.1", func() bool { return false })
		if decision.authorized || decision.retryAfter != 0 {
			t.Fatalf("failure %d decision = %#v, want unauthorized without retry", attempt, decision)
		}
	}

	decision := limiter.evaluate("192.0.2.1", func() bool { return false })
	if decision.authorized || decision.retryAfter != authLockDuration {
		t.Fatalf("fifth failure decision = %#v, want unauthorized with retry %s", decision, authLockDuration)
	}
}

func TestAuthFailureLimiterUsesRollingThirtyMinuteWindow(t *testing.T) {
	firstFailure := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
	current := firstFailure
	limiter := newAuthFailureLimiter()
	limiter.now = func() time.Time { return current }

	for minute := 0; minute < 4; minute++ {
		current = firstFailure.Add(time.Duration(minute) * time.Minute)
		if decision := limiter.evaluate("192.0.2.1", func() bool { return false }); decision.retryAfter != 0 {
			t.Fatalf("failure at %s decision = %#v, want no lock", current, decision)
		}
	}

	current = firstFailure.Add(authFailureWindow)
	decision := limiter.evaluate("192.0.2.1", func() bool { return false })
	if decision.retryAfter != 0 {
		t.Fatalf("failure at exact cutoff decision = %#v, want first failure expired", decision)
	}
	decision = limiter.evaluate("192.0.2.1", func() bool { return false })
	if decision.retryAfter != authLockDuration {
		t.Fatalf("next rolling-window failure decision = %#v, want lock", decision)
	}
}

func TestAuthFailureLimiterSuccessBeforeThresholdClearsFailures(t *testing.T) {
	current := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
	limiter := newAuthFailureLimiter()
	limiter.now = func() time.Time { return current }

	for attempt := 0; attempt < authFailureLimit-1; attempt++ {
		limiter.evaluate("192.0.2.1", func() bool { return false })
	}
	if decision := limiter.evaluate("192.0.2.1", func() bool { return true }); !decision.authorized || decision.retryAfter != 0 {
		t.Fatalf("successful decision = %#v, want authorized", decision)
	}
	for attempt := 0; attempt < authFailureLimit-1; attempt++ {
		if decision := limiter.evaluate("192.0.2.1", func() bool { return false }); decision.retryAfter != 0 {
			t.Fatalf("failure after success %d decision = %#v, want no lock", attempt+1, decision)
		}
	}
}

func TestAuthFailureLimiterLockedCredentialSkipsComparison(t *testing.T) {
	current := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
	limiter := newAuthFailureLimiter()
	limiter.now = func() time.Time { return current }
	for attempt := 0; attempt < authFailureLimit; attempt++ {
		limiter.evaluate("192.0.2.1", func() bool { return false })
	}

	comparisons := 0
	decision := limiter.evaluate("192.0.2.1", func() bool {
		comparisons++
		return true
	})
	if decision.authorized || decision.retryAfter <= 0 || comparisons != 0 {
		t.Fatalf("locked decision = %#v, comparisons = %d", decision, comparisons)
	}
}

func TestAuthFailureLimiterUnlocksAfterThirtyMinutes(t *testing.T) {
	current := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
	limiter := newAuthFailureLimiter()
	limiter.now = func() time.Time { return current }
	for attempt := 0; attempt < authFailureLimit; attempt++ {
		limiter.evaluate("192.0.2.1", func() bool { return false })
	}

	current = current.Add(authLockDuration)
	comparisons := 0
	decision := limiter.evaluate("192.0.2.1", func() bool {
		comparisons++
		return true
	})
	if !decision.authorized || decision.retryAfter != 0 || comparisons != 1 {
		t.Fatalf("post-expiry decision = %#v, comparisons = %d", decision, comparisons)
	}
}

func TestAuthFailureLimiterKeepsPeersIsolated(t *testing.T) {
	current := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
	limiter := newAuthFailureLimiter()
	limiter.now = func() time.Time { return current }
	for attempt := 0; attempt < authFailureLimit; attempt++ {
		limiter.evaluate("192.0.2.1", func() bool { return false })
	}

	if decision := limiter.evaluate("192.0.2.2", func() bool { return true }); !decision.authorized || decision.retryAfter != 0 {
		t.Fatalf("other peer decision = %#v, want authorized", decision)
	}
	if decision := limiter.evaluate("192.0.2.1", func() bool { return true }); decision.authorized || decision.retryAfter <= 0 {
		t.Fatalf("locked peer decision = %#v, want lock preserved", decision)
	}
}

func TestAuthFailureLimiterLazilyRemovesExpiredEntries(t *testing.T) {
	firstFailure := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
	current := firstFailure
	limiter := newAuthFailureLimiter()
	limiter.now = func() time.Time { return current }
	limiter.evaluate("expired", func() bool { return false })

	current = current.Add(time.Minute)
	for attempt := 0; attempt < authFailureLimit; attempt++ {
		limiter.evaluate("locked", func() bool { return false })
	}

	current = firstFailure.Add(authFailureWindow)
	limiter.evaluate("trigger", func() bool { return false })
	if _, found := limiter.entries["expired"]; found {
		t.Fatal("expired unlocked entry remains after lazy cleanup")
	}
	if _, found := limiter.entries["locked"]; !found {
		t.Fatal("active locked entry removed before its lock expires")
	}

	current = firstFailure.Add(authFailureWindow + time.Minute)
	limiter.evaluate("trigger", func() bool { return false })
	if _, found := limiter.entries["locked"]; found {
		t.Fatal("expired locked entry remains after lazy cleanup")
	}
}

func TestAuthFailureLimiterConcurrentEvaluation(t *testing.T) {
	current := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
	limiter := newAuthFailureLimiter()
	limiter.now = func() time.Time { return current }

	const evaluations = 32
	var comparisons int
	var ready sync.WaitGroup
	var done sync.WaitGroup
	start := make(chan struct{})
	ready.Add(evaluations)
	done.Add(evaluations)
	for index := 0; index < evaluations; index++ {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			limiter.evaluate("192.0.2.1", func() bool {
				comparisons++
				return false
			})
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()

	decision := limiter.evaluate("192.0.2.1", func() bool { return true })
	if decision.authorized || decision.retryAfter <= 0 {
		t.Fatalf("concurrent final decision = %#v, want lock", decision)
	}
	if comparisons != authFailureLimit {
		t.Fatalf("credential comparisons = %d, want %d before lock", comparisons, authFailureLimit)
	}
}
