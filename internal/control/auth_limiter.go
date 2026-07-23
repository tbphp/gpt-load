package control

import (
	"sync"
	"time"
)

const (
	authFailureWindow   = 30 * time.Minute
	authFailureLimit    = 5
	authLockDuration    = 30 * time.Minute
	authCleanupInterval = time.Minute
)

type authFailureEntry struct {
	failures    []time.Time
	lockedUntil time.Time
}

type authDecision struct {
	authorized bool
	retryAfter time.Duration
}

type authFailureLimiter struct {
	mu          sync.Mutex
	entries     map[string]authFailureEntry
	now         func() time.Time
	lastCleanup time.Time
}

func newAuthFailureLimiter() *authFailureLimiter {
	return &authFailureLimiter{
		entries: make(map[string]authFailureEntry),
		now:     time.Now,
	}
}

func (l *authFailureLimiter) evaluate(
	peer string,
	credentialValid func() bool,
) authDecision {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.cleanup(now)
	entry := l.entries[peer]
	if now.Before(entry.lockedUntil) {
		return authDecision{retryAfter: entry.lockedUntil.Sub(now)}
	}
	if !entry.lockedUntil.IsZero() {
		delete(l.entries, peer)
		entry = authFailureEntry{}
	}

	entry.failures = retainRecentFailures(entry.failures, now.Add(-authFailureWindow))
	if credentialValid() {
		delete(l.entries, peer)
		return authDecision{authorized: true}
	}

	entry.failures = append(entry.failures, now)
	if len(entry.failures) >= authFailureLimit {
		entry.failures = nil
		entry.lockedUntil = now.Add(authLockDuration)
		l.entries[peer] = entry
		return authDecision{retryAfter: authLockDuration}
	}
	l.entries[peer] = entry
	return authDecision{}
}

func (l *authFailureLimiter) cleanup(now time.Time) {
	if !l.lastCleanup.IsZero() && now.Before(l.lastCleanup.Add(authCleanupInterval)) {
		return
	}
	l.lastCleanup = now
	cutoff := now.Add(-authFailureWindow)
	for peer, entry := range l.entries {
		if !entry.lockedUntil.IsZero() {
			if !now.Before(entry.lockedUntil) {
				delete(l.entries, peer)
			}
			continue
		}
		entry.failures = retainRecentFailures(entry.failures, cutoff)
		if len(entry.failures) == 0 {
			delete(l.entries, peer)
			continue
		}
		l.entries[peer] = entry
	}
}

func retainRecentFailures(values []time.Time, cutoff time.Time) []time.Time {
	retained := values[:0]
	for _, value := range values {
		if value.After(cutoff) {
			retained = append(retained, value)
		}
	}
	return retained
}
