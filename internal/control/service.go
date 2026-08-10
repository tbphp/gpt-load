package control

import (
	"context"
	"crypto/rand"
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
	"gpt-load/internal/platform/dbtx"
	"gpt-load/internal/platform/encryption"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

const (
	defaultModelDiscoveryTimeout     = 30 * time.Second
	controlTransactionCleanupTimeout = time.Second
)

type Service struct {
	db                          *gorm.DB
	manager                     *state.Manager
	registry                    *state.CredentialRegistry
	channelRegistry             *channel.Registry
	registrySnapshot            func() []state.CredentialRuntimeView
	priceRuntime                *PriceRuntime
	catalogRuntime              *catalog.Runtime
	catalogSync                 *CatalogSyncCoordinator
	modelsDevAutoSyncOverride   *bool
	encryption                  encryption.Service
	executor                    execution.Executor
	requestLogs                 RequestLogReader
	usageStats                  UsageStatReader
	homeStatistics              HomeStatisticsReader
	stats                       *health.StatsStore
	mutations                   credentialMutationCoordinator
	requestLogStats             RequestLogStatsReader
	modelDiscoveryTimeout       time.Duration
	random                      io.Reader
	operationRandom             io.Reader
	now                         func() time.Time
	publishSnapshot             func(state.CompileInput) (*state.ConfigSnapshot, error)
	reconcileRegistryGroup      func(uint, []state.CredentialEntry) (bool, error)
	applyBatchRegistryMutation  func(uint, []uint, CredentialBatchAction) error
	restoreBatchRegistryEntries func(uint, []state.CredentialEntry) error
	beforeAdvanceOperationStage func(
		context.Context,
		*models.ControlOperation,
		operationStage,
	) error
	operationRecoveryWake chan struct{}
	writeMu               sync.RWMutex
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
	service := &Service{
		db: db, manager: manager, registry: registry,
		channelRegistry: channelRegistry,
		priceRuntime:    priceRuntime,
		catalogRuntime:  catalogRuntime,
		encryption:      encryptionService, executor: executor, requestLogs: requestLogs,
		usageStats: usageStats, homeStatistics: homeStatistics,
		stats: stats, mutations: mutations, requestLogStats: requestLogStats,
		modelDiscoveryTimeout: defaultModelDiscoveryTimeout,
		random:                rand.Reader,
		operationRandom:       rand.Reader,
		now:                   time.Now,
		operationRecoveryWake: make(chan struct{}, 1),
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
	return service
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
		priceTable, err := loadPriceTable(ctx, tx)
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
			return nil, newControlOperationError(stageApplyCommittedRegistryMutation)
		}
	}
	snapshot, err := s.publishSnapshot(publication.ConfigInput)
	if err != nil {
		return nil, newControlOperationError(stagePublishCommittedSnapshot)
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
			return nil, newControlOperationError(stageApplyCommittedRegistryMutation)
		}
	}
	snapshot, err := s.manager.Publish(input)
	if err != nil {
		return nil, newControlOperationError(stagePublishCommittedSnapshot)
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

	if err := s.withControlTransaction(ctx, mutate); err != nil {
		return err
	}
	if afterCommit != nil {
		if err := afterCommit(); err != nil {
			return withControlOperationContext(
				newControlOperationError(stageApplyCommittedRegistryMutation),
				groupID,
				credentialID,
			)
		}
	}
	return nil
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
