package storage_test

import (
	"testing"

	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
)

func TestAutoMigrateUpgradesLegacyRequestLogColumns(t *testing.T) {
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
		id varchar(36) PRIMARY KEY NOT NULL,
		status varchar(32) NOT NULL DEFAULT '',
		upstream_model varchar(255) NOT NULL DEFAULT ''
	)`).Error; err != nil {
		t.Fatalf("create legacy request_logs: %v", err)
	}
	if err := db.Exec(`INSERT INTO request_logs(id, status, upstream_model) VALUES
		('success-modeled', 'success', 'model-a'),
		('success-model-less', 'success', ''),
		('failed-modeled', 'error', 'model-a')`).Error; err != nil {
		t.Fatalf("seed legacy request logs: %v", err)
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
		"UpstreamReportedModel", "ModelConsistency",
	} {
		if !db.Migrator().HasColumn(&models.RequestLog{}, field) {
			t.Errorf("legacy upgrade did not add request_logs.%s", field)
		}
	}
	if !db.Migrator().HasConstraint(&models.RequestLog{}, "chk_request_log_model_consistency") {
		t.Fatal("legacy upgrade did not create the request log model consistency constraint")
	}
	var migrationCount int64
	if err := db.Table("schema_migrations").Count(&migrationCount).Error; err != nil {
		t.Fatalf("count migration ledger: %v", err)
	}
	if migrationCount != 5 {
		t.Fatalf("migration ledger count = %d, want 5", migrationCount)
	}
	var consistencyByID map[string]string
	var rows []struct {
		ID               string
		ModelConsistency string
	}
	if err := db.Table("request_logs").
		Select("id, model_consistency").
		Order("id ASC").
		Scan(&rows).Error; err != nil {
		t.Fatalf("read upgraded request logs: %v", err)
	}
	consistencyByID = make(map[string]string, len(rows))
	for _, row := range rows {
		consistencyByID[row.ID] = row.ModelConsistency
	}
	if consistencyByID["success-modeled"] != "unknown" ||
		consistencyByID["success-model-less"] != "not_applicable" ||
		consistencyByID["failed-modeled"] != "not_applicable" {
		t.Fatalf("upgraded model consistency = %#v", consistencyByID)
	}
	if err := db.Model(&models.RequestLog{}).
		Where("id = ?", "success-modeled").
		Update("model_consistency", "invalid").Error; err == nil {
		t.Fatal("legacy upgrade accepted an invalid request log model consistency value")
	}

	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("second AutoMigrate() error = %v", err)
	}
	if err := db.Table("schema_migrations").Count(&migrationCount).Error; err != nil {
		t.Fatalf("count migration ledger after second run: %v", err)
	}
	if migrationCount != 5 {
		t.Fatalf("migration ledger count after second run = %d, want 5", migrationCount)
	}
}
