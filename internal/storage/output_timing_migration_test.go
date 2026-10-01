package storage

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"
)

func TestOutputTimingMigrationContract(t *testing.T) {
	testOutputTimingMigration(t, openInternalMigrationTestDatabase)
}

func TestExternalOutputTimingMigrationContract(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testOutputTimingMigration(t, func(t *testing.T) *gorm.DB { return openExternalIncrementalMigrationDatabase(t, dsn) })
}

func testOutputTimingMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
	for _, scenario := range []string{"fresh", "upgrade", "interrupted_first_column", "interrupted_complete"} {
		t.Run(scenario, func(t *testing.T) {
			db := open(t)
			if len(migrations) < 24 {
				t.Fatal("output timing migration is missing")
			}
			if scenario != "fresh" {
				if err := applyMigrationRegistry(db, migrations[:23]); err != nil {
					t.Fatal(err)
				}
				if err := db.Table("request_logs").Create(map[string]any{
					"id": "existing", "completed_at_ms": 1, "access_key_id": 1, "protocol": "openai-responses", "client_model": "test", "upstream_model": "test", "status": "success", "status_code": 200, "duration_ms": 900, "first_response_ms": 50, "output_tokens": 100, "error_summary": "",
				}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "interrupted_first_column" || scenario == "interrupted_complete" {
				entry := migrations[23]
				up := entry.Up
				entry.Up = func(tx *gorm.DB) error {
					if scenario == "interrupted_first_column" {
						if err := tx.Exec("ALTER TABLE request_logs ADD COLUMN first_output_ms BIGINT NULL CONSTRAINT chk_request_log_first_output CHECK (first_output_ms >= 0)").Error; err != nil {
							return err
						}
					} else if err := up(tx); err != nil {
						return err
					}
					return fmt.Errorf("interrupt output timing DDL")
				}
				registry := append(append([]migration(nil), migrations[:23]...), entry)
				if err := applyMigrationRegistry(db, registry); err == nil {
					t.Fatal("expected migration interruption")
				}
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"first_output_ms", "last_output_ms"} {
				if !db.Migrator().HasColumn("request_logs", name) {
					t.Fatalf("missing column %s", name)
				}
			}
			if scenario != "fresh" {
				var row struct {
					FirstOutputMs, LastOutputMs               *int64
					FirstResponseMs, DurationMs, OutputTokens int64
				}
				if err := db.Table("request_logs").Where("id = ?", "existing").Take(&row).Error; err != nil {
					t.Fatal(err)
				}
				if row.FirstOutputMs != nil || row.LastOutputMs != nil || row.FirstResponseMs != 50 || row.DurationMs != 900 || row.OutputTokens != 100 {
					t.Fatalf("historical data changed: %+v", row)
				}
				if err := db.Table("request_logs").Where("id = ?", "existing").Updates(map[string]any{"first_output_ms": 100, "last_output_ms": 800}).Error; err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"first_output_ms", "last_output_ms"} {
					if err := db.Table("request_logs").Where("id = ?", "existing").Update(name, -1).Error; err == nil {
						t.Fatalf("negative %s accepted", name)
					}
				}
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
		})
	}
}
