package storage

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"
)

func TestValidationProtocolMigrationContract(t *testing.T) {
	t.Parallel()
	testValidationProtocolMigration(t, openInternalMigrationTestDatabase)
}

func TestExternalValidationProtocolMigrationContract(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testValidationProtocolMigration(t, func(t *testing.T) *gorm.DB { return openExternalIncrementalMigrationDatabase(t, dsn) })
}

func testValidationProtocolMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
	for _, scenario := range []string{"fresh", "upgrade", "interrupted"} {
		t.Run(scenario, func(t *testing.T) {
			db := open(t)
			if db.Dialector.Name() == "sqlite" {
				t.Parallel()
			}
			if scenario != "fresh" {
				if err := applyMigrationRegistry(db, migrations[:12]); err != nil {
					t.Fatal(err)
				}
				if err := db.Table("groups").Create(map[string]any{"id": 1, "name": "existing", "channel_id": "openai", "params": "{}", "models": "[]", "validation_model": "existing-model", "created_at_ms": 1, "updated_at_ms": 1}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "interrupted" {
				entry := migrations[12]
				up := entry.Up
				entry.Up = func(tx *gorm.DB) error {
					if err := up(tx); err != nil {
						return err
					}
					return fmt.Errorf("interrupt after protocol DDL")
				}
				registry := append(append([]migration(nil), migrations[:12]...), entry)
				if err := applyMigrationRegistry(db, registry); err == nil {
					t.Fatal("expected interruption")
				}
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
			if scenario != "fresh" {
				var row struct {
					Name               string
					ValidationModel    string
					ValidationProtocol *string
				}
				if err := db.Table("groups").Where("id = ?", 1).Take(&row).Error; err != nil {
					t.Fatal(err)
				}
				if row.Name != "existing" || row.ValidationModel != "existing-model" || row.ValidationProtocol != nil {
					t.Fatalf("existing group changed: %#v", row)
				}
			}
			if !db.Migrator().HasColumn("groups", "validation_protocol") {
				t.Fatal("validation protocol is missing")
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
		})
	}
}
