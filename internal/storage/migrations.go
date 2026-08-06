package storage

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const migrationLedgerTable = "schema_migrations"

type migration struct {
	ID string
	Up func(*gorm.DB) error
}

var migrations = []migration{
	{
		ID: "0001_initial_v2",
		Up: func(db *gorm.DB) error {
			if err := createInitialV2Tables(db); err != nil {
				return err
			}
			return createInitialV2Indexes(db)
		},
	},
}

func applyMigrations(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("apply SQLite migrations: db is nil")
	}
	if !db.Migrator().HasTable(migrationLedgerTable) {
		tables, err := db.Migrator().GetTables()
		if err != nil {
			return fmt.Errorf("list SQLite tables: %w", err)
		}
		for _, table := range tables {
			if !strings.HasPrefix(table, "sqlite_") {
				return fmt.Errorf("initialize SQLite schema: non-empty database without schema_migrations")
			}
		}
		if err := db.Exec(`CREATE TABLE schema_migrations (id varchar(255) PRIMARY KEY NOT NULL)`).Error; err != nil {
			return fmt.Errorf("create schema_migrations: %w", err)
		}
	}

	var applied []string
	if err := db.Table(migrationLedgerTable).Order("id ASC").Pluck("id", &applied).Error; err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	for index, id := range applied {
		if index >= len(migrations) || migrations[index].ID != id {
			return fmt.Errorf("schema_migrations contains unknown or non-contiguous migration %q", id)
		}
	}
	for _, entry := range migrations[len(applied):] {
		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := entry.Up(tx); err != nil {
				return fmt.Errorf("apply migration %s: %w", entry.ID, err)
			}
			if err := tx.Exec(`INSERT INTO schema_migrations(id) VALUES (?)`, entry.ID).Error; err != nil {
				return fmt.Errorf("record migration %s: %w", entry.ID, err)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return validateMigrationForeignKeys(db)
}
