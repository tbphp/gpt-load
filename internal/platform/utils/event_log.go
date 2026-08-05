package utils

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

type LogPlane string

const (
	LogPlaneData    LogPlane = "data"
	LogPlaneControl LogPlane = "control"
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

func LogPlaneBestEffort(
	logger *logrus.Logger,
	level logrus.Level,
	plane LogPlane,
	fields logrus.Fields,
	message string,
) {
	if logger == nil || !logger.IsLevelEnabled(level) {
		return
	}

	var prefix string
	switch plane {
	case LogPlaneData:
		prefix = "[DATA] "
	case LogPlaneControl:
		prefix = "[CONTROL] "
	default:
		return
	}

	projected := make(logrus.Fields, len(fields))
	for name, value := range fields {
		projected[name] = value
	}
	delete(projected, "plane")
	LogBestEffort(logger, level, projected, prefix+message)
}
