package storage

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"
)

func TestClientIPMigrationContract(t *testing.T) {
	testClientIPMigration(t, openInternalMigrationTestDatabase)
}

func TestExternalClientIPMigrationContract(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testClientIPMigration(t, func(t *testing.T) *gorm.DB { return openExternalIncrementalMigrationDatabase(t, dsn) })
}

func testClientIPMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
	for _, scenario := range []string{"fresh", "upgrade", "interrupted_column", "interrupted_complete"} {
		t.Run(scenario, func(t *testing.T) {
			db := open(t)
			if len(migrations) < 26 {
				t.Fatal("client IP migration is missing")
			}
			if scenario != "fresh" {
				if err := applyMigrationRegistry(db, migrations[:25]); err != nil {
					t.Fatal(err)
				}
				if err := db.Table("request_logs").Create(map[string]any{
					"id": "existing", "completed_at_ms": 1, "access_key_id": 1, "protocol": "openai-responses", "client_model": "test", "upstream_model": "test", "status": "success", "status_code": 200, "duration_ms": 900, "error_summary": "",
				}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "interrupted_column" || scenario == "interrupted_complete" {
				entry := migrations[25]
				up := entry.Up
				entry.Up = func(tx *gorm.DB) error {
					if scenario == "interrupted_column" {
						if err := tx.Exec("ALTER TABLE request_logs ADD COLUMN client_ip VARCHAR(45) NULL").Error; err != nil {
							return err
						}
					} else if err := up(tx); err != nil {
						return err
					}
					return fmt.Errorf("interrupt client IP DDL")
				}
				registry := append(append([]migration(nil), migrations[:25]...), entry)
				if err := applyMigrationRegistry(db, registry); err == nil {
					t.Fatal("expected migration interruption")
				}
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
			if !db.Migrator().HasColumn("request_logs", "client_ip") || !db.Migrator().HasIndex("request_logs", "idx_request_logs_ip_completed_id") {
				t.Fatal("client IP schema is incomplete")
			}
			if scenario != "fresh" {
				var row struct {
					ClientIP   *string
					DurationMs int64
				}
				if err := db.Table("request_logs").Where("id = ?", "existing").Take(&row).Error; err != nil {
					t.Fatal(err)
				}
				if row.ClientIP != nil || row.DurationMs != 900 {
					t.Fatalf("historical data changed: %+v", row)
				}
				for _, ip := range []string{"192.0.2.1", "2001:db8:1234:5678:9abc:def0:1234:5678"} {
					if err := db.Table("request_logs").Where("id = ?", "existing").Update("client_ip", ip).Error; err != nil {
						t.Fatal(err)
					}
					var count int64
					if err := db.Table("request_logs").Where("client_ip = ?", ip).Count(&count).Error; err != nil || count != 1 {
						t.Fatalf("exact IP query count=%d, error=%v", count, err)
					}
				}
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
		})
	}
}
