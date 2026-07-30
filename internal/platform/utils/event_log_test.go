package utils

import (
	"bytes"
	"encoding/json"
	"io"
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

func TestLogPlaneBestEffortAddsCanonicalPlaneAndPrefix(t *testing.T) {
	tests := []struct {
		name        string
		plane       LogPlane
		message     string
		wantPlane   string
		wantMessage string
	}{
		{
			name:        "data",
			plane:       LogPlaneData,
			message:     "Request completed",
			wantPlane:   "data",
			wantMessage: "[DATA] Request completed",
		},
		{
			name:        "control",
			plane:       LogPlaneControl,
			message:     "Mutation completed",
			wantPlane:   "control",
			wantMessage: "[CONTROL] Mutation completed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := logrus.New()
			logger.SetOutput(&output)
			logger.SetFormatter(
				&logrus.JSONFormatter{DisableTimestamp: true},
			)
			fields := logrus.Fields{
				"event": "probe",
				"plane": "caller-value",
			}

			LogPlaneBestEffort(
				logger,
				logrus.InfoLevel,
				test.plane,
				fields,
				test.message,
			)

			var entry map[string]any
			if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
				t.Fatalf("decode log entry: %v", err)
			}
			if entry["event"] != "probe" ||
				entry["plane"] != test.wantPlane ||
				entry["msg"] != test.wantMessage {
				t.Fatalf("entry = %#v", entry)
			}
			if fields["plane"] != "caller-value" || len(fields) != 2 {
				t.Fatalf("caller fields mutated: %#v", fields)
			}
		})
	}
}

func TestLogPlaneBestEffortDropsUnknownPlane(t *testing.T) {
	var output bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&output)
	logger.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})

	LogPlaneBestEffort(
		logger,
		logrus.WarnLevel,
		LogPlane("unknown"),
		logrus.Fields{"event": "must-not-log"},
		"Unknown",
	)

	if output.Len() != 0 {
		t.Fatalf("unknown plane output = %q, want empty", output.String())
	}
}

func TestLogPlaneBestEffortAvoidsProjectionWhenLoggingIsDisabled(t *testing.T) {
	disabledLogger := logrus.New()
	disabledLogger.SetLevel(logrus.WarnLevel)
	fields := logrus.Fields{
		"event": "probe",
		"plane": "caller-value",
	}
	tests := []struct {
		name   string
		logger *logrus.Logger
	}{
		{name: "nil logger"},
		{name: "disabled level", logger: disabledLogger},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			allocations := testing.AllocsPerRun(1000, func() {
				LogPlaneBestEffort(
					test.logger,
					logrus.InfoLevel,
					LogPlaneData,
					fields,
					"Request completed",
				)
			})
			if allocations != 0 {
				t.Fatalf(
					"disabled LogPlaneBestEffort allocations = %v, want 0",
					allocations,
				)
			}
		})
	}
}

func TestLogPlaneBestEffortConcurrentReuseDoesNotMutateFields(t *testing.T) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	fields := logrus.Fields{
		"event": "probe",
		"plane": "caller-value",
	}

	var workers sync.WaitGroup
	for range 64 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			LogPlaneBestEffort(
				logger,
				logrus.InfoLevel,
				LogPlaneData,
				fields,
				"Request completed",
			)
		}()
	}
	workers.Wait()

	if fields["plane"] != "caller-value" || len(fields) != 2 {
		t.Fatalf("caller fields mutated: %#v", fields)
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

func TestLogPlaneBestEffortRecoversLoggerHookPanic(t *testing.T) {
	logger := logrus.New()
	logger.AddHook(panicLogHook{})

	LogPlaneBestEffort(
		logger,
		logrus.WarnLevel,
		LogPlaneControl,
		logrus.Fields{"event": "probe"},
		"Probe",
	)
}
