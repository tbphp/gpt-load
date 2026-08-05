package storage_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
)

type schemaV5Column struct {
	Name         string
	Type         string
	NotNull      int     `gorm:"column:notnull"`
	DefaultValue *string `gorm:"column:dflt_value"`
}

func TestAutoMigrateCreatesFinalPricingSchema(t *testing.T) {
	db := openSchemaV5TestDatabase(t)
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	if storage.CurrentSchemaVersion != 5 {
		t.Fatalf("CurrentSchemaVersion = %d, want 5", storage.CurrentSchemaVersion)
	}
	assertColumns(t, db, "model_prices", []string{
		"price_scope_key", "model_id",
		"input_price_nano_usd_per_million_tokens",
		"output_price_nano_usd_per_million_tokens",
		"cache_read_price_nano_usd_per_million_tokens",
		"cache_write_price_nano_usd_per_million_tokens",
		"context_price_tiers", "is_manual", "created_at_ms", "updated_at_ms",
	})
	assertUniqueIndex(t, db, "model_prices", "idx_model_prices_scope_model",
		[]string{"price_scope_key", "model_id"})

	columns := schemaV5Columns(t, db, "model_prices")
	for _, name := range []string{
		"input_price_nano_usd_per_million_tokens",
		"output_price_nano_usd_per_million_tokens",
		"cache_read_price_nano_usd_per_million_tokens",
		"cache_write_price_nano_usd_per_million_tokens",
		"context_price_tiers",
	} {
		if columns[name].NotNull != 0 {
			t.Errorf("model_prices.%s notnull = %d, want nullable", name, columns[name].NotNull)
		}
	}
	for _, removed := range []string{
		"pattern", "source",
		"cache_write_5m_price_nano_usd_per_million_tokens",
		"cache_write_1h_price_nano_usd_per_million_tokens",
	} {
		if _, ok := columns[removed]; ok {
			t.Errorf("model_prices retains obsolete column %q", removed)
		}
	}
}

func TestAutoMigrateCreatesNormalizedRequestLogSchemaV5(t *testing.T) {
	db := openSchemaV5TestDatabase(t)
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	if storage.CurrentSchemaVersion != 5 {
		t.Fatalf("CurrentSchemaVersion = %d, want 5", storage.CurrentSchemaVersion)
	}
	requestColumns := schemaV5Columns(t, db, "request_logs")
	for _, name := range []string{"stream", "first_response_ms", "attempt_count"} {
		if _, ok := requestColumns[name]; !ok {
			t.Errorf("request_logs.%s is missing", name)
		}
	}
	if _, ok := requestColumns["attempts"]; ok {
		t.Error("request_logs.attempts JSON column still exists")
	}
	assertColumns(t, db, "request_log_attempts", []string{
		"request_id", "sequence", "completed_at_ms", "group_id", "group_name",
		"key_id", "upstream_model", "status_code", "duration_ms",
		"failure_category", "action", "will_retry", "error_code", "error_summary",
		"pricing_receipt",
	})

	var foreignKeys []struct {
		Table    string
		From     string
		To       string
		OnDelete string `gorm:"column:on_delete"`
	}
	if err := db.Raw("PRAGMA foreign_key_list('request_log_attempts')").Scan(&foreignKeys).Error; err != nil {
		t.Fatalf("inspect request_log_attempts foreign keys: %v", err)
	}
	if len(foreignKeys) != 1 || foreignKeys[0].Table != "request_logs" ||
		foreignKeys[0].From != "request_id" || foreignKeys[0].To != "id" ||
		foreignKeys[0].OnDelete != "CASCADE" {
		t.Fatalf("request_log_attempts foreign keys = %#v", foreignKeys)
	}
}

func TestAutoMigrateRejectsUnreleasedLegacySchema(t *testing.T) {
	for _, version := range []uint{1, 2, 3, 4} {
		t.Run(fmt.Sprintf("version_%d", version), func(t *testing.T) {
			db := openSchemaV5TestDatabase(t)
			if err := db.Exec("CREATE TABLE schema_info (version integer PRIMARY KEY)").Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec("INSERT INTO schema_info(version) VALUES (?)", version).Error; err != nil {
				t.Fatal(err)
			}
			if err := storage.AutoMigrate(db); err == nil ||
				!strings.Contains(err.Error(), fmt.Sprintf("unsupported schema version %d", version)) {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
		})
	}
}

func TestAutoMigrateCreatesFinalCatalogAndUsageColumns(t *testing.T) {
	db := openSchemaV5TestDatabase(t)
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}

	groups := schemaV5Columns(t, db, "groups")
	providerID, ok := groups["provider_id"]
	if !ok {
		t.Fatal("groups.provider_id is missing")
	}
	if providerID.NotNull != 0 || !strings.EqualFold(providerID.Type, "varchar(255)") {
		t.Fatalf("groups.provider_id = type:%q notnull:%d, want varchar(255) nullable",
			providerID.Type, providerID.NotNull)
	}

	for table, columns := range map[string][]string{
		"request_logs": {"cache_write_unknown_tokens", "pricing_completeness"},
		"usage_stats":  {"cache_write_unknown_tokens", "pricing_partial_count"},
	} {
		got := schemaV5Columns(t, db, table)
		for _, name := range columns {
			column, found := got[name]
			if !found {
				t.Errorf("%s.%s is missing", table, name)
				continue
			}
			if column.NotNull != 1 || column.DefaultValue == nil {
				t.Errorf("%s.%s = notnull:%d default:%v, want NOT NULL with default",
					table, name, column.NotNull, column.DefaultValue)
			}
		}
	}
	pricingCompleteness := schemaV5Columns(t, db, "request_logs")["pricing_completeness"]
	if pricingCompleteness.DefaultValue == nil ||
		strings.Trim(*pricingCompleteness.DefaultValue, "'\"") != "not_applicable" {
		t.Errorf("request_logs.pricing_completeness default = %v, want not_applicable",
			pricingCompleteness.DefaultValue)
	}
}

func TestFinalModelPriceRejectsNegativeScalarPrices(t *testing.T) {
	db := openMigratedSchemaV5TestDatabase(t)
	priceColumns := []string{
		"input_price_nano_usd_per_million_tokens",
		"output_price_nano_usd_per_million_tokens",
		"cache_read_price_nano_usd_per_million_tokens",
		"cache_write_price_nano_usd_per_million_tokens",
	}
	for index, column := range priceColumns {
		t.Run(column, func(t *testing.T) {
			query := fmt.Sprintf(`INSERT INTO model_prices (
				price_scope_key, model_id, %s, is_manual, created_at_ms, updated_at_ms
			) VALUES (?, ?, -1, false, 1, 1)`, column)
			if err := db.Exec(query, "provider:openai", fmt.Sprintf("model-%d", index)).Error; err == nil {
				t.Fatalf("negative %s was accepted", column)
			}
		})
	}
}

func TestModelPriceValidatesAndNormalizesContextTiersBeforePersistence(t *testing.T) {
	db := openMigratedSchemaV5TestDatabase(t)

	empty := models.ModelPrice{
		PriceScopeKey:     "provider:openai",
		ModelID:           "empty-tiers",
		ContextPriceTiers: models.JSON(`[]`),
	}
	if err := db.Create(&empty).Error; err != nil {
		t.Fatalf("persist empty tiers: %v", err)
	}
	var isNull bool
	if err := db.Raw(`SELECT context_price_tiers IS NULL FROM model_prices
		WHERE price_scope_key = ? AND model_id = ?`, empty.PriceScopeKey, empty.ModelID).
		Scan(&isNull).Error; err != nil {
		t.Fatalf("inspect normalized tiers: %v", err)
	}
	if !isNull {
		t.Fatal("empty context tiers were not normalized to SQL NULL")
	}

	valid := models.ModelPrice{
		PriceScopeKey: "provider:openai",
		ModelID:       "valid-tiers",
		ContextPriceTiers: models.JSON(`[
			{"threshold_tokens":0,"input_price_nano_usd_per_million_tokens":1},
			{"threshold_tokens":100,"cache_write_price_nano_usd_per_million_tokens":0}
		]`),
	}
	if err := db.Create(&valid).Error; err != nil {
		t.Fatalf("persist valid tiers: %v", err)
	}

	invalid := []struct {
		name string
		raw  string
	}{
		{name: "object", raw: `{}`},
		{name: "trailing data", raw: `[] {}`},
		{name: "unknown field", raw: `[{"threshold_tokens":0,"unknown":1}]`},
		{name: "missing threshold", raw: `[{"input_price_nano_usd_per_million_tokens":1}]`},
		{name: "negative threshold", raw: `[{"threshold_tokens":-1,"input_price_nano_usd_per_million_tokens":1}]`},
		{name: "duplicate threshold", raw: `[{"threshold_tokens":1,"input_price_nano_usd_per_million_tokens":1},{"threshold_tokens":1,"output_price_nano_usd_per_million_tokens":1}]`},
		{name: "descending threshold", raw: `[{"threshold_tokens":2,"input_price_nano_usd_per_million_tokens":1},{"threshold_tokens":1,"output_price_nano_usd_per_million_tokens":1}]`},
		{name: "negative input price", raw: `[{"threshold_tokens":0,"input_price_nano_usd_per_million_tokens":-1}]`},
		{name: "negative output price", raw: `[{"threshold_tokens":0,"output_price_nano_usd_per_million_tokens":-1}]`},
		{name: "negative cache read price", raw: `[{"threshold_tokens":0,"cache_read_price_nano_usd_per_million_tokens":-1}]`},
		{name: "negative cache write price", raw: `[{"threshold_tokens":0,"cache_write_price_nano_usd_per_million_tokens":-1}]`},
		{name: "no effective price", raw: `[{"threshold_tokens":0}]`},
	}
	for index, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			row := models.ModelPrice{
				PriceScopeKey:     "provider:openai",
				ModelID:           fmt.Sprintf("invalid-tier-%d", index),
				ContextPriceTiers: models.JSON(test.raw),
			}
			if err := db.Create(&row).Error; err == nil {
				t.Fatalf("invalid context tiers %s were accepted", test.raw)
			}
		})
	}
}

func TestModelPriceValidatesContextTiersAcrossGORMWritePaths(t *testing.T) {
	type writePath struct {
		name  string
		write func(*gorm.DB, models.ModelPrice, models.JSON) error
	}
	paths := []writePath{
		{
			name: "save",
			write: func(db *gorm.DB, row models.ModelPrice, tiers models.JSON) error {
				row.ContextPriceTiers = tiers
				return db.Save(&row).Error
			},
		},
		{
			name: "struct updates",
			write: func(db *gorm.DB, row models.ModelPrice, tiers models.JSON) error {
				return db.Model(&row).Updates(models.ModelPrice{ContextPriceTiers: tiers}).Error
			},
		},
		{
			name: "map updates",
			write: func(db *gorm.DB, row models.ModelPrice, tiers models.JSON) error {
				return db.Model(&row).Updates(map[string]any{"context_price_tiers": tiers}).Error
			},
		},
		{
			name: "single column update",
			write: func(db *gorm.DB, row models.ModelPrice, tiers models.JSON) error {
				return db.Model(&row).Update("context_price_tiers", tiers).Error
			},
		},
		{
			name: "on conflict upsert assignment",
			write: func(db *gorm.DB, row models.ModelPrice, tiers models.JSON) error {
				incoming := row
				incoming.ID = 0
				return db.Clauses(clause.OnConflict{
					Columns: []clause.Column{{Name: "price_scope_key"}, {Name: "model_id"}},
					DoUpdates: clause.Assignments(map[string]any{
						"context_price_tiers": tiers,
					}),
				}).Create(&incoming).Error
			},
		},
	}

	for _, path := range paths {
		t.Run(path.name+" rejects invalid tiers", func(t *testing.T) {
			db, row := createModelPriceForWritePath(t, path.name+"-invalid")
			invalid := models.JSON(`[{"threshold_tokens":0}]`)
			if err := path.write(db, row, invalid); err == nil {
				t.Fatal("invalid context tiers were persisted")
			}
		})
		t.Run(path.name+" normalizes empty tiers", func(t *testing.T) {
			db, row := createModelPriceForWritePath(t, path.name+"-empty")
			if err := path.write(db, row, models.JSON(`[]`)); err != nil {
				t.Fatalf("write empty tiers: %v", err)
			}
			assertModelPriceTiersNull(t, db, row.PriceScopeKey, row.ModelID)
		})
	}
}

func TestFinalRequestLogEnforcesStateContract(t *testing.T) {
	db := openMigratedSchemaV5TestDatabase(t)
	valid := []requestStateFixture{
		{id: "valid-priced-complete", status: "success", usage: "complete", cost: "priced", pricing: "complete", estimatedCost: 1},
		{id: "valid-priced-partial", status: "incomplete", usage: "partial", cost: "priced", pricing: "partial", estimatedCost: 0},
		{id: "valid-unpriced-missing", status: "error", usage: "missing", cost: "unpriced", pricing: "unavailable", estimatedCost: 0},
		{id: "valid-not-applicable", status: "canceled", usage: "not_applicable", cost: "not_applicable", pricing: "not_applicable", estimatedCost: 0},
	}
	for _, fixture := range valid {
		if err := insertRequestStateFixture(db, fixture); err != nil {
			t.Errorf("valid fixture %q rejected: %v", fixture.id, err)
		}
	}

	invalid := []requestStateFixture{
		{id: "invalid-status", status: "pending", usage: "complete", cost: "priced", pricing: "complete", estimatedCost: 0},
		{id: "invalid-usage", status: "success", usage: "unknown", cost: "priced", pricing: "complete", estimatedCost: 0},
		{id: "invalid-cost", status: "success", usage: "complete", cost: "unknown", pricing: "complete", estimatedCost: 0},
		{id: "invalid-pricing", status: "success", usage: "complete", cost: "priced", pricing: "unknown", estimatedCost: 0},
		{id: "not-applicable-priced", status: "success", usage: "not_applicable", cost: "priced", pricing: "complete", estimatedCost: 1},
		{id: "unpriced-complete", status: "success", usage: "complete", cost: "unpriced", pricing: "complete", estimatedCost: 0},
		{id: "unpriced-nonzero", status: "success", usage: "complete", cost: "unpriced", pricing: "unavailable", estimatedCost: 1},
		{id: "priced-unavailable", status: "success", usage: "complete", cost: "priced", pricing: "unavailable", estimatedCost: 0},
		{id: "missing-priced", status: "success", usage: "missing", cost: "priced", pricing: "complete", estimatedCost: 1},
	}
	for _, fixture := range invalid {
		if err := insertRequestStateFixture(db, fixture); err == nil {
			t.Errorf("invalid fixture %q was accepted", fixture.id)
		}
	}
}

func TestFinalSchemaUsesMillisecondIntegersAndEnforcesCounters(t *testing.T) {
	db := openMigratedSchemaV5TestDatabase(t)

	for table, names := range map[string][]string{
		"groups":             {"created_at_ms", "updated_at_ms"},
		"upstream_keys":      {"created_at_ms", "updated_at_ms"},
		"access_keys":        {"created_at_ms", "updated_at_ms"},
		"model_prices":       {"created_at_ms", "updated_at_ms"},
		"request_logs":       {"completed_at_ms"},
		"usage_stats":        {"bucket_start_ms"},
		"system_settings":    {"updated_at_ms"},
		"jobs":               {"created_at_ms", "started_at_ms", "finished_at_ms"},
		"control_operations": {"completed_at_ms", "compacted_at_ms", "created_at_ms", "updated_at_ms"},
	} {
		columns := schemaV5Columns(t, db, table)
		for _, name := range names {
			column, ok := columns[name]
			if !ok {
				t.Errorf("%s.%s is missing", table, name)
				continue
			}
			if !strings.EqualFold(column.Type, "INTEGER") {
				t.Errorf("%s.%s type = %q, want INTEGER", table, name, column.Type)
			}
		}
	}

	invalidStatements := []struct {
		name      string
		statement string
	}{
		{
			name: "negative access key daily cost",
			statement: `INSERT INTO access_keys (
				name, key_value, key_hash, key_suffix, status, rpm_limit,
				daily_cost_limit_nano_usd, monthly_cost_limit_nano_usd,
				created_at_ms, updated_at_ms
			) VALUES ('negative', 'cipher', 'negative-daily', '7f2a', 'active',
				0, -1, 0, 1, 1)`,
		},
		{
			name: "negative access key monthly cost",
			statement: `INSERT INTO access_keys (
				name, key_value, key_hash, key_suffix, status, rpm_limit,
				daily_cost_limit_nano_usd, monthly_cost_limit_nano_usd,
				created_at_ms, updated_at_ms
			) VALUES ('negative-monthly', 'cipher', 'negative-monthly', '7f2b',
				'active', 0, 0, -1, 1, 1)`,
		},
		{
			name: "negative request duration",
			statement: `INSERT INTO request_logs (
				id, completed_at_ms, access_key_id, group_id, protocol, client_model,
				upstream_model, status, status_code, duration_ms, error_code, error_summary,
				affinity_hit, estimated_cost_nano_usd, usage_state, cost_state,
				pricing_completeness
			) VALUES ('negative-request-duration', 1, 0, 0, 'openai-completions', '', '',
				'success', 200, -1, '', '', false, 0,
				'complete', 'priced', 'complete')`,
		},
		{
			name: "negative request token",
			statement: `INSERT INTO request_logs (
				id, completed_at_ms, access_key_id, group_id, protocol, client_model,
				upstream_model, status, status_code, duration_ms, error_code, error_summary,
				affinity_hit, uncached_input_tokens, output_tokens, cache_read_tokens,
				cache_write_5m_tokens, cache_write_1h_tokens, cache_write_unknown_tokens,
				estimated_cost_nano_usd, usage_state, cost_state, pricing_completeness
			) VALUES ('negative-request-token', 1, 0, 0, 'openai-completions', '', '',
				'success', 200, 0, '', '', false, 0, 0, 0, 0, 0, -1, 0,
				'complete', 'priced', 'complete')`,
		},
		{
			name: "negative request cost",
			statement: `INSERT INTO request_logs (
				id, completed_at_ms, access_key_id, group_id, protocol, client_model,
				upstream_model, status, status_code, duration_ms, error_code, error_summary,
				affinity_hit, estimated_cost_nano_usd, usage_state, cost_state,
				pricing_completeness
			) VALUES ('negative-request-cost', 1, 0, 0, 'openai-completions', '', '',
				'success', 200, 0, '', '', false, -1,
				'complete', 'priced', 'complete')`,
		},
		{
			name: "negative usage unknown cache write",
			statement: `INSERT INTO usage_stats (
				bucket_start_ms, access_key_id, group_id, model,
				request_count, success_count, failure_count, cache_write_unknown_tokens
			) VALUES (1, 0, 0, 'negative-unknown', 0, 0, 0, -1)`,
		},
		{
			name: "negative pricing partial count",
			statement: `INSERT INTO usage_stats (
				bucket_start_ms, access_key_id, group_id, model,
				request_count, success_count, failure_count, pricing_partial_count
			) VALUES (2, 0, 0, 'negative-pricing-partial', 0, 0, 0, -1)`,
		},
		{
			name: "request outcome mismatch",
			statement: `INSERT INTO usage_stats (
				bucket_start_ms, access_key_id, group_id, model,
				request_count, success_count, failure_count
			) VALUES (3, 0, 0, 'outcome-mismatch', 1, 0, 0)`,
		},
		{
			name: "negative usage request count",
			statement: `INSERT INTO usage_stats (
				bucket_start_ms, access_key_id, group_id, model,
				request_count, success_count, failure_count
			) VALUES (4, 0, 0, 'negative-request-count', -1, 0, 0)`,
		},
	}
	for _, invalid := range invalidStatements {
		t.Run(invalid.name, func(t *testing.T) {
			if err := db.Exec(invalid.statement).Error; err == nil {
				t.Fatal("invalid final-schema row was accepted")
			}
		})
	}
}

func TestAutoMigrateRejectsCorruptCanonicalTable(t *testing.T) {
	db := openMigratedSchemaV5TestDatabase(t)
	if err := db.Exec(`ALTER TABLE usage_stats RENAME TO usage_stats_checked`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE usage_stats AS
		SELECT * FROM usage_stats_checked WHERE 0`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`DROP TABLE usage_stats_checked`).Error; err != nil {
		t.Fatal(err)
	}
	if err := storage.AutoMigrate(db); err == nil || !strings.Contains(err.Error(), "table usage_stats differs") {
		t.Fatalf("AutoMigrate(corrupt v5 table) error = %v", err)
	}
}

func TestAutoMigrateValidatesExistingVersionFiveSchema(t *testing.T) {
	db := openMigratedSchemaV5TestDatabase(t)
	if err := db.Exec("DROP INDEX idx_model_prices_scope_model").Error; err != nil {
		t.Fatal(err)
	}
	if err := storage.AutoMigrate(db); err == nil || !strings.Contains(err.Error(), "idx_model_prices_scope_model") {
		t.Fatalf("AutoMigrate(corrupt v5) error = %v, want missing-index validation", err)
	}
}

type requestStateFixture struct {
	id            string
	status        string
	usage         string
	cost          string
	pricing       string
	estimatedCost int64
}

func insertRequestStateFixture(db *gorm.DB, fixture requestStateFixture) error {
	return db.Exec(`INSERT INTO request_logs (
		id, completed_at_ms, access_key_id, group_id, protocol, client_model,
		upstream_model, status, status_code, duration_ms, error_code, error_summary,
		affinity_hit, uncached_input_tokens, output_tokens, cache_read_tokens,
		cache_write_5m_tokens, cache_write_1h_tokens, cache_write_unknown_tokens,
		estimated_cost_nano_usd, usage_state, cost_state, pricing_completeness
	) VALUES (?, 1, 0, 0, 'openai-completions', '', '', ?, 200, 0, '', '',
		false, 0, 0, 0, 0, 0, 0, ?, ?, ?, ?)`,
		fixture.id, fixture.status, fixture.estimatedCost,
		fixture.usage, fixture.cost, fixture.pricing,
	).Error
}

func createModelPriceForWritePath(t *testing.T, modelID string) (*gorm.DB, models.ModelPrice) {
	t.Helper()
	db := openMigratedSchemaV5TestDatabase(t)
	row := models.ModelPrice{
		PriceScopeKey: "provider:openai",
		ModelID:       modelID,
		ContextPriceTiers: models.JSON(
			`[{"threshold_tokens":0,"input_price_nano_usd_per_million_tokens":1}]`,
		),
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create model price fixture: %v", err)
	}
	return db, row
}

func assertModelPriceTiersNull(
	t *testing.T,
	db *gorm.DB,
	priceScopeKey string,
	modelID string,
) {
	t.Helper()
	var isNull bool
	if err := db.Raw(`SELECT context_price_tiers IS NULL FROM model_prices
		WHERE price_scope_key = ? AND model_id = ?`, priceScopeKey, modelID).
		Scan(&isNull).Error; err != nil {
		t.Fatalf("inspect normalized tiers: %v", err)
	}
	if !isNull {
		t.Fatal("empty context tiers were not normalized to SQL NULL")
	}
}

func openSchemaV5TestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return db
}

func openMigratedSchemaV5TestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	db := openSchemaV5TestDatabase(t)
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	return db
}

func schemaV5Columns(t *testing.T, db *gorm.DB, table string) map[string]schemaV5Column {
	t.Helper()
	var columns []schemaV5Column
	if err := db.Raw("PRAGMA table_info('" + table + "')").Scan(&columns).Error; err != nil {
		t.Fatalf("inspect %s columns: %v", table, err)
	}
	result := make(map[string]schemaV5Column, len(columns))
	for _, column := range columns {
		result[column.Name] = column
	}
	return result
}

func assertColumns(t *testing.T, db *gorm.DB, table string, want []string) {
	t.Helper()
	got := schemaV5Columns(t, db, table)
	for _, name := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("%s.%s is missing", table, name)
		}
	}
}

func assertUniqueIndex(
	t *testing.T,
	db *gorm.DB,
	table string,
	indexName string,
	wantColumns []string,
) {
	t.Helper()
	var indexes []struct {
		Name   string
		Unique int
	}
	if err := db.Raw("PRAGMA index_list('" + table + "')").Scan(&indexes).Error; err != nil {
		t.Fatalf("inspect %s indexes: %v", table, err)
	}
	for _, index := range indexes {
		if index.Name != indexName {
			continue
		}
		if index.Unique != 1 {
			t.Fatalf("%s unique = %d, want 1", indexName, index.Unique)
		}
		var columns []struct {
			Name string
			Key  int
		}
		if err := db.Raw("PRAGMA index_xinfo('" + indexName + "')").Scan(&columns).Error; err != nil {
			t.Fatalf("inspect %s columns: %v", indexName, err)
		}
		gotColumns := make([]string, 0, len(wantColumns))
		for _, column := range columns {
			if column.Key == 1 {
				gotColumns = append(gotColumns, column.Name)
			}
		}
		if !reflect.DeepEqual(gotColumns, wantColumns) {
			t.Fatalf("%s columns = %v, want %v", indexName, gotColumns, wantColumns)
		}
		return
	}
	t.Fatalf("%s is missing", indexName)
}
