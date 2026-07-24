package control

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gpt-load/internal/dialect"
	"gpt-load/internal/health"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

type autoWeightWrite struct {
	keyID  uint
	weight int
}

type fakeAutoWeightRegistry struct {
	mu        sync.Mutex
	activeIDs []uint
	reject    map[uint]bool
	writes    []autoWeightWrite
	wrote     chan autoWeightWrite
}

func newFakeAutoWeightRegistry(activeIDs ...uint) *fakeAutoWeightRegistry {
	return &fakeAutoWeightRegistry{
		activeIDs: append([]uint(nil), activeIDs...),
		reject:    make(map[uint]bool),
		wrote:     make(chan autoWeightWrite, 32),
	}
}

func (registry *fakeAutoWeightRegistry) ActiveKeyIDs() []uint {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return append([]uint(nil), registry.activeIDs...)
}

func (registry *fakeAutoWeightRegistry) SetAutoWeight(keyID uint, weight int) bool {
	write := autoWeightWrite{keyID: keyID, weight: weight}
	registry.mu.Lock()
	registry.writes = append(registry.writes, write)
	rejected := registry.reject[keyID]
	registry.mu.Unlock()
	registry.wrote <- write
	return !rejected
}

func (registry *fakeAutoWeightRegistry) snapshotWrites() []autoWeightWrite {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return append([]autoWeightWrite(nil), registry.writes...)
}

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

func TestRuntimeSweepsRequestLogsImmediatelyAndHourlyWithoutOverlap(t *testing.T) {
	base := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	clock := &fakeRuntimeClock{now: base}
	cleaner := newControlledRequestLogCleaner(false)
	autoTicker := newFakeRuntimeTicker()
	validationTicker := newFakeRuntimeTicker()
	retentionTicker := newFakeRuntimeTicker()
	created := make(chan time.Duration, 3)
	runtime := newTestRuntime(
		newFakeAutoWeightRegistry(1),
		health.NewStatsStore(),
		newFakeValidationSweep(false),
		autoTicker,
		validationTicker,
		created,
		clock.current,
	)
	runtime.requestLogCleaner = cleaner
	runtime.newTicker = func(interval time.Duration) runtimeTicker {
		created <- interval
		switch interval {
		case 30 * time.Second:
			return autoTicker
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
	if interval := awaitValue(t, created); interval != 30*time.Second {
		t.Fatalf("auto-weight ticker interval = %v, want 30s", interval)
	}
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
	awaitSignal(t, autoTicker.stopped)
	awaitSignal(t, validationTicker.stopped)
	awaitSignal(t, retentionTicker.stopped)
	if got := cleaner.maxActive.Load(); got != 1 {
		t.Fatalf("maximum concurrent Sweeps = %d, want 1", got)
	}
}

func TestRuntimeCancellationWaitsForRetentionSweep(t *testing.T) {
	cleaner := newControlledRequestLogCleaner(true)
	autoTicker := newFakeRuntimeTicker()
	validationTicker := newFakeRuntimeTicker()
	retentionTicker := newFakeRuntimeTicker()
	created := make(chan time.Duration, 3)
	runtime := newTestRuntime(
		newFakeAutoWeightRegistry(1),
		health.NewStatsStore(),
		newFakeValidationSweep(false),
		autoTicker,
		validationTicker,
		created,
		time.Now,
	)
	runtime.requestLogCleaner = cleaner
	runtime.newTicker = func(interval time.Duration) runtimeTicker {
		created <- interval
		switch interval {
		case 30 * time.Second:
			return autoTicker
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

func TestRuntimeCreatesAutoWeightAndJitteredValidationTickers(t *testing.T) {
	registry := newFakeAutoWeightRegistry(1)
	autoTicker := newFakeRuntimeTicker()
	validationTicker := newFakeRuntimeTicker()
	created := make(chan time.Duration, 2)
	runtime := newTestRuntime(registry, health.NewStatsStore(), newFakeValidationSweep(false), autoTicker, validationTicker, created, time.Now)
	runtime.validationJitter = func() time.Duration { return 2 * time.Minute }

	cancel, done := startRuntime(t, runtime)
	if interval := awaitValue(t, created); interval != 30*time.Second {
		t.Fatalf("auto-weight ticker interval = %v, want 30s", interval)
	}
	if interval := awaitValue(t, created); interval != 32*time.Minute {
		t.Fatalf("validation ticker interval = %v, want 32m", interval)
	}
	stopRuntime(t, cancel, done)
	awaitSignal(t, autoTicker.stopped)
	awaitSignal(t, validationTicker.stopped)
}

func TestRuntimeWaitsForTickBeforeRecompute(t *testing.T) {
	registry := newFakeAutoWeightRegistry(1)
	runtime, autoTicker, _, created := newRuntimeHarness(registry, health.NewStatsStore(), newFakeValidationSweep(false), time.Now)

	cancel, done := startRuntime(t, runtime)
	awaitTickers(t, created)
	if got := registry.snapshotWrites(); len(got) != 0 {
		t.Fatalf("writes before first tick = %v, want none", got)
	}
	stopRuntime(t, cancel, done)
	awaitSignal(t, autoTicker.stopped)
}

func TestRuntimeRecomputesEveryActiveKey(t *testing.T) {
	registry := newFakeAutoWeightRegistry(3, 7, 9)
	runtime, autoTicker, _, created := newRuntimeHarness(registry, health.NewStatsStore(), newFakeValidationSweep(false), time.Now)

	cancel, done := startRuntime(t, runtime)
	awaitTickers(t, created)
	for tick := 0; tick < 2; tick++ {
		autoTicker.ticks <- time.Now()
		for _, keyID := range []uint{3, 7, 9} {
			if got, want := awaitValue(t, registry.wrote), (autoWeightWrite{keyID: keyID, weight: state.DefaultWeight}); got != want {
				t.Fatalf("tick %d write = %#v, want %#v", tick+1, got, want)
			}
		}
	}
	stopRuntime(t, cancel, done)

	want := []autoWeightWrite{
		{keyID: 3, weight: 50}, {keyID: 7, weight: 50}, {keyID: 9, weight: 50},
		{keyID: 3, weight: 50}, {keyID: 7, weight: 50}, {keyID: 9, weight: 50},
	}
	if got := registry.snapshotWrites(); !reflect.DeepEqual(got, want) {
		t.Fatalf("writes = %#v, want %#v", got, want)
	}
}

func TestRuntimeResetsExpiredStatsToDefaultWeight(t *testing.T) {
	base := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	stats := health.NewStatsStore()
	for sample := 0; sample < 10; sample++ {
		stats.Record(1, true, base)
	}
	registry := newFakeAutoWeightRegistry(1)
	clock := &fakeRuntimeClock{now: base}
	runtime, autoTicker, _, created := newRuntimeHarness(registry, stats, newFakeValidationSweep(false), clock.current)

	cancel, done := startRuntime(t, runtime)
	awaitTickers(t, created)
	autoTicker.ticks <- base
	if got, want := awaitValue(t, registry.wrote), (autoWeightWrite{keyID: 1, weight: 92}); got != want {
		t.Fatalf("write with populated window = %#v, want %#v", got, want)
	}

	clock.set(base.Add(5 * time.Minute))
	autoTicker.ticks <- base.Add(5 * time.Minute)
	if got, want := awaitValue(t, registry.wrote), (autoWeightWrite{keyID: 1, weight: state.DefaultWeight}); got != want {
		t.Fatalf("write after window expiry = %#v, want %#v", got, want)
	}
	stopRuntime(t, cancel, done)
}

func TestRuntimeContinuesWhenKeyDisappears(t *testing.T) {
	registry := newFakeAutoWeightRegistry(1, 2, 3)
	registry.reject[2] = true
	runtime, autoTicker, _, created := newRuntimeHarness(registry, health.NewStatsStore(), newFakeValidationSweep(false), time.Now)

	cancel, done := startRuntime(t, runtime)
	awaitTickers(t, created)
	autoTicker.ticks <- time.Now()
	for _, keyID := range []uint{1, 2, 3} {
		if got, want := awaitValue(t, registry.wrote), (autoWeightWrite{keyID: keyID, weight: state.DefaultWeight}); got != want {
			t.Fatalf("write = %#v, want %#v", got, want)
		}
	}
	stopRuntime(t, cancel, done)
}

func TestRuntimeUpdatesRegistryWeightSeenByCandidateCollection(t *testing.T) {
	base := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	registry := state.NewKeyRegistry()
	if err := registry.Replace([]state.KeyEntry{{
		ID: 1, GroupID: 10, Status: state.KeyStatusActive, EncryptedValue: "cipher-one",
	}}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	stats := health.NewStatsStore()
	for sample := 0; sample < 10; sample++ {
		stats.Record(1, true, base)
	}

	runtime := &Runtime{registry: registry, stats: stats}
	runtime.recompute(base)
	candidates := registry.CollectCandidates([]uint{10}, nil, base)
	if len(candidates) != 1 || candidates[0].WeightAuto != 92 {
		t.Fatalf("CollectCandidates() = %#v, want one candidate with WeightAuto 92", candidates)
	}
}

func TestRuntimeAutoWeightCannotOverwriteCompletedValidationRecovery(t *testing.T) {
	base := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	stats := health.NewStatsStore()
	for range 10 {
		stats.Record(1, true, base)
	}
	registry := newInterleavingRegistry()
	probePassed := make(chan struct{})
	probes := &validationProbeRecorder{probe: func(context.Context, protocol.Protocol, string, string) error {
		close(probePassed)
		return nil
	}}
	runtime := &Runtime{registry: registry, stats: stats}
	worker := &validationWorker{
		snapshots: &validationSnapshotRecorder{snapshot: validationSnapshot(map[uint]state.GroupView{
			1: validationGroup([]protocol.Protocol{protocol.OpenAI}, "model", nil),
		})},
		registry:    registry,
		stats:       stats,
		decryptor:   validationDecryptor{},
		dialects:    dialect.Set{protocol.OpenAI: &validationTestDialect{protocol: protocol.OpenAI, probes: probes}},
		maintenance: &runtime.maintenance,
	}

	autoDone := make(chan struct{})
	go func() {
		runtime.recompute(base)
		close(autoDone)
	}()
	awaitSignal(t, registry.autoBlocked)

	validationDone := make(chan struct{})
	go func() {
		worker.Validate(context.Background())
		close(validationDone)
	}()
	awaitSignal(t, probePassed)
	close(registry.releaseAuto)
	awaitSignal(t, autoDone)
	awaitSignal(t, validationDone)

	if got := registry.weight(); got != state.DefaultWeight {
		t.Fatalf("final weight = %d, want recovered default %d", got, state.DefaultWeight)
	}
	if got := registry.recoveryCount(); got != 1 {
		t.Fatalf("conditional recoveries = %d, want 1", got)
	}
}

func TestRuntimeWaitsForValidationTick(t *testing.T) {
	registry := newFakeAutoWeightRegistry(1)
	validator := newFakeValidationSweep(false)
	runtime, _, validationTicker, created := newRuntimeHarness(registry, health.NewStatsStore(), validator, time.Now)

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

func TestRuntimeKeepsRecomputingWhileValidationIsBlocked(t *testing.T) {
	registry := newFakeAutoWeightRegistry(1)
	validator := newFakeValidationSweep(true)
	runtime, autoTicker, validationTicker, created := newRuntimeHarness(registry, health.NewStatsStore(), validator, time.Now)

	cancel, done := startRuntime(t, runtime)
	awaitTickers(t, created)
	validationTicker.ticks <- time.Now()
	awaitSignal(t, validator.started)
	autoTicker.ticks <- time.Now()
	if got, want := awaitValue(t, registry.wrote), (autoWeightWrite{keyID: 1, weight: state.DefaultWeight}); got != want {
		t.Fatalf("auto-weight write while validation is blocked = %#v, want %#v", got, want)
	}
	stopRuntime(t, cancel, done)
	awaitSignal(t, validator.returned)
}

func TestRuntimeCancellationStopsBothTickersAndWaitsForValidation(t *testing.T) {
	registry := newFakeAutoWeightRegistry(1)
	validator := newFakeValidationSweep(true)
	runtime, autoTicker, validationTicker, created := newRuntimeHarness(registry, health.NewStatsStore(), validator, time.Now)

	cancel, done := startRuntime(t, runtime)
	awaitTickers(t, created)
	validationTicker.ticks <- time.Now()
	awaitSignal(t, validator.started)
	cancel()
	awaitSignal(t, validator.returned)
	awaitSignal(t, done)
	awaitSignal(t, autoTicker.stopped)
	awaitSignal(t, validationTicker.stopped)
}

func TestRuntimeStopsOnContextCancellation(t *testing.T) {
	registry := newFakeAutoWeightRegistry(1)
	runtime, autoTicker, validationTicker, created := newRuntimeHarness(registry, health.NewStatsStore(), newFakeValidationSweep(false), time.Now)

	cancel, done := startRuntime(t, runtime)
	awaitTickers(t, created)
	stopRuntime(t, cancel, done)
	awaitSignal(t, autoTicker.stopped)
	awaitSignal(t, validationTicker.stopped)

	autoTicker.ticks <- time.Now()
	if got := registry.snapshotWrites(); len(got) != 0 {
		t.Fatalf("writes after cancellation = %v, want none", got)
	}
}

func newRuntimeHarness(
	registry autoWeightRegistry,
	stats *health.StatsStore,
	validator validationSweep,
	now func() time.Time,
) (*Runtime, *fakeRuntimeTicker, *fakeRuntimeTicker, <-chan time.Duration) {
	autoTicker := newFakeRuntimeTicker()
	validationTicker := newFakeRuntimeTicker()
	created := make(chan time.Duration, 2)
	runtime := newTestRuntime(registry, stats, validator, autoTicker, validationTicker, created, now)
	return runtime, autoTicker, validationTicker, created
}

func newTestRuntime(
	registry autoWeightRegistry,
	stats *health.StatsStore,
	validator validationSweep,
	autoTicker *fakeRuntimeTicker,
	validationTicker *fakeRuntimeTicker,
	created chan<- time.Duration,
	now func() time.Time,
) *Runtime {
	return &Runtime{
		registry:           registry,
		stats:              stats,
		validator:          validator,
		autoWeightInterval: 30 * time.Second,
		validationInterval: 30 * time.Minute,
		validationJitter:   func() time.Duration { return 2 * time.Minute },
		now:                now,
		newTicker: func(interval time.Duration) runtimeTicker {
			created <- interval
			switch interval {
			case 30 * time.Second:
				return autoTicker
			case 32 * time.Minute:
				return validationTicker
			default:
				testingPanic("unexpected ticker interval", interval)
				return nil
			}
		},
	}
}

func testingPanic(message string, value time.Duration) {
	panic(message + ": " + value.String())
}

func awaitTickers(t *testing.T, created <-chan time.Duration) {
	t.Helper()
	if interval := awaitValue(t, created); interval != 30*time.Second {
		t.Fatalf("auto-weight ticker interval = %v, want 30s", interval)
	}
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

type interleavingRegistry struct {
	mu            sync.Mutex
	autoBlocked   chan struct{}
	releaseAuto   chan struct{}
	blockAutoOnce sync.Once
	currentWeight int
	recoveries    int
}

func newInterleavingRegistry() *interleavingRegistry {
	return &interleavingRegistry{
		autoBlocked:   make(chan struct{}),
		releaseAuto:   make(chan struct{}),
		currentWeight: 17,
	}
}

func (*interleavingRegistry) ActiveKeyIDs() []uint {
	return []uint{1}
}

func (registry *interleavingRegistry) SetAutoWeight(_ uint, weight int) bool {
	if weight == 92 {
		registry.blockAutoOnce.Do(func() { close(registry.autoBlocked) })
		<-registry.releaseAuto
	}
	registry.mu.Lock()
	registry.currentWeight = weight
	registry.mu.Unlock()
	return true
}

func (*interleavingRegistry) BlacklistedKeys() []state.KeyRef {
	return []state.KeyRef{{ID: 1, GroupID: 1, EncryptedValue: "cipher-one"}}
}

func (registry *interleavingRegistry) RecoverIfMatch(_ state.KeyRef, weight int) bool {
	registry.mu.Lock()
	registry.currentWeight = weight
	registry.recoveries++
	registry.mu.Unlock()
	return true
}

func (registry *interleavingRegistry) weight() int {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.currentWeight
}

func (registry *interleavingRegistry) recoveryCount() int {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.recoveries
}
