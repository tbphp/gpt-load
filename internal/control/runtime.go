package control

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/releasecheck"
	"gpt-load/internal/state"
)

const (
	autoWeightInterval        = 30 * time.Second
	defaultValidationInterval = 10 * time.Minute
	maxValidationJitter       = 3 * time.Minute
	retentionInterval         = time.Hour
)

type autoWeightRegistry interface {
	ActiveCredentialIDs() []uint
	SetAutoWeight(credentialID uint, weight int) bool
}

type credentialMutationCoordinator interface {
	Do(uint, func())
}

type operationRecoveryRuntime interface {
	RunOperationRecovery(context.Context)
}

type credentialObservationRuntime interface {
	RunCredentialObservationRefresh(context.Context)
}

type catalogSyncRuntime interface {
	Run(context.Context)
}

type releaseCheckRuntime interface {
	Run(context.Context)
}

// RequestLogCleaner is the control-owned scheduling view of request log
// retention. The requestlog package owns all cleanup semantics.
type RequestLogCleaner interface {
	Sweep(context.Context, time.Time)
}

type credentialStageCleaner interface {
	CleanupCredentialStages(context.Context, time.Time) error
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
	registry               autoWeightRegistry
	stats                  *health.StatsStore
	mutations              credentialMutationCoordinator
	validator              validationSweep
	requestLogCleaner      RequestLogCleaner
	stageCleaner           credentialStageCleaner
	operationRecovery      operationRecoveryRuntime
	credentialObservations credentialObservationRuntime
	catalogSync            catalogSyncRuntime
	releaseCheck           releaseCheckRuntime
	oauthCallback          *OAuthCallbackManager
	manager                *state.Manager
	autoWeightInterval     time.Duration
	validationInterval     time.Duration
	validationJitter       func() time.Duration
	now                    func() time.Time
	newTicker              func(time.Duration) runtimeTicker
}

func NewRuntime(
	registry *state.CredentialRegistry,
	stats *health.StatsStore,
	mutations *health.MutationCoordinator,
	manager *state.Manager,
	encryptionService encryption.Service,
	channelRegistry *channel.Registry,
	executor execution.Executor,
	requestLogCleaner RequestLogCleaner,
	operationRecovery *Service,
	catalogSync *CatalogSyncCoordinator,
	releaseChecker *releasecheck.Checker,
) *Runtime {
	runtime := &Runtime{
		registry:               registry,
		stats:                  stats,
		mutations:              mutations,
		requestLogCleaner:      requestLogCleaner,
		stageCleaner:           operationRecovery,
		operationRecovery:      operationRecovery,
		credentialObservations: operationRecovery,
		catalogSync:            catalogSync,
		releaseCheck:           releaseChecker,
		manager:                manager,
		autoWeightInterval:     autoWeightInterval,
		validationInterval:     defaultValidationInterval,
		validationJitter: func() time.Duration {
			return time.Duration(rand.Int64N(int64(maxValidationJitter) + 1))
		},
		now: time.Now,
		newTicker: func(interval time.Duration) runtimeTicker {
			return standardRuntimeTicker{ticker: time.NewTicker(interval)}
		},
	}
	if operationRecovery != nil {
		runtime.oauthCallback = operationRecovery.oauthCallback
	}
	runtime.validator = newValidationWorker(
		manager,
		registry,
		stats,
		mutations,
		encryptionService,
		channelRegistry,
		executor,
	)
	return runtime
}

func (runtime *Runtime) Run(ctx context.Context) {
	autoTicker := runtime.newTicker(runtime.autoWeightInterval)
	currentValidationInterval, validationUpdates := runtime.currentValidationSchedule()
	validationTicker := runtime.newTicker(validationTickerInterval(
		currentValidationInterval,
		runtime.validationJitter(),
	))

	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		runtime.runAutoWeight(ctx, autoTicker)
	}()
	go func() {
		defer wait.Done()
		runtime.runValidation(ctx, validationTicker, currentValidationInterval, validationUpdates)
	}()
	if runtime.requestLogCleaner != nil || runtime.stageCleaner != nil {
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
	if runtime.credentialObservations != nil {
		wait.Add(1)
		go func() {
			defer wait.Done()
			runtime.credentialObservations.RunCredentialObservationRefresh(ctx)
		}()
	}
	if runtime.catalogSync != nil {
		wait.Add(1)
		go func() {
			defer wait.Done()
			runtime.catalogSync.Run(ctx)
		}()
	}
	if runtime.releaseCheck != nil {
		wait.Add(1)
		go func() {
			defer wait.Done()
			runtime.releaseCheck.Run(ctx)
		}()
	}
	if runtime.oauthCallback != nil {
		wait.Add(1)
		go func() {
			defer wait.Done()
			runtime.oauthCallback.Run(ctx)
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

func (runtime *Runtime) runValidation(
	ctx context.Context,
	ticker runtimeTicker,
	interval time.Duration,
	updates <-chan struct{},
) {
	defer func() { ticker.Stop() }()
	for {
		select {
		case <-ctx.Done():
			return
		case <-updates:
			nextInterval, nextUpdates := runtime.currentValidationSchedule()
			updates = nextUpdates
			if nextInterval == interval {
				continue
			}
			ticker.Stop()
			interval = nextInterval
			ticker = runtime.newTicker(validationTickerInterval(interval, runtime.validationJitter()))
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

func validationTickerInterval(interval, jitter time.Duration) time.Duration {
	const maxDuration = time.Duration(1<<63 - 1)
	if jitter <= 0 {
		return interval
	}
	if interval > maxDuration-jitter {
		return maxDuration
	}
	return interval + jitter
}

func (runtime *Runtime) currentValidationSchedule() (time.Duration, <-chan struct{}) {
	interval := runtime.validationInterval
	if interval <= 0 {
		interval = defaultValidationInterval
	}
	if runtime.manager == nil {
		return interval, nil
	}
	snapshot, updates := runtime.manager.CurrentWithUpdates()
	if snapshot != nil && snapshot.Settings.ValidationInterval > 0 {
		interval = snapshot.Settings.ValidationInterval
	}
	return interval, updates
}

func (runtime *Runtime) runRetention(ctx context.Context, ticker runtimeTicker) {
	defer ticker.Stop()
	if ctx.Err() != nil {
		return
	}
	runtime.sweepRetention(ctx, runtime.now())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			if ctx.Err() != nil {
				return
			}
			runtime.sweepRetention(ctx, runtime.now())
		}
	}
}

func (runtime *Runtime) sweepRetention(ctx context.Context, now time.Time) {
	if runtime.requestLogCleaner != nil {
		runtime.requestLogCleaner.Sweep(ctx, now)
	}
	if runtime.stageCleaner != nil {
		if err := runtime.stageCleaner.CleanupCredentialStages(ctx, now); err != nil {
			logrus.WithError(err).WithField("event", "control.credential_stage_cleanup_failed").Warn("credential stage cleanup failed")
		}
	}
}

func (runtime *Runtime) recompute(now time.Time) {
	for _, credentialID := range runtime.registry.ActiveCredentialIDs() {
		runtime.mutations.Do(credentialID, func() {
			stats := runtime.stats.Snapshot(credentialID, now)
			runtime.registry.SetAutoWeight(credentialID, calculateAutoWeight(stats))
		})
	}
}
