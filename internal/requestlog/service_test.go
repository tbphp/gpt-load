package requestlog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/usage"
)

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
		newStaticPriceTableProvider(),
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
	var attempts []Attempt
	if err := json.Unmarshal(rows[0].Attempts, &attempts); err != nil {
		t.Fatalf("unmarshal attempts: %v", err)
	}
	if len(attempts) != 1 || attempts[0].GroupName != "primary" {
		t.Fatalf("written attempts = %+v, want original deep copy", attempts)
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
		newStaticPriceTableProvider(),
	)
	if err := neverStarted.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() in new state error = %v", err)
	}
	if err := neverStarted.Start(); !errors.Is(err, ErrNotRestartable) {
		t.Fatalf("Start() after new.Stop error = %v, want ErrNotRestartable", err)
	}

	for name, misconfigured := range map[string]*Service{
		"nil database": NewService(
			nil, redact.New(), staticRetentionPolicy{days: 7}, newStaticPriceTableProvider(),
		),
		"nil retention provider": NewService(
			openRequestLogQueryDB(t), redact.New(), nil, newStaticPriceTableProvider(),
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

func TestServiceStartRequiresPublishedPriceTable(t *testing.T) {
	db := openRequestLogQueryDB(t)
	tests := []struct {
		name     string
		provider PriceTableProvider
		want     string
	}{
		{
			name: "nil provider",
			want: "start request log service: price table provider is nil",
		},
		{
			name:     "unpublished provider",
			provider: &staticPriceTableProvider{},
			want:     "start request log service: price table is not published",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewService(
				db,
				redact.New(),
				staticRetentionPolicy{days: 7},
				tt.provider,
			)
			if err := service.Start(); err == nil || err.Error() != tt.want {
				t.Fatalf("Start() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestServiceDropsNewEventAtExactQueueCapacity(t *testing.T) {
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
		newStaticPriceTableProvider(),
	)
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
	service.Emit(testEvent("must-drop"))
	stats := service.Stats()
	if stats.DroppedQueueFullTotal != 1 || stats.EnqueuedTotal != batchSize+queueCapacity ||
		stats.DroppedTotal != 1 {
		t.Fatalf("stats at capacity = %+v", stats)
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
		newStaticPriceTableProvider(),
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
		newStaticPriceTableProvider(),
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
	sensitive.Attempts[0].KeyMask = "sensitive-key-mask"
	service.Emit(sensitive)
	service.Emit(sensitive)

	logged := output.String()
	if got := bytes.Count(output.Bytes(), []byte("\n")); got != 1 {
		t.Fatalf("warning count = %d, want throttled count 1; output=%s", got, logged)
	}
	for _, forbidden := range []string{
		sensitive.RequestID,
		sensitive.ErrorSummary,
		sensitive.Attempts[0].KeyMask,
		"sk-sensitive-warning-secret",
	} {
		if bytes.Contains(output.Bytes(), []byte(forbidden)) {
			t.Fatalf("warning output contains event content %q: %s", forbidden, logged)
		}
	}

	close(releaseWrite)
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestEmitFreezesPriceTableBeforeWorkerFlush(t *testing.T) {
	tableA := compileRequestLogTestPriceTable(t, "snapshot-model", pricing.Prices{
		Output: pricing.Price{Value: 1, Set: true},
	})
	tableB := compileRequestLogTestPriceTable(t, "snapshot-model", pricing.Prices{
		Output: pricing.Price{Value: 2, Set: true},
	})
	provider := &publishingPriceTableProvider{}
	provider.Publish(tableA)
	timers := newManualTimerFactory()
	writes := make(chan []models.RequestLog, 1)
	service := newService(
		batchWriterFunc(func(_ context.Context, rows []models.RequestLog) error {
			writes <- append([]models.RequestLog(nil), rows...)
			return nil
		}),
		redact.New(),
		timers.New,
		provider,
	)
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	eventA := testEvent("snapshot-a")
	eventA.UpstreamModel = "snapshot-model"
	eventA.Usage.Result = usage.Result{
		State:  usage.StateComplete,
		Tokens: usage.Tokens{Output: 1_000_000},
	}
	service.Emit(eventA)
	timer := receiveValue(t, timers.created)

	provider.Publish(tableB)
	eventB := testEvent("snapshot-b")
	eventB.UpstreamModel = "snapshot-model"
	eventB.Usage.Result = usage.Result{
		State:  usage.StateComplete,
		Tokens: usage.Tokens{Output: 1_000_000},
	}
	service.Emit(eventB)
	timer.Fire()

	rows := receiveValue(t, writes)
	if len(rows) != 2 || rows[0].ID != "snapshot-a" || rows[0].Cost != 1 ||
		rows[1].ID != "snapshot-b" || rows[1].Cost != 2 {
		t.Fatalf("snapshot-priced rows = %+v, want A/1 then B/2", rows)
	}
	if got := provider.loads.Load(); got != 3 {
		t.Fatalf("PriceTableProvider.Load calls = %d, want Start once plus one per Emit", got)
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}
