package storage

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
)

// migrateGlobalModelPrices collapses the former (scope, model) rows into one
// row per exact upstream model. A conflicting manual value cannot be selected
// safely, so the migration fails before changing either data or its ledger.
func migrateGlobalModelPrices(db *gorm.DB) error {
	if !db.Migrator().HasColumn(&models.ModelPrice{}, "price_scope_key") {
		return nil
	}
	driver := strings.ToLower(db.Dialector.Name())
	if driver != "sqlite" && driver != "mysql" && driver != "postgres" && driver != "postgresql" {
		return fmt.Errorf("migrate global model prices: unsupported database driver %q", db.Dialector.Name())
	}

	var rows []models.ModelPrice
	if err := db.Order("model_id ASC").Order("id ASC").Find(&rows).Error; err != nil {
		return fmt.Errorf("load scoped model prices: %w", err)
	}

	loserIDs := make([]uint, 0)
	conflicts := make([]string, 0)
	for start := 0; start < len(rows); {
		end := start + 1
		for end < len(rows) && rows[end].ModelID == rows[start].ModelID {
			end++
		}
		group := append([]models.ModelPrice(nil), rows[start:end]...)
		sort.SliceStable(group, func(left, right int) bool {
			if group[left].IsManual != group[right].IsManual {
				return group[left].IsManual
			}
			if group[left].UpdatedAtMS != group[right].UpdatedAtMS {
				return group[left].UpdatedAtMS > group[right].UpdatedAtMS
			}
			return group[left].ID > group[right].ID
		})

		manuals := make([]models.ModelPrice, 0, len(group))
		for _, row := range group {
			if row.IsManual {
				manuals = append(manuals, row)
			}
		}
		if len(manuals) > 1 {
			for _, candidate := range manuals[1:] {
				if !globalModelPriceValuesEqual(manuals[0], candidate) {
					conflicts = append(conflicts, rows[start].ModelID)
					break
				}
			}
		}
		for _, row := range group[1:] {
			loserIDs = append(loserIDs, row.ID)
		}
		start = end
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("migrate global model prices: conflicting manual prices for %s", strings.Join(conflicts, ", "))
	}
	if len(loserIDs) > 0 {
		if err := db.Where("id IN ?", loserIDs).Delete(&models.ModelPrice{}).Error; err != nil {
			return fmt.Errorf("remove superseded scoped model prices: %w", err)
		}
	}
	return migrateGlobalModelPriceSchema(db)
}

func migrateGlobalModelPriceSchema(db *gorm.DB) error {
	if !strings.EqualFold(db.Dialector.Name(), "sqlite") {
		statements, err := globalModelPriceSchemaStatements(db.Dialector.Name())
		if err != nil {
			return err
		}
		for _, statement := range statements {
			if err := db.Exec(statement).Error; err != nil {
				return fmt.Errorf("update global model price schema: %w", err)
			}
		}
		return nil
	}
	if err := db.Exec(`CREATE TABLE model_prices_global_migration (
		id integer PRIMARY KEY AUTOINCREMENT,
		model_id varchar(255) NOT NULL,
		input_price_nano_usd_per_million_tokens integer NULL CONSTRAINT chk_model_price_input_nano CHECK (input_price_nano_usd_per_million_tokens IS NULL OR input_price_nano_usd_per_million_tokens >= 0),
		output_price_nano_usd_per_million_tokens integer NULL CONSTRAINT chk_model_price_output_nano CHECK (output_price_nano_usd_per_million_tokens IS NULL OR output_price_nano_usd_per_million_tokens >= 0),
		cache_read_price_nano_usd_per_million_tokens integer NULL CONSTRAINT chk_model_price_cache_read_nano CHECK (cache_read_price_nano_usd_per_million_tokens IS NULL OR cache_read_price_nano_usd_per_million_tokens >= 0),
		cache_write_price_nano_usd_per_million_tokens integer NULL CONSTRAINT chk_model_price_cache_write_nano CHECK (cache_write_price_nano_usd_per_million_tokens IS NULL OR cache_write_price_nano_usd_per_million_tokens >= 0),
		context_price_tiers json NULL,
		is_manual boolean NOT NULL DEFAULT false,
		created_at_ms integer NOT NULL CONSTRAINT chk_model_price_created_at CHECK (created_at_ms >= 0),
		updated_at_ms integer NOT NULL CONSTRAINT chk_model_price_updated_at CHECK (updated_at_ms >= 0)
	)`).Error; err != nil {
		return fmt.Errorf("create global model price table: %w", err)
	}
	if err := db.Exec(`INSERT INTO model_prices_global_migration (
		id, model_id, input_price_nano_usd_per_million_tokens,
		output_price_nano_usd_per_million_tokens, cache_read_price_nano_usd_per_million_tokens,
		cache_write_price_nano_usd_per_million_tokens, context_price_tiers, is_manual,
		created_at_ms, updated_at_ms
	) SELECT id, model_id, input_price_nano_usd_per_million_tokens,
		output_price_nano_usd_per_million_tokens, cache_read_price_nano_usd_per_million_tokens,
		cache_write_price_nano_usd_per_million_tokens, context_price_tiers, is_manual,
		created_at_ms, updated_at_ms FROM model_prices`).Error; err != nil {
		return fmt.Errorf("copy global model prices: %w", err)
	}
	if err := db.Exec("DROP TABLE model_prices").Error; err != nil {
		return fmt.Errorf("drop scoped model price table: %w", err)
	}
	if err := db.Exec("ALTER TABLE model_prices_global_migration RENAME TO model_prices").Error; err != nil {
		return fmt.Errorf("rename global model price table: %w", err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX idx_model_prices_model ON model_prices(model_id)").Error; err != nil {
		return fmt.Errorf("create global model price index: %w", err)
	}
	return nil
}

// globalModelPriceSchemaStatements contains only fixed, schema-owned
// identifiers. MySQL combines the three operations into one ALTER TABLE so a
// failed DDL statement cannot leave the old table shape half-applied.
func globalModelPriceSchemaStatements(driver string) ([]string, error) {
	switch strings.ToLower(driver) {
	case "postgres", "postgresql":
		return []string{
			"DROP INDEX idx_model_prices_scope_model",
			"ALTER TABLE model_prices DROP COLUMN price_scope_key",
			"CREATE UNIQUE INDEX idx_model_prices_model ON model_prices(model_id)",
		}, nil
	case "mysql":
		return []string{
			"ALTER TABLE model_prices DROP INDEX idx_model_prices_scope_model, DROP COLUMN price_scope_key, ADD UNIQUE INDEX idx_model_prices_model (model_id)",
		}, nil
	default:
		return nil, fmt.Errorf("migrate global model prices: unsupported database driver %q", driver)
	}
}

func globalModelPriceValuesEqual(left, right models.ModelPrice) bool {
	if !globalModelPricePointerEqual(left.InputPriceNanoUSDPerMillionTokens, right.InputPriceNanoUSDPerMillionTokens) ||
		!globalModelPricePointerEqual(left.OutputPriceNanoUSDPerMillionTokens, right.OutputPriceNanoUSDPerMillionTokens) ||
		!globalModelPricePointerEqual(left.CacheReadPriceNanoUSDPerMillionTokens, right.CacheReadPriceNanoUSDPerMillionTokens) ||
		!globalModelPricePointerEqual(left.CacheWritePriceNanoUSDPerMillionTokens, right.CacheWritePriceNanoUSDPerMillionTokens) {
		return false
	}
	leftTiers, leftErr := models.NormalizeContextPriceTiers(left.ContextPriceTiers)
	rightTiers, rightErr := models.NormalizeContextPriceTiers(right.ContextPriceTiers)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftTiers, rightTiers)
}

func globalModelPricePointerEqual(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
