package control

import (
	"context"
	"time"

	"gpt-load/internal/health"
	"gpt-load/internal/state"
)

type autoWeightRegistry interface {
	ActiveKeyIDs() []uint
	SetAutoWeight(keyID uint, weight int) bool
}

type runtimeTicker interface {
	C() <-chan time.Time
	Stop()
}

type standardRuntimeTicker struct {
	ticker *time.Ticker
}

func (ticker standardRuntimeTicker) C() <-chan time.Time {
	return ticker.ticker.C
}

func (ticker standardRuntimeTicker) Stop() {
	ticker.ticker.Stop()
}

type Runtime struct {
	registry  autoWeightRegistry
	stats     *health.StatsStore
	interval  time.Duration
	now       func() time.Time
	newTicker func(time.Duration) runtimeTicker
}

func NewRuntime(registry *state.KeyRegistry, stats *health.StatsStore) *Runtime {
	return &Runtime{
		registry: registry,
		stats:    stats,
		interval: 30 * time.Second,
		now:      time.Now,
		newTicker: func(interval time.Duration) runtimeTicker {
			return standardRuntimeTicker{ticker: time.NewTicker(interval)}
		},
	}
}

func (runtime *Runtime) Run(ctx context.Context) {
	ticker := runtime.newTicker(runtime.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			runtime.recompute(runtime.now())
		}
	}
}

func (runtime *Runtime) recompute(now time.Time) {
	for _, keyID := range runtime.registry.ActiveKeyIDs() {
		stats := runtime.stats.Snapshot(keyID, now)
		runtime.registry.SetAutoWeight(keyID, calculateAutoWeight(stats))
	}
}
