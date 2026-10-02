package storage

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"
)

func TestRemoveOutputTimingMigrationContract(t *testing.T) {
	testRemoveOutputTimingMigration(t, openInternalMigrationTestDatabase)
}

func TestExternalRemoveOutputTimingMigrationContract(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testRemoveOutputTimingMigration(t, func(t *testing.T) *gorm.DB { return openExternalIncrementalMigrationDatabase(t, dsn) })
}

func testRemoveOutputTimingMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
	for _, version := range []int{0, 23, 24, 25, 26} {
		t.Run(fmt.Sprintf("upgrade_%d", version), func(t *testing.T) {
			db := open(t)
			if version > 0 {
				if err := applyMigrationRegistry(db, migrations[:version]); err != nil {
					t.Fatal(err)
				}
				seedTimingCleanupLog(t, db)
			}
			for range 2 {
				if err := AutoMigrate(db); err != nil {
					t.Fatal(err)
				}
				assertOutputTimingRemoved(t, db)
			}
			if version > 0 {
				assertTimingCleanupData(t, db)
			}
		})
	}
	if len(migrations) < 27 {
		t.Fatal("cleanup migration is missing")
	}
	for _, columns := range []int{1, 2} {
		t.Run(fmt.Sprintf("interrupted_%d_columns", columns), func(t *testing.T) {
			db := open(t)
			if err := applyMigrationRegistry(db, migrations[:26]); err != nil {
				t.Fatal(err)
			}
			seedTimingCleanupLog(t, db)
			entry := migrations[26]
			entry.Up = func(tx *gorm.DB) error {
				for _, name := range []string{"first_output", "last_output"}[:columns] {
					statement := "ALTER TABLE request_logs DROP COLUMN " + name + "_ms"
					if tx.Dialector.Name() == "mysql" {
						statement = "ALTER TABLE request_logs DROP CHECK chk_request_log_" + name + ", DROP COLUMN " + name + "_ms"
					}
					if err := tx.Exec(statement).Error; err != nil {
						return err
					}
				}
				return fmt.Errorf("interrupt cleanup DDL")
			}
			registry := append(append([]migration(nil), migrations[:26]...), entry)
			if err := applyMigrationRegistry(db, registry); err == nil {
				t.Fatal("expected interruption")
			}
			for range 2 {
				if err := AutoMigrate(db); err != nil {
					t.Fatal(err)
				}
			}
			assertOutputTimingRemoved(t, db)
			assertTimingCleanupData(t, db)
		})
	}
	t.Run("unexplained_missing_column", func(t *testing.T) {
		db := open(t)
		if err := applyMigrationRegistry(db, migrations[:26]); err != nil {
			t.Fatal(err)
		}
		statement := "ALTER TABLE request_logs DROP COLUMN first_output_ms"
		if db.Dialector.Name() == "mysql" {
			statement = "ALTER TABLE request_logs DROP CHECK chk_request_log_first_output, DROP COLUMN first_output_ms"
		}
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
		if err := AutoMigrate(db); err == nil {
			t.Fatal("missing column without migration evidence accepted")
		}
	})
}

func seedTimingCleanupLog(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Table("request_logs").Create(map[string]any{
		"id": "timing-history", "completed_at_ms": 1000, "access_key_id": 1, "protocol": "openai-responses", "client_model": "model", "upstream_model": "model", "status": "success", "status_code": 200, "duration_ms": 900, "first_response_ms": 50, "output_tokens": 100, "error_summary": "", "usage_state": "complete", "cost_state": "priced", "pricing_completeness": "complete", "estimated_cost_nano_usd": 123,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("request_log_attempts").Create(map[string]any{
		"request_id": "timing-history", "sequence": 1, "completed_at_ms": 1000, "group_id": 1, "group_name": "group", "credential_id": 1, "status_code": 200, "duration_ms": 900, "failure_category": "ok", "action": "terminate", "error_summary": "", "pricing_receipt": "{\"preserved\":true}",
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func assertOutputTimingRemoved(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, name := range []string{"first_output", "last_output"} {
		if db.Migrator().HasColumn("request_logs", name+"_ms") || db.Migrator().HasConstraint("request_logs", "chk_request_log_"+name) {
			t.Fatalf("obsolete %s remains", name)
		}
	}
}

func assertTimingCleanupData(t *testing.T, db *gorm.DB) {
	t.Helper()
	var row struct{ FirstResponseMs, DurationMs, OutputTokens, EstimatedCostNanoUSD int64 }
	if err := db.Table("request_logs").Where("id = ?", "timing-history").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.FirstResponseMs != 50 || row.DurationMs != 900 || row.OutputTokens != 100 || row.EstimatedCostNanoUSD != 123 {
		t.Fatalf("historical data changed: %+v", row)
	}
	var count int64
	if err := db.Table("request_log_attempts").Where("request_id = ?", "timing-history").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("attempt lost: %d %v", count, err)
	}
	var receipt string
	if err := db.Table("request_log_attempts").Where("request_id = ?", "timing-history").Pluck("pricing_receipt", &receipt).Error; err != nil {
		t.Fatal(err)
	}
	if receipt != `{"preserved":true}` && receipt != `{"preserved": true}` {
		t.Fatalf("receipt changed: %s", receipt)
	}
}
