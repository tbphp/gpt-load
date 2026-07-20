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

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/encryption"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
)

const (
	defaultModelDiscoveryTimeout     = 30 * time.Second
	controlTransactionCleanupTimeout = time.Second
)

type Service struct {
	db                    *gorm.DB
	manager               *state.Manager
	registry              *state.KeyRegistry
	encryption            encryption.Service
	dialects              dialect.Set
	modelDiscoveryTimeout time.Duration
	random                io.Reader
	writeMu               sync.Mutex
}

func NewService(
	db *gorm.DB,
	manager *state.Manager,
	registry *state.KeyRegistry,
	encryptionService encryption.Service,
	dialects dialect.Set,
) *Service {
	return &Service{
		db: db, manager: manager, registry: registry,
		encryption: encryptionService, dialects: dialects,
		modelDiscoveryTimeout: defaultModelDiscoveryTimeout, random: rand.Reader,
	}
}

func (s *Service) writeConfig(
	ctx context.Context,
	mutate func(*gorm.DB) error,
	afterCommitBeforePublish func() error,
) (*state.ConfigSnapshot, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

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
			return nil, fmt.Errorf("apply committed runtime update: %w", err)
		}
	}
	snapshot, err := s.manager.Publish(input)
	if err != nil {
		return nil, fmt.Errorf("publish committed config: %w", err)
	}
	return snapshot, nil
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
