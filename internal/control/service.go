package control

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/catalog"
	"gpt-load/internal/dialect"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/config"
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
	registry                    *state.KeyRegistry
	registrySnapshot            func() []state.KeyRuntimeView
	priceRuntime                *PriceRuntime
	catalogRuntime              *catalog.Runtime
	catalogSync                 *CatalogSyncCoordinator
	modelsDevAutoSyncOverride   *bool
	encryption                  encryption.Service
	dialects                    dialect.Set
	requestLogs                 RequestLogReader
	usageStats                  UsageStatReader
	homeStatistics              HomeStatisticsReader
	stats                       *health.StatsStore
	mutations                   keyMutationCoordinator
	requestLogStats             RequestLogStatsReader
	modelDiscoveryTimeout       time.Duration
	random                      io.Reader
	operationRandom             io.Reader
	now                         func() time.Time
	publishSnapshot             func(state.CompileInput) (*state.ConfigSnapshot, error)
	reconcileRegistryGroup      func(uint, []state.KeyEntry) (bool, error)
	applyBatchRegistryMutation  func(uint, []uint, GroupKeyBatchAction) error
	restoreBatchRegistryEntries func(uint, []state.KeyEntry) error
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
	registry *state.KeyRegistry,
	priceRuntime *PriceRuntime,
	catalogRuntime *catalog.Runtime,
	cfg *config.Config,
	encryptionService encryption.Service,
	dialects dialect.Set,
	requestLogs RequestLogReader,
	usageStats UsageStatReader,
	homeStatistics HomeStatisticsReader,
	stats *health.StatsStore,
	mutations *health.MutationCoordinator,
	requestLogStats RequestLogStatsReader,
) *Service {
	service := &Service{
		db: db, manager: manager, registry: registry,
		priceRuntime:   priceRuntime,
		catalogRuntime: catalogRuntime,
		encryption:     encryptionService, dialects: dialects, requestLogs: requestLogs,
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
	service.applyBatchRegistryMutation = service.applyGroupKeyBatchRegistryMutation
	service.restoreBatchRegistryEntries = registry.RestoreGroupKeyEntriesExact
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
		input, err := stateloader.BuildCompileInput(ctx, tx)
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
		input, err = stateloader.BuildCompileInput(ctx, tx)
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

func (s *Service) writeKeyConfig(
	ctx context.Context,
	groupID uint,
	keyID uint,
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
				keyID,
			)
		}
	}
	return nil
}

func (s *Service) withControlTransaction(
	ctx context.Context,
	mutate func(*gorm.DB) error,
) error {
	return s.db.WithContext(ctx).Connection(func(connection *gorm.DB) error {
		sqlConn, ok := connection.Statement.ConnPool.(*sql.Conn)
		if !ok {
			return fmt.Errorf("pin control transaction connection: %w", app_errors.ErrInternalServer)
		}
		transaction := connection.Session(&gorm.Session{
			NewDB: true, SkipDefaultTransaction: true, Context: ctx,
		})
		if err := transaction.Exec("BEGIN IMMEDIATE").Error; err != nil {
			return fmt.Errorf("begin control transaction: %v: %w", err, app_errors.ErrDatabase)
		}

		active := true
		defer func() {
			if active {
				_ = rollbackControlTransaction(connection, sqlConn, false)
			}
		}()
		if err := mutate(transaction); err != nil {
			cleanupErr := rollbackControlTransaction(connection, sqlConn, false)
			active = false
			return errors.Join(err, cleanupErr)
		}
		if err := transaction.Exec("COMMIT").Error; err != nil {
			commitErr := fmt.Errorf("commit control transaction: %v: %w", err, app_errors.ErrDatabase)
			cleanupErr := rollbackControlTransaction(connection, sqlConn, true)
			active = false
			return errors.Join(commitErr, cleanupErr)
		}
		active = false
		return nil
	})
}

func (s *Service) withReadSnapshot(
	ctx context.Context,
	read func(*gorm.DB) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := s.db.WithContext(ctx).Connection(func(connection *gorm.DB) error {
		sqlConn, ok := connection.Statement.ConnPool.(*sql.Conn)
		if !ok {
			return fmt.Errorf("pin read snapshot connection: %w", app_errors.ErrInternalServer)
		}
		snapshot := connection.Session(&gorm.Session{
			NewDB: true, SkipDefaultTransaction: true, Context: ctx,
		})
		if err := snapshot.Exec("BEGIN").Error; err != nil {
			if parentErr := ctx.Err(); parentErr != nil {
				return parentErr
			}
			return fmt.Errorf("begin read snapshot: %w", app_errors.ErrDatabase)
		}

		active := true
		defer func() {
			if active {
				_ = rollbackReadSnapshot(connection, sqlConn, false)
			}
		}()
		if err := read(snapshot); err != nil {
			cleanupErr := rollbackReadSnapshot(connection, sqlConn, false)
			active = false
			if parentErr := ctx.Err(); parentErr != nil {
				return errors.Join(parentErr, cleanupErr)
			}
			return errors.Join(err, cleanupErr)
		}
		if parentErr := ctx.Err(); parentErr != nil {
			cleanupErr := rollbackReadSnapshot(connection, sqlConn, false)
			active = false
			return errors.Join(parentErr, cleanupErr)
		}
		if err := snapshot.Exec("COMMIT").Error; err != nil {
			cleanupErr := rollbackReadSnapshot(connection, sqlConn, true)
			active = false
			if parentErr := ctx.Err(); parentErr != nil {
				return errors.Join(parentErr, cleanupErr)
			}
			return errors.Join(
				fmt.Errorf("commit read snapshot: %w", app_errors.ErrDatabase),
				cleanupErr,
			)
		}
		active = false
		return nil
	})
	if parentErr := ctx.Err(); parentErr != nil {
		return parentErr
	}
	return err
}

func rollbackReadSnapshot(
	connection *gorm.DB,
	sqlConn *sql.Conn,
	discardAlways bool,
) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), controlTransactionCleanupTimeout)
	defer cancel()
	cleanupDB := connection.Session(&gorm.Session{
		NewDB: true, SkipDefaultTransaction: true, Context: cleanupCtx,
	})
	rollbackErr := cleanupDB.Exec("ROLLBACK").Error
	var discardErr error
	if rollbackErr != nil || discardAlways {
		discardErr = discardControlConnection(sqlConn)
	}
	if rollbackErr == nil && discardErr == nil {
		return nil
	}
	return fmt.Errorf("cleanup read snapshot: %w", app_errors.ErrDatabase)
}

func rollbackControlTransaction(
	connection *gorm.DB,
	sqlConn *sql.Conn,
	discardAlways bool,
) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), controlTransactionCleanupTimeout)
	defer cancel()
	cleanupDB := connection.Session(&gorm.Session{
		NewDB: true, SkipDefaultTransaction: true, Context: cleanupCtx,
	})
	rollbackErr := cleanupDB.Exec("ROLLBACK").Error
	var discardErr error
	if rollbackErr != nil || discardAlways {
		discardErr = discardControlConnection(sqlConn)
	}
	if rollbackErr != nil {
		rollbackErr = fmt.Errorf("rollback control transaction: %w", rollbackErr)
	}
	return errors.Join(rollbackErr, discardErr)
}

func discardControlConnection(sqlConn *sql.Conn) error {
	err := sqlConn.Raw(func(any) error { return driver.ErrBadConn })
	if err == nil || errors.Is(err, driver.ErrBadConn) {
		return nil
	}
	return fmt.Errorf("discard control database connection: %w", err)
}
