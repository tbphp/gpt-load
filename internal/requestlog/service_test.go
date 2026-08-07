package requestlog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

func TestServiceEmitLogsAcceptedCompletionExactlyOnce(t *testing.T) {
	var output bytes.Buffer
	timers := newManualTimerFactory()
	service := newService(
		batchWriterFunc(func(context.Context, []models.RequestLog) error {
			return nil
		}),
		redact.New(),
		timers.New,
	)
	service.logger = newRequestLogJSONLogger(&output)
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	service.Emit(testEvent("accepted"))

	if got := service.Stats().EnqueuedTotal; got != 1 {
		t.Fatalf("EnqueuedTotal = %d, want 1", got)
	}
	entries := processLogEntries(t, output.Bytes())
	if len(entries) != 1 ||
		entries[0]["msg"] != "[DATA] Request completed" {
		t.Fatalf("completion entries = %#v, want accepted event", entries)
	}
	if _, exists := entries[0]["req_id"]; exists {
		t.Fatalf("completion entry contains redundant req_id field: %#v", entries[0])
	}
	if _, exists := entries[0]["plane"]; exists {
		t.Fatalf("completion entry contains redundant plane field: %#v", entries[0])
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestServiceEmitSkipsCompletionForEmptyIDAndInactiveLifecycle(t *testing.T) {
	var output bytes.Buffer
	timers := newManualTimerFactory()
	service := newService(
		batchWriterFunc(func(context.Context, []models.RequestLog) error {
			return nil
		}),
		redact.New(),
		timers.New,
	)
	service.logger = newRequestLogJSONLogger(&output)

	service.Emit(testEvent("before-start"))
	if len(processLogEntries(t, output.Bytes())) != 0 {
		t.Fatalf("new lifecycle output = %s", output.String())
	}
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	service.Emit(testEvent(""))
	if got := service.Stats().EnqueuedTotal; got != 1 {
		t.Fatalf("empty-ID EnqueuedTotal = %d, want 1", got)
	}
	if len(processLogEntries(t, output.Bytes())) != 0 {
		t.Fatalf("empty-ID output = %s", output.String())
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	service.Emit(testEvent("after-stop"))
	if len(processLogEntries(t, output.Bytes())) != 0 {
		t.Fatalf("stopped lifecycle output = %s", output.String())
	}
}

type countingPanicLogHook struct {
	calls atomic.Uint64
}

func (hook *countingPanicLogHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (hook *countingPanicLogHook) Fire(*logrus.Entry) error {
	hook.calls.Add(1)
	panic("request log hook must be isolated")
}

func TestServiceEmitLoggerPanicDoesNotPreventEnqueue(t *testing.T) {
	service := newService(
		batchWriterFunc(func(context.Context, []models.RequestLog) error {
			return nil
		}),
		redact.New(),
		newManualTimerFactory().New,
	)
	hook := &countingPanicLogHook{}
	service.logger = logrus.New()
	service.logger.AddHook(hook)
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	service.Emit(testEvent("panic-hook"))

	if hook.calls.Load() != 1 {
		t.Fatalf("hook calls = %d, want 1", hook.calls.Load())
	}
	if got := service.Stats().EnqueuedTotal; got != 1 {
		t.Fatalf("EnqueuedTotal = %d, want 1", got)
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestServiceEmitCompletionSurvivesLaterWriteFailure(t *testing.T) {
	var output bytes.Buffer
	timers := newManualTimerFactory()
	service := newService(
		batchWriterFunc(func(context.Context, []models.RequestLog) error {
			return errors.New("injected write failure")
		}),
		redact.New(),
		timers.New,
	)
	service.logger = newRequestLogJSONLogger(&output)
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	service.Emit(testEvent("persist-failed"))
	receiveValue(t, timers.created).Fire()
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if got := service.Stats().WriteFailureTotal; got != 1 {
		t.Fatalf("WriteFailureTotal = %d, want 1", got)
	}
	entries := processLogEntries(t, output.Bytes())
	if len(entries) != 1 || entries[0]["msg"] != "[DATA] Request completed" {
		t.Fatalf("completion entries = %#v, want persisted-failed event", entries)
	}
}

func TestServiceEmitLifecycleAndDeepCopy(t *testing.T) {
	timers := newManualTimerFactory()
	writes := make(chan []models.RequestLog, 1)
	service := newService(
		batchWriterFunc(func(_ context.Context, rows []models.RequestLog) error {
			writes <- append([]models.RequestLog(nil), rows...)
			return nil
		}),
		redact.New(),
		timers.New,
	)

	beforeStart := testEvent("before-start")
	service.Emit(beforeStart)
	if got := service.Stats().DroppedNotRunningTotal; got != 1 {
		t.Fatalf("DroppedNotRunningTotal = %d, want 1", got)
	}
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := service.Start(); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("second Start() error = %v, want ErrAlreadyStarted", err)
	}

	event := testEvent("deep-copy")
	service.Emit(event)
	timer := receiveValue(t, timers.created)
	event.RequestID = "mutated-request"
	event.Attempts[0].GroupName = "mutated-group"
	timer.Fire()

	rows := receiveValue(t, writes)
	if len(rows) != 1 || rows[0].ID != "deep-copy" {
		t.Fatalf("written rows = %+v, want original request", rows)
	}
	if len(rows[0].AttemptRows) != 1 || rows[0].AttemptRows[0].GroupName != "primary" {
		t.Fatalf("written attempts = %+v, want original deep copy", rows[0].AttemptRows)
	}

	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	service.Emit(testEvent("after-stop"))
	if got := service.Stats().DroppedStoppingTotal; got != 1 {
		t.Fatalf("DroppedStoppingTotal = %d, want 1", got)
	}
	if err := service.Start(); !errors.Is(err, ErrNotRestartable) {
		t.Fatalf("Start() after Stop error = %v, want ErrNotRestartable", err)
	}

	neverStarted := newService(
		batchWriterFunc(func(context.Context, []models.RequestLog) error { return nil }),
		redact.New(),
		timers.New,
	)
	if err := neverStarted.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() in new state error = %v", err)
	}
	if err := neverStarted.Start(); !errors.Is(err, ErrNotRestartable) {
		t.Fatalf("Start() after new.Stop error = %v, want ErrNotRestartable", err)
	}

	for name, misconfigured := range map[string]*Service{
		"nil database": NewService(
			nil, redact.New(), staticRetentionPolicy{days: 7},
		),
		"nil retention provider": NewService(
			openRequestLogQueryDB(t), redact.New(), nil,
		),
	} {
		t.Run(name, func(t *testing.T) {
			if err := misconfigured.Start(); err == nil {
				t.Fatal("Start() error = nil, want initialization failure")
			}
			if err := misconfigured.Start(); !errors.Is(err, ErrNotRestartable) {
				t.Fatalf("Start() after initialization failure error = %v, want ErrNotRestartable", err)
			}
		})
	}
}

func TestServiceStartsWithoutPricingRuntime(t *testing.T) {
	service := NewService(
		openRequestLogQueryDB(t),
		redact.New(),
		staticRetentionPolicy{days: 7},
	)
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestServiceDropsNewEventAtExactQueueCapacity(t *testing.T) {
	writeStarted := make(chan struct{}, 1)
	releaseWrite := make(chan struct{})
	var signalWriteStarted sync.Once
	var output bytes.Buffer
	service := newService(
		batchWriterFunc(func(ctx context.Context, _ []models.RequestLog) error {
			signalWriteStarted.Do(func() {
				writeStarted <- struct{}{}
			})
			select {
			case <-releaseWrite:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}),
		redact.New(),
		newManualTimerFactory().New,
	)
	service.logger = newRequestLogJSONLogger(&output)
	service.logger.SetLevel(logrus.WarnLevel)
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	for index := 0; index < batchSize; index++ {
		service.Emit(testEvent(fmt.Sprintf("batch-%03d", index)))
	}
	receiveValue(t, writeStarted)
	for index := 0; index < queueCapacity; index++ {
		service.Emit(testEvent(fmt.Sprintf("queued-%04d", index)))
	}
	if got := service.Stats().QueueDepth; got != queueCapacity {
		t.Fatalf("QueueDepth = %d, want exact capacity %d", got, queueCapacity)
	}
	dropped := testEvent("must-drop")
	dropped.Status = telemetry.RequestStatusError
	dropped.StatusCode = 502
	dropped.ErrorCode = "upstream_error"
	service.Emit(dropped)
	stats := service.Stats()
	if stats.DroppedQueueFullTotal != 1 || stats.EnqueuedTotal != batchSize+queueCapacity ||
		stats.DroppedTotal != 1 {
		t.Fatalf("stats at capacity = %+v", stats)
	}
	entries := processLogEntries(t, output.Bytes())
	if len(entries) != 1 || entries[0]["msg"] != "[DATA] Request completed" {
		t.Fatalf("queue-full completion entries = %#v", entries)
	}

	close(releaseWrite)
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestServiceConcurrentEmitStatsAndStop(t *testing.T) {
	immediateTimerFactory := func(time.Duration) workerTimer {
		timer := newManualTimer()
		timer.Fire()
		return timer
	}
	service := newService(
		batchWriterFunc(func(context.Context, []models.RequestLog) error { return nil }),
		redact.New(),
		immediateTimerFactory,
	)
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	const emitters = 8
	const eventsPerEmitter = 1000
	start := make(chan struct{})
	var emitGroup sync.WaitGroup
	for emitter := 0; emitter < emitters; emitter++ {
		emitGroup.Add(1)
		go func(emitter int) {
			defer emitGroup.Done()
			<-start
			for index := 0; index < eventsPerEmitter; index++ {
				service.Emit(testEvent(fmt.Sprintf("%d-%d", emitter, index)))
				_ = service.Stats()
			}
		}(emitter)
	}
	close(start)

	stopResult := make(chan error, 1)
	go func() {
		stopResult <- service.Stop(context.Background())
	}()
	waitGroupDone(t, &emitGroup)
	if err := receiveValue(t, stopResult); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	stats := service.Stats()
	totalEmitted := uint64(emitters * eventsPerEmitter)
	if stats.EnqueuedTotal+stats.DroppedStoppingTotal+stats.DroppedQueueFullTotal != totalEmitted {
		t.Fatalf("accounted emitted events = %d + %d + %d, want %d",
			stats.EnqueuedTotal, stats.DroppedStoppingTotal, stats.DroppedQueueFullTotal, totalEmitted)
	}
	if stats.PersistedTotal != stats.EnqueuedTotal || stats.DroppedPersistFailedTotal != 0 ||
		stats.DroppedShutdownTotal != 0 {
		t.Fatalf("final concurrent stats = %+v", stats)
	}
}

func TestServiceWarningsExcludeEventContentAndThrottle(t *testing.T) {
	var output bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&output)
	logger.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})

	writeStarted := make(chan struct{}, 1)
	releaseWrite := make(chan struct{})
	var signalWriteStarted sync.Once
	service := newService(
		batchWriterFunc(func(ctx context.Context, _ []models.RequestLog) error {
			signalWriteStarted.Do(func() {
				writeStarted <- struct{}{}
			})
			select {
			case <-releaseWrite:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}),
		redact.New(),
		newManualTimerFactory().New,
	)
	service.logger = logger
	service.now = func() time.Time {
		return time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	}
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	for index := 0; index < batchSize; index++ {
		service.Emit(testEvent(fmt.Sprintf("warning-batch-%03d", index)))
	}
	receiveValue(t, writeStarted)
	for index := 0; index < queueCapacity; index++ {
		service.Emit(testEvent(fmt.Sprintf("warning-queued-%04d", index)))
	}
	sensitive := testEvent("request-id-must-not-appear")
	sensitive.ErrorSummary = "authorization: Bearer sk-sensitive-warning-secret"
	service.Emit(sensitive)
	service.Emit(sensitive)

	var warnings []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n")) {
		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatalf("decode logger output: %v", err)
		}
		if entry["msg"] == "[DATA] Request log event loss" {
			warnings = append(warnings, entry)
		}
	}
	if len(warnings) != 1 {
		t.Fatalf("warning entries = %#v, want one throttled warning", warnings)
	}
	if _, exists := warnings[0]["plane"]; exists {
		t.Fatalf("warning entry contains redundant plane field: %#v", warnings[0])
	}
	encodedWarnings, err := json.Marshal(warnings)
	if err != nil {
		t.Fatalf("encode warning entries: %v", err)
	}
	for _, forbidden := range []string{
		sensitive.RequestID,
		sensitive.ErrorSummary,
		"sk-sensitive-warning-secret",
	} {
		if bytes.Contains(encodedWarnings, []byte(forbidden)) {
			t.Fatalf(
				"warning output contains event content %q: %s",
				forbidden,
				encodedWarnings,
			)
		}
	}

	close(releaseWrite)
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestEmitPersistsGatewayFrozenQuoteWithoutRepricing(t *testing.T) {
	timers := newManualTimerFactory()
	writes := make(chan []models.RequestLog, 1)
	var output bytes.Buffer
	service := newService(
		batchWriterFunc(func(_ context.Context, rows []models.RequestLog) error {
			writes <- append([]models.RequestLog(nil), rows...)
			return nil
		}),
		redact.New(),
		timers.New,
	)
	service.logger = newRequestLogJSONLogger(&output)
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	eventA := testEvent("snapshot-a")
	eventA.UpstreamModel = "snapshot-model"
	eventA.Attempts[0].UpstreamModel = "snapshot-model"
	eventA.Usage.Result = usage.Result{
		State:  usage.StateComplete,
		Tokens: usage.Tokens{Output: 1_000_000},
	}
	eventA.Usage.AttemptSequence = 1
	eventA.Usage.KeyID = 8
	eventA.Usage.Pricing = telemetry.PricingObservation{
		UpstreamModel: "snapshot-model",
		CostState:     string(pricing.CostStatePriced), PricingCompleteness: string(pricing.CompletenessComplete),
		EstimatedCostNanoUSD: 1_000_000_000,
	}
	service.Emit(eventA)
	timer := receiveValue(t, timers.created)
	entries := processLogEntries(t, output.Bytes())
	if len(entries) != 1 ||
		entries[0]["cost_usd"] != "1" {
		t.Fatalf("snapshot-a completion entries = %#v", entries)
	}

	eventB := testEvent("snapshot-b")
	eventB.UpstreamModel = "snapshot-model"
	eventB.Attempts[0].UpstreamModel = "snapshot-model"
	eventB.Usage.Result = usage.Result{
		State:  usage.StateComplete,
		Tokens: usage.Tokens{Output: 1_000_000},
	}
	eventB.Usage.AttemptSequence = 1
	eventB.Usage.KeyID = 8
	eventB.Usage.Pricing = telemetry.PricingObservation{
		UpstreamModel: "snapshot-model",
		CostState:     string(pricing.CostStatePriced), PricingCompleteness: string(pricing.CompletenessComplete),
		EstimatedCostNanoUSD: 2_000_000_000,
	}
	service.Emit(eventB)
	timer.Fire()

	rows := receiveValue(t, writes)
	if len(rows) != 2 || rows[0].ID != "snapshot-a" ||
		rows[0].EstimatedCostNanoUSD != 1_000_000_000 ||
		rows[1].ID != "snapshot-b" ||
		rows[1].EstimatedCostNanoUSD != 2_000_000_000 {
		t.Fatalf("snapshot-priced rows = %+v, want A/1000000000 then B/2000000000", rows)
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}
