package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/platform/dbtx"
)

const (
	migrationLedgerTable                       = "schema_migrations"
	initialV2MigrationID                       = "0001_final_v2"
	requestLogExecutionObservationMigrationID  = "0002_request_log_execution_observation"
	requestLogConversionTraceMigrationID       = "0003_request_log_conversion_trace"
	removeRequestLogConversionTraceMigrationID = "0004_remove_request_log_conversion_trace"
)

const (
	migrationLockName    = "gpt-load:schema-migrations:v2"
	migrationLockTimeout = time.Minute
)

type schemaMigration struct {
	ID string `gorm:"column:id;type:varchar(255);primaryKey;not null"`
}

func (schemaMigration) TableName() string {
	return migrationLedgerTable
}

type migration struct {
	ID string
	Up func(*gorm.DB) error
}

var migrations = []migration{
	{
		ID: initialV2MigrationID,
		Up: createInitialV2Tables,
	},
	{
		ID: requestLogExecutionObservationMigrationID,
		Up: addRequestLogExecutionObservation,
	},
	{
		ID: requestLogConversionTraceMigrationID,
		Up: addRequestLogConversionTrace,
	},
	{
		ID: removeRequestLogConversionTraceMigrationID,
		Up: removeRequestLogConversionTrace,
	},
}

func applyMigrations(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("apply migrations: db is nil")
	}
	if db.Dialector == nil {
		return fmt.Errorf("apply migrations: database dialector is nil")
	}

	switch strings.ToLower(db.Dialector.Name()) {
	case "sqlite":
		// SQLite has no advisory-lock API. BEGIN IMMEDIATE pins a connection and
		// serializes competing writers before any schema inspection occurs.
		return dbtx.Run(context.Background(), db, dbtx.Options{
			Mode:      dbtx.Write,
			Operation: "database migration",
		}, func(transaction *gorm.DB) error {
			return applyMigrationsLocked(transaction, false)
		})
	case "mysql", "postgres", "postgresql":
		return db.Connection(func(connection *gorm.DB) error {
			if err := acquireMigrationLock(connection); err != nil {
				return err
			}
			// Raw lock acquisition and Scan may populate GORM's statement schema
			// with the scalar result type. Start fresh sessions for migration and
			// release so that state cannot leak into the schema/table operations.
			operationErr := applyMigrationsLocked(
				connection.Session(&gorm.Session{NewDB: true}),
				true,
			)
			releaseErr := releaseMigrationLock(connection.Session(&gorm.Session{NewDB: true}))
			return errors.Join(operationErr, releaseErr)
		})
	default:
		return fmt.Errorf("apply migrations: unsupported database driver %q", db.Dialector.Name())
	}
}

func applyMigrationsLocked(db *gorm.DB, useMigrationTransactions bool) error {

	if !db.Migrator().HasTable(migrationLedgerTable) {
		tables, err := db.Migrator().GetTables()
		if err != nil {
			return fmt.Errorf("list database tables: %w", err)
		}
		for _, table := range tables {
			if !isDatabaseSystemTable(db, table) {
				return fmt.Errorf("initialize database schema: non-empty database without schema_migrations")
			}
		}
		if err := db.AutoMigrate(&schemaMigration{}); err != nil {
			return fmt.Errorf("create schema_migrations: %w", err)
		}
	}

	var applied []string
	if err := db.Table(migrationLedgerTable).Order("id ASC").Pluck("id", &applied).Error; err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	resumeMarker := false
	if len(applied) > 0 {
		lastIndex := len(applied) - 1
		if lastIndex < len(migrations) &&
			applied[lastIndex] == migrationResumeMarker(migrations[lastIndex].ID) {
			resumeMarker = true
			applied = applied[:lastIndex]
		}
	}
	for index, id := range applied {
		if index >= len(migrations) || migrations[index].ID != id {
			return fmt.Errorf("schema_migrations contains unknown or non-contiguous migration %q", id)
		}
	}
	if len(applied) == 0 && !resumeMarker {
		tables, err := db.Migrator().GetTables()
		if err != nil {
			return fmt.Errorf("list database tables before baseline migration: %w", err)
		}
		for _, table := range tables {
			if strings.EqualFold(table, migrationLedgerTable) || isDatabaseSystemTable(db, table) {
				continue
			}
			return fmt.Errorf("initialize database schema: empty migration ledger beside existing tables")
		}
	}

	for _, entry := range migrations[len(applied):] {
		if err := applyMigration(db, entry, useMigrationTransactions); err != nil {
			return err
		}
	}
	return validateMigrationForeignKeys(db)
}

func applyMigration(db *gorm.DB, entry migration, useMigrationTransactions bool) error {
	if strings.EqualFold(db.Dialector.Name(), "mysql") && entry.ID == initialV2MigrationID {
		return applyMySQLInitialV2Migration(db, entry)
	}
	apply := func(tx *gorm.DB) error {
		if err := entry.Up(tx); err != nil {
			return fmt.Errorf("apply migration %s: %w", entry.ID, err)
		}
		if entry.ID == initialV2MigrationID {
			if err := validateInitialV2Schema(tx); err != nil {
				return fmt.Errorf("validate migration %s: %w", entry.ID, err)
			}
		}
		if err := tx.Create(&schemaMigration{ID: entry.ID}).Error; err != nil {
			return fmt.Errorf("record migration %s: %w", entry.ID, err)
		}
		return nil
	}

	// MySQL DDL implicitly commits. Running the DDL and ledger insert in a
	// GORM transaction would therefore make the final Commit fail with an
	// already-committed transaction. PostgreSQL and SQLite retain transactional
	// DDL, so preserve their all-or-nothing migration behavior.
	if !useMigrationTransactions || strings.EqualFold(db.Dialector.Name(), "mysql") {
		return apply(db)
	}
	if err := db.Transaction(apply); err != nil {
		return err
	}
	return nil
}

func applyMySQLInitialV2Migration(db *gorm.DB, entry migration) error {
	marker := migrationResumeMarker(entry.ID)
	var markerCount int64
	if err := db.Model(&schemaMigration{}).Where("id = ?", marker).Count(&markerCount).Error; err != nil {
		return fmt.Errorf("inspect MySQL migration %s marker: %w", entry.ID, err)
	}
	if markerCount > 1 {
		return fmt.Errorf("apply MySQL migration %s: invalid resume marker count", entry.ID)
	}
	if markerCount == 0 {
		if err := db.Create(&schemaMigration{ID: marker}).Error; err != nil {
			return fmt.Errorf("record MySQL migration %s resume marker: %w", entry.ID, err)
		}
	} else if err := validateRecoverableInitialV2Schema(db); err != nil {
		return fmt.Errorf("apply MySQL migration %s: unsafe interrupted baseline: %w", entry.ID, err)
	}

	if err := entry.Up(db); err != nil {
		return fmt.Errorf("apply migration %s: %w", entry.ID, err)
	}
	if err := validateInitialV2Schema(db); err != nil {
		return fmt.Errorf("validate migration %s: %w", entry.ID, err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&schemaMigration{}, "id = ?", marker).Error; err != nil {
			return err
		}
		return tx.Create(&schemaMigration{ID: entry.ID}).Error
	}); err != nil {
		return fmt.Errorf("finalize MySQL migration %s: %w", entry.ID, err)
	}
	return nil
}

func migrationResumeMarker(id string) string {
	return id + "#building"
}

func acquireMigrationLock(db *gorm.DB) error {
	switch strings.ToLower(db.Dialector.Name()) {
	case "mysql":
		var result sql.NullInt64
		if err := db.Raw("SELECT GET_LOCK(?, ?)", migrationLockName, int(migrationLockTimeout/time.Second)).Scan(&result).Error; err != nil {
			return fmt.Errorf("acquire MySQL migration lock: %w", err)
		}
		if !result.Valid || result.Int64 != 1 {
			return fmt.Errorf("acquire MySQL migration lock: timed out")
		}
		return nil
	case "postgres", "postgresql":
		if err := db.Exec("SELECT pg_advisory_lock(hashtext(?))", migrationLockName).Error; err != nil {
			return fmt.Errorf("acquire PostgreSQL migration lock: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("acquire migration lock: unsupported database driver %q", db.Dialector.Name())
	}
}

func releaseMigrationLock(db *gorm.DB) error {
	switch strings.ToLower(db.Dialector.Name()) {
	case "mysql":
		var result sql.NullInt64
		if err := db.Raw("SELECT RELEASE_LOCK(?)", migrationLockName).Scan(&result).Error; err != nil {
			return fmt.Errorf("release MySQL migration lock: %w", err)
		}
		if !result.Valid || result.Int64 != 1 {
			return fmt.Errorf("release MySQL migration lock: lock was not held")
		}
		return nil
	case "postgres", "postgresql":
		var released bool
		if err := db.Raw("SELECT pg_advisory_unlock(hashtext(?))", migrationLockName).Scan(&released).Error; err != nil {
			return fmt.Errorf("release PostgreSQL migration lock: %w", err)
		}
		if !released {
			return fmt.Errorf("release PostgreSQL migration lock: lock was not held")
		}
		return nil
	default:
		return fmt.Errorf("release migration lock: unsupported database driver %q", db.Dialector.Name())
	}
}

func isDatabaseSystemTable(db *gorm.DB, table string) bool {
	if db == nil || db.Dialector == nil {
		return false
	}
	if strings.EqualFold(db.Dialector.Name(), "sqlite") {
		return strings.HasPrefix(strings.ToLower(table), "sqlite_")
	}
	return false
}

// AutoMigrate applies every pending migration before the application starts.
func AutoMigrate(db *gorm.DB) error {
	return applyMigrations(db)
}
