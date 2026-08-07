package storage_test

import (
	"testing"

	"gorm.io/gorm"

	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
)

func TestAutoMigrateCollapsesScopedModelPricesIntoGlobalRows(t *testing.T) {
	db := openInitialV2TestDatabase(t)
	createScopedModelPriceSchema(t, db)
	if err := db.Exec(`INSERT INTO model_prices (
		id, price_scope_key, model_id, input_price_nano_usd_per_million_tokens,
		is_manual, created_at_ms, updated_at_ms
	) VALUES
		(1, 'provider:openai', 'shared', 1, false, 1, 10),
		(2, 'group:7', 'shared', 2, true, 2, 20),
		(3, 'provider:anthropic', 'automatic', 3, false, 3, 30)`).Error; err != nil {
		t.Fatalf("seed scoped model prices: %v", err)
	}

	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	if db.Migrator().HasColumn(&models.ModelPrice{}, "price_scope_key") {
		t.Fatal("model_prices retains price_scope_key after global migration")
	}
	assertUniqueIndex(t, db, "model_prices", "idx_model_prices_model", []string{"model_id"})

	var rows []models.ModelPrice
	if err := db.Order("model_id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("load migrated prices: %v", err)
	}
	if len(rows) != 2 || rows[0].ModelID != "automatic" || rows[1].ModelID != "shared" {
		t.Fatalf("migrated rows = %#v, want one row per model", rows)
	}
	if rows[1].ID != 2 || !rows[1].IsManual || rows[1].InputPriceNanoUSDPerMillionTokens == nil ||
		*rows[1].InputPriceNanoUSDPerMillionTokens != 2 {
		t.Fatalf("shared winner = %#v, want newest manual row", rows[1])
	}
}

func TestAutoMigrateRejectsConflictingScopedManualPricesWithoutMutation(t *testing.T) {
	db := openInitialV2TestDatabase(t)
	createScopedModelPriceSchema(t, db)
	if err := db.Exec(`INSERT INTO model_prices (
		id, price_scope_key, model_id, input_price_nano_usd_per_million_tokens,
		is_manual, created_at_ms, updated_at_ms
	) VALUES
		(1, 'provider:openai', 'shared', 1, true, 1, 10),
		(2, 'group:7', 'shared', 2, true, 2, 20)`).Error; err != nil {
		t.Fatalf("seed conflicting scoped prices: %v", err)
	}

	if err := storage.AutoMigrate(db); err == nil {
		t.Fatal("AutoMigrate() accepted conflicting manual model prices")
	}
	if !db.Migrator().HasColumn(&models.ModelPrice{}, "price_scope_key") {
		t.Fatal("failed global migration mutated model_prices schema")
	}
	var count int64
	if err := db.Table("model_prices").Count(&count).Error; err != nil {
		t.Fatalf("count scoped prices after failed migration: %v", err)
	}
	if count != 2 {
		t.Fatalf("scoped prices after failed migration = %d, want 2", count)
	}
	if err := db.Table("schema_migrations").Count(&count).Error; err != nil {
		t.Fatalf("count migration ledger after failure: %v", err)
	}
	if count != 2 {
		t.Fatalf("migration ledger after failure = %d, want 2", count)
	}
}

func createScopedModelPriceSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, statement := range []string{
		`CREATE TABLE schema_migrations (id varchar(255) PRIMARY KEY NOT NULL)`,
		`INSERT INTO schema_migrations(id) VALUES ('0001_initial_v2'), ('0002_request_log_reasoning')`,
		`CREATE TABLE model_prices (
			id integer PRIMARY KEY AUTOINCREMENT,
			price_scope_key varchar(255) NOT NULL,
			model_id varchar(255) NOT NULL,
			input_price_nano_usd_per_million_tokens integer NULL,
			output_price_nano_usd_per_million_tokens integer NULL,
			cache_read_price_nano_usd_per_million_tokens integer NULL,
			cache_write_price_nano_usd_per_million_tokens integer NULL,
			context_price_tiers json NULL,
			is_manual boolean NOT NULL DEFAULT false,
			created_at_ms integer NOT NULL,
			updated_at_ms integer NOT NULL
		)`,
		`CREATE UNIQUE INDEX idx_model_prices_scope_model ON model_prices(price_scope_key, model_id)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create scoped model price fixture: %v", err)
		}
	}
}
