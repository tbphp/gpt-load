package control

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	"gpt-load/internal/dialect"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/state"
)

const (
	autoWeightInterval  = 30 * time.Second
	validationInterval  = 30 * time.Minute
	maxValidationJitter = 3 * time.Minute
	retentionInterval   = time.Hour
)

type autoWeightRegistry interface {
	ActiveKeyIDs() []uint
	SetAutoWeight(keyID uint, weight int) bool
}

type keyMutationCoordinator interface {
	Do(uint, func())
}

type operationRecoveryRuntime interface {
	RunOperationRecovery(context.Context)
}

// RequestLogCleaner is the control-owned scheduling view of request log
// retention. The requestlog package owns all cleanup semantics.
type RequestLogCleaner interface {
	Sweep(context.Context, time.Time)
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
	registry           autoWeightRegistry
	stats              *health.StatsStore
	mutations          keyMutationCoordinator
	validator          validationSweep
	requestLogCleaner  RequestLogCleaner
	operationRecovery  operationRecoveryRuntime
	autoWeightInterval time.Duration
	validationInterval time.Duration
	validationJitter   func() time.Duration
	now                func() time.Time
	newTicker          func(time.Duration) runtimeTicker
}

func NewRuntime(
	registry *state.KeyRegistry,
	stats *health.StatsStore,
	mutations *health.MutationCoordinator,
	manager *state.Manager,
	encryptionService encryption.Service,
	dialects dialect.Set,
	requestLogCleaner RequestLogCleaner,
	operationRecovery *Service,
) *Runtime {
	runtime := &Runtime{
		registry:           registry,
		stats:              stats,
		mutations:          mutations,
		requestLogCleaner:  requestLogCleaner,
		operationRecovery:  operationRecovery,
		autoWeightInterval: autoWeightInterval,
		validationInterval: validationInterval,
		validationJitter: func() time.Duration {
			return time.Duration(rand.Int64N(int64(maxValidationJitter) + 1))
		},
		now: time.Now,
		newTicker: func(interval time.Duration) runtimeTicker {
			return standardRuntimeTicker{ticker: time.NewTicker(interval)}
		},
	}
	runtime.validator = newValidationWorker(
		manager,
		registry,
		stats,
		mutations,
		encryptionService,
		dialects,
	)
	return runtime
}

func (runtime *Runtime) Run(ctx context.Context) {
	autoTicker := runtime.newTicker(runtime.autoWeightInterval)
	validationTicker := runtime.newTicker(runtime.validationInterval + runtime.validationJitter())

	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		runtime.runAutoWeight(ctx, autoTicker)
	}()
	go func() {
		defer wait.Done()
		runtime.runValidation(ctx, validationTicker)
	}()
	if runtime.requestLogCleaner != nil {
		retentionTicker := runtime.newTicker(retentionInterval)
		wait.Add(1)
		go func() {
			defer wait.Done()
			runtime.runRetention(ctx, retentionTicker)
		}()
	}
	if runtime.operationRecovery != nil {
		wait.Add(1)
		go func() {
			defer wait.Done()
			runtime.operationRecovery.RunOperationRecovery(ctx)
		}()
	}
	wait.Wait()
}

func (runtime *Runtime) runAutoWeight(ctx context.Context, ticker runtimeTicker) {
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			if ctx.Err() != nil {
				return
			}
			runtime.recompute(runtime.now())
		}
	}
}

func (runtime *Runtime) runValidation(ctx context.Context, ticker runtimeTicker) {
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			if ctx.Err() != nil {
				return
			}
			if runtime.validator != nil {
				runtime.validator.Validate(ctx)
			}
		}
	}
}

func (runtime *Runtime) runRetention(ctx context.Context, ticker runtimeTicker) {
	defer ticker.Stop()
	if ctx.Err() != nil {
		return
	}
	runtime.requestLogCleaner.Sweep(ctx, runtime.now())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			if ctx.Err() != nil {
				return
			}
			runtime.requestLogCleaner.Sweep(ctx, runtime.now())
		}
	}
}

func (runtime *Runtime) recompute(now time.Time) {
	for _, keyID := range runtime.registry.ActiveKeyIDs() {
		runtime.mutations.Do(keyID, func() {
			stats := runtime.stats.Snapshot(keyID, now)
			runtime.registry.SetAutoWeight(keyID, calculateAutoWeight(stats))
		})
	}
}
