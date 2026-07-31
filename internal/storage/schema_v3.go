package storage

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/platform/epochms"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
	"gpt-load/internal/usagecost"
)

const schemaV2Version uint = 2

type legacyGroupV2 struct {
	ID              uint
	Name            string
	UpstreamURL     string
	Protocols       models.JSON
	Models          models.JSON
	ConvertEnabled  bool
	WeightManual    *int
	ValidationModel *string
	Config          models.JSON
	Enabled         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type legacyUpstreamKeyV2 struct {
	ID           uint
	GroupID      uint
	KeyValue     string
	KeyHash      string
	Status       string
	WeightManual *int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type legacyAccessKeyV2 struct {
	ID               uint
	Name             string
	KeyValue         string
	KeyHash          string
	KeySuffix        string
	Status           string
	Filters          models.JSON
	RPMLimit         int64
	DailyCostLimit   float64
	MonthlyCostLimit float64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type legacyModelPriceV2 struct {
	ID                uint
	Pattern           string
	InputPrice        *float64
	OutputPrice       *float64
	CacheReadPrice    *float64
	CacheWrite5MPrice *float64 `gorm:"column:cache_write_5m_price"`
	CacheWrite1HPrice *float64 `gorm:"column:cache_write_1h_price"`
	Source            string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type legacyRequestLogV2 struct {
	ID                 string
	CreatedAt          time.Time
	AccessKeyID        uint
	GroupID            uint
	Protocol           string
	ClientModel        string
	UpstreamModel      string
	Status             string
	StatusCode         int
	DurationMs         int64
	ErrorCode          string
	ErrorSummary       string
	AffinityHit        bool
	InputTokens        int64
	OutputTokens       int64
	CacheReadTokens    int64
	CacheWrite5MTokens int64 `gorm:"column:cache_write_5m_tokens"`
	CacheWrite1HTokens int64 `gorm:"column:cache_write_1h_tokens"`
	Cost               float64
	UsageState         string
	CostState          string
	Attempts           models.JSON
}

type legacySystemSettingV2 struct {
	Key       string
	Value     string
	UpdatedAt time.Time
}

type legacyJobV2 struct {
	ID         string
	Type       string
	Status     string
	Payload    models.JSON
	Result     models.JSON
	Error      string
	CreatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

type legacyControlOperationV2 struct {
	CommitSequence     uint64
	OperationID        string
	IdempotencyKey     string
	DigestVersion      uint
	RequestDigest      []byte
	OperationKind      string
	ResourceIdentity   string
	CanonicalResult    []byte
	RequiredStages     models.JSON
	LastCompletedStage string
	FailedStage        string
	CompletedAt        *time.Time
	CompactedAt        *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func migrateSchemaV2ToV3(db *gorm.DB) error {
	if err := createSchemaV3MigrationTables(db); err != nil {
		return err
	}
	if err := copyGroupsToSchemaV3(db); err != nil {
		return err
	}
	if err := copyUpstreamKeysToSchemaV3(db); err != nil {
		return err
	}
	if err := copyAccessKeysToSchemaV3(db); err != nil {
		return err
	}
	if err := copyModelPricesToSchemaV3(db); err != nil {
		return err
	}
	if err := copyRequestLogsToSchemaV3(db); err != nil {
		return err
	}
	if err := copySystemSettingsToSchemaV3(db); err != nil {
		return err
	}
	if err := copyJobsToSchemaV3(db); err != nil {
		return err
	}
	if err := copyControlOperationsToSchemaV3(db); err != nil {
		return err
	}
	if err := backfillUsageStatsV3(db); err != nil {
		return err
	}
	if err := replaceSchemaV2Tables(db); err != nil {
		return err
	}
	if err := createSchemaV3Indexes(db); err != nil {
		return err
	}
	if err := validateSchemaV3ForeignKeys(db); err != nil {
		return err
	}

	result := db.Model(&schemaInfo{}).
		Where("version = ?", schemaV2Version).
		Update("version", CurrentSchemaVersion)
	if result.Error != nil {
		return fmt.Errorf("update schema_info to version 3: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("update schema_info to version 3: version changed concurrently")
	}
	if err := replaceSchemaV3InfoTable(db); err != nil {
		return err
	}
	return validateSchemaV3(db)
}

func createSchemaV3MigrationTables(db *gorm.DB) error {
	return createSchemaV3Tables(db, "__v3")
}

func createSchemaV3Tables(db *gorm.DB, suffix string) error {
	if suffix != "" && suffix != "__v3" {
		return fmt.Errorf("create schema v3 tables: unsupported suffix %q", suffix)
	}
	statements := []string{
		`CREATE TABLE groups__v3 (
			id integer PRIMARY KEY AUTOINCREMENT,
			name varchar(255) NOT NULL,
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
		`CREATE TABLE upstream_keys__v3 (
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
			FOREIGN KEY (group_id) REFERENCES groups__v3(id)
				ON UPDATE CASCADE ON DELETE CASCADE
		)`,
		`CREATE TABLE access_keys__v3 (
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
		`CREATE TABLE model_prices__v3 (
			id integer PRIMARY KEY AUTOINCREMENT,
			pattern varchar(255) NOT NULL,
			input_price_nano_usd_per_million_tokens integer,
			output_price_nano_usd_per_million_tokens integer,
			cache_read_price_nano_usd_per_million_tokens integer,
			cache_write_5m_price_nano_usd_per_million_tokens integer,
			cache_write_1h_price_nano_usd_per_million_tokens integer,
			source varchar(32) NOT NULL,
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
			CONSTRAINT chk_model_price_cache_write_5m_nano CHECK (
				cache_write_5m_price_nano_usd_per_million_tokens IS NULL OR
				cache_write_5m_price_nano_usd_per_million_tokens >= 0
			),
			CONSTRAINT chk_model_price_cache_write_1h_nano CHECK (
				cache_write_1h_price_nano_usd_per_million_tokens IS NULL OR
				cache_write_1h_price_nano_usd_per_million_tokens >= 0
			),
			CONSTRAINT chk_model_price_source CHECK (source = 'user')
		)`,
		`CREATE TABLE request_logs__v3 (
			id varchar(36) PRIMARY KEY NOT NULL,
			completed_at_ms integer NOT NULL CHECK (completed_at_ms >= 0),
			access_key_id integer NOT NULL,
			group_id integer NOT NULL DEFAULT 0,
			protocol varchar(32) NOT NULL,
			client_model varchar(255) NOT NULL,
			upstream_model varchar(255) NOT NULL,
			status varchar(32) NOT NULL,
			status_code integer NOT NULL,
			duration_ms integer NOT NULL CHECK (duration_ms >= 0),
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
			estimated_cost_nano_usd integer NOT NULL DEFAULT 0,
			usage_state varchar(32) NOT NULL DEFAULT 'not_applicable',
			cost_state varchar(32) NOT NULL DEFAULT 'not_applicable',
			attempts json,
			CONSTRAINT chk_request_log_cost_nano
				CHECK (estimated_cost_nano_usd >= 0),
			CONSTRAINT chk_request_log_usage_state
				CHECK (usage_state IN ('complete','partial','missing','not_applicable')),
			CONSTRAINT chk_request_log_cost_state
				CHECK (cost_state IN ('priced','unpriced','not_applicable')),
			CONSTRAINT chk_request_log_usage_cost_state CHECK (
				(usage_state IN ('complete','partial')
					AND cost_state IN ('priced','unpriced')) OR
				(usage_state = 'missing' AND cost_state = 'unpriced') OR
				(usage_state = 'not_applicable' AND cost_state = 'not_applicable')
			),
			CONSTRAINT chk_request_log_nonpriced_cost_zero CHECK (
				cost_state = 'priced' OR estimated_cost_nano_usd = 0
			)
		)`,
		`CREATE TABLE usage_stats__v3 (
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
			estimated_cost_nano_usd integer NOT NULL DEFAULT 0,
			usage_missing_count integer NOT NULL DEFAULT 0
				CHECK (usage_missing_count >= 0),
			partial_count integer NOT NULL DEFAULT 0 CHECK (partial_count >= 0),
			unpriced_request_count integer NOT NULL DEFAULT 0
				CHECK (unpriced_request_count >= 0),
			CONSTRAINT chk_usage_stat_cost_nano
				CHECK (estimated_cost_nano_usd >= 0),
			CONSTRAINT chk_usage_stat_request_outcome
				CHECK (request_count = success_count + failure_count)
		)`,
		`CREATE TABLE system_settings__v3 (
			key varchar(255) PRIMARY KEY NOT NULL,
			value text NOT NULL,
			updated_at_ms integer NOT NULL CHECK (updated_at_ms >= 0)
		)`,
		`CREATE TABLE jobs__v3 (
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
		`CREATE TABLE control_operations__v3 (
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
	for _, statement := range statements {
		statement = strings.ReplaceAll(statement, "__v3", suffix)
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("create schema v3 table: %w", err)
		}
	}
	return nil
}

func copyGroupsToSchemaV3(db *gorm.DB) error {
	return forEachLegacyRow[uint, legacyGroupV2](
		db,
		"groups",
		"id",
		func(row legacyGroupV2) error {
			createdAtMS, err := legacyTimeToMS("groups", row.ID, "created_at", row.CreatedAt)
			if err != nil {
				return err
			}
			updatedAtMS, err := legacyTimeToMS("groups", row.ID, "updated_at", row.UpdatedAt)
			if err != nil {
				return err
			}
			if err := db.Exec(`INSERT INTO groups__v3 (
				id, name, upstream_url, protocols, models, convert_enabled, weight_manual,
				validation_model, config, enabled, created_at_ms, updated_at_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				row.ID, row.Name, row.UpstreamURL, row.Protocols, row.Models,
				row.ConvertEnabled, row.WeightManual, row.ValidationModel, row.Config,
				row.Enabled, createdAtMS, updatedAtMS,
			).Error; err != nil {
				return schemaV3RowError("groups", row.ID, err)
			}
			return nil
		},
	)
}

func copyUpstreamKeysToSchemaV3(db *gorm.DB) error {
	return forEachLegacyRow[uint, legacyUpstreamKeyV2](
		db,
		"upstream_keys",
		"id",
		func(row legacyUpstreamKeyV2) error {
			createdAtMS, err := legacyTimeToMS("upstream_keys", row.ID, "created_at", row.CreatedAt)
			if err != nil {
				return err
			}
			updatedAtMS, err := legacyTimeToMS("upstream_keys", row.ID, "updated_at", row.UpdatedAt)
			if err != nil {
				return err
			}
			if err := db.Exec(`INSERT INTO upstream_keys__v3 (
				id, group_id, key_value, key_hash, status, weight_manual,
				created_at_ms, updated_at_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				row.ID, row.GroupID, row.KeyValue, row.KeyHash, row.Status,
				row.WeightManual, createdAtMS, updatedAtMS,
			).Error; err != nil {
				return schemaV3RowError("upstream_keys", row.ID, err)
			}
			return nil
		},
	)
}

func copyAccessKeysToSchemaV3(db *gorm.DB) error {
	return forEachLegacyRow[uint, legacyAccessKeyV2](
		db,
		"access_keys",
		"id",
		func(row legacyAccessKeyV2) error {
			createdAtMS, err := legacyTimeToMS("access_keys", row.ID, "created_at", row.CreatedAt)
			if err != nil {
				return err
			}
			updatedAtMS, err := legacyTimeToMS("access_keys", row.ID, "updated_at", row.UpdatedAt)
			if err != nil {
				return err
			}
			dailyCostLimitNanoUSD, err := legacyUSDToNano(
				"access_keys",
				row.ID,
				"daily_cost_limit",
				row.DailyCostLimit,
			)
			if err != nil {
				return err
			}
			monthlyCostLimitNanoUSD, err := legacyUSDToNano(
				"access_keys",
				row.ID,
				"monthly_cost_limit",
				row.MonthlyCostLimit,
			)
			if err != nil {
				return err
			}
			if err := db.Exec(`INSERT INTO access_keys__v3 (
				id, name, key_value, key_hash, key_suffix, status, filters, rpm_limit,
				daily_cost_limit_nano_usd, monthly_cost_limit_nano_usd,
				created_at_ms, updated_at_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				row.ID, row.Name, row.KeyValue, row.KeyHash, row.KeySuffix, row.Status,
				row.Filters, row.RPMLimit, dailyCostLimitNanoUSD, monthlyCostLimitNanoUSD,
				createdAtMS, updatedAtMS,
			).Error; err != nil {
				return schemaV3RowError("access_keys", row.ID, err)
			}
			return nil
		},
	)
}

func copyModelPricesToSchemaV3(db *gorm.DB) error {
	return forEachLegacyRow[uint, legacyModelPriceV2](
		db,
		"model_prices",
		"id",
		func(row legacyModelPriceV2) error {
			createdAtMS, err := legacyTimeToMS("model_prices", row.ID, "created_at", row.CreatedAt)
			if err != nil {
				return err
			}
			updatedAtMS, err := legacyTimeToMS("model_prices", row.ID, "updated_at", row.UpdatedAt)
			if err != nil {
				return err
			}
			input, err := legacyOptionalUSDToNano("model_prices", row.ID, "input_price", row.InputPrice)
			if err != nil {
				return err
			}
			output, err := legacyOptionalUSDToNano("model_prices", row.ID, "output_price", row.OutputPrice)
			if err != nil {
				return err
			}
			cacheRead, err := legacyOptionalUSDToNano(
				"model_prices", row.ID, "cache_read_price", row.CacheReadPrice,
			)
			if err != nil {
				return err
			}
			cacheWrite5M, err := legacyOptionalUSDToNano(
				"model_prices", row.ID, "cache_write_5m_price", row.CacheWrite5MPrice,
			)
			if err != nil {
				return err
			}
			cacheWrite1H, err := legacyOptionalUSDToNano(
				"model_prices", row.ID, "cache_write_1h_price", row.CacheWrite1HPrice,
			)
			if err != nil {
				return err
			}
			if err := db.Exec(`INSERT INTO model_prices__v3 (
				id, pattern,
				input_price_nano_usd_per_million_tokens,
				output_price_nano_usd_per_million_tokens,
				cache_read_price_nano_usd_per_million_tokens,
				cache_write_5m_price_nano_usd_per_million_tokens,
				cache_write_1h_price_nano_usd_per_million_tokens,
				source, created_at_ms, updated_at_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				row.ID, row.Pattern, input, output, cacheRead, cacheWrite5M,
				cacheWrite1H, row.Source, createdAtMS, updatedAtMS,
			).Error; err != nil {
				return schemaV3RowError("model_prices", row.ID, err)
			}
			return nil
		},
	)
}

func copyRequestLogsToSchemaV3(db *gorm.DB) error {
	return forEachLegacyRow[string, legacyRequestLogV2](
		db,
		"request_logs",
		"id",
		func(row legacyRequestLogV2) error {
			if err := validateLegacyRequestLog(row); err != nil {
				return schemaV3RowError("request_logs", row.ID, err)
			}
			completedAtMS, err := legacyTimeToMS(
				"request_logs", row.ID, "created_at", row.CreatedAt,
			)
			if err != nil {
				return err
			}
			cost, err := legacyUSDToNano(
				"request_logs", row.ID, "cost", row.Cost,
			)
			if err != nil {
				return err
			}
			if err := usagecost.Validate(
				usage.State(row.UsageState),
				pricing.CostState(row.CostState),
				cost,
			); err != nil {
				return schemaV3RowError(
					"request_logs",
					row.ID,
					fmt.Errorf("validate usage/cost state: %w", err),
				)
			}
			if err := db.Exec(`INSERT INTO request_logs__v3 (
				id, completed_at_ms, access_key_id, group_id, protocol, client_model,
				upstream_model, status, status_code, duration_ms, error_code,
				error_summary, affinity_hit, uncached_input_tokens, output_tokens,
				cache_read_tokens, cache_write_5m_tokens, cache_write_1h_tokens,
				estimated_cost_nano_usd, usage_state, cost_state, attempts
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				row.ID, completedAtMS, row.AccessKeyID, row.GroupID, row.Protocol,
				row.ClientModel, row.UpstreamModel, row.Status, row.StatusCode,
				row.DurationMs, row.ErrorCode, row.ErrorSummary, row.AffinityHit,
				row.InputTokens, row.OutputTokens, row.CacheReadTokens,
				row.CacheWrite5MTokens, row.CacheWrite1HTokens, cost,
				row.UsageState, row.CostState, row.Attempts,
			).Error; err != nil {
				return schemaV3RowError("request_logs", row.ID, err)
			}
			return nil
		},
	)
}

func copySystemSettingsToSchemaV3(db *gorm.DB) error {
	return forEachLegacyRow[string, legacySystemSettingV2](
		db,
		"system_settings",
		"key",
		func(row legacySystemSettingV2) error {
			updatedAtMS, err := legacyTimeToMS(
				"system_settings", row.Key, "updated_at", row.UpdatedAt,
			)
			if err != nil {
				return err
			}
			if err := db.Exec(`INSERT INTO system_settings__v3 (
				key, value, updated_at_ms
			) VALUES (?, ?, ?)`, row.Key, row.Value, updatedAtMS).Error; err != nil {
				return schemaV3RowError("system_settings", row.Key, err)
			}
			return nil
		},
	)
}

func copyJobsToSchemaV3(db *gorm.DB) error {
	return forEachLegacyRow[string, legacyJobV2](
		db,
		"jobs",
		"id",
		func(row legacyJobV2) error {
			createdAtMS, err := legacyTimeToMS("jobs", row.ID, "created_at", row.CreatedAt)
			if err != nil {
				return err
			}
			startedAtMS, err := legacyOptionalTimeToMS(
				"jobs", row.ID, "started_at", row.StartedAt,
			)
			if err != nil {
				return err
			}
			finishedAtMS, err := legacyOptionalTimeToMS(
				"jobs", row.ID, "finished_at", row.FinishedAt,
			)
			if err != nil {
				return err
			}
			if err := db.Exec(`INSERT INTO jobs__v3 (
				id, type, status, payload, result, error, created_at_ms,
				started_at_ms, finished_at_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				row.ID, row.Type, row.Status, row.Payload, row.Result, row.Error,
				createdAtMS, startedAtMS, finishedAtMS,
			).Error; err != nil {
				return schemaV3RowError("jobs", row.ID, err)
			}
			return nil
		},
	)
}

func copyControlOperationsToSchemaV3(db *gorm.DB) error {
	return forEachLegacyRow[uint64, legacyControlOperationV2](
		db,
		"control_operations",
		"commit_sequence",
		func(row legacyControlOperationV2) error {
			createdAtMS, err := legacyTimeToMS(
				"control_operations", row.CommitSequence, "created_at", row.CreatedAt,
			)
			if err != nil {
				return err
			}
			updatedAtMS, err := legacyTimeToMS(
				"control_operations", row.CommitSequence, "updated_at", row.UpdatedAt,
			)
			if err != nil {
				return err
			}
			completedAtMS, err := legacyOptionalTimeToMS(
				"control_operations", row.CommitSequence, "completed_at", row.CompletedAt,
			)
			if err != nil {
				return err
			}
			compactedAtMS, err := legacyOptionalTimeToMS(
				"control_operations", row.CommitSequence, "compacted_at", row.CompactedAt,
			)
			if err != nil {
				return err
			}
			if err := db.Exec(`INSERT INTO control_operations__v3 (
				commit_sequence, operation_id, idempotency_key, digest_version,
				request_digest, operation_kind, resource_identity, canonical_result,
				required_stages, last_completed_stage, failed_stage, completed_at_ms,
				compacted_at_ms, created_at_ms, updated_at_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				row.CommitSequence, row.OperationID, row.IdempotencyKey,
				row.DigestVersion, row.RequestDigest, row.OperationKind,
				row.ResourceIdentity, row.CanonicalResult, row.RequiredStages,
				row.LastCompletedStage, row.FailedStage, completedAtMS,
				compactedAtMS, createdAtMS, updatedAtMS,
			).Error; err != nil {
				return schemaV3RowError("control_operations", row.CommitSequence, err)
			}
			return nil
		},
	)
}

func backfillUsageStatsV3(db *gorm.DB) error {
	const statement = `
INSERT INTO usage_stats__v3 (
	bucket_start_ms,
	access_key_id,
	group_id,
	model,
	request_count,
	success_count,
	failure_count,
	uncached_input_tokens,
	cache_read_tokens,
	cache_write_5m_tokens,
	cache_write_1h_tokens,
	output_tokens,
	estimated_cost_nano_usd,
	usage_missing_count,
	partial_count,
	unpriced_request_count
)
SELECT
	completed_at_ms / 3600000 * 3600000,
	access_key_id,
	group_id,
	client_model,
	COUNT(*),
	SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END),
	SUM(CASE WHEN status IN ('error', 'incomplete', 'canceled') THEN 1 ELSE 0 END),
	SUM(CASE WHEN usage_state IN ('complete', 'partial')
		THEN uncached_input_tokens ELSE 0 END),
	SUM(CASE WHEN usage_state IN ('complete', 'partial')
		THEN cache_read_tokens ELSE 0 END),
	SUM(CASE WHEN usage_state IN ('complete', 'partial')
		THEN cache_write_5m_tokens ELSE 0 END),
	SUM(CASE WHEN usage_state IN ('complete', 'partial')
		THEN cache_write_1h_tokens ELSE 0 END),
	SUM(CASE WHEN usage_state IN ('complete', 'partial')
		THEN output_tokens ELSE 0 END),
	SUM(CASE WHEN usage_state IN ('complete', 'partial') AND cost_state = 'priced'
		THEN estimated_cost_nano_usd ELSE 0 END),
	SUM(CASE WHEN usage_state = 'missing' THEN 1 ELSE 0 END),
	SUM(CASE WHEN usage_state = 'partial' THEN 1 ELSE 0 END),
	SUM(CASE WHEN usage_state IN ('complete', 'partial') AND cost_state = 'unpriced'
		THEN 1 ELSE 0 END)
FROM request_logs__v3
GROUP BY
	completed_at_ms / 3600000 * 3600000,
	access_key_id,
	group_id,
	client_model`
	if err := db.Exec(statement).Error; err != nil {
		return fmt.Errorf("backfill schema v3 usage_stats from request_logs: %w", err)
	}
	return nil
}

func replaceSchemaV2Tables(db *gorm.DB) error {
	for _, table := range []string{
		"upstream_keys",
		"groups",
		"access_keys",
		"usage_stats",
		"request_logs",
		"model_prices",
		"system_settings",
		"jobs",
		"control_operations",
	} {
		if err := db.Exec("DROP TABLE " + table).Error; err != nil {
			return fmt.Errorf("drop schema v2 table %s: %w", table, err)
		}
	}
	for _, table := range []string{
		"groups",
		"upstream_keys",
		"access_keys",
		"request_logs",
		"usage_stats",
		"model_prices",
		"system_settings",
		"jobs",
		"control_operations",
	} {
		if err := db.Exec(
			"ALTER TABLE " + table + "__v3 RENAME TO " + table,
		).Error; err != nil {
			return fmt.Errorf("rename schema v3 table %s: %w", table, err)
		}
	}
	return nil
}

func createSchemaV3Indexes(db *gorm.DB) error {
	for _, statement := range schemaV3IndexStatements() {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("create schema v3 index: %w", err)
		}
	}
	return nil
}

func schemaV3IndexStatements() []string {
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
		`CREATE UNIQUE INDEX idx_usage_stats_bucket_access_group_model
			ON usage_stats(bucket_start_ms, access_key_id, group_id, model)`,
		`CREATE UNIQUE INDEX idx_model_prices_pattern ON model_prices(pattern)`,
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

func validateSchemaV3ForeignKeys(db *gorm.DB) error {
	var violations []struct {
		Table string
		RowID int64 `gorm:"column:rowid"`
	}
	if err := db.Raw("PRAGMA foreign_key_check").Scan(&violations).Error; err != nil {
		return fmt.Errorf("validate schema v3 foreign keys: %w", err)
	}
	if len(violations) != 0 {
		return fmt.Errorf(
			"validate schema v3 foreign keys: %d violation(s)",
			len(violations),
		)
	}
	return nil
}

func forEachLegacyRow[K ~string | ~uint | ~uint64, R any](
	db *gorm.DB,
	table string,
	keyColumn string,
	visit func(R) error,
) error {
	var identities []K
	if err := db.Table(table).Order(keyColumn+" ASC").
		Pluck(keyColumn, &identities).Error; err != nil {
		return fmt.Errorf("read schema v2 %s identities: %w", table, err)
	}
	for _, identity := range identities {
		var row R
		if err := db.Table(table).
			Where(keyColumn+" = ?", identity).
			Take(&row).Error; err != nil {
			return schemaV3RowError(table, identity, err)
		}
		if err := visit(row); err != nil {
			return err
		}
	}
	return nil
}

func legacyTimeToMS(
	table string,
	identity any,
	field string,
	value time.Time,
) (int64, error) {
	result, err := epochms.FromTime(value)
	if err != nil {
		return 0, schemaV3RowError(
			table,
			identity,
			fmt.Errorf("convert %s to epoch milliseconds: %w", field, err),
		)
	}
	return result, nil
}

func legacyOptionalTimeToMS(
	table string,
	identity any,
	field string,
	value *time.Time,
) (*int64, error) {
	if value == nil {
		return nil, nil
	}
	result, err := legacyTimeToMS(table, identity, field, *value)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func legacyOptionalUSDToNano(
	table string,
	identity any,
	field string,
	value *float64,
) (*int64, error) {
	if value == nil {
		return nil, nil
	}
	result, err := legacyUSDToNano(table, identity, field, *value)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func legacyUSDToNano(
	table string,
	identity any,
	field string,
	value float64,
) (int64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, schemaV3RowError(
			table,
			identity,
			fmt.Errorf("convert %s to nano USD: invalid non-negative finite amount", field),
		)
	}

	decimal := strconv.FormatFloat(value, 'g', -1, 64)
	rational := new(big.Rat)
	if _, ok := rational.SetString(decimal); !ok || rational.Sign() < 0 {
		return 0, schemaV3RowError(
			table,
			identity,
			fmt.Errorf("convert %s to nano USD: invalid decimal", field),
		)
	}
	numerator := new(big.Int).Mul(rational.Num(), big.NewInt(1_000_000_000))
	denominator := rational.Denom()
	rounded, remainder := new(big.Int), new(big.Int)
	rounded.QuoRem(numerator, denominator, remainder)
	if new(big.Int).Lsh(remainder, 1).Cmp(denominator) >= 0 {
		rounded.Add(rounded, big.NewInt(1))
	}
	if rounded.Sign() < 0 || !rounded.IsInt64() {
		return 0, schemaV3RowError(
			table,
			identity,
			fmt.Errorf("convert %s to nano USD: amount exceeds int64", field),
		)
	}

	normalized := pricing.FormatUSD(pricing.NanoUSD(rounded.Int64()))
	parsed, err := pricing.ParseUSD(normalized)
	if err != nil {
		return 0, schemaV3RowError(
			table,
			identity,
			fmt.Errorf("convert %s to nano USD: %w", field, err),
		)
	}
	return int64(parsed), nil
}

func validateLegacyRequestLog(row legacyRequestLogV2) error {
	switch row.Status {
	case string(telemetry.RequestStatusSuccess),
		string(telemetry.RequestStatusError),
		string(telemetry.RequestStatusIncomplete),
		string(telemetry.RequestStatusCanceled):
	default:
		return fmt.Errorf("invalid request status %q", row.Status)
	}
	if row.DurationMs < 0 {
		return fmt.Errorf("negative duration_ms")
	}
	for name, value := range map[string]int64{
		"input_tokens":          row.InputTokens,
		"output_tokens":         row.OutputTokens,
		"cache_read_tokens":     row.CacheReadTokens,
		"cache_write_5m_tokens": row.CacheWrite5MTokens,
		"cache_write_1h_tokens": row.CacheWrite1HTokens,
	} {
		if value < 0 {
			return fmt.Errorf("negative %s", name)
		}
	}
	return nil
}

func schemaV3RowError(table string, identity any, err error) error {
	return fmt.Errorf("migrate %s row %v to schema v3: %w", table, identity, err)
}

func ensureSchemaV2TablesForV1(db *gorm.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS groups (
			id integer PRIMARY KEY AUTOINCREMENT,
			name varchar(255) NOT NULL,
			upstream_url text NOT NULL,
			protocols json NOT NULL,
			models json NOT NULL,
			convert_enabled numeric NOT NULL DEFAULT false,
			weight_manual integer,
			validation_model varchar(255),
			config json,
			enabled numeric NOT NULL DEFAULT true,
			created_at datetime,
			updated_at datetime
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_groups_name ON groups(name)`,
		`CREATE TABLE IF NOT EXISTS upstream_keys (
			id integer PRIMARY KEY AUTOINCREMENT,
			group_id integer NOT NULL,
			key_value text NOT NULL,
			key_hash varchar(128) NOT NULL,
			status varchar(32) NOT NULL DEFAULT 'active',
			weight_manual integer,
			request_count integer NOT NULL DEFAULT 0,
			tokens_total integer NOT NULL DEFAULT 0,
			cost_total real NOT NULL DEFAULT 0,
			created_at datetime,
			updated_at datetime,
			CONSTRAINT chk_upstream_key_status
				CHECK (status IN ('active','disabled')),
			FOREIGN KEY (group_id) REFERENCES groups(id)
				ON UPDATE CASCADE ON DELETE CASCADE
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_upstream_keys_group_hash
			ON upstream_keys(group_id, key_hash)`,
		`CREATE TABLE IF NOT EXISTS access_keys (
			id integer PRIMARY KEY AUTOINCREMENT,
			name varchar(255) NOT NULL,
			key_value text NOT NULL,
			key_hash varchar(128) NOT NULL,
			key_suffix char(4) NOT NULL,
			status varchar(32) NOT NULL DEFAULT 'active',
			filters json,
			rpm_limit integer NOT NULL DEFAULT 0,
			daily_cost_limit real NOT NULL DEFAULT 0,
			monthly_cost_limit real NOT NULL DEFAULT 0,
			created_at datetime,
			updated_at datetime,
			CONSTRAINT chk_access_key_suffix
				CHECK (key_suffix GLOB '[0-9a-f][0-9a-f][0-9a-f][0-9a-f]'),
			CONSTRAINT chk_access_key_status
				CHECK (status IN ('active','disabled'))
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_access_keys_key_hash
			ON access_keys(key_hash)`,
		`CREATE TABLE IF NOT EXISTS request_logs (
			id varchar(36) PRIMARY KEY NOT NULL,
			created_at datetime NOT NULL,
			access_key_id integer NOT NULL,
			group_id integer NOT NULL DEFAULT 0,
			protocol varchar(32) NOT NULL,
			client_model varchar(255) NOT NULL,
			upstream_model varchar(255) NOT NULL,
			status varchar(32) NOT NULL,
			status_code integer NOT NULL,
			duration_ms integer NOT NULL,
			error_code varchar(64) NOT NULL DEFAULT '',
			error_summary text NOT NULL DEFAULT '',
			affinity_hit numeric NOT NULL DEFAULT false,
			input_tokens integer NOT NULL DEFAULT 0,
			output_tokens integer NOT NULL DEFAULT 0,
			cache_read_tokens integer NOT NULL DEFAULT 0,
			cache_write_5m_tokens integer NOT NULL DEFAULT 0,
			cache_write_1h_tokens integer NOT NULL DEFAULT 0,
			cost real NOT NULL DEFAULT 0,
			usage_state varchar(32) NOT NULL DEFAULT 'not_applicable',
			cost_state varchar(32) NOT NULL DEFAULT 'not_applicable',
			attempts json,
			CONSTRAINT chk_request_log_usage_state
				CHECK (usage_state IN ('complete','partial','missing','not_applicable')),
			CONSTRAINT chk_request_log_cost_state
				CHECK (cost_state IN ('priced','unpriced','not_applicable'))
		)`,
		`CREATE TABLE IF NOT EXISTS usage_stats (
			id integer PRIMARY KEY AUTOINCREMENT,
			hour_bucket datetime NOT NULL,
			group_id integer NOT NULL,
			model varchar(255) NOT NULL,
			request_count integer NOT NULL DEFAULT 0,
			success_count integer NOT NULL DEFAULT 0,
			failure_count integer NOT NULL DEFAULT 0,
			input_tokens integer NOT NULL DEFAULT 0,
			output_tokens integer NOT NULL DEFAULT 0,
			cache_read_tokens integer NOT NULL DEFAULT 0,
			cache_write_5m_tokens integer NOT NULL DEFAULT 0,
			cache_write_1h_tokens integer NOT NULL DEFAULT 0,
			cost real NOT NULL DEFAULT 0,
			usage_missing_count integer NOT NULL DEFAULT 0,
			partial_count integer NOT NULL DEFAULT 0,
			unpriced_request_count integer NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS model_prices (
			id integer PRIMARY KEY AUTOINCREMENT,
			pattern varchar(255) NOT NULL,
			input_price real,
			output_price real,
			cache_read_price real,
			cache_write_5m_price real,
			cache_write_1h_price real,
			source varchar(32) NOT NULL,
			created_at datetime,
			updated_at datetime,
			CONSTRAINT chk_model_price_source CHECK (source = 'user')
		)`,
		`CREATE TABLE IF NOT EXISTS system_settings (
			key varchar(255) PRIMARY KEY NOT NULL,
			value text NOT NULL,
			updated_at datetime
		)`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id varchar(36) PRIMARY KEY NOT NULL,
			type varchar(64) NOT NULL,
			status varchar(32) NOT NULL DEFAULT 'pending',
			payload json,
			result json,
			error text,
			created_at datetime NOT NULL,
			started_at datetime,
			finished_at datetime
		)`,
		`CREATE TABLE IF NOT EXISTS control_operations (
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
			completed_at datetime,
			compacted_at datetime,
			created_at datetime,
			updated_at datetime,
			CONSTRAINT chk_control_operation_digest_version
				CHECK (digest_version > 0),
			CONSTRAINT chk_control_operation_digest
				CHECK (length(request_digest) = 32)
		)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("prepare schema v2 during version 1 upgrade: %w", err)
		}
	}
	return nil
}
