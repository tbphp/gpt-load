package storage

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const schemaV3InfoTableSQL = `CREATE TABLE schema_info (
	version integer PRIMARY KEY NOT NULL,
	CONSTRAINT chk_schema_info_version CHECK (version = 3)
)`

func createSchemaV3InfoTable(db *gorm.DB, table string) error {
	if table != "schema_info" && table != "schema_info__v3" {
		return fmt.Errorf("create schema v3 info table: unsupported name %q", table)
	}
	statement := strings.Replace(schemaV3InfoTableSQL, "schema_info", table, 1)
	if err := db.Exec(statement).Error; err != nil {
		return fmt.Errorf("create schema v3 info table: %w", err)
	}
	return nil
}

func replaceSchemaV3InfoTable(db *gorm.DB) error {
	if err := createSchemaV3InfoTable(db, "schema_info__v3"); err != nil {
		return err
	}
	if err := db.Exec(`INSERT INTO schema_info__v3(version)
		SELECT version FROM schema_info`).Error; err != nil {
		return fmt.Errorf("copy schema_info to schema v3: %w", err)
	}
	if err := db.Exec(`DROP TABLE schema_info`).Error; err != nil {
		return fmt.Errorf("drop schema v2 schema_info: %w", err)
	}
	if err := db.Exec(`ALTER TABLE schema_info__v3 RENAME TO schema_info`).Error; err != nil {
		return fmt.Errorf("rename schema v3 schema_info: %w", err)
	}
	return nil
}

func validateSchemaV3(db *gorm.DB) error {
	requiredColumns := map[string][]string{
		"groups":             {"created_at_ms", "updated_at_ms"},
		"upstream_keys":      {"group_id", "created_at_ms", "updated_at_ms"},
		"access_keys":        {"daily_cost_limit_nano_usd", "monthly_cost_limit_nano_usd", "created_at_ms", "updated_at_ms"},
		"request_logs":       {"completed_at_ms", "duration_ms", "estimated_cost_nano_usd", "usage_state", "cost_state"},
		"usage_stats":        {"bucket_start_ms", "request_count", "success_count", "failure_count", "estimated_cost_nano_usd"},
		"model_prices":       {"input_price_nano_usd_per_million_tokens", "output_price_nano_usd_per_million_tokens"},
		"system_settings":    {"updated_at_ms"},
		"jobs":               {"created_at_ms", "started_at_ms", "finished_at_ms"},
		"control_operations": {"created_at_ms", "updated_at_ms", "completed_at_ms", "compacted_at_ms"},
		"schema_info":        {"version"},
	}
	for table, columns := range requiredColumns {
		for _, column := range columns {
			if !db.Migrator().HasColumn(table, column) {
				return fmt.Errorf(
					"validate SQLite schema version 3: %s.%s is missing",
					table,
					column,
				)
			}
		}
	}

	requiredTableFragments := map[string][]string{
		"groups": {
			"check (created_at_ms >= 0)",
			"check (updated_at_ms >= 0)",
		},
		"upstream_keys": {
			"check (created_at_ms >= 0)",
			"check (updated_at_ms >= 0)",
			"constraint chk_upstream_key_status check (status in ('active','disabled'))",
		},
		"access_keys": {
			"constraint chk_access_key_suffix check (key_suffix glob '[0-9a-f][0-9a-f][0-9a-f][0-9a-f]')",
			"constraint chk_access_key_status check (status in ('active','disabled'))",
			"constraint chk_access_key_daily_cost_limit_nano check (daily_cost_limit_nano_usd >= 0)",
			"constraint chk_access_key_monthly_cost_limit_nano check (monthly_cost_limit_nano_usd >= 0)",
		},
		"model_prices": {
			"constraint chk_model_price_input_nano check",
			"constraint chk_model_price_output_nano check",
			"constraint chk_model_price_cache_read_nano check",
			"constraint chk_model_price_cache_write_5m_nano check",
			"constraint chk_model_price_cache_write_1h_nano check",
			"constraint chk_model_price_source check (source = 'user')",
		},
		"request_logs": {
			"check (completed_at_ms >= 0)",
			"check (duration_ms >= 0)",
			"check (uncached_input_tokens >= 0)",
			"check (output_tokens >= 0)",
			"check (cache_read_tokens >= 0)",
			"check (cache_write_5m_tokens >= 0)",
			"check (cache_write_1h_tokens >= 0)",
			"constraint chk_request_log_cost_nano check (estimated_cost_nano_usd >= 0)",
			"constraint chk_request_log_usage_state check (usage_state in ('complete','partial','missing','not_applicable'))",
			"constraint chk_request_log_cost_state check (cost_state in ('priced','unpriced','not_applicable'))",
			"constraint chk_request_log_usage_cost_state check",
			"constraint chk_request_log_nonpriced_cost_zero check",
		},
		"usage_stats": {
			"check (bucket_start_ms >= 0)",
			"check (request_count >= 0)",
			"check (success_count >= 0)",
			"check (failure_count >= 0)",
			"check (uncached_input_tokens >= 0)",
			"check (output_tokens >= 0)",
			"check (cache_read_tokens >= 0)",
			"check (cache_write_5m_tokens >= 0)",
			"check (cache_write_1h_tokens >= 0)",
			"check (usage_missing_count >= 0)",
			"check (partial_count >= 0)",
			"check (unpriced_request_count >= 0)",
			"constraint chk_usage_stat_cost_nano check (estimated_cost_nano_usd >= 0)",
			"constraint chk_usage_stat_request_outcome check (request_count = success_count + failure_count)",
		},
		"system_settings": {
			"check (updated_at_ms >= 0)",
		},
		"jobs": {
			"check (created_at_ms >= 0)",
			"check (started_at_ms is null or started_at_ms >= 0)",
			"check (finished_at_ms is null or finished_at_ms >= 0)",
		},
		"control_operations": {
			"check (completed_at_ms is null or completed_at_ms >= 0)",
			"check (compacted_at_ms is null or compacted_at_ms >= 0)",
			"check (created_at_ms >= 0)",
			"check (updated_at_ms >= 0)",
			"constraint chk_control_operation_digest_version check (digest_version > 0)",
			"constraint chk_control_operation_digest check (length(request_digest) = 32)",
		},
		"schema_info": {
			"constraint chk_schema_info_version check (version = 3)",
		},
	}
	for table, fragments := range requiredTableFragments {
		statement, err := sqliteSchemaSQL(db, "table", table)
		if err != nil {
			return err
		}
		normalized := normalizeSQLiteDDL(statement)
		for _, fragment := range fragments {
			if !strings.Contains(normalized, normalizeSQLiteDDL(fragment)) {
				return fmt.Errorf(
					"validate SQLite schema version 3: %s is missing constraint %q",
					table,
					fragment,
				)
			}
		}
	}

	for _, expected := range schemaV3IndexStatements() {
		fields := strings.Fields(expected)
		if len(fields) < 4 {
			return fmt.Errorf("validate SQLite schema version 3: invalid canonical index DDL")
		}
		nameIndex := 2
		if strings.EqualFold(fields[1], "unique") {
			nameIndex = 3
		}
		name := fields[nameIndex]
		actual, err := sqliteSchemaSQL(db, "index", name)
		if err != nil {
			return err
		}
		if normalizeSQLiteDDL(actual) != normalizeSQLiteDDL(expected) {
			return fmt.Errorf(
				"validate SQLite schema version 3: critical index %s differs",
				name,
			)
		}
	}
	return validateSchemaV3ForeignKeys(db)
}

func sqliteSchemaSQL(db *gorm.DB, objectType, name string) (string, error) {
	var statement string
	result := db.Raw(
		`SELECT sql FROM sqlite_master WHERE type = ? AND name = ?`,
		objectType,
		name,
	).Scan(&statement)
	if result.Error != nil {
		return "", fmt.Errorf(
			"validate SQLite schema version 3: read %s %s: %w",
			objectType,
			name,
			result.Error,
		)
	}
	if result.RowsAffected != 1 || strings.TrimSpace(statement) == "" {
		return "", fmt.Errorf(
			"validate SQLite schema version 3: %s %s is missing",
			objectType,
			name,
		)
	}
	return statement, nil
}

func normalizeSQLiteDDL(statement string) string {
	return strings.ToLower(strings.Join(strings.Fields(statement), " "))
}
