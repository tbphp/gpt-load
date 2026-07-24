package ratelimit

import (
	"sync"
	"time"
)

type LimitDecision struct {
	Allowed    bool
	RetryAfter time.Duration
}

type timestampDeque struct {
	values []time.Time
	head   int
}

func (deque *timestampDeque) dropThrough(cutoff time.Time) {
	for deque.head < len(deque.values) && !deque.values[deque.head].After(cutoff) {
		deque.head++
	}
	if deque.head == len(deque.values) {
		deque.values = deque.values[:0]
		deque.head = 0
	} else if deque.head > 64 && deque.head*2 >= len(deque.values) {
		deque.values = append([]time.Time(nil), deque.values[deque.head:]...)
		deque.head = 0
	}
}

func (deque timestampDeque) len() int {
	return len(deque.values) - deque.head
}

func (deque timestampDeque) at(index int) time.Time {
	return deque.values[deque.head+index]
}

func (deque *timestampDeque) push(value time.Time) {
	deque.values = append(deque.values, value)
}

type AccessKeyRPM struct {
	mu          sync.Mutex
	windows     map[uint]timestampDeque
	now         func() time.Time
	lastCleanup time.Time
}

func NewAccessKeyRPM() *AccessKeyRPM {
	return &AccessKeyRPM{windows: make(map[uint]timestampDeque), now: time.Now}
}

func (limiter *AccessKeyRPM) Allow(accessKeyID uint, limit int64) LimitDecision {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	now := limiter.now()
	limiter.cleanup(now)
	if limit <= 0 {
		delete(limiter.windows, accessKeyID)
		return LimitDecision{Allowed: true}
	}

	window := limiter.windows[accessKeyID]
	window.dropThrough(now.Add(-time.Minute))
	count := window.len()
	if int64(count) >= limit {
		target := window.at(count - int(limit))
		retryAfter := ceilToSecond(target.Add(time.Minute).Sub(now))
		if retryAfter < time.Second {
			retryAfter = time.Second
		}
		if retryAfter > time.Minute {
			retryAfter = time.Minute
		}
		limiter.windows[accessKeyID] = window
		return LimitDecision{Allowed: false, RetryAfter: retryAfter}
	}
	window.push(now)
	limiter.windows[accessKeyID] = window
	return LimitDecision{Allowed: true}
}

func (limiter *AccessKeyRPM) cleanup(now time.Time) {
	if !limiter.lastCleanup.IsZero() && now.Sub(limiter.lastCleanup) < time.Minute {
		return
	}

	cutoff := now.Add(-time.Minute)
	for accessKeyID, window := range limiter.windows {
		window.dropThrough(cutoff)
		if window.len() == 0 {
			delete(limiter.windows, accessKeyID)
			continue
		}
		limiter.windows[accessKeyID] = window
	}
	limiter.lastCleanup = now
}

func ceilToSecond(duration time.Duration) time.Duration {
	if duration <= 0 {
		return duration
	}
	return ((duration + time.Second - 1) / time.Second) * time.Second
}
