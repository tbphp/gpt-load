package storage

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"
)

func TestOperationIndexMigrationContract(t *testing.T) {
	testOperationIndexMigration(t, openInternalMigrationTestDatabase)
}

func TestExternalOperationIndexMigrationContract(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testOperationIndexMigration(t, func(t *testing.T) *gorm.DB {
		return openExternalIncrementalMigrationDatabase(t, dsn)
	})
}

func testOperationIndexMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
	t.Helper()
	for _, scenario := range []string{"fresh", "existing", "interrupted"} {
		t.Run(scenario, func(t *testing.T) {
			db := open(t)
			if scenario != "fresh" {
				if err := applyMigrationRegistry(db, migrations[:len(migrations)-1]); err != nil {
					t.Fatal(err)
				}
				if err := db.Table("request_logs").Create(map[string]any{
					"id": "existing", "completed_at_ms": 1, "access_key_id": 1,
					"protocol": "openai-responses", "operation": "responses_retrieve",
					"client_model": "test", "upstream_model": "test", "status": "error",
					"status_code": 500, "duration_ms": 1, "error_summary": "existing diagnostic",
				}).Error; err != nil {
					t.Fatal(err)
				}
				if scenario == "interrupted" {
					registry := append([]migration(nil), migrations...)
					up := registry[len(registry)-1].Up
					registry[len(registry)-1].Up = func(tx *gorm.DB) error {
						if err := up(tx); err != nil {
							return err
						}
						return fmt.Errorf("interrupt after operation index DDL")
					}
					if err := applyMigrationRegistry(db, registry); err == nil {
						t.Fatal("expected migration interruption")
					}
				}
			}
			for range 2 {
				if err := AutoMigrate(db); err != nil {
					t.Fatal(err)
				}
				if !db.Migrator().HasIndex("request_logs", "idx_request_logs_operation_completed_id") {
					t.Fatal("operation cursor index is missing")
				}
				if scenario != "fresh" {
					var operation string
					if err := db.Table("request_logs").Where("id = ?", "existing").Pluck("operation", &operation).Error; err != nil || operation != "responses_retrieve" {
						t.Fatalf("existing operation = %q, error = %v", operation, err)
					}
				}
			}
		})
	}
	t.Run("unexpected index definition", func(t *testing.T) {
		db := open(t)
		if err := applyMigrationRegistry(db, migrations[:len(migrations)-1]); err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("CREATE INDEX idx_request_logs_operation_completed_id ON request_logs (status)").Error; err != nil {
			t.Fatal(err)
		}
		if err := AutoMigrate(db); err == nil {
			t.Fatal("unexpected operation index definition accepted")
		}
	})
	t.Run("unexpected index direction", func(t *testing.T) {
		db := open(t)
		if err := applyMigrationRegistry(db, migrations[:len(migrations)-1]); err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(
			"CREATE INDEX idx_request_logs_operation_completed_id ON request_logs (operation, completed_at_ms DESC, id ASC)",
		).Error; err != nil {
			t.Fatal(err)
		}
		if err := AutoMigrate(db); err == nil {
			t.Fatal("unexpected operation index direction accepted")
		}
	})
}
