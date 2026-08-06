package dbtx

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Mode controls the transaction behavior selected by the database driver.
type Mode uint8

const (
	Write Mode = iota
	ReadSnapshot
)

// Phase identifies the infrastructure operation that failed.
type Phase string

const (
	PhaseInput      Phase = "validate transaction"
	PhaseDriver     Phase = "detect database driver"
	PhaseConnection Phase = "pin database connection"
	PhaseBegin      Phase = "begin transaction"
	PhaseCommit     Phase = "commit transaction"
	PhaseRollback   Phase = "rollback transaction"
	PhaseDiscard    Phase = "discard database connection"
)

// Error represents a transaction infrastructure failure. Callback errors are
// returned directly so callers can preserve their business error semantics.
type Error struct {
	Operation string
	Phase     Phase
	Err       error
}

func (err *Error) Error() string {
	if err == nil {
		return "<nil>"
	}
	label := string(err.Phase)
	if err.Operation != "" {
		label = err.Operation + ": " + label
	}
	if err.Err == nil {
		return label
	}
	return fmt.Sprintf("%s: %v", label, err.Err)
}

func (err *Error) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

// IsInfrastructure reports whether an error contains a transaction
// infrastructure failure, including a cleanup failure joined with a callback
// error.
func IsInfrastructure(err error) bool {
	var target *Error
	return errors.As(err, &target)
}

// Capabilities describes the transaction statements required by a driver.
// The read modes deliberately establish one stable snapshot for all reads in
// a report, while SQLite retains its deferred snapshot and immediate write
// behavior.
type Capabilities struct {
	Driver     string
	WriteBegin BeginMode
	ReadBegin  BeginMode
}

// BeginMode is the driver-specific SQL transaction start strategy.
type BeginMode string

const (
	BeginStandard                BeginMode = "standard"
	BeginSQLiteImmediate         BeginMode = "sqlite_immediate"
	BeginMySQLConsistentSnapshot BeginMode = "mysql_consistent_snapshot"
	BeginPostgresRepeatableRead  BeginMode = "postgres_repeatable_read"
)

// CapabilitiesForDriver maps the GORM driver name to the transaction
// capability used by Run. The aliases accepted here avoid coupling callers to
// one spelling of the PostgreSQL driver name.
func CapabilitiesForDriver(driverName string) (Capabilities, error) {
	switch strings.ToLower(strings.TrimSpace(driverName)) {
	case "sqlite":
		return Capabilities{
			Driver:     "sqlite",
			WriteBegin: BeginSQLiteImmediate,
			ReadBegin:  BeginStandard,
		}, nil
	case "mysql":
		return Capabilities{
			Driver:     "mysql",
			WriteBegin: BeginStandard,
			ReadBegin:  BeginMySQLConsistentSnapshot,
		}, nil
	case "postgres", "postgresql":
		return Capabilities{
			Driver:     "postgres",
			WriteBegin: BeginStandard,
			ReadBegin:  BeginPostgresRepeatableRead,
		}, nil
	default:
		return Capabilities{}, &Error{
			Phase: PhaseDriver,
			Err:   fmt.Errorf("unsupported GORM driver %q", driverName),
		}
	}
}

func CapabilitiesFor(db *gorm.DB) (Capabilities, error) {
	if db == nil || db.Dialector == nil {
		return Capabilities{}, &Error{
			Phase: PhaseInput,
			Err:   errors.New("database is nil"),
		}
	}
	return CapabilitiesForDriver(db.Dialector.Name())
}

type Options struct {
	Mode           Mode
	CleanupTimeout time.Duration
	Operation      string
}

// Run executes callback inside a pinned SQL connection and a driver-aware
// transaction. A failed callback is rolled back; a failed rollback or commit
// causes the connection to be discarded so it cannot return to the pool in an
// unknown transaction state.
func Run(
	ctx context.Context,
	db *gorm.DB,
	options Options,
	callback func(*gorm.DB) error,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil {
		return newError(options.Operation, PhaseInput, errors.New("database is nil"))
	}
	if callback == nil {
		return newError(options.Operation, PhaseInput, errors.New("transaction callback is nil"))
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	capabilities, err := CapabilitiesFor(db)
	if err != nil {
		return withOperation(err, options.Operation)
	}
	beginSQL, err := capabilities.beginSQL(options.Mode)
	if err != nil {
		return withOperation(err, options.Operation)
	}
	cleanupTimeout := options.CleanupTimeout
	if cleanupTimeout <= 0 {
		cleanupTimeout = time.Second
	}

	return db.WithContext(ctx).Connection(func(connection *gorm.DB) error {
		sqlConn, ok := connection.Statement.ConnPool.(*sql.Conn)
		if !ok {
			return newError(
				options.Operation,
				PhaseConnection,
				fmt.Errorf("expected *sql.Conn, got %T", connection.Statement.ConnPool),
			)
		}

		if _, err := sqlConn.ExecContext(ctx, beginSQL); err != nil {
			return errors.Join(
				newError(options.Operation, PhaseBegin, err),
				discardBadConnection(options.Operation, sqlConn, err),
			)
		}

		transaction := connection.Session(&gorm.Session{
			NewDB: true, SkipDefaultTransaction: true, Context: ctx,
		})
		active := true
		defer func() {
			if active {
				_ = rollback(options.Operation, sqlConn, cleanupTimeout, false)
			}
		}()

		if err := callback(transaction); err != nil {
			cleanupErr := rollback(options.Operation, sqlConn, cleanupTimeout, false)
			active = false
			if options.Mode == ReadSnapshot {
				if parentErr := ctx.Err(); parentErr != nil {
					return errors.Join(parentErr, cleanupErr)
				}
			}
			return errors.Join(err, cleanupErr)
		}
		if options.Mode == ReadSnapshot {
			if parentErr := ctx.Err(); parentErr != nil {
				cleanupErr := rollback(options.Operation, sqlConn, cleanupTimeout, false)
				active = false
				return errors.Join(parentErr, cleanupErr)
			}
		}

		if _, err := sqlConn.ExecContext(ctx, "COMMIT"); err != nil {
			commitErr := newError(options.Operation, PhaseCommit, err)
			cleanupErr := rollback(options.Operation, sqlConn, cleanupTimeout, true)
			active = false
			if options.Mode == ReadSnapshot {
				if parentErr := ctx.Err(); parentErr != nil {
					return errors.Join(parentErr, cleanupErr)
				}
			}
			return errors.Join(commitErr, cleanupErr)
		}
		active = false
		return nil
	})
}

func (capabilities Capabilities) beginSQL(mode Mode) (string, error) {
	beginMode := capabilities.WriteBegin
	if mode == ReadSnapshot {
		beginMode = capabilities.ReadBegin
	} else if mode != Write {
		return "", &Error{
			Phase: PhaseInput,
			Err:   fmt.Errorf("unsupported transaction mode %d", mode),
		}
	}

	switch beginMode {
	case BeginStandard:
		// SQLite BEGIN is its deferred transaction form and retains the
		// existing read-snapshot behavior without exposing DEFERRED SQL to
		// every caller.
		return "BEGIN", nil
	case BeginSQLiteImmediate:
		return "BEGIN IMMEDIATE", nil
	case BeginMySQLConsistentSnapshot:
		return "START TRANSACTION WITH CONSISTENT SNAPSHOT", nil
	case BeginPostgresRepeatableRead:
		return "BEGIN ISOLATION LEVEL REPEATABLE READ", nil
	default:
		return "", &Error{
			Phase: PhaseDriver,
			Err:   fmt.Errorf("unsupported transaction begin mode %q", beginMode),
		}
	}
}

func rollback(
	operation string,
	sqlConn *sql.Conn,
	cleanupTimeout time.Duration,
	discardAlways bool,
) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	_, rollbackErr := sqlConn.ExecContext(cleanupCtx, "ROLLBACK")

	var discardErr error
	if rollbackErr != nil || discardAlways {
		discardErr = discardConnection(operation, sqlConn)
	}
	var result []error
	if rollbackErr != nil {
		result = append(result, newError(operation, PhaseRollback, rollbackErr))
	}
	if discardErr != nil {
		result = append(result, discardErr)
	}
	return errors.Join(result...)
}

func discardBadConnection(
	operation string,
	sqlConn *sql.Conn,
	err error,
) error {
	if !errors.Is(err, driver.ErrBadConn) {
		return nil
	}
	return discardConnection(operation, sqlConn)
}

func discardConnection(operation string, sqlConn *sql.Conn) error {
	err := sqlConn.Raw(func(any) error { return driver.ErrBadConn })
	if err == nil || errors.Is(err, driver.ErrBadConn) {
		return nil
	}
	return newError(operation, PhaseDiscard, err)
}

func newError(operation string, phase Phase, err error) error {
	return &Error{Operation: operation, Phase: phase, Err: err}
}

func withOperation(err error, operation string) error {
	var transactionErr *Error
	if !errors.As(err, &transactionErr) || transactionErr.Operation != "" {
		return err
	}
	transactionErr.Operation = operation
	return err
}
