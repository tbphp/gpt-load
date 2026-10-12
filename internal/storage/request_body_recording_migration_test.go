package storage

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"
)

func TestRequestBodyRecordingMigrationContract(t *testing.T) {
	t.Parallel()

	testRequestBodyRecordingMigration(t, openInternalMigrationTestDatabase)
}

func TestExternalRequestBodyRecordingMigrationContract(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testRequestBodyRecordingMigration(t, func(t *testing.T) *gorm.DB { return openExternalIncrementalMigrationDatabase(t, dsn) })
}

func testRequestBodyRecordingMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
	for _, scenario := range []string{"fresh", "upgrade", "interrupted_column", "interrupted_complete"} {
		t.Run(scenario, func(t *testing.T) {
			db := open(t)
			if db.Dialector.Name() == "sqlite" {
				t.Parallel()
			}
			if len(migrations) < 30 {
				t.Fatal("request body recording migration is missing")
			}
			if scenario != "fresh" {
				if err := applyMigrationRegistry(db, migrations[:29]); err != nil {
					t.Fatal(err)
				}
				if err := db.Table("access_keys").Create(map[string]any{"id": 1, "name": "existing", "key_value": "test-cipher", "key_hash": "test-hash", "key_suffix": "cafe", "status": "active", "created_at_ms": 1, "updated_at_ms": 1}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "interrupted_column" || scenario == "interrupted_complete" {
				entry := migrations[29]
				up := entry.Up
				entry.Up = func(tx *gorm.DB) error {
					if scenario == "interrupted_column" {
						if err := tx.Exec("ALTER TABLE access_keys ADD COLUMN record_request_body BOOLEAN NOT NULL DEFAULT FALSE").Error; err != nil {
							return err
						}
					} else if err := up(tx); err != nil {
						return err
					}
					return fmt.Errorf("interrupt request body recording DDL")
				}
				registry := append(append([]migration(nil), migrations[:29]...), entry)
				if err := applyMigrationRegistry(db, registry); err == nil {
					t.Fatal("expected migration interruption")
				}
			}
			for range 2 {
				if err := AutoMigrate(db); err != nil {
					t.Fatal(err)
				}
			}
			if !db.Migrator().HasColumn("access_keys", "record_request_body") || !db.Migrator().HasTable("request_log_bodies") {
				t.Fatal("request body recording schema is incomplete")
			}
			if scenario != "fresh" {
				var row struct{ RecordRequestBody bool }
				if err := db.Table("access_keys").Select("record_request_body").Where("id = ?", 1).Take(&row).Error; err != nil || row.RecordRequestBody {
					t.Fatalf("existing access key recording = %v, error = %v", row.RecordRequestBody, err)
				}
			}
			assertRequestLogBodyContract(t, db)
		})
	}
}

func assertRequestLogBodyContract(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Table("request_logs").Create(map[string]any{
		"id": "logged", "completed_at_ms": 1, "access_key_id": 1, "protocol": "openai-responses", "client_model": "test", "upstream_model": "test", "status": "success", "status_code": 200, "duration_ms": 1, "error_summary": "",
	}).Error; err != nil {
		t.Fatal(err)
	}
	body := `{"model":"test"}`
	for name, values := range map[string]map[string]any{
		"unknown state":       {"request_id": "logged", "state": "bogus", "original_bytes": 1},
		"recorded state null": {"request_id": "logged", "state": "original", "original_bytes": 16},
		"skipped with body":   {"request_id": "logged", "state": "non_json", "original_bytes": 16, "body": body},
		"negative size":       {"request_id": "logged", "state": "too_large", "original_bytes": -1},
		"missing request log": {"request_id": "missing", "state": "too_large", "original_bytes": 1},
	} {
		if err := db.Table("request_log_bodies").Create(values).Error; err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	if err := db.Table("request_log_bodies").Create(map[string]any{
		"request_id": "logged", "state": "original", "original_bytes": len(body), "body": body,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DELETE FROM request_logs WHERE id = ?", "logged").Error; err != nil {
		t.Fatal(err)
	}
	var remaining int64
	if err := db.Table("request_log_bodies").Count(&remaining).Error; err != nil || remaining != 0 {
		t.Fatalf("request log bodies after cascade = %d, error = %v", remaining, err)
	}
}
