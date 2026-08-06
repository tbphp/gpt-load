package storage

import (
	"fmt"

	"gorm.io/gorm"
)

func createSchemaV5Tables(db *gorm.DB) error {
	for _, statement := range schemaV5TableStatements() {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("create schema v5 table: %w", err)
		}
	}
	return nil
}

func schemaV5TableStatements() []string {
	return []string{
		`CREATE TABLE groups (
			id integer PRIMARY KEY AUTOINCREMENT,
			name varchar(255) NOT NULL,
			provider_id varchar(255),
			upstream_url text NOT NULL,
			protocols json NOT NULL,
			models json NOT NULL,
			convert_enabled numeric NOT NULL DEFAULT false,
			weight_manual integer,
			validation_model varchar(255),
			config json,
			enabled numeric NOT NULL DEFAULT true,
			created_at_ms integer NOT NULL CHECK (created_at_ms >= 0),
			updated_at_ms integer NOT NULL CHECK (updated_at_ms >= 0)
		)`,
		`CREATE TABLE upstream_keys (
			id integer PRIMARY KEY AUTOINCREMENT,
			group_id integer NOT NULL,
			key_value text NOT NULL,
			key_hash varchar(128) NOT NULL,
			status varchar(32) NOT NULL DEFAULT 'active',
			weight_manual integer,
			created_at_ms integer NOT NULL CHECK (created_at_ms >= 0),
			updated_at_ms integer NOT NULL CHECK (updated_at_ms >= 0),
			CONSTRAINT chk_upstream_key_status
				CHECK (status IN ('active','disabled')),
			FOREIGN KEY (group_id) REFERENCES groups(id)
				ON UPDATE CASCADE ON DELETE CASCADE
		)`,
		`CREATE TABLE access_keys (
			id integer PRIMARY KEY AUTOINCREMENT,
			name varchar(255) NOT NULL,
			key_value text NOT NULL,
			key_hash varchar(128) NOT NULL,
			key_suffix char(4) NOT NULL,
			status varchar(32) NOT NULL DEFAULT 'active',
			filters json,
			rpm_limit integer NOT NULL DEFAULT 0,
			daily_cost_limit_nano_usd integer NOT NULL DEFAULT 0,
			monthly_cost_limit_nano_usd integer NOT NULL DEFAULT 0,
			created_at_ms integer NOT NULL CHECK (created_at_ms >= 0),
			updated_at_ms integer NOT NULL CHECK (updated_at_ms >= 0),
			CONSTRAINT chk_access_key_suffix
				CHECK (key_suffix GLOB '[0-9a-f][0-9a-f][0-9a-f][0-9a-f]'),
			CONSTRAINT chk_access_key_status
				CHECK (status IN ('active','disabled')),
			CONSTRAINT chk_access_key_daily_cost_limit_nano
				CHECK (daily_cost_limit_nano_usd >= 0),
			CONSTRAINT chk_access_key_monthly_cost_limit_nano
				CHECK (monthly_cost_limit_nano_usd >= 0)
		)`,
		`CREATE TABLE request_logs (
			id varchar(36) PRIMARY KEY NOT NULL,
			completed_at_ms integer NOT NULL CHECK (completed_at_ms >= 0),
			access_key_id integer NOT NULL,
			group_id integer NOT NULL DEFAULT 0,
			protocol varchar(32) NOT NULL,
			client_model varchar(255) NOT NULL,
			upstream_model varchar(255) NOT NULL,
			status varchar(32) NOT NULL,
			status_code integer NOT NULL,
			stream numeric NOT NULL DEFAULT false,
			first_response_ms integer
				CHECK (first_response_ms IS NULL OR first_response_ms >= 0),
			duration_ms integer NOT NULL CHECK (duration_ms >= 0),
			attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
			error_code varchar(64) NOT NULL DEFAULT '',
			error_summary text NOT NULL DEFAULT '',
			affinity_hit numeric NOT NULL DEFAULT false,
			uncached_input_tokens integer NOT NULL DEFAULT 0
				CHECK (uncached_input_tokens >= 0),
			output_tokens integer NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
			cache_read_tokens integer NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0),
			cache_write_5m_tokens integer NOT NULL DEFAULT 0
				CHECK (cache_write_5m_tokens >= 0),
			cache_write_1h_tokens integer NOT NULL DEFAULT 0
				CHECK (cache_write_1h_tokens >= 0),
			cache_write_unknown_tokens integer NOT NULL DEFAULT 0
				CHECK (cache_write_unknown_tokens >= 0),
			estimated_cost_nano_usd integer NOT NULL DEFAULT 0,
			usage_state varchar(32) NOT NULL DEFAULT 'not_applicable',
			cost_state varchar(32) NOT NULL DEFAULT 'not_applicable',
			pricing_completeness varchar(32) NOT NULL DEFAULT 'not_applicable',
			CONSTRAINT chk_request_log_cost_nano
				CHECK (estimated_cost_nano_usd >= 0),
			CONSTRAINT chk_request_log_status
				CHECK (status IN ('success','error','incomplete','canceled')),
			CONSTRAINT chk_request_log_usage_state
				CHECK (usage_state IN ('complete','partial','missing','not_applicable')),
			CONSTRAINT chk_request_log_cost_state
				CHECK (cost_state IN ('priced','unpriced','not_applicable')),
			CONSTRAINT chk_request_log_pricing_completeness CHECK (
				pricing_completeness IN ('complete','partial','unavailable','not_applicable')
			),
			CONSTRAINT chk_request_log_usage_pricing_state CHECK (
				(usage_state = 'not_applicable'
					AND cost_state = 'not_applicable'
					AND pricing_completeness = 'not_applicable'
					AND estimated_cost_nano_usd = 0) OR
				(usage_state = 'missing'
					AND cost_state = 'unpriced'
					AND pricing_completeness = 'unavailable'
					AND estimated_cost_nano_usd = 0) OR
				(usage_state IN ('complete','partial') AND (
					(cost_state = 'unpriced'
						AND pricing_completeness = 'unavailable'
						AND estimated_cost_nano_usd = 0) OR
					(cost_state = 'priced'
						AND pricing_completeness IN ('complete','partial'))
				))
			)
		)`,
		`CREATE TABLE request_log_attempts (
			request_id varchar(36) NOT NULL,
			sequence integer NOT NULL CHECK (sequence > 0),
			completed_at_ms integer NOT NULL CHECK (completed_at_ms >= 0),
			group_id integer NOT NULL CHECK (group_id > 0),
			group_name varchar(255) NOT NULL,
			key_id integer NOT NULL CHECK (key_id > 0),
			upstream_model varchar(255) NOT NULL DEFAULT '',
			status_code integer NOT NULL,
			duration_ms integer NOT NULL CHECK (duration_ms >= 0),
			failure_category varchar(32) NOT NULL,
			action varchar(32) NOT NULL,
			will_retry numeric NOT NULL DEFAULT false,
			error_code varchar(64) NOT NULL DEFAULT '',
			error_summary text NOT NULL DEFAULT '',
			pricing_receipt json,
			PRIMARY KEY (request_id, sequence),
			CONSTRAINT chk_request_log_attempt_failure_category CHECK (
				failure_category IN (
					'ok','rate_limited','model_unavailable','invalid_key',
					'upstream_host_error','client_error','downstream_cancel','ambiguous'
				)
			),
			CONSTRAINT chk_request_log_attempt_action CHECK (
				action IN ('terminate','retry','cooldown_key','fail_key','skip_group')
			),
			FOREIGN KEY (request_id) REFERENCES request_logs(id)
				ON UPDATE CASCADE ON DELETE CASCADE
		)`,
		`CREATE TABLE usage_aggregation_journal (
			request_id varchar(36) PRIMARY KEY NOT NULL,
			bucket_start_ms integer NOT NULL CHECK (bucket_start_ms >= 0),
			access_key_id integer NOT NULL,
			group_id integer NOT NULL,
			model varchar(255) NOT NULL,
			request_count integer NOT NULL CHECK (request_count = 1),
			success_count integer NOT NULL CHECK (success_count >= 0),
			failure_count integer NOT NULL CHECK (failure_count >= 0),
			uncached_input_tokens integer NOT NULL CHECK (uncached_input_tokens >= 0),
			output_tokens integer NOT NULL CHECK (output_tokens >= 0),
			cache_read_tokens integer NOT NULL CHECK (cache_read_tokens >= 0),
			cache_write_5m_tokens integer NOT NULL CHECK (cache_write_5m_tokens >= 0),
			cache_write_1h_tokens integer NOT NULL CHECK (cache_write_1h_tokens >= 0),
			cache_write_unknown_tokens integer NOT NULL
				CHECK (cache_write_unknown_tokens >= 0),
			estimated_cost_nano_usd integer NOT NULL CHECK (estimated_cost_nano_usd >= 0),
			usage_missing_count integer NOT NULL CHECK (usage_missing_count >= 0),
			partial_count integer NOT NULL CHECK (partial_count >= 0),
			unpriced_request_count integer NOT NULL CHECK (unpriced_request_count >= 0),
			pricing_partial_count integer NOT NULL CHECK (pricing_partial_count >= 0),
			applied numeric NOT NULL DEFAULT false,
			CONSTRAINT chk_usage_journal_request_outcome
				CHECK (request_count = success_count + failure_count),
			CONSTRAINT chk_usage_journal_applied CHECK (applied IN (0, 1))
		)`,
		`CREATE TABLE usage_stats (
			id integer PRIMARY KEY AUTOINCREMENT,
			bucket_start_ms integer NOT NULL CHECK (bucket_start_ms >= 0),
			access_key_id integer NOT NULL,
			group_id integer NOT NULL,
			model varchar(255) NOT NULL,
			request_count integer NOT NULL DEFAULT 0 CHECK (request_count >= 0),
			success_count integer NOT NULL DEFAULT 0 CHECK (success_count >= 0),
			failure_count integer NOT NULL DEFAULT 0 CHECK (failure_count >= 0),
			uncached_input_tokens integer NOT NULL DEFAULT 0
				CHECK (uncached_input_tokens >= 0),
			output_tokens integer NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
			cache_read_tokens integer NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0),
			cache_write_5m_tokens integer NOT NULL DEFAULT 0
				CHECK (cache_write_5m_tokens >= 0),
			cache_write_1h_tokens integer NOT NULL DEFAULT 0
				CHECK (cache_write_1h_tokens >= 0),
			cache_write_unknown_tokens integer NOT NULL DEFAULT 0
				CHECK (cache_write_unknown_tokens >= 0),
			estimated_cost_nano_usd integer NOT NULL DEFAULT 0,
			usage_missing_count integer NOT NULL DEFAULT 0
				CHECK (usage_missing_count >= 0),
			partial_count integer NOT NULL DEFAULT 0 CHECK (partial_count >= 0),
			unpriced_request_count integer NOT NULL DEFAULT 0
				CHECK (unpriced_request_count >= 0),
			pricing_partial_count integer NOT NULL DEFAULT 0
				CHECK (pricing_partial_count >= 0),
			CONSTRAINT chk_usage_stat_cost_nano
				CHECK (estimated_cost_nano_usd >= 0),
			CONSTRAINT chk_usage_stat_request_outcome
				CHECK (request_count = success_count + failure_count)
		)`,
		`CREATE TABLE model_prices (
			id integer PRIMARY KEY AUTOINCREMENT,
			price_scope_key varchar(255) NOT NULL,
			model_id varchar(255) NOT NULL,
			input_price_nano_usd_per_million_tokens integer,
			output_price_nano_usd_per_million_tokens integer,
			cache_read_price_nano_usd_per_million_tokens integer,
			cache_write_price_nano_usd_per_million_tokens integer,
			context_price_tiers json,
			is_manual numeric NOT NULL DEFAULT false,
			created_at_ms integer NOT NULL CHECK (created_at_ms >= 0),
			updated_at_ms integer NOT NULL CHECK (updated_at_ms >= 0),
			CONSTRAINT chk_model_price_input_nano CHECK (
				input_price_nano_usd_per_million_tokens IS NULL OR
				input_price_nano_usd_per_million_tokens >= 0
			),
			CONSTRAINT chk_model_price_output_nano CHECK (
				output_price_nano_usd_per_million_tokens IS NULL OR
				output_price_nano_usd_per_million_tokens >= 0
			),
			CONSTRAINT chk_model_price_cache_read_nano CHECK (
				cache_read_price_nano_usd_per_million_tokens IS NULL OR
				cache_read_price_nano_usd_per_million_tokens >= 0
			),
			CONSTRAINT chk_model_price_cache_write_nano CHECK (
				cache_write_price_nano_usd_per_million_tokens IS NULL OR
				cache_write_price_nano_usd_per_million_tokens >= 0
			)
		)`,
		`CREATE TABLE system_settings (
			key varchar(255) PRIMARY KEY NOT NULL,
			value text NOT NULL,
			updated_at_ms integer NOT NULL CHECK (updated_at_ms >= 0)
		)`,
		`CREATE TABLE jobs (
			id varchar(36) PRIMARY KEY NOT NULL,
			type varchar(64) NOT NULL,
			status varchar(32) NOT NULL DEFAULT 'pending',
			payload json,
			result json,
			error text,
			created_at_ms integer NOT NULL CHECK (created_at_ms >= 0),
			started_at_ms integer CHECK (started_at_ms IS NULL OR started_at_ms >= 0),
			finished_at_ms integer CHECK (finished_at_ms IS NULL OR finished_at_ms >= 0)
		)`,
		`CREATE TABLE control_operations (
			commit_sequence integer PRIMARY KEY AUTOINCREMENT,
			operation_id char(36) NOT NULL,
			idempotency_key char(36) NOT NULL,
			digest_version integer NOT NULL,
			request_digest blob NOT NULL,
			operation_kind varchar(32) NOT NULL,
			resource_identity varchar(64) NOT NULL,
			canonical_result blob,
			required_stages json,
			last_completed_stage varchar(32),
			failed_stage varchar(32),
			completed_at_ms integer
				CHECK (completed_at_ms IS NULL OR completed_at_ms >= 0),
			compacted_at_ms integer
				CHECK (compacted_at_ms IS NULL OR compacted_at_ms >= 0),
			created_at_ms integer NOT NULL CHECK (created_at_ms >= 0),
			updated_at_ms integer NOT NULL CHECK (updated_at_ms >= 0),
			CONSTRAINT chk_control_operation_digest_version
				CHECK (digest_version > 0),
			CONSTRAINT chk_control_operation_digest
				CHECK (length(request_digest) = 32)
		)`,
	}
}

func createSchemaV5Indexes(db *gorm.DB) error {
	for _, statement := range schemaV5IndexStatements() {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("create schema v5 index: %w", err)
		}
	}
	return nil
}

func schemaV5IndexStatements() []string {
	return []string{
		`CREATE UNIQUE INDEX idx_groups_name ON groups(name)`,
		`CREATE UNIQUE INDEX idx_upstream_keys_group_hash
			ON upstream_keys(group_id, key_hash)`,
		`CREATE UNIQUE INDEX idx_access_keys_key_hash ON access_keys(key_hash)`,
		`CREATE INDEX idx_request_logs_completed_id
			ON request_logs(completed_at_ms DESC, id DESC)`,
		`CREATE INDEX idx_request_logs_access_completed_id
			ON request_logs(access_key_id, completed_at_ms DESC, id DESC)`,
		`CREATE INDEX idx_request_logs_status_completed_id
			ON request_logs(status, completed_at_ms DESC, id DESC)`,
		`CREATE INDEX idx_request_logs_model_completed_id
			ON request_logs(client_model, completed_at_ms DESC, id DESC)`,
		`CREATE INDEX idx_request_logs_upstream_model_completed_id
			ON request_logs(upstream_model, completed_at_ms DESC, id DESC)`,
		`CREATE INDEX idx_request_log_attempts_group_completed_request
			ON request_log_attempts(group_id, completed_at_ms DESC, request_id)`,
		`CREATE INDEX idx_request_log_attempts_key_completed_request
			ON request_log_attempts(key_id, completed_at_ms DESC, request_id)`,
		`CREATE INDEX idx_request_log_attempts_model_completed_request
			ON request_log_attempts(upstream_model, completed_at_ms DESC, request_id)`,
		`CREATE INDEX idx_request_log_attempts_status_completed_request
			ON request_log_attempts(status_code, completed_at_ms DESC, request_id)`,
		`CREATE INDEX idx_request_log_attempts_failure_completed_request
			ON request_log_attempts(failure_category, completed_at_ms DESC, request_id)`,
		`CREATE INDEX idx_request_log_attempts_error_completed_request
			ON request_log_attempts(error_code, completed_at_ms DESC, request_id)`,
		`CREATE UNIQUE INDEX idx_usage_stats_bucket_access_group_model
			ON usage_stats(bucket_start_ms, access_key_id, group_id, model)`,
		`CREATE INDEX idx_usage_aggregation_journal_pending_bucket
			ON usage_aggregation_journal(applied, bucket_start_ms, request_id)`,
		`CREATE UNIQUE INDEX idx_model_prices_scope_model
			ON model_prices(price_scope_key, model_id)`,
		`CREATE INDEX idx_jobs_type ON jobs(type)`,
		`CREATE INDEX idx_jobs_status ON jobs(status)`,
		`CREATE INDEX idx_jobs_created_at_ms ON jobs(created_at_ms)`,
		`CREATE UNIQUE INDEX idx_control_operations_operation_id
			ON control_operations(operation_id)`,
		`CREATE UNIQUE INDEX idx_control_operations_idempotency_key
			ON control_operations(idempotency_key)`,
		`CREATE INDEX idx_control_operations_completed_at_ms
			ON control_operations(completed_at_ms)`,
	}
}

func validateSchemaV5ForeignKeys(db *gorm.DB) error {
	var violations []struct {
		Table string
		RowID int64 `gorm:"column:rowid"`
	}
	if err := db.Raw("PRAGMA foreign_key_check").Scan(&violations).Error; err != nil {
		return fmt.Errorf("validate schema v5 foreign keys: %w", err)
	}
	if len(violations) != 0 {
		return fmt.Errorf("validate schema v5 foreign keys: %d violation(s)", len(violations))
	}
	return nil
}
