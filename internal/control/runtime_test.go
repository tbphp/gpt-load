package control

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
)

type fakeRuntimeTicker struct {
	ticks    chan time.Time
	stopped  chan struct{}
	stopOnce sync.Once
}

func newFakeRuntimeTicker() *fakeRuntimeTicker {
	return &fakeRuntimeTicker{
		ticks:   make(chan time.Time, 8),
		stopped: make(chan struct{}),
	}
}

func (ticker *fakeRuntimeTicker) C() <-chan time.Time {
	return ticker.ticks
}

func (ticker *fakeRuntimeTicker) Stop() {
	ticker.stopOnce.Do(func() { close(ticker.stopped) })
}

type fakeValidationSweep struct {
	started  chan struct{}
	returned chan struct{}
	block    bool
	once     sync.Once
}

func newFakeValidationSweep(block bool) *fakeValidationSweep {
	return &fakeValidationSweep{
		started:  make(chan struct{}),
		returned: make(chan struct{}),
		block:    block,
	}
}

func (sweep *fakeValidationSweep) Validate(ctx context.Context) {
	sweep.once.Do(func() { close(sweep.started) })
	if sweep.block {
		<-ctx.Done()
	}
	close(sweep.returned)
}

type fakeRuntimeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *fakeRuntimeClock) set(now time.Time) {
	clock.mu.Lock()
	clock.now = now
	clock.mu.Unlock()
}

func (clock *fakeRuntimeClock) current() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

type controlledRequestLogCleaner struct {
	calls        chan time.Time
	release      chan struct{}
	returned     chan struct{}
	active       atomic.Int64
	maxActive    atomic.Int64
	ignoreCancel bool
}

func newControlledRequestLogCleaner(ignoreCancel bool) *controlledRequestLogCleaner {
	return &controlledRequestLogCleaner{
		calls:        make(chan time.Time, 8),
		release:      make(chan struct{}, 8),
		returned:     make(chan struct{}, 8),
		ignoreCancel: ignoreCancel,
	}
}

func (cleaner *controlledRequestLogCleaner) Sweep(ctx context.Context, now time.Time) {
	active := cleaner.active.Add(1)
	for {
		maxActive := cleaner.maxActive.Load()
		if active <= maxActive || cleaner.maxActive.CompareAndSwap(maxActive, active) {
			break
		}
	}
	cleaner.calls <- now
	if cleaner.ignoreCancel {
		<-cleaner.release
	} else {
		select {
		case <-cleaner.release:
		case <-ctx.Done():
		}
	}
	cleaner.active.Add(-1)
	cleaner.returned <- struct{}{}
}

type controlledOperationRecovery struct {
	started  chan struct{}
	returned chan struct{}
}

type controlledStageCleaner struct {
	calls chan time.Time
}

func (cleaner *controlledStageCleaner) CleanupCredentialStages(_ context.Context, now time.Time) error {
	cleaner.calls <- now
	return nil
}

func (recovery *controlledOperationRecovery) RunOperationRecovery(ctx context.Context) {
	close(recovery.started)
	<-ctx.Done()
	close(recovery.returned)
}

func TestRuntimeRunsOperationRecoveryUntilCancellation(t *testing.T) {
	t.Parallel()
	recovery := &controlledOperationRecovery{
		started:  make(chan struct{}),
		returned: make(chan struct{}),
	}
	runtime, _, created := newRuntimeHarness(
		newFakeValidationSweep(false),
		time.Now,
	)
	runtime.operationRecovery = recovery

	cancel, done := startRuntime(t, runtime)
	awaitTickers(t, created)
	awaitSignal(t, recovery.started)
	cancel()
	awaitSignal(t, recovery.returned)
	awaitSignal(t, done)
}

func TestRuntimeSweepsRequestLogsImmediatelyAndHourlyWithoutOverlap(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	clock := &fakeRuntimeClock{now: base}
	cleaner := newControlledRequestLogCleaner(false)
	validationTicker := newFakeRuntimeTicker()
	retentionTicker := newFakeRuntimeTicker()
	created := make(chan time.Duration, 3)
	runtime := newTestRuntime(
		newFakeValidationSweep(false),
		validationTicker,
		created,
		clock.current,
	)
	runtime.requestLogCleaner = cleaner
	runtime.newTicker = func(interval time.Duration) runtimeTicker {
		created <- interval
		switch interval {
		case 32 * time.Minute:
			return validationTicker
		case time.Hour:
			return retentionTicker
		default:
			testingPanic("unexpected ticker interval", interval)
			return nil
		}
	}

	cancel, done := startRuntime(t, runtime)
	if interval := awaitValue(t, created); interval != 32*time.Minute {
		t.Fatalf("validation ticker interval = %v, want 32m", interval)
	}
	if interval := awaitValue(t, created); interval != time.Hour {
		t.Fatalf("retention ticker interval = %v, want 1h", interval)
	}
	if got := awaitValue(t, cleaner.calls); !got.Equal(base) {
		t.Fatalf("immediate Sweep time = %v, want %v", got, base)
	}

	clock.set(base.Add(time.Hour))
	retentionTicker.ticks <- base.Add(99 * time.Hour)
	select {
	case got := <-cleaner.calls:
		t.Fatalf("overlapping Sweep started at %v before first returned", got)
	case <-time.After(25 * time.Millisecond):
	}
	cleaner.release <- struct{}{}
	awaitSignal(t, cleaner.returned)
	if got := awaitValue(t, cleaner.calls); !got.Equal(base.Add(time.Hour)) {
		t.Fatalf("hourly Sweep time = %v, want injected clock %v", got, base.Add(time.Hour))
	}
	cleaner.release <- struct{}{}
	awaitSignal(t, cleaner.returned)

	stopRuntime(t, cancel, done)
	awaitSignal(t, validationTicker.stopped)
	awaitSignal(t, retentionTicker.stopped)
	if got := cleaner.maxActive.Load(); got != 1 {
		t.Fatalf("maximum concurrent Sweeps = %d, want 1", got)
	}
}

func TestRuntimeSweepsCredentialStagesWithoutRequestLogCleaner(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, time.August, 13, 8, 0, 0, 0, time.UTC)
	validationTicker := newFakeRuntimeTicker()
	retentionTicker := newFakeRuntimeTicker()
	created := make(chan time.Duration, 3)
	runtime := newTestRuntime(newFakeValidationSweep(false), validationTicker, created, func() time.Time { return base })
	cleaner := &controlledStageCleaner{calls: make(chan time.Time, 2)}
	runtime.stageCleaner = cleaner
	runtime.newTicker = func(interval time.Duration) runtimeTicker {
		created <- interval
		switch interval {
		case 32 * time.Minute:
			return validationTicker
		case time.Hour:
			return retentionTicker
		default:
			testingPanic("unexpected ticker interval", interval)
			return nil
		}
	}
	cancel, done := startRuntime(t, runtime)
	awaitTickers(t, created)
	if interval := awaitValue(t, created); interval != time.Hour {
		t.Fatalf("retention interval = %v", interval)
	}
	if got := awaitValue(t, cleaner.calls); !got.Equal(base) {
		t.Fatalf("cleanup time = %v", got)
	}
	stopRuntime(t, cancel, done)
	awaitSignal(t, retentionTicker.stopped)
}

func TestRuntimeCancellationWaitsForRetentionSweep(t *testing.T) {
	t.Parallel()
	cleaner := newControlledRequestLogCleaner(true)
	validationTicker := newFakeRuntimeTicker()
	retentionTicker := newFakeRuntimeTicker()
	created := make(chan time.Duration, 3)
	runtime := newTestRuntime(
		newFakeValidationSweep(false),
		validationTicker,
		created,
		time.Now,
	)
	runtime.requestLogCleaner = cleaner
	runtime.newTicker = func(interval time.Duration) runtimeTicker {
		created <- interval
		switch interval {
		case 32 * time.Minute:
			return validationTicker
		case time.Hour:
			return retentionTicker
		default:
			testingPanic("unexpected ticker interval", interval)
			return nil
		}
	}

	cancel, done := startRuntime(t, runtime)
	awaitTickers(t, created)
	if interval := awaitValue(t, created); interval != time.Hour {
		t.Fatalf("retention ticker interval = %v, want 1h", interval)
	}
	_ = awaitValue(t, cleaner.calls)
	cancel()
	select {
	case <-done:
		t.Fatal("Runtime.Run returned before active retention Sweep completed")
	case <-time.After(25 * time.Millisecond):
	}
	cleaner.release <- struct{}{}
	awaitSignal(t, cleaner.returned)
	awaitSignal(t, done)
	awaitSignal(t, retentionTicker.stopped)
}

func TestRuntimeCreatesOnlyJitteredValidationTicker(t *testing.T) {
	t.Parallel()
	validationTicker := newFakeRuntimeTicker()
	created := make(chan time.Duration, 2)
	runtime := newTestRuntime(newFakeValidationSweep(false), validationTicker, created, time.Now)
	runtime.validationJitter = func() time.Duration { return 2 * time.Minute }

	cancel, done := startRuntime(t, runtime)
	if interval := awaitValue(t, created); interval != 32*time.Minute {
		t.Fatalf("validation ticker interval = %v, want 32m", interval)
	}
	stopRuntime(t, cancel, done)
	awaitSignal(t, validationTicker.stopped)
}

func TestRuntimeValidationJitterDoesNotOverflow(t *testing.T) {
	t.Parallel()
	validationTicker := newFakeRuntimeTicker()
	created := make(chan time.Duration, 2)
	runtime := newTestRuntime(
		newFakeValidationSweep(false),
		validationTicker,
		created,
		time.Now,
	)
	runtime.validationInterval = time.Duration(1<<63 - 1)
	runtime.validationJitter = func() time.Duration { return maxValidationJitter }
	runtime.newTicker = func(interval time.Duration) runtimeTicker {
		created <- interval
		return validationTicker
	}

	cancel, done := startRuntime(t, runtime)
	if interval := awaitValue(t, created); interval != time.Duration(1<<63-1) {
		t.Fatalf("validation ticker interval = %v, want capped maximum duration", interval)
	}
	stopRuntime(t, cancel, done)
	awaitSignal(t, validationTicker.stopped)
}

func TestRuntimeReschedulesValidationWhenPublishedIntervalChanges(t *testing.T) {
	t.Parallel()
	manager := state.NewManager()
	if _, err := manager.Publish(state.CompileInput{}); err != nil {
		t.Fatal(err)
	}
	defaultTicker := newFakeRuntimeTicker()
	overriddenTicker := newFakeRuntimeTicker()
	created := make(chan time.Duration, 3)
	runtime := newTestRuntime(
		newFakeValidationSweep(false),
		defaultTicker,
		created,
		time.Now,
	)
	runtime.manager = manager
	runtime.newTicker = func(interval time.Duration) runtimeTicker {
		created <- interval
		switch interval {
		case 12 * time.Minute:
			return defaultTicker
		case 22 * time.Minute:
			return overriddenTicker
		default:
			testingPanic("unexpected ticker interval", interval)
			return nil
		}
	}

	cancel, done := startRuntime(t, runtime)
	if interval := awaitValue(t, created); interval != 12*time.Minute {
		t.Fatalf("default validation ticker interval = %v, want 12m", interval)
	}
	if _, err := manager.Publish(state.CompileInput{SystemSettings: config.Settings{
		state.SettingValidationInterval: json.Number("1200"),
	}}); err != nil {
		t.Fatal(err)
	}
	if interval := awaitValue(t, created); interval != 22*time.Minute {
		t.Fatalf("updated validation ticker interval = %v, want 22m", interval)
	}
	awaitSignal(t, defaultTicker.stopped)

	stopRuntime(t, cancel, done)
	awaitSignal(t, overriddenTicker.stopped)
}

func TestRuntimeWaitsForValidationTick(t *testing.T) {
	t.Parallel()
	validator := newFakeValidationSweep(false)
	runtime, validationTicker, created := newRuntimeHarness(validator, time.Now)

	cancel, done := startRuntime(t, runtime)
	awaitTickers(t, created)
	select {
	case <-validator.started:
		t.Fatal("validation ran before its first tick")
	default:
	}
	validationTicker.ticks <- time.Now()
	awaitSignal(t, validator.started)
	awaitSignal(t, validator.returned)
	stopRuntime(t, cancel, done)
}

func TestRuntimeCancellationStopsTickerAndWaitsForValidation(t *testing.T) {
	t.Parallel()
	validator := newFakeValidationSweep(true)
	runtime, validationTicker, created := newRuntimeHarness(validator, time.Now)

	cancel, done := startRuntime(t, runtime)
	awaitTickers(t, created)
	validationTicker.ticks <- time.Now()
	awaitSignal(t, validator.started)
	cancel()
	awaitSignal(t, validator.returned)
	awaitSignal(t, done)
	awaitSignal(t, validationTicker.stopped)
}

func TestRuntimeStopsOnContextCancellation(t *testing.T) {
	t.Parallel()
	runtime, validationTicker, created := newRuntimeHarness(newFakeValidationSweep(false), time.Now)

	cancel, done := startRuntime(t, runtime)
	awaitTickers(t, created)
	stopRuntime(t, cancel, done)
	awaitSignal(t, validationTicker.stopped)

}

func testingPanic(message string, value time.Duration) {
	panic(message + ": " + value.String())
}

func awaitTickers(t *testing.T, created <-chan time.Duration) {
	t.Helper()
	if interval := awaitValue(t, created); interval != 32*time.Minute {
		t.Fatalf("validation ticker interval = %v, want 32m", interval)
	}
}

func startRuntime(t *testing.T, runtime *Runtime) (context.CancelFunc, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Errorf("Runtime.Run did not return during cleanup")
		}
	})
	return cancel, done
}

func stopRuntime(t *testing.T, cancel context.CancelFunc, done <-chan struct{}) {
	t.Helper()
	cancel()
	awaitSignal(t, done)
}

func awaitValue[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for channel value")
		var zero T
		return zero
	}
}

func awaitSignal(t *testing.T, channel <-chan struct{}) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for signal")
	}
}

func newRuntimeHarness(validator validationSweep, now func() time.Time) (*Runtime, *fakeRuntimeTicker, <-chan time.Duration) {
	validationTicker := newFakeRuntimeTicker()
	created := make(chan time.Duration, 2)
	return newTestRuntime(validator, validationTicker, created, now), validationTicker, created
}

func newTestRuntime(validator validationSweep, validationTicker *fakeRuntimeTicker, created chan<- time.Duration, now func() time.Time) *Runtime {
	return &Runtime{
		validator: validator, validationInterval: 30 * time.Minute,
		validationJitter: func() time.Duration { return 2 * time.Minute }, now: now,
		newTicker: func(interval time.Duration) runtimeTicker {
			created <- interval
			if interval != 32*time.Minute {
				testingPanic("unexpected ticker interval", interval)
			}
			return validationTicker
		},
	}
}
