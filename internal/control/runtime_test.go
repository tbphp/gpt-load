package control

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"gpt-load/internal/health"
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

func TestRuntimeWaitsForTickBeforeRecompute(t *testing.T) {
	registry := newFakeAutoWeightRegistry(1)
	ticker := newFakeRuntimeTicker()
	created := make(chan time.Duration, 1)
	runtime := NewRuntime(state.NewKeyRegistry(), health.NewStatsStore())
	runtime.registry = registry
	runtime.newTicker = func(interval time.Duration) runtimeTicker {
		created <- interval
		return ticker
	}

	cancel, done := startRuntime(t, runtime)
	if interval := awaitValue(t, created); interval != 30*time.Second {
		t.Fatalf("ticker interval = %v, want 30s", interval)
	}
	if got := registry.snapshotWrites(); len(got) != 0 {
		t.Fatalf("writes before first tick = %v, want none", got)
	}
	stopRuntime(t, cancel, done)
}

func TestRuntimeRecomputesEveryActiveKey(t *testing.T) {
	registry := newFakeAutoWeightRegistry(3, 7, 9)
	ticker := newFakeRuntimeTicker()
	created := make(chan time.Duration, 1)
	runtime := newTestRuntime(registry, health.NewStatsStore(), ticker, created, time.Now)

	cancel, done := startRuntime(t, runtime)
	_ = awaitValue(t, created)
	for tick := 0; tick < 2; tick++ {
		ticker.ticks <- time.Now()
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
	ticker := newFakeRuntimeTicker()
	created := make(chan time.Duration, 1)
	clock := &fakeRuntimeClock{now: base}
	runtime := newTestRuntime(registry, stats, ticker, created, clock.current)

	cancel, done := startRuntime(t, runtime)
	_ = awaitValue(t, created)
	ticker.ticks <- base
	if got, want := awaitValue(t, registry.wrote), (autoWeightWrite{keyID: 1, weight: 92}); got != want {
		t.Fatalf("write with populated window = %#v, want %#v", got, want)
	}

	clock.set(base.Add(5 * time.Minute))
	ticker.ticks <- base.Add(5 * time.Minute)
	if got, want := awaitValue(t, registry.wrote), (autoWeightWrite{keyID: 1, weight: state.DefaultWeight}); got != want {
		t.Fatalf("write after window expiry = %#v, want %#v", got, want)
	}
	stopRuntime(t, cancel, done)
}

func TestRuntimeContinuesWhenKeyDisappears(t *testing.T) {
	registry := newFakeAutoWeightRegistry(1, 2, 3)
	registry.reject[2] = true
	ticker := newFakeRuntimeTicker()
	created := make(chan time.Duration, 1)
	runtime := newTestRuntime(registry, health.NewStatsStore(), ticker, created, time.Now)

	cancel, done := startRuntime(t, runtime)
	_ = awaitValue(t, created)
	ticker.ticks <- time.Now()
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

	runtime := NewRuntime(registry, stats)
	runtime.recompute(base)
	candidates := registry.CollectCandidates([]uint{10}, nil, base)
	if len(candidates) != 1 || candidates[0].WeightAuto != 92 {
		t.Fatalf("CollectCandidates() = %#v, want one candidate with WeightAuto 92", candidates)
	}
}

func TestRuntimeStopsOnContextCancellation(t *testing.T) {
	registry := newFakeAutoWeightRegistry(1)
	ticker := newFakeRuntimeTicker()
	created := make(chan time.Duration, 1)
	runtime := newTestRuntime(registry, health.NewStatsStore(), ticker, created, time.Now)

	cancel, done := startRuntime(t, runtime)
	_ = awaitValue(t, created)
	stopRuntime(t, cancel, done)
	awaitSignal(t, ticker.stopped)

	ticker.ticks <- time.Now()
	if got := registry.snapshotWrites(); len(got) != 0 {
		t.Fatalf("writes after cancellation = %v, want none", got)
	}
}

func newTestRuntime(
	registry autoWeightRegistry,
	stats *health.StatsStore,
	ticker *fakeRuntimeTicker,
	created chan<- time.Duration,
	now func() time.Time,
) *Runtime {
	return &Runtime{
		registry: registry,
		stats:    stats,
		interval: 30 * time.Second,
		now:      now,
		newTicker: func(interval time.Duration) runtimeTicker {
			created <- interval
			return ticker
		},
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
