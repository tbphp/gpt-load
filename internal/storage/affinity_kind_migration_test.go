package storage

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"
)

func TestAffinityKindMigrationContract(t *testing.T) {
	testAffinityKindMigration(t, openInternalMigrationTestDatabase)
}
func TestExternalAffinityKindMigrationContract(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testAffinityKindMigration(t, func(t *testing.T) *gorm.DB { return openExternalIncrementalMigrationDatabase(t, dsn) })
}
func testAffinityKindMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
	for _, scenario := range []string{"fresh", "upgrade", "interrupted"} {
		t.Run(scenario, func(t *testing.T) {
			db := open(t)
			if scenario != "fresh" {
				if err := applyMigrationRegistry(db, migrations[:13]); err != nil {
					t.Fatal(err)
				}
				if err := db.Table("request_logs").Create(map[string]any{
					"id": "existing", "completed_at_ms": 1, "access_key_id": 1, "protocol": "openai-responses", "client_model": "test", "upstream_model": "test", "status": "success", "status_code": 200, "duration_ms": 1, "error_summary": "", "affinity_hit": true,
				}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "interrupted" && len(migrations) > 13 {
				entry := migrations[13]
				up := entry.Up
				entry.Up = func(tx *gorm.DB) error {
					if err := up(tx); err != nil {
						return err
					}
					return fmt.Errorf("interrupt after affinity DDL")
				}
				registry := append(append([]migration(nil), migrations[:13]...), entry)
				if err := applyMigrationRegistry(db, registry); err == nil {
					t.Fatal("expected interruption")
				}
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
			if !db.Migrator().HasColumn("request_logs", "affinity_kind") {
				t.Fatal("affinity_kind is missing")
			}
			if scenario != "fresh" {
				var row struct {
					AffinityHit  bool
					AffinityKind string
				}
				if err := db.Table("request_logs").Where("id = ?", "existing").Take(&row).Error; err != nil {
					t.Fatal(err)
				}
				if !row.AffinityHit || row.AffinityKind != "" {
					t.Fatalf("legacy affinity changed: %#v", row)
				}
			}
			if err := db.Table("request_logs").Create(map[string]any{
				"id": "new", "completed_at_ms": 1, "access_key_id": 1, "protocol": "openai-responses", "client_model": "test", "upstream_model": "test", "status": "success", "status_code": 200, "duration_ms": 1, "error_summary": "", "affinity_hit": true, "affinity_kind": "prompt_cache_key",
			}).Error; err != nil {
				t.Fatal(err)
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
		})
	}
}
