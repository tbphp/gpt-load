package requestlog

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/storage/models"
)

func TestServiceFlushesAtBatchSizeAndDelayInFIFOOrder(t *testing.T) {
	timers := newManualTimerFactory()
	writes := make(chan []models.RequestLog, 2)
	service := newService(
		batchWriterFunc(func(_ context.Context, rows []models.RequestLog) error {
			writes <- append([]models.RequestLog(nil), rows...)
			return nil
		}),
		redact.New(),
		timers.New,
	)
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	for index := 0; index < batchSize; index++ {
		service.Emit(testEvent(fmt.Sprintf("batch-%03d", index)))
	}
	firstTimer := receiveValue(t, timers.created)
	firstRows := receiveValue(t, writes)
	if len(firstRows) != batchSize {
		t.Fatalf("batch-size flush rows = %d, want %d", len(firstRows), batchSize)
	}
	for index, row := range firstRows {
		wantID := fmt.Sprintf("batch-%03d", index)
		if row.ID != wantID {
			t.Fatalf("batch row %d ID = %q, want %q", index, row.ID, wantID)
		}
	}
	if firstTimer.Stop() {
		t.Fatal("batch-size flush did not stop its one-shot timer")
	}

	service.Emit(testEvent("delay-1"))
	service.Emit(testEvent("delay-2"))
	secondTimer := receiveValue(t, timers.created)
	secondTimer.Fire()
	secondRows := receiveValue(t, writes)
	if len(secondRows) != 2 || secondRows[0].ID != "delay-1" || secondRows[1].ID != "delay-2" {
		t.Fatalf("delay flush rows = %+v, want FIFO delay-1/delay-2", secondRows)
	}

	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestServiceDropsFailedBatchAndContinues(t *testing.T) {
	writeFailure := errors.New("write failed")
	timers := newManualTimerFactory()
	writes := make(chan []models.RequestLog, 2)
	var callMu sync.Mutex
	call := 0
	service := newService(
		batchWriterFunc(func(_ context.Context, rows []models.RequestLog) error {
			copied := append([]models.RequestLog(nil), rows...)
			writes <- copied
			callMu.Lock()
			defer callMu.Unlock()
			call++
			if call == 1 {
				return writeFailure
			}
			return nil
		}),
		redact.New(),
		timers.New,
	)
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	service.Emit(testEvent("failed"))
	receiveValue(t, timers.created).Fire()
	receiveValue(t, writes)
	service.Emit(testEvent("continued"))
	receiveValue(t, timers.created).Fire()
	secondRows := receiveValue(t, writes)
	if len(secondRows) != 1 || secondRows[0].ID != "continued" {
		t.Fatalf("second write rows = %+v", secondRows)
	}

	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	stats := service.Stats()
	if stats.WriteFailureTotal != 1 || stats.DroppedPersistFailedTotal != 1 ||
		stats.PersistedTotal != 1 || stats.DroppedTotal != 1 ||
		stats.LastWriteFailureAt.IsZero() {
		t.Fatalf("stats after failed batch and continuation = %+v", stats)
	}
}

func TestServiceStopDrainsAndIsIdempotent(t *testing.T) {
	writes := make(chan []models.RequestLog, 2)
	service := newService(
		batchWriterFunc(func(_ context.Context, rows []models.RequestLog) error {
			writes <- append([]models.RequestLog(nil), rows...)
			return nil
		}),
		redact.New(),
		newManualTimerFactory().New,
	)
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	for _, id := range []string{"drain-1", "drain-2", "drain-3"} {
		service.Emit(testEvent(id))
	}

	startStops := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-startStops
			results <- service.Stop(context.Background())
		}()
	}
	close(startStops)
	waitGroupDone(t, &group)
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent Stop() error = %v", err)
		}
	}

	rows := receiveValue(t, writes)
	if len(rows) != 3 {
		t.Fatalf("drained rows = %d, want 3", len(rows))
	}
	select {
	case duplicate := <-writes:
		t.Fatalf("Stop flushed duplicate batch: %+v", duplicate)
	default:
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("idempotent Stop() error = %v", err)
	}
	if stats := service.Stats(); stats.PersistedTotal != 3 || stats.QueueDepth != 0 {
		t.Fatalf("stats after drain = %+v", stats)
	}
}

func TestServiceStopDeadlineSeparatesPersistAndShutdownDrops(t *testing.T) {
	writeStarted := make(chan struct{}, 1)
	service := newService(
		batchWriterFunc(func(ctx context.Context, _ []models.RequestLog) error {
			writeStarted <- struct{}{}
			<-ctx.Done()
			return ctx.Err()
		}),
		redact.New(),
		newManualTimerFactory().New,
	)
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	for index := 0; index < batchSize; index++ {
		service.Emit(testEvent(fmt.Sprintf("attempted-%03d", index)))
	}
	receiveValue(t, writeStarted)
	for index := 0; index < 3; index++ {
		service.Emit(testEvent(fmt.Sprintf("shutdown-drop-%d", index)))
	}

	stopContext, cancel := context.WithCancel(context.Background())
	cancel()
	err := service.Stop(stopContext)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop() error = %v, want wrapped context.Canceled", err)
	}
	if err := service.Stop(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop() after worker cancellation error = %v, want stable context.Canceled", err)
	}
	stats := service.Stats()
	if stats.WriteFailureTotal != 1 || stats.DroppedPersistFailedTotal != batchSize ||
		stats.DroppedShutdownTotal != 3 || stats.DroppedTotal != batchSize+3 ||
		stats.PersistedTotal != 0 || stats.QueueDepth != 0 {
		t.Fatalf("deadline stats = %+v", stats)
	}
}

func TestServiceStopDeadlineIsHardBoundaryWhenWriterIgnoresCancellation(t *testing.T) {
	writeStarted := make(chan struct{}, 1)
	releaseWrite := make(chan struct{})
	service := newService(
		batchWriterFunc(func(context.Context, []models.RequestLog) error {
			writeStarted <- struct{}{}
			<-releaseWrite
			return nil
		}),
		redact.New(),
		newManualTimerFactory().New,
	)
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	for index := 0; index < batchSize; index++ {
		service.Emit(testEvent(fmt.Sprintf("hard-deadline-%03d", index)))
	}
	receiveValue(t, writeStarted)

	stopContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	stopResult := make(chan error, 1)
	go func() {
		stopResult <- service.Stop(stopContext)
	}()

	var stopErr error
	select {
	case stopErr = <-stopResult:
	case <-time.After(250 * time.Millisecond):
		close(releaseWrite)
		receiveValue(t, stopResult)
		t.Fatal("Stop() waited for a writer that ignored cancellation")
	}
	if !errors.Is(stopErr, context.DeadlineExceeded) {
		close(releaseWrite)
		t.Fatalf("Stop() error = %v, want wrapped context.DeadlineExceeded", stopErr)
	}

	close(releaseWrite)
	if err := service.Stop(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() after worker completion error = %v, want stable deadline error", err)
	}
	if stats := service.Stats(); stats.PersistedTotal != batchSize ||
		stats.DroppedPersistFailedTotal != 0 || stats.DroppedShutdownTotal != 0 {
		t.Fatalf("stats after delayed successful write = %+v", stats)
	}
}

func TestServiceConcurrentStopHonorsOwnDeadlineWithoutDuplicateDrain(t *testing.T) {
	writeStarted := make(chan struct{}, 1)
	releaseWrite := make(chan struct{})
	var writeCalls atomic.Uint64
	service := newService(
		batchWriterFunc(func(context.Context, []models.RequestLog) error {
			writeCalls.Add(1)
			writeStarted <- struct{}{}
			<-releaseWrite
			return nil
		}),
		redact.New(),
		newManualTimerFactory().New,
	)
	if err := service.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	service.Emit(testEvent("single-drain"))

	backgroundStop := make(chan error, 1)
	go func() {
		backgroundStop <- service.Stop(context.Background())
	}()
	receiveValue(t, writeStarted)

	shortContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	shortStop := make(chan error, 1)
	go func() {
		shortStop <- service.Stop(shortContext)
	}()

	var shortErr error
	select {
	case shortErr = <-shortStop:
	case <-time.After(250 * time.Millisecond):
		close(releaseWrite)
		receiveValue(t, shortStop)
		receiveValue(t, backgroundStop)
		t.Fatal("second Stop() ignored its own deadline while a shared drain was active")
	}
	if !errors.Is(shortErr, context.DeadlineExceeded) {
		close(releaseWrite)
		t.Fatalf("second Stop() error = %v, want wrapped context.DeadlineExceeded", shortErr)
	}

	close(releaseWrite)
	if err := receiveValue(t, backgroundStop); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("background Stop() error = %v, want shared deadline error", err)
	}
	if got := writeCalls.Load(); got != 1 {
		t.Fatalf("WriteBatch calls = %d, want one shared drain/flush", got)
	}
	if err := service.Stop(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("completed Stop() error = %v, want stable deadline error", err)
	}
	if stats := service.Stats(); stats.PersistedTotal != 1 || stats.DroppedTotal != 0 {
		t.Fatalf("stats after shared drain = %+v", stats)
	}
}
