package storage

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"gpt-load/internal/storage/dbtx"
	migrationfiles "gpt-load/internal/storage/migrations"
)

const migrationLedgerTable = "schema_migrations"

var migrationIDPattern = regexp.MustCompile(`^(\d{4})_[a-z0-9]+(?:_[a-z0-9]+)*$`)

type schemaMigration struct {
	ID string `gorm:"column:id;type:varchar(255);primaryKey;not null"`
}

func (schemaMigration) TableName() string {
	return migrationLedgerTable
}

type migration struct {
	ID                  string
	Up                  func(*gorm.DB) error
	Validate            func(*gorm.DB) error
	ValidateRecoverable func(*gorm.DB) error
}

var migrations = []migration{
	{
		ID:                  migrationfiles.ID0001,
		Up:                  migrationfiles.Up0001,
		Validate:            migrationfiles.Validate0001,
		ValidateRecoverable: migrationfiles.ValidateRecoverable0001,
	},
	{
		ID:                  migrationfiles.ID0002,
		Up:                  migrationfiles.Up0002,
		Validate:            migrationfiles.Validate0002,
		ValidateRecoverable: migrationfiles.ValidateRecoverable0002,
	},
	{
		ID:                  migrationfiles.ID0003,
		Up:                  migrationfiles.Up0003,
		Validate:            migrationfiles.Validate0003,
		ValidateRecoverable: migrationfiles.ValidateRecoverable0003,
	},
}

func applyMigrations(db *gorm.DB) error {
	return applyMigrationRegistry(db, migrations)
}

func applyMigrationRegistry(db *gorm.DB, entries []migration) error {
	if db == nil {
		return fmt.Errorf("apply migrations: db is nil")
	}
	if db.Dialector == nil {
		return fmt.Errorf("apply migrations: database dialector is nil")
	}
	if err := validateMigrationRegistry(entries); err != nil {
		return err
	}

	switch strings.ToLower(db.Dialector.Name()) {
	case "sqlite":
		// SQLite has no advisory-lock API. BEGIN IMMEDIATE pins a connection and
		// serializes competing writers before any schema inspection occurs.
		return dbtx.Run(context.Background(), db, dbtx.Options{
			Mode:      dbtx.Write,
			Operation: "database migration",
		}, func(transaction *gorm.DB) error {
			return applyMigrationsLocked(transaction, entries, false)
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
				entries,
				true,
			)
			releaseErr := releaseMigrationLock(connection.Session(&gorm.Session{NewDB: true}))
			return errors.Join(operationErr, releaseErr)
		})
	default:
		return fmt.Errorf("apply migrations: unsupported database driver %q", db.Dialector.Name())
	}
}

func validateMigrationRegistry(entries []migration) error {
	for index, entry := range entries {
		position := index + 1
		matches := migrationIDPattern.FindStringSubmatch(entry.ID)
		if len(matches) != 2 {
			return fmt.Errorf("migration registry entry %d has invalid ID %q", position, entry.ID)
		}
		number, err := strconv.Atoi(matches[1])
		if err != nil || number != position {
			return fmt.Errorf(
				"migration registry entry %d has non-contiguous ID %q",
				position,
				entry.ID,
			)
		}
		if entry.Up == nil || entry.Validate == nil || entry.ValidateRecoverable == nil {
			return fmt.Errorf("migration registry entry %d (%s) is incomplete", position, entry.ID)
		}
	}
	return nil
}

func applyMigrationsLocked(db *gorm.DB, entries []migration, useMigrationTransactions bool) error {

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
		if lastIndex < len(entries) &&
			applied[lastIndex] == migrationResumeMarker(entries[lastIndex].ID) {
			resumeMarker = true
			applied = applied[:lastIndex]
		}
	}
	for index, id := range applied {
		if index >= len(entries) || entries[index].ID != id {
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

	for _, entry := range entries[len(applied):] {
		if err := applyMigration(db, entry, useMigrationTransactions); err != nil {
			return err
		}
	}
	return validateMigrationForeignKeys(db)
}

func applyMigration(db *gorm.DB, entry migration, useMigrationTransactions bool) error {
	if strings.EqualFold(db.Dialector.Name(), "mysql") {
		return applyMySQLMigration(db, entry)
	}
	apply := func(tx *gorm.DB) error {
		if err := entry.Up(tx); err != nil {
			return fmt.Errorf("apply migration %s: %w", entry.ID, err)
		}
		if entry.Validate != nil {
			if err := entry.Validate(tx); err != nil {
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
