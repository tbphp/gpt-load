package control

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/catalog"
	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/encryption"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/dbtx"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/subscription"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

const (
	defaultModelDiscoveryTimeout      = 30 * time.Second
	defaultSubscriptionControlTimeout = 30 * time.Second
	controlTransactionCleanupTimeout  = time.Second
)

type Service struct {
	db                                *gorm.DB
	manager                           *state.Manager
	registry                          *state.CredentialRegistry
	channelRegistry                   *channel.Registry
	channelDefaultBaseURLs            channelDefaultBaseURLProvider
	registrySnapshot                  func() []state.CredentialRuntimeView
	priceRuntime                      *PriceRuntime
	catalogRuntime                    *catalog.Runtime
	catalogSync                       *CatalogSyncCoordinator
	modelsDevAutoSyncOverride         *bool
	encryption                        encryption.Service
	executor                          execution.Executor
	subscriptions                     *subscriptionruntime.Runtime
	requestLogs                       RequestLogReader
	usageStats                        UsageStatReader
	credentialWindowUsage             credentialWindowUsageReader
	credentialActivity                credentialActivityReader
	homeStatistics                    HomeStatisticsReader
	stats                             *health.StatsStore
	mutations                         credentialMutationCoordinator
	requestLogStats                   RequestLogStatsReader
	modelDiscoveryTimeout             time.Duration
	random                            io.Reader
	operationRandom                   io.Reader
	beginSubscriptionAuthorization    func(channel.ID) (subscriptionruntime.Authorization, error)
	completeSubscriptionAuthorization func(context.Context, channel.ID, subscriptionruntime.AuthorizationCompletion) (subscriptionruntime.Credential, error)
	refreshSubscriptionCredential     func(context.Context, channel.ID, subscriptionruntime.Credential) (subscriptionruntime.Credential, error)
	prepareSubscriptionCredential     func(context.Context, channel.ID, execution.CredentialSnapshot, bool) (subscriptionruntime.Credential, *execution.ErrorEvidence)
	discoverSubscriptionModels        func(context.Context, channel.ID, subscriptionruntime.Credential) ([]string, error)
	observeSubscriptionAccount        func(context.Context, channel.ID, subscriptionruntime.Credential) (subscriptionruntime.Observation, error)
	consumeSubscriptionResetCredit    func(context.Context, channel.ID, subscriptionruntime.Credential, string) (subscriptionruntime.ResetCreditResult, error)
	oauthCallback                     *OAuthCallbackServer
	now                               func() time.Time
	publishSnapshot                   func(state.CompileInput) (*state.ConfigSnapshot, error)
	reconcileRegistryGroup            func(uint, []state.CredentialEntry) (bool, error)
	applyBatchRegistryMutation        func(uint, []uint, CredentialBatchAction) error
	restoreBatchRegistryEntries       func(uint, []state.CredentialEntry) error
	beforeAdvanceOperationStage       func(
		context.Context,
		*models.ControlOperation,
		operationStage,
	) error
	operationRecoveryWake chan struct{}
	writeMu               sync.RWMutex
	observationMu         sync.Mutex
	observationFlights    map[observationFlightKey]*observationFlight
	observationSemaphore  chan struct{}
}

type credentialRuntimeRetirer interface {
	RetireCredential(uint)
}

type credentialWindowUsageReader interface {
	QueryCredentialWindowUsage(
		context.Context,
		requestlog.CredentialWindowUsageQuery,
	) (requestlog.CredentialWindowUsage, error)
}

type credentialActivityReader interface {
	QueryCredentialActivity(
		context.Context,
		requestlog.CredentialActivityQuery,
	) (map[uint]requestlog.CredentialActivity, error)
}

type credentialMultiMutationCoordinator interface {
	DoMany([]uint, func())
}

func (s *Service) doCredentialMutations(credentialIDs []uint, fn func()) error {
	if fn == nil {
		return nil
	}
	if len(credentialIDs) == 0 || s.mutations == nil {
		fn()
		return nil
	}
	if len(credentialIDs) == 1 {
		s.mutations.Do(credentialIDs[0], fn)
		return nil
	}
	coordinator, ok := s.mutations.(credentialMultiMutationCoordinator)
	if !ok {
		return fmt.Errorf("credential mutation coordinator unavailable: %w", app_errors.ErrInternalServer)
	}
	coordinator.DoMany(credentialIDs, fn)
	return nil
}

func (s *Service) retireCredentialRuntime(credentialID uint) {
	if s == nil || credentialID == 0 {
		return
	}
	if runtime, ok := s.executor.(credentialRuntimeRetirer); ok {
		runtime.RetireCredential(credentialID)
	}
}

func NewService(
	db *gorm.DB,
	manager *state.Manager,
	registry *state.CredentialRegistry,
	priceRuntime *PriceRuntime,
	catalogRuntime *catalog.Runtime,
	cfg *config.Config,
	encryptionService encryption.Service,
	executor execution.Executor,
	subscriptionCredentials *subscription.CredentialManager,
	requestLogs RequestLogReader,
	usageStats UsageStatReader,
	homeStatistics HomeStatisticsReader,
	stats *health.StatsStore,
	mutations *health.MutationCoordinator,
	requestLogStats RequestLogStatsReader,
	channelRegistries ...*channel.Registry,
) *Service {
	channelRegistry := channel.NewRegistry()
	for _, candidate := range channelRegistries {
		if candidate != nil {
			channelRegistry = candidate
			break
		}
	}
	var subscriptions *subscriptionruntime.Runtime
	if subscriptionCredentials != nil {
		subscriptions = subscriptionCredentials.Runtime()
	}
	service := &Service{
		db: db, manager: manager, registry: registry,
		channelRegistry: channelRegistry,
		priceRuntime:    priceRuntime,
		catalogRuntime:  catalogRuntime,
		encryption:      encryptionService, executor: executor, subscriptions: subscriptions, requestLogs: requestLogs,
		usageStats: usageStats, homeStatistics: homeStatistics,
		stats: stats, mutations: mutations, requestLogStats: requestLogStats,
		modelDiscoveryTimeout: defaultModelDiscoveryTimeout,
		random:                rand.Reader,
		operationRandom:       rand.Reader,
		beginSubscriptionAuthorization: func(channelID channel.ID) (subscriptionruntime.Authorization, error) {
			browser, ok := subscriptionsBrowser(subscriptions, channelID)
			if !ok {
				return subscriptionruntime.Authorization{}, app_errors.ErrAuthorizationUnavailable
			}
			authorization, err := browser.BeginAuthorization()
			if err == nil {
				authorization.LocalCallback = browser.RequiresLocalCallback()
			}
			return authorization, err
		},
		completeSubscriptionAuthorization: func(ctx context.Context, channelID channel.ID, completion subscriptionruntime.AuthorizationCompletion) (subscriptionruntime.Credential, error) {
			browser, ok := subscriptionsBrowser(subscriptions, channelID)
			if !ok {
				return subscriptionruntime.Credential{}, app_errors.ErrAuthorizationUnavailable
			}
			return browser.CompleteAuthorization(ctx, completion)
		},
		refreshSubscriptionCredential: func(ctx context.Context, channelID channel.ID, credential subscriptionruntime.Credential) (subscriptionruntime.Credential, error) {
			driver, ok := subscriptionsDriver(subscriptions, channelID)
			if !ok {
				return subscriptionruntime.Credential{}, app_errors.ErrAuthorizationUnavailable
			}
			return driver.Refresh(ctx, credential)
		},
		discoverSubscriptionModels: func(ctx context.Context, channelID channel.ID, credential subscriptionruntime.Credential) ([]string, error) {
			capability, ok := subscriptions.ModelDiscovery(channelID)
			if !ok {
				return nil, app_errors.ErrValidation
			}
			return capability.DiscoverModels(ctx, credential)
		},
		observeSubscriptionAccount: func(ctx context.Context, channelID channel.ID, credential subscriptionruntime.Credential) (subscriptionruntime.Observation, error) {
			capability, ok := subscriptions.QuotaObservation(channelID)
			if !ok {
				return subscriptionruntime.Observation{}, app_errors.ErrValidation
			}
			return capability.Observe(ctx, credential)
		},
		consumeSubscriptionResetCredit: func(ctx context.Context, channelID channel.ID, credential subscriptionruntime.Credential, requestID string) (subscriptionruntime.ResetCreditResult, error) {
			capability, ok := subscriptions.ResetCreditAction(channelID)
			if !ok {
				return subscriptionruntime.ResetCreditResult{}, app_errors.ErrValidation
			}
			return capability.Consume(ctx, credential, requestID)
		},
		now:                   time.Now,
		operationRecoveryWake: make(chan struct{}, 1),
		observationFlights:    make(map[observationFlightKey]*observationFlight),
		observationSemaphore:  make(chan struct{}, 1),
	}
	if subscriptionCredentials != nil {
		service.prepareSubscriptionCredential = subscriptionCredentials.Prepare
		service.subscriptions = subscriptionCredentials.Runtime()
	}
	if reader, ok := usageStats.(credentialWindowUsageReader); ok {
		service.credentialWindowUsage = reader
	} else if reader, ok := requestLogs.(credentialWindowUsageReader); ok {
		service.credentialWindowUsage = reader
	}
	if reader, ok := usageStats.(credentialActivityReader); ok {
		service.credentialActivity = reader
	} else if reader, ok := requestLogs.(credentialActivityReader); ok {
		service.credentialActivity = reader
	}
	if provider, ok := executor.(channelDefaultBaseURLProvider); ok {
		service.channelDefaultBaseURLs = provider
	}
	if cfg != nil && cfg.ModelsDevAutoSyncOverride != nil {
		value := *cfg.ModelsDevAutoSyncOverride
		service.modelsDevAutoSyncOverride = &value
	}
	service.publishSnapshot = manager.Publish
	service.reconcileRegistryGroup = registry.ReconcileGroup
	service.applyBatchRegistryMutation = service.applyCredentialBatchRegistryMutation
	service.restoreBatchRegistryEntries = registry.RestoreGroupCredentialEntriesExact
	service.registrySnapshot = registry.Snapshot
	service.oauthCallback = NewOAuthCallbackServer(service)
	return service
}

func subscriptionsDriver(runtime *subscriptionruntime.Runtime, channelID channel.ID) (subscriptionruntime.Driver, bool) {
	if runtime == nil {
		return nil, false
	}
	return runtime.Driver(channelID)
}

func subscriptionsBrowser(runtime *subscriptionruntime.Runtime, channelID channel.ID) (subscriptionruntime.BrowserAuthorizationDriver, bool) {
	if runtime == nil {
		return nil, false
	}
	return runtime.BrowserAuthorization(channelID)
}

type configMutationPublication struct {
	ConfigInput state.CompileInput
	PriceTable  *pricing.Table
}

func (s *Service) writeGroupConfig(
	ctx context.Context,
	mutate func(*gorm.DB) error,
	afterCommitBeforePublish func() error,
) (*state.ConfigSnapshot, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.enforceOperationRecoveryBarrierLocked(ctx, 0); err != nil {
		return nil, err
	}
	var credentialIDs []uint
	if err := s.db.WithContext(ctx).Model(&models.Credential{}).
		Order("id ASC").Pluck("id", &credentialIDs).Error; err != nil {
		return nil, app_errors.ParseDBError(err)
	}
	var snapshot *state.ConfigSnapshot
	var resultErr error
	apply := func() {
		snapshot, resultErr = s.writeGroupConfigLocked(ctx, mutate, afterCommitBeforePublish)
	}
	if err := s.doCredentialMutations(credentialIDs, apply); err != nil {
		return nil, err
	}
	return snapshot, resultErr
}

func (s *Service) writeGroupConfigLocked(
	ctx context.Context,
	mutate func(*gorm.DB) error,
	afterCommitBeforePublish func() error,
) (*state.ConfigSnapshot, error) {
	var catalogSnapshot *catalog.Snapshot
	if s.catalogRuntime != nil {
		catalogSnapshot = s.catalogRuntime.Load()
	}
	publication := configMutationPublication{}
	err := s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		if err := mutate(tx); err != nil {
			return err
		}
		if err := reconcileReferencedPrices(tx, catalogSnapshot); err != nil {
			return err
		}
		if err := cleanupUnreferencedAutomaticPrices(tx); err != nil {
			return err
		}
		input, err := stateloader.BuildCompileInput(ctx, tx, s.channelRegistry)
		if err != nil {
			return err
		}
		if _, err := state.Compile(input); err != nil {
			return err
		}
		priceTable, err := loadPriceTable(ctx, tx, catalogSnapshot)
		if err != nil {
			return err
		}
		publication = configMutationPublication{ConfigInput: input, PriceTable: priceTable}
		return nil
	})
	if err != nil {
		return nil, err
	}

	s.priceRuntime.Publish(publication.PriceTable)
	if afterCommitBeforePublish != nil {
		if err := afterCommitBeforePublish(); err != nil {
			operationErr := newControlOperationError(stageApplyCommittedRegistryMutation)
			return nil, joinCommittedRuntimeRecovery(
				operationErr,
				s.recoverCommittedRuntime(ctx, true),
			)
		}
	}
	snapshot, err := s.publishSnapshot(publication.ConfigInput)
	if err != nil {
		operationErr := newControlOperationError(stagePublishCommittedSnapshot)
		return nil, joinCommittedRuntimeRecovery(
			operationErr,
			s.recoverCommittedRuntime(ctx, true),
		)
	}
	return snapshot, nil
}

func (s *Service) writeConfig(
	ctx context.Context,
	mutate func(*gorm.DB) error,
	afterCommitBeforePublish func() error,
) (*state.ConfigSnapshot, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.enforceOperationRecoveryBarrierLocked(ctx, 0); err != nil {
		return nil, err
	}

	var input state.CompileInput
	err := s.withControlTransaction(ctx, func(tx *gorm.DB) error {
		if err := mutate(tx); err != nil {
			return err
		}
		var err error
		input, err = stateloader.BuildCompileInput(ctx, tx, s.channelRegistry)
		if err != nil {
			return err
		}
		if _, err := state.Compile(input); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if afterCommitBeforePublish != nil {
		if err := afterCommitBeforePublish(); err != nil {
			operationErr := newControlOperationError(stageApplyCommittedRegistryMutation)
			return nil, joinCommittedRuntimeRecovery(
				operationErr,
				s.recoverCommittedRuntime(ctx, false),
			)
		}
	}
	snapshot, err := s.publishSnapshot(input)
	if err != nil {
		operationErr := newControlOperationError(stagePublishCommittedSnapshot)
		return nil, joinCommittedRuntimeRecovery(
			operationErr,
			s.recoverCommittedRuntime(ctx, false),
		)
	}
	return snapshot, nil
}

func (s *Service) writeCredentialConfig(
	ctx context.Context,
	groupID uint,
	credentialID uint,
	mutate func(*gorm.DB) error,
	afterCommit func() error,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.enforceOperationRecoveryBarrierLocked(ctx, 0); err != nil {
		return err
	}
	var result error
	apply := func() {
		if err := s.withControlTransaction(ctx, mutate); err != nil {
			result = err
			return
		}
		if afterCommit == nil {
			return
		}
		if err := afterCommit(); err != nil {
			operationErr := withControlOperationContext(
				newControlOperationError(stageApplyCommittedRegistryMutation),
				groupID,
				credentialID,
			)
			result = joinCommittedRuntimeRecovery(
				operationErr,
				s.recoverCommittedCredentialRegistryGroup(ctx, groupID),
			)
		}
	}
	if credentialID != 0 && s.mutations != nil {
		s.mutations.Do(credentialID, apply)
	} else {
		apply()
	}
	return result
}

func (s *Service) recoverCommittedRuntime(ctx context.Context, includePrices bool) error {
	input, err := stateloader.BuildCompileInput(ctx, s.db, s.channelRegistry)
	if err != nil {
		return fmt.Errorf("reload committed configuration: %w", err)
	}
	if _, err := state.Compile(input); err != nil {
		return fmt.Errorf("compile committed configuration: %w", err)
	}
	var priceTable *pricing.Table
	if includePrices {
		entries, entriesErr := stateloader.BuildCredentialEntries(ctx, s.db)
		if entriesErr != nil {
			return fmt.Errorf("reload committed credentials: %w", entriesErr)
		}
		var catalogSnapshot *catalog.Snapshot
		if s.catalogRuntime != nil {
			catalogSnapshot = s.catalogRuntime.Load()
		}
		priceTable, err = loadPriceTable(ctx, s.db, catalogSnapshot)
		if err != nil {
			return fmt.Errorf("reload committed prices: %w", err)
		}
		s.priceRuntime.Publish(priceTable)
		if err := s.registry.ReplaceCredentials(entries); err != nil {
			return fmt.Errorf("replace committed credentials: %w", err)
		}
		if err := s.restoreCredentialQuotaObservations(ctx); err != nil {
			return fmt.Errorf("restore committed credential quota observations: %w", err)
		}
	}
	if _, err := s.manager.Publish(input); err != nil {
		return fmt.Errorf("publish committed configuration: %w", err)
	}
	return nil
}

func (s *Service) recoverCommittedCredentialRegistryGroup(ctx context.Context, groupID uint) error {
	entries, err := stateloader.BuildGroupCredentialEntries(ctx, s.db, groupID)
	if err != nil {
		return fmt.Errorf("reload committed group credentials: %w", err)
	}
	if _, err := s.reconcileRegistryGroup(groupID, entries); err != nil {
		return fmt.Errorf("reconcile committed group credentials: %w", err)
	}
	return nil
}

func joinCommittedRuntimeRecovery(operationErr, recoveryErr error) error {
	if recoveryErr == nil {
		return operationErr
	}
	return errors.Join(operationErr, recoveryErr)
}

func (s *Service) withControlTransaction(
	ctx context.Context,
	mutate func(*gorm.DB) error,
) error {
	err := dbtx.Run(ctx, s.db, dbtx.Options{
		Mode:           dbtx.Write,
		CleanupTimeout: controlTransactionCleanupTimeout,
		Operation:      "control transaction",
	}, mutate)
	if dbtx.IsInfrastructure(err) {
		return fmt.Errorf("%v: %w", err, app_errors.ErrDatabase)
	}
	return err
}

func (s *Service) withReadSnapshot(
	ctx context.Context,
	read func(*gorm.DB) error,
) error {
	err := dbtx.Run(ctx, s.db, dbtx.Options{
		Mode:           dbtx.ReadSnapshot,
		CleanupTimeout: controlTransactionCleanupTimeout,
		Operation:      "read snapshot",
	}, read)
	if dbtx.IsInfrastructure(err) {
		return fmt.Errorf("%v: %w", err, app_errors.ErrDatabase)
	}
	return err
}
