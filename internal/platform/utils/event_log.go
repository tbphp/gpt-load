package utils

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

type RateLimitedEventCounter struct {
	total  atomic.Uint64
	mu     sync.Mutex
	last   time.Time
	window time.Duration
	now    func() time.Time
}

func NewRateLimitedEventCounter(
	window time.Duration,
	now func() time.Time,
) *RateLimitedEventCounter {
	return &RateLimitedEventCounter{
		window: window,
		now:    now,
	}
}

func (counter *RateLimitedEventCounter) Observe() (uint64, bool) {
	total := counter.total.Add(1)
	now := counter.now()

	counter.mu.Lock()
	defer counter.mu.Unlock()
	if !counter.last.IsZero() &&
		now.Before(counter.last.Add(counter.window)) {
		return total, false
	}
	counter.last = now
	return total, true
}

func LogBestEffort(
	logger *logrus.Logger,
	level logrus.Level,
	fields logrus.Fields,
	message string,
) {
	if logger == nil || !logger.IsLevelEnabled(level) {
		return
	}
	defer func() {
		_ = recover()
	}()
	logger.WithFields(fields).Log(level, message)
}
