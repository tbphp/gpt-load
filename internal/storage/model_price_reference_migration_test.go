package storage

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"
)

func TestModelPriceReferenceMigrationContract(t *testing.T) {
	t.Parallel()
	testModelPriceReferenceMigration(t, openInternalMigrationTestDatabase)
}

func TestExternalModelPriceReferenceMigrationContract(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testModelPriceReferenceMigration(t, func(t *testing.T) *gorm.DB {
		return openExternalIncrementalMigrationDatabase(t, dsn)
	})
}

func testModelPriceReferenceMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
	for _, scenario := range []string{"fresh", "upgrade", "interrupted_column", "interrupted_complete"} {
		t.Run(scenario, func(t *testing.T) {
			if len(migrations) < 31 {
				t.Fatal("model price directory reference migration is missing")
			}
			db := open(t)
			if db.Dialector.Name() == "sqlite" {
				t.Parallel()
			}
			if scenario != "fresh" {
				if err := applyMigrationRegistry(db, migrations[:30]); err != nil {
					t.Fatal(err)
				}
				if err := db.Table("model_prices").Create(map[string]any{
					"channel_id": "openai", "model_id": "existing", "is_manual": true,
					"input_price_nano_usd_per_million_tokens": 123, "created_at_ms": 1, "updated_at_ms": 1,
				}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "interrupted_column" || scenario == "interrupted_complete" {
				entry := migrations[30]
				up := entry.Up
				entry.Up = func(tx *gorm.DB) error {
					if scenario == "interrupted_column" {
						if err := tx.Exec("ALTER TABLE model_prices ADD COLUMN reference_provider_id VARCHAR(246) NOT NULL DEFAULT ''").Error; err != nil {
							return err
						}
					} else if err := up(tx); err != nil {
						return err
					}
					return fmt.Errorf("interrupt model price reference DDL")
				}
				registry := append(append([]migration(nil), migrations[:30]...), entry)
				if err := applyMigrationRegistry(db, registry); err == nil {
					t.Fatal("expected migration interruption")
				}
			}
			for range 2 {
				if err := AutoMigrate(db); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"reference_provider_id", "reference_model_id"} {
				if !db.Migrator().HasColumn("model_prices", name) {
					t.Fatalf("model price reference column %s is missing", name)
				}
			}
			if scenario != "fresh" {
				var row struct {
					ReferenceProviderID               string
					ReferenceModelID                  string
					IsManual                          bool
					InputPriceNanoUSDPerMillionTokens int64
				}
				if err := db.Table("model_prices").Where("model_id = ?", "existing").Take(&row).Error; err != nil {
					t.Fatal(err)
				}
				if row.ReferenceProviderID != "" || row.ReferenceModelID != "" || !row.IsManual || row.InputPriceNanoUSDPerMillionTokens != 123 {
					t.Fatalf("migration changed existing price: %#v", row)
				}
			}
		})
	}
}
