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
	for _, scenario := range []string{"fresh", "upgrade", "interrupted_column", "interrupted_index", "interrupted_complete"} {
		t.Run(scenario, func(t *testing.T) {
			if len(migrations) < 31 {
				t.Fatal("model price reference migration is missing")
			}
			db := open(t)
			var removed struct{ ID uint }
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
				if err := db.Table("model_prices").Create(map[string]any{
					"channel_id": "openai", "model_id": "removed-before-migration", "is_manual": true,
					"created_at_ms": 1, "updated_at_ms": 1,
				}).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Table("model_prices").Where("model_id = ?", "removed-before-migration").Take(&removed).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Exec("DELETE FROM model_prices WHERE id = ?", removed.ID).Error; err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "interrupted_column" || scenario == "interrupted_index" || scenario == "interrupted_complete" {
				entry := migrations[30]
				up := entry.Up
				entry.Up = func(tx *gorm.DB) error {
					if scenario == "interrupted_column" || scenario == "interrupted_index" {
						columnType := "BIGINT"
						if tx.Dialector.Name() == "mysql" {
							columnType = "BIGINT UNSIGNED"
						}
						if err := tx.Exec("ALTER TABLE model_prices ADD COLUMN source_price_id " + columnType + " NULL").Error; err != nil {
							return err
						}
						if scenario == "interrupted_index" {
							if err := tx.Exec("CREATE INDEX idx_model_prices_source ON model_prices (source_price_id)").Error; err != nil {
								return err
							}
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
			if !db.Migrator().HasColumn("model_prices", "source_price_id") {
				t.Fatal("model price reference column is missing")
			}
			for _, name := range []string{"idx_model_prices_channel_model", "idx_model_prices_source"} {
				if !db.Migrator().HasIndex("model_prices", name) {
					t.Fatalf("model price index %s was lost", name)
				}
			}
			var parent struct{ ID uint }
			if err := db.Table("model_prices").Create(map[string]any{"channel_id": "openai", "model_id": "reference-parent", "is_manual": true, "created_at_ms": 1, "updated_at_ms": 1}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Table("model_prices").Where("model_id = ?", "reference-parent").Take(&parent).Error; err != nil {
				t.Fatal(err)
			}
			if parent.ID <= removed.ID {
				t.Fatal("migration reused a deleted model price ID")
			}
			if err := db.Table("model_prices").Create(map[string]any{"channel_id": "openai", "model_id": "reference-parent", "is_manual": true, "created_at_ms": 1, "updated_at_ms": 1}).Error; err == nil {
				t.Fatal("migration lost model price identity uniqueness")
			}
			if err := db.Table("model_prices").Create(map[string]any{"channel_id": "openai", "model_id": "reference-child", "source_price_id": parent.ID, "is_manual": false, "created_at_ms": 1, "updated_at_ms": 1}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec("DELETE FROM model_prices WHERE id = ?", parent.ID).Error; err == nil {
				t.Fatal("source deletion was not restricted")
			}
			if err := db.Table("model_prices").Create(map[string]any{"channel_id": "openai", "model_id": "missing-source", "source_price_id": parent.ID + 1000, "is_manual": false, "created_at_ms": 1, "updated_at_ms": 1}).Error; err == nil {
				t.Fatal("missing source was accepted")
			}
			if scenario != "fresh" {
				var row struct {
					SourcePriceID                     *uint
					IsManual                          bool
					InputPriceNanoUSDPerMillionTokens int64
				}
				if err := db.Table("model_prices").Where("model_id = ?", "existing").Take(&row).Error; err != nil {
					t.Fatal(err)
				}
				if row.SourcePriceID != nil || !row.IsManual || row.InputPriceNanoUSDPerMillionTokens != 123 {
					t.Fatalf("migration changed existing price: %#v", row)
				}
			}
		})
	}
}
