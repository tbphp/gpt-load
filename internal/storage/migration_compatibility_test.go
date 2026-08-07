package storage_test

import (
	"testing"

	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
)

func TestAutoMigrateUpgradesLegacyRequestLogReasoningMigration(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := db.Exec(`CREATE TABLE request_logs (
		id varchar(36) PRIMARY KEY NOT NULL
	)`).Error; err != nil {
		t.Fatalf("create legacy request_logs: %v", err)
	}
	if err := db.Exec(`CREATE TABLE schema_migrations (
		id varchar(255) PRIMARY KEY NOT NULL
	)`).Error; err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	if err := db.Exec("INSERT INTO schema_migrations(id) VALUES (?)", "0001_initial_v2").Error; err != nil {
		t.Fatalf("seed legacy migration ledger: %v", err)
	}

	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	for _, field := range []string{
		"ReasoningMode", "ReasoningEffort", "ReasoningBudgetTokens",
	} {
		if !db.Migrator().HasColumn(&models.RequestLog{}, field) {
			t.Errorf("legacy upgrade did not add request_logs.%s", field)
		}
	}
	var migrationCount int64
	if err := db.Table("schema_migrations").Count(&migrationCount).Error; err != nil {
		t.Fatalf("count migration ledger: %v", err)
	}
	if migrationCount != 3 {
		t.Fatalf("migration ledger count = %d, want 3", migrationCount)
	}

	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("second AutoMigrate() error = %v", err)
	}
	if err := db.Table("schema_migrations").Count(&migrationCount).Error; err != nil {
		t.Fatalf("count migration ledger after second run: %v", err)
	}
	if migrationCount != 3 {
		t.Fatalf("migration ledger count after second run = %d, want 3", migrationCount)
	}
}
