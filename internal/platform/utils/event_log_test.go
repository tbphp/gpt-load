package utils

import (
	"bytes"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestRateLimitedEventCounterCountsEveryObservationAndThrottles(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	counter := NewRateLimitedEventCounter(time.Minute, func() time.Time {
		return now
	})

	if total, shouldLog := counter.Observe(); total != 1 || !shouldLog {
		t.Fatalf("first observation = %d/%t, want 1/true", total, shouldLog)
	}
	if total, shouldLog := counter.Observe(); total != 2 || shouldLog {
		t.Fatalf("second observation = %d/%t, want 2/false", total, shouldLog)
	}
	now = now.Add(time.Minute)
	if total, shouldLog := counter.Observe(); total != 3 || !shouldLog {
		t.Fatalf("next-window observation = %d/%t, want 3/true", total, shouldLog)
	}
}

func TestRateLimitedEventCounterIsConcurrent(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	counter := NewRateLimitedEventCounter(time.Minute, func() time.Time {
		return now
	})
	type result struct {
		total     uint64
		shouldLog bool
	}
	results := make(chan result, 64)
	var workers sync.WaitGroup
	for range 64 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			total, shouldLog := counter.Observe()
			results <- result{total: total, shouldLog: shouldLog}
		}()
	}
	workers.Wait()
	close(results)

	var maximum uint64
	logged := 0
	for item := range results {
		if item.total > maximum {
			maximum = item.total
		}
		if item.shouldLog {
			logged++
		}
	}
	if maximum != 64 || logged != 1 {
		t.Fatalf("maximum/logged = %d/%d, want 64/1", maximum, logged)
	}
}

func TestLogBestEffortWritesStructuredEntry(t *testing.T) {
	var output bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&output)
	logger.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})

	LogBestEffort(
		logger,
		logrus.WarnLevel,
		logrus.Fields{"event": "probe", "total": uint64(3)},
		"Probe warning",
	)

	var entry struct {
		Event string `json:"event"`
		Level string `json:"level"`
		Msg   string `json:"msg"`
		Total uint64 `json:"total"`
	}
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("decode log entry: %v", err)
	}
	if entry.Event != "probe" || entry.Level != "warning" ||
		entry.Msg != "Probe warning" || entry.Total != 3 {
		t.Fatalf("entry = %#v", entry)
	}
}

type panicLogHook struct{}

func (panicLogHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (panicLogHook) Fire(*logrus.Entry) error {
	panic("hook must be isolated")
}

func TestLogBestEffortRecoversLoggerHookPanic(t *testing.T) {
	logger := logrus.New()
	logger.AddHook(panicLogHook{})

	LogBestEffort(
		logger,
		logrus.WarnLevel,
		logrus.Fields{"event": "probe"},
		"Probe",
	)
}
