package requestlog

import (
	"context"
	"errors"
	"sync"
	"testing"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/storage/models"
)

type fakePassiveQuotaFlusher struct {
	mu        sync.Mutex
	calls     int
	remaining []bool
	errs      []error
	notifier  func()
}

func (f *fakePassiveQuotaFlusher) FlushPassiveQuotaObservations(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	index := f.calls
	f.calls++
	var remaining bool
	if index < len(f.remaining) {
		remaining = f.remaining[index]
	}
	var err error
	if index < len(f.errs) {
		err = f.errs[index]
	}
	return remaining, err
}

func (f *fakePassiveQuotaFlusher) SetPassiveQuotaDirtyNotifier(notifier func()) {
	f.mu.Lock()
	f.notifier = notifier
	f.mu.Unlock()
}

func (f *fakePassiveQuotaFlusher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestSetPassiveQuotaFlusherSharesQuotaWakeAndRegistersNotifier(t *testing.T) {
	service := newService(
		batchWriterFunc(func(context.Context, []models.RequestLog) error { return nil }),
		redact.New(),
		newManualTimerFactory().New,
	)
	if service.quotaWake != nil {
		t.Fatal("quotaWake already exists before any quota source was installed")
	}
	flusher := &fakePassiveQuotaFlusher{}
	service.SetPassiveQuotaFlusher(flusher)
	if service.quotaWake == nil {
		t.Fatal("SetPassiveQuotaFlusher() did not create quotaWake")
	}
	if flusher.notifier == nil {
		t.Fatal("SetPassiveQuotaFlusher() did not register a dirty notifier")
	}
	flusher.notifier()
	select {
	case <-service.quotaWake:
	default:
		t.Fatal("dirty notifier did not wake quotaWake")
	}
}

func TestWriteBatchFlushesPassiveQuotaWithoutAffectingItsOwnErrorOrCounters(t *testing.T) {
	service := newService(
		batchWriterFunc(func(context.Context, []models.RequestLog) error { return nil }),
		redact.New(),
		newManualTimerFactory().New,
	)
	flusher := &fakePassiveQuotaFlusher{errs: []error{errors.New("boom")}}
	service.SetPassiveQuotaFlusher(flusher)

	if err := service.writeBatch(t.Context(), nil); err != nil {
		t.Fatalf("writeBatch() error = %v, want nil despite a passive quota failure", err)
	}
	if flusher.callCount() != 1 {
		t.Fatalf("passive quota flush calls = %d, want 1", flusher.callCount())
	}
	stats := service.Stats()
	if stats.WriteFailureTotal != 0 || stats.DroppedPersistFailedTotal != 0 {
		t.Fatalf("RequestLog failure counters changed from a passive quota failure: %#v", stats)
	}
}

func TestWriteBatchRewakesWhenPassiveQuotaHasRemainingPending(t *testing.T) {
	service := newService(
		batchWriterFunc(func(context.Context, []models.RequestLog) error { return nil }),
		redact.New(),
		newManualTimerFactory().New,
	)
	flusher := &fakePassiveQuotaFlusher{remaining: []bool{true}}
	service.SetPassiveQuotaFlusher(flusher)

	if err := service.writeBatch(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-service.quotaWake:
	default:
		t.Fatal("writeBatch() did not re-wake for remaining passive quota pending")
	}
}

func TestRequestLogWorkerStopSucceedsDespitePassiveQuotaDrainFailure(t *testing.T) {
	timers := newManualTimerFactory()
	service := newService(
		batchWriterFunc(func(context.Context, []models.RequestLog) error { return nil }),
		redact.New(),
		timers.New,
	)
	flusher := &fakePassiveQuotaFlusher{remaining: []bool{true}, errs: []error{nil, errors.New("boom")}}
	service.SetPassiveQuotaFlusher(flusher)
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}

	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v, want nil despite a passive quota drain failure", err)
	}
}
