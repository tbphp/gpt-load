package storage_test

import (
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/storage"
)

type schemaV3Column struct {
	Name         string
	Type         string
	NotNull      int     `gorm:"column:notnull"`
	DefaultValue *string `gorm:"column:dflt_value"`
}

func TestAutoMigrateCreatesSchemaV3IntegerStorage(t *testing.T) {
	db := openSchemaV3TestDatabase(t)
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	if storage.CurrentSchemaVersion != 3 {
		t.Fatalf("CurrentSchemaVersion = %d, want 3", storage.CurrentSchemaVersion)
	}

	for table, columns := range map[string][]string{
		"groups":             {"created_at_ms", "updated_at_ms"},
		"upstream_keys":      {"created_at_ms", "updated_at_ms"},
		"access_keys":        {"created_at_ms", "updated_at_ms"},
		"model_prices":       {"created_at_ms", "updated_at_ms"},
		"request_logs":       {"completed_at_ms"},
		"system_settings":    {"updated_at_ms"},
		"jobs":               {"created_at_ms", "started_at_ms", "finished_at_ms"},
		"control_operations": {"completed_at_ms", "compacted_at_ms", "created_at_ms", "updated_at_ms"},
	} {
		got := schemaV3Columns(t, db, table)
		for _, name := range columns {
			column, ok := got[name]
			if !ok {
				t.Errorf("%s.%s is missing", table, name)
				continue
			}
			if !strings.EqualFold(column.Type, "INTEGER") {
				t.Errorf("%s.%s type = %q, want INTEGER", table, name, column.Type)
			}
			oldName := strings.TrimSuffix(name, "_ms")
			if _, exists := got[oldName]; exists {
				t.Errorf("%s retains legacy absolute-time column %q", table, oldName)
			}
		}
	}

	requestColumns := schemaV3Columns(t, db, "request_logs")
	assertSchemaV3IntegerColumn(t, requestColumns, "estimated_cost_nano_usd", false)
	if err := db.Exec(`INSERT INTO request_logs (
		id, completed_at_ms, access_key_id, group_id, protocol, client_model,
		upstream_model, status, status_code, duration_ms, error_code, error_summary,
		affinity_hit, uncached_input_tokens, output_tokens, cache_read_tokens,
		cache_write_5m_tokens, cache_write_1h_tokens, estimated_cost_nano_usd,
		usage_state, cost_state, attempts
	) VALUES (
		'negative-cost', 1, 0, 0, 'openai-completions', '', '', 'success', 200, 1,
		'', '', false, 0, 0, 0, 0, 0, -1, 'complete', 'priced', '[]'
	)`).Error; err == nil {
		t.Fatal("negative request log nano USD cost was accepted")
	}

	accessColumns := schemaV3Columns(t, db, "access_keys")
	assertSchemaV3IntegerColumn(t, accessColumns, "daily_cost_limit_nano_usd", false)
	assertSchemaV3IntegerColumn(t, accessColumns, "monthly_cost_limit_nano_usd", false)
	if err := db.Exec(`INSERT INTO access_keys (
		id, name, key_value, key_hash, key_suffix, status, rpm_limit,
		daily_cost_limit_nano_usd, monthly_cost_limit_nano_usd,
		created_at_ms, updated_at_ms
	) VALUES (
		991, 'negative-limit', 'cipher', 'negative-limit-hash', '7f2a', 'active',
		0, -1, 0, 1, 1
	)`).Error; err == nil {
		t.Fatal("negative access-key nano USD limit was accepted")
	}
	if err := db.Exec(`INSERT INTO access_keys (
		id, name, key_value, key_hash, key_suffix, status, rpm_limit,
		daily_cost_limit_nano_usd, monthly_cost_limit_nano_usd,
		created_at_ms, updated_at_ms
	) VALUES (
		992, 'negative-monthly-limit', 'cipher', 'negative-monthly-limit-hash',
		'7f2b', 'active', 0, 0, -1, 1, 1
	)`).Error; err == nil {
		t.Fatal("negative access-key monthly nano USD limit was accepted")
	}

	priceColumns := schemaV3Columns(t, db, "model_prices")
	for _, name := range []string{
		"input_price_nano_usd_per_million_tokens",
		"output_price_nano_usd_per_million_tokens",
		"cache_read_price_nano_usd_per_million_tokens",
		"cache_write_5m_price_nano_usd_per_million_tokens",
		"cache_write_1h_price_nano_usd_per_million_tokens",
	} {
		assertSchemaV3IntegerColumn(t, priceColumns, name, true)
	}

	upstreamColumns := schemaV3Columns(t, db, "upstream_keys")
	for _, removed := range []string{"request_count", "tokens_total", "cost_total"} {
		if _, ok := upstreamColumns[removed]; ok {
			t.Errorf("upstream_keys retains removed column %q", removed)
		}
	}

	usageColumns := schemaV3Columns(t, db, "usage_stats")
	for _, name := range []string{
		"bucket_start_ms",
		"access_key_id",
		"group_id",
		"model",
		"request_count",
		"success_count",
		"failure_count",
		"uncached_input_tokens",
		"cache_read_tokens",
		"cache_write_5m_tokens",
		"cache_write_1h_tokens",
		"output_tokens",
		"estimated_cost_nano_usd",
		"usage_missing_count",
		"partial_count",
		"unpriced_request_count",
	} {
		if _, ok := usageColumns[name]; !ok {
			t.Errorf("usage_stats.%s is missing", name)
		}
	}
	assertSchemaV3IndexColumns(t, db,
		"idx_usage_stats_bucket_access_group_model",
		[]string{"bucket_start_ms", "access_key_id", "group_id", "model"},
	)
	var foreignKeys []struct {
		Table string
	}
	if err := db.Raw("PRAGMA foreign_key_list('usage_stats')").Scan(&foreignKeys).Error; err != nil {
		t.Fatalf("inspect usage_stats foreign keys: %v", err)
	}
	if len(foreignKeys) != 0 {
		t.Fatalf("usage_stats foreign keys = %+v, want none", foreignKeys)
	}

	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("second AutoMigrate() error = %v", err)
	}
	var versions []uint
	if err := db.Table("schema_info").Pluck("version", &versions).Error; err != nil {
		t.Fatalf("read schema_info: %v", err)
	}
	if !reflect.DeepEqual(versions, []uint{3}) {
		t.Fatalf("schema_info versions = %v, want [3]", versions)
	}
}

func TestSchemaV3FreshAndMigratedSchemasEnforceEquivalentConstraints(t *testing.T) {
	factories := []struct {
		name string
		open func(*testing.T) *gorm.DB
	}{
		{
			name: "fresh",
			open: func(t *testing.T) *gorm.DB {
				db := openSchemaV3TestDatabase(t)
				if err := storage.AutoMigrate(db); err != nil {
					t.Fatalf("AutoMigrate(fresh) error = %v", err)
				}
				return db
			},
		},
		{
			name: "migrated",
			open: func(t *testing.T) *gorm.DB {
				db := openSchemaV3TestDatabase(t)
				createSchemaV2Fixture(t, db)
				if err := storage.AutoMigrate(db); err != nil {
					t.Fatalf("AutoMigrate(v2) error = %v", err)
				}
				return db
			},
		},
	}
	invalidStatements := []struct {
		name      string
		statement string
		args      []any
	}{
		{
			name: "negative absolute time",
			statement: `INSERT INTO groups (
				id, name, upstream_url, protocols, models, convert_enabled,
				enabled, created_at_ms, updated_at_ms
			) VALUES (901, 'negative-time', 'https://invalid.example', '[]', '[]',
				false, true, -1, 1)`,
		},
		{
			name:      "negative duration",
			statement: schemaV3InvalidRequestInsert,
			args:      []any{"negative-duration", -1, 0},
		},
		{
			name:      "negative token",
			statement: schemaV3InvalidRequestInsert,
			args:      []any{"negative-token", 0, -1},
		},
		{
			name: "negative aggregate counter",
			statement: `INSERT INTO usage_stats (
				bucket_start_ms, access_key_id, group_id, model,
				request_count, success_count, failure_count
			) VALUES (3600000, 0, 0, '', -1, 0, 0)`,
		},
		{
			name: "aggregate outcome mismatch",
			statement: `INSERT INTO usage_stats (
				bucket_start_ms, access_key_id, group_id, model,
				request_count, success_count, failure_count
			) VALUES (7200000, 0, 0, '', 1, 0, 0)`,
		},
		{
			name:      "invalid request usage cost combination",
			statement: schemaV3InvalidUsageCostInsert,
			args:      []any{"missing-priced"},
		},
	}

	for _, factory := range factories {
		t.Run(factory.name, func(t *testing.T) {
			db := factory.open(t)
			assertSchemaV3IndexColumns(t, db,
				"idx_request_logs_completed_id",
				[]string{"completed_at_ms", "id"},
			)
			assertSchemaV3IndexColumns(t, db,
				"idx_usage_stats_bucket_access_group_model",
				[]string{"bucket_start_ms", "access_key_id", "group_id", "model"},
			)
			for _, invalid := range invalidStatements {
				t.Run(invalid.name, func(t *testing.T) {
					if err := db.Exec(invalid.statement, invalid.args...).Error; err == nil {
						t.Fatalf("%s schema accepted %s", factory.name, invalid.name)
					}
				})
			}
		})
	}
}

func TestAutoMigrateSchemaV3RejectsMissingConstraintsAndCriticalIndexes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *gorm.DB)
	}{
		{
			name: "critical index",
			mutate: func(t *testing.T, db *gorm.DB) {
				mustExecSchemaV3(t, db, `DROP INDEX idx_request_logs_completed_id`)
			},
		},
		{
			name: "table constraints",
			mutate: func(t *testing.T, db *gorm.DB) {
				mustExecSchemaV3(t, db, `ALTER TABLE usage_stats RENAME TO usage_stats_checked`)
				mustExecSchemaV3(t, db,
					`CREATE TABLE usage_stats AS SELECT * FROM usage_stats_checked WHERE 0`)
				mustExecSchemaV3(t, db, `DROP TABLE usage_stats_checked`)
				mustExecSchemaV3(t, db, `CREATE UNIQUE INDEX
					idx_usage_stats_bucket_access_group_model
					ON usage_stats(bucket_start_ms, access_key_id, group_id, model)`)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openSchemaV3TestDatabase(t)
			if err := storage.AutoMigrate(db); err != nil {
				t.Fatalf("AutoMigrate(fresh) error = %v", err)
			}
			test.mutate(t, db)
			if err := storage.AutoMigrate(db); err == nil {
				t.Fatal("AutoMigrate(corrupt v3) error = nil, want strict validation failure")
			}
		})
	}
}

const schemaV3InvalidRequestInsert = `INSERT INTO request_logs (
	id, completed_at_ms, access_key_id, group_id, protocol, client_model,
	upstream_model, status, status_code, duration_ms, error_code, error_summary,
	affinity_hit, uncached_input_tokens, output_tokens, cache_read_tokens,
	cache_write_5m_tokens, cache_write_1h_tokens, estimated_cost_nano_usd,
	usage_state, cost_state, attempts
) VALUES (
	?, 1, 0, 0, 'openai-completions', '', '', 'success', 200, ?,
	'', '', false, ?, 0, 0, 0, 0, 0, 'complete', 'priced', '[]'
)`

const schemaV3InvalidUsageCostInsert = `INSERT INTO request_logs (
	id, completed_at_ms, access_key_id, group_id, protocol, client_model,
	upstream_model, status, status_code, duration_ms, error_code, error_summary,
	affinity_hit, uncached_input_tokens, output_tokens, cache_read_tokens,
	cache_write_5m_tokens, cache_write_1h_tokens, estimated_cost_nano_usd,
	usage_state, cost_state, attempts
) VALUES (
	?, 1, 0, 0, 'openai-completions', '', '', 'success', 200, 0,
	'', '', false, 0, 0, 0, 0, 0, 0, 'missing', 'priced', '[]'
)`

func TestAutoMigrateSchemaV3MigratesV2RowsAndBackfillsUsage(t *testing.T) {
	db := openSchemaV3TestDatabase(t)
	createSchemaV2Fixture(t, db)

	createdAt := time.Date(2026, 7, 30, 8, 15, 30, 123_000_000, time.UTC)
	updatedAt := time.Date(2026, 7, 30, 9, 16, 31, 456_000_000, time.UTC)
	completedAt := time.Date(2026, 7, 30, 10, 45, 12, 789_000_000, time.UTC)
	finishedAt := time.Date(2026, 7, 30, 11, 0, 0, 999_000_000, time.UTC)

	mustExecSchemaV3(t, db, `INSERT INTO groups (
		id, name, upstream_url, protocols, models, convert_enabled, weight_manual,
		validation_model, config, enabled, created_at, updated_at
	) VALUES (7, 'legacy-group', 'https://upstream.example/v1',
		'["anthropic"]', '[{"id":"upstream-model","alias":"client-model"}]', true, 9,
		'validation-model', '{"retry":2}', false, ?, ?)`, createdAt, updatedAt)
	mustExecSchemaV3(t, db, `INSERT INTO upstream_keys (
		id, group_id, key_value, key_hash, status, weight_manual,
		request_count, tokens_total, cost_total, created_at, updated_at
	) VALUES (11, 7, 'cipher-upstream', 'hash-upstream', 'disabled', 3,
		91, 92, 93.5, ?, ?)`, createdAt, updatedAt)
	mustExecSchemaV3(t, db, `INSERT INTO access_keys (
		id, name, key_value, key_hash, key_suffix, status, filters, rpm_limit,
		daily_cost_limit, monthly_cost_limit, created_at, updated_at
	) VALUES (13, 'legacy-access', 'cipher-access', 'hash-access', '7f2a', 'active',
		'{"groups":[7],"protocols":["anthropic"],"models":["client-model"]}',
		17, 18.0000000005, 19.5, ?, ?)`, createdAt, updatedAt)
	mustExecSchemaV3(t, db, `INSERT INTO model_prices (
		id, pattern, input_price, output_price, cache_read_price,
		cache_write_5m_price, cache_write_1h_price, source, created_at, updated_at
	) VALUES (17, 'model-*', 1.2345678905, 2.0000000004, NULL,
		0.0000000005, 4.5, 'user', ?, ?)`, createdAt, updatedAt)
	mustExecSchemaV3(t, db, `INSERT INTO system_settings (
		key, value, updated_at
	) VALUES ('request_log.retention_days', '17', ?)`, updatedAt)
	mustExecSchemaV3(t, db, `INSERT INTO jobs (
		id, type, status, payload, result, error, created_at, started_at, finished_at
	) VALUES ('job-1', 'discover', 'succeeded', '{"group_id":7}', '{"models":1}',
		'', ?, NULL, ?)`, createdAt, finishedAt)
	mustExecSchemaV3(t, db, `INSERT INTO control_operations (
		commit_sequence, operation_id, idempotency_key, digest_version, request_digest,
		operation_kind, resource_identity, canonical_result, required_stages,
		last_completed_stage, failed_stage, completed_at, compacted_at, created_at, updated_at
	) VALUES (23, '00000000-0000-0000-0000-000000000023',
		'00000000-0000-0000-0000-000000000024', 1, zeroblob(32), 'group.create',
		'group:7', '{"id":7}', '["db","snapshot"]', 'db', '', NULL, ?, ?, ?)`,
		finishedAt, createdAt, updatedAt)

	insertLegacyRequestLog(t, db, "request-complete", completedAt, 13, 7,
		"client-model", "upstream-model", "success", 10, 20, 30, 40, 50,
		0.0000000005, "complete", "priced")
	insertLegacyRequestLog(t, db, "request-partial",
		completedAt.Add(5*time.Minute), 13, 7,
		"client-model", "different-upstream", "error", 1, 2, 3, 4, 5,
		0, "partial", "unpriced")
	insertLegacyRequestLog(t, db, "request-missing",
		completedAt.Add(10*time.Minute), 0, 0,
		"", "", "incomplete", 100, 200, 300, 400, 500,
		0, "missing", "unpriced")
	insertLegacyRequestLog(t, db, "request-not-applicable",
		completedAt.Add(14*time.Minute), 0, 0,
		"", "", "canceled", 100, 200, 300, 400, 500,
		0, "not_applicable", "not_applicable")

	mustExecSchemaV3(t, db, `INSERT INTO usage_stats (
		hour_bucket, group_id, model, request_count, success_count, failure_count,
		input_tokens, output_tokens, cache_read_tokens, cache_write_5m_tokens,
		cache_write_1h_tokens, cost, usage_missing_count, partial_count,
		unpriced_request_count
	) VALUES (?, 999, 'must-be-discarded', 999, 999, 0, 999, 999, 999, 999, 999,
		999, 0, 0, 0)`, completedAt.Truncate(time.Hour))

	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate(v2) error = %v", err)
	}

	var version uint
	if err := db.Raw("SELECT version FROM schema_info").Scan(&version).Error; err != nil {
		t.Fatalf("read migrated schema version: %v", err)
	}
	if version != 3 {
		t.Fatalf("schema version = %d, want 3", version)
	}

	type migratedGroup struct {
		Name        string
		Protocols   string
		Models      string
		Config      string
		Enabled     bool
		CreatedAtMS int64 `gorm:"column:created_at_ms"`
		UpdatedAtMS int64 `gorm:"column:updated_at_ms"`
	}
	var group migratedGroup
	if err := db.Raw(`SELECT name, protocols, models, config, enabled,
		created_at_ms, updated_at_ms FROM groups WHERE id = 7`).Scan(&group).Error; err != nil {
		t.Fatalf("read migrated group: %v", err)
	}
	if group.Name != "legacy-group" || group.Protocols != `["anthropic"]` ||
		group.Models != `[{"id":"upstream-model","alias":"client-model"}]` ||
		group.Config != `{"retry":2}` || group.Enabled ||
		group.CreatedAtMS != createdAt.UnixMilli() || group.UpdatedAtMS != updatedAt.UnixMilli() {
		t.Errorf("migrated group = %+v", group)
	}

	type migratedCredential struct {
		KeyValue                string
		KeyHash                 string
		KeySuffix               string
		Filters                 string
		DailyCostLimitNanoUSD   int64 `gorm:"column:daily_cost_limit_nano_usd"`
		MonthlyCostLimitNanoUSD int64 `gorm:"column:monthly_cost_limit_nano_usd"`
		CreatedAtMS             int64 `gorm:"column:created_at_ms"`
		UpdatedAtMS             int64 `gorm:"column:updated_at_ms"`
	}
	var access migratedCredential
	if err := db.Raw(`SELECT key_value, key_hash, key_suffix, filters,
		daily_cost_limit_nano_usd, monthly_cost_limit_nano_usd,
		created_at_ms, updated_at_ms FROM access_keys WHERE id = 13`).Scan(&access).Error; err != nil {
		t.Fatalf("read migrated access key: %v", err)
	}
	if access.KeyValue != "cipher-access" || access.KeyHash != "hash-access" ||
		access.KeySuffix != "7f2a" ||
		access.Filters != `{"groups":[7],"protocols":["anthropic"],"models":["client-model"]}` ||
		access.DailyCostLimitNanoUSD != 18_000_000_001 ||
		access.MonthlyCostLimitNanoUSD != 19_500_000_000 ||
		access.CreatedAtMS != createdAt.UnixMilli() || access.UpdatedAtMS != updatedAt.UnixMilli() {
		t.Errorf("migrated access key = %+v", access)
	}
	var upstream migratedCredential
	if err := db.Raw(`SELECT key_value, key_hash, '' AS key_suffix, '' AS filters,
		created_at_ms, updated_at_ms FROM upstream_keys WHERE id = 11`).Scan(&upstream).Error; err != nil {
		t.Fatalf("read migrated upstream key: %v", err)
	}
	if upstream.KeyValue != "cipher-upstream" || upstream.KeyHash != "hash-upstream" ||
		upstream.CreatedAtMS != createdAt.UnixMilli() || upstream.UpdatedAtMS != updatedAt.UnixMilli() {
		t.Errorf("migrated upstream key = %+v", upstream)
	}

	type migratedPrice struct {
		InputPrice        *int64 `gorm:"column:input_price_nano_usd_per_million_tokens"`
		OutputPrice       *int64 `gorm:"column:output_price_nano_usd_per_million_tokens"`
		CacheReadPrice    *int64 `gorm:"column:cache_read_price_nano_usd_per_million_tokens"`
		CacheWrite5MPrice *int64 `gorm:"column:cache_write_5m_price_nano_usd_per_million_tokens"`
		CacheWrite1HPrice *int64 `gorm:"column:cache_write_1h_price_nano_usd_per_million_tokens"`
	}
	var price migratedPrice
	if err := db.Raw(`SELECT
		input_price_nano_usd_per_million_tokens,
		output_price_nano_usd_per_million_tokens,
		cache_read_price_nano_usd_per_million_tokens,
		cache_write_5m_price_nano_usd_per_million_tokens,
		cache_write_1h_price_nano_usd_per_million_tokens
		FROM model_prices WHERE id = 17`).Scan(&price).Error; err != nil {
		t.Fatalf("read migrated price: %v", err)
	}
	if price.InputPrice == nil || *price.InputPrice != 1_234_567_891 ||
		price.OutputPrice == nil || *price.OutputPrice != 2_000_000_000 ||
		price.CacheReadPrice != nil ||
		price.CacheWrite5MPrice == nil || *price.CacheWrite5MPrice != 1 ||
		price.CacheWrite1HPrice == nil || *price.CacheWrite1HPrice != 4_500_000_000 {
		t.Errorf("migrated model price = %+v", price)
	}

	type migratedRequest struct {
		CompletedAtMS        int64 `gorm:"column:completed_at_ms"`
		EstimatedCostNanoUSD int64 `gorm:"column:estimated_cost_nano_usd"`
		UncachedInputTokens  int64 `gorm:"column:uncached_input_tokens"`
	}
	var request migratedRequest
	if err := db.Raw(`SELECT completed_at_ms, estimated_cost_nano_usd,
		uncached_input_tokens FROM request_logs WHERE id = 'request-complete'`).
		Scan(&request).Error; err != nil {
		t.Fatalf("read migrated request log: %v", err)
	}
	if request.CompletedAtMS != completedAt.UnixMilli() ||
		request.EstimatedCostNanoUSD != 1 || request.UncachedInputTokens != 10 {
		t.Errorf("migrated request log = %+v", request)
	}

	type migratedUsage struct {
		BucketStartMS        int64 `gorm:"column:bucket_start_ms"`
		AccessKeyID          uint  `gorm:"column:access_key_id"`
		GroupID              uint  `gorm:"column:group_id"`
		Model                string
		RequestCount         int64
		SuccessCount         int64
		FailureCount         int64
		UncachedInputTokens  int64 `gorm:"column:uncached_input_tokens"`
		OutputTokens         int64
		CacheReadTokens      int64
		CacheWrite5MTokens   int64 `gorm:"column:cache_write_5m_tokens"`
		CacheWrite1HTokens   int64 `gorm:"column:cache_write_1h_tokens"`
		EstimatedCostNanoUSD int64 `gorm:"column:estimated_cost_nano_usd"`
		UsageMissingCount    int64
		PartialCount         int64
		UnpricedRequestCount int64
	}
	var stats []migratedUsage
	if err := db.Raw(`SELECT * FROM usage_stats
		ORDER BY access_key_id, group_id, model`).Scan(&stats).Error; err != nil {
		t.Fatalf("read migrated usage stats: %v", err)
	}
	wantStats := []migratedUsage{
		{
			BucketStartMS: completedAt.Truncate(time.Hour).UnixMilli(),
			RequestCount:  2, FailureCount: 2, UsageMissingCount: 1,
		},
		{
			BucketStartMS: completedAt.Truncate(time.Hour).UnixMilli(),
			AccessKeyID:   13, GroupID: 7, Model: "client-model",
			RequestCount: 2, SuccessCount: 1, FailureCount: 1,
			UncachedInputTokens: 11, OutputTokens: 22, CacheReadTokens: 33,
			CacheWrite5MTokens: 44, CacheWrite1HTokens: 55,
			EstimatedCostNanoUSD: 1, PartialCount: 1, UnpricedRequestCount: 1,
		},
	}
	if !reflect.DeepEqual(stats, wantStats) {
		t.Fatalf("migrated usage stats = %+v, want %+v", stats, wantStats)
	}

	var job struct {
		Payload      string
		Result       string
		StartedAtMS  *int64 `gorm:"column:started_at_ms"`
		FinishedAtMS *int64 `gorm:"column:finished_at_ms"`
	}
	if err := db.Raw(`SELECT payload, result, started_at_ms, finished_at_ms
		FROM jobs WHERE id = 'job-1'`).Scan(&job).Error; err != nil {
		t.Fatalf("read migrated job: %v", err)
	}
	if job.Payload != `{"group_id":7}` || job.Result != `{"models":1}` ||
		job.StartedAtMS != nil || job.FinishedAtMS == nil ||
		*job.FinishedAtMS != finishedAt.UnixMilli() {
		t.Errorf("migrated job = %+v", job)
	}
	var operation struct {
		CanonicalResult string
		RequiredStages  string
		CompletedAtMS   *int64 `gorm:"column:completed_at_ms"`
		CompactedAtMS   *int64 `gorm:"column:compacted_at_ms"`
	}
	if err := db.Raw(`SELECT canonical_result, required_stages, completed_at_ms,
		compacted_at_ms FROM control_operations WHERE commit_sequence = 23`).
		Scan(&operation).Error; err != nil {
		t.Fatalf("read migrated control operation: %v", err)
	}
	if operation.CanonicalResult != `{"id":7}` ||
		operation.RequiredStages != `["db","snapshot"]` ||
		operation.CompletedAtMS != nil || operation.CompactedAtMS == nil ||
		*operation.CompactedAtMS != finishedAt.UnixMilli() {
		t.Errorf("migrated control operation = %+v", operation)
	}
}

func TestAutoMigrateSchemaV3RejectsInvalidV2RowsAndRollsBack(t *testing.T) {
	tests := []struct {
		name      string
		table     string
		identity  string
		insertRow func(*testing.T, *gorm.DB)
	}{
		{
			name: "unparseable absolute time", table: "groups", identity: "31",
			insertRow: func(t *testing.T, db *gorm.DB) {
				mustExecSchemaV3(t, db, `INSERT INTO groups (
					id, name, upstream_url, protocols, models, convert_enabled,
					config, enabled, created_at, updated_at
				) VALUES (31, 'bad-time', 'https://invalid.example', '[]', '[]',
					false, '{}', true, 'not-an-instant', '2026-07-30T00:00:00Z')`)
			},
		},
		{
			name: "NaN price text", table: "model_prices", identity: "32",
			insertRow: func(t *testing.T, db *gorm.DB) {
				mustExecSchemaV3(t, db, `INSERT INTO model_prices (
					id, pattern, input_price, source, created_at, updated_at
				) VALUES (32, 'nan-price', 'NaN', 'user',
					'2026-07-30T00:00:00Z', '2026-07-30T00:00:00Z')`)
			},
		},
		{
			name: "infinite price", table: "model_prices", identity: "33",
			insertRow: func(t *testing.T, db *gorm.DB) {
				mustExecSchemaV3(t, db, `INSERT INTO model_prices (
					id, pattern, input_price, source, created_at, updated_at
				) VALUES (33, 'infinite-price', 1e999, 'user',
					'2026-07-30T00:00:00Z', '2026-07-30T00:00:00Z')`)
			},
		},
		{
			name: "negative price", table: "model_prices", identity: "34",
			insertRow: func(t *testing.T, db *gorm.DB) {
				mustExecSchemaV3(t, db, `INSERT INTO model_prices (
					id, pattern, input_price, source, created_at, updated_at
				) VALUES (34, 'negative-price', -0.1, 'user',
					'2026-07-30T00:00:00Z', '2026-07-30T00:00:00Z')`)
			},
		},
		{
			name: "price overflows int64 nano USD", table: "model_prices", identity: "35",
			insertRow: func(t *testing.T, db *gorm.DB) {
				mustExecSchemaV3(t, db, `INSERT INTO model_prices (
					id, pattern, input_price, source, created_at, updated_at
				) VALUES (35, 'overflow-price', 10000000000, 'user',
					'2026-07-30T00:00:00Z', '2026-07-30T00:00:00Z')`)
			},
		},
		{
			name: "negative access key daily limit", table: "access_keys", identity: "36",
			insertRow: func(t *testing.T, db *gorm.DB) {
				insertLegacyAccessKeyLimits(t, db, 36, -0.1, 0)
			},
		},
		{
			name: "NaN access key daily limit text", table: "access_keys", identity: "37",
			insertRow: func(t *testing.T, db *gorm.DB) {
				mustExecSchemaV3(t, db, `INSERT INTO access_keys (
					id, name, key_value, key_hash, key_suffix, status, rpm_limit,
					daily_cost_limit, monthly_cost_limit, created_at, updated_at
				) VALUES (
					37, 'nan-limit', 'cipher', 'nan-limit-hash', '7f2a', 'active',
					0, 'NaN', 0, '2026-07-30T00:00:00Z', '2026-07-30T00:00:00Z'
				)`)
			},
		},
		{
			name: "infinite access key monthly limit", table: "access_keys", identity: "38",
			insertRow: func(t *testing.T, db *gorm.DB) {
				insertLegacyAccessKeyLimits(t, db, 38, 0, math.Inf(1))
			},
		},
		{
			name:  "access key daily limit overflows int64 nano USD",
			table: "access_keys", identity: "39",
			insertRow: func(t *testing.T, db *gorm.DB) {
				insertLegacyAccessKeyLimits(t, db, 39, 10_000_000_000, 0)
			},
		},
		{
			name: "negative request cost", table: "request_logs", identity: "negative-cost",
			insertRow: func(t *testing.T, db *gorm.DB) {
				insertLegacyRequestLog(t, db, "negative-cost",
					time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
					0, 0, "", "", "success", 0, 0, 0, 0, 0,
					-0.1, "complete", "priced")
			},
		},
		{
			name: "request cost overflows int64 nano USD", table: "request_logs", identity: "overflow-cost",
			insertRow: func(t *testing.T, db *gorm.DB) {
				insertLegacyRequestLog(t, db, "overflow-cost",
					time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
					0, 0, "", "", "success", 0, 0, 0, 0, 0,
					10_000_000_000, "complete", "priced")
			},
		},
		{
			name:  "missing usage cannot be priced",
			table: "request_logs", identity: "missing-priced",
			insertRow: func(t *testing.T, db *gorm.DB) {
				insertLegacyRequestLog(t, db, "missing-priced",
					time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
					0, 0, "", "", "success", 0, 0, 0, 0, 0,
					0, "missing", "priced")
			},
		},
		{
			name:  "unpriced request cost must be zero",
			table: "request_logs", identity: "unpriced-nonzero",
			insertRow: func(t *testing.T, db *gorm.DB) {
				insertLegacyRequestLog(t, db, "unpriced-nonzero",
					time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
					0, 0, "", "", "success", 0, 0, 0, 0, 0,
					0.000000001, "complete", "unpriced")
			},
		},
		{
			name:  "not applicable request cost must be zero",
			table: "request_logs", identity: "not-applicable-nonzero",
			insertRow: func(t *testing.T, db *gorm.DB) {
				insertLegacyRequestLog(t, db, "not-applicable-nonzero",
					time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
					0, 0, "", "", "success", 0, 0, 0, 0, 0,
					0.000000001, "not_applicable", "not_applicable")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openSchemaV3TestDatabase(t)
			createSchemaV2Fixture(t, db)
			test.insertRow(t, db)

			err := storage.AutoMigrate(db)
			if err == nil {
				t.Fatal("AutoMigrate(v2 invalid row) error = nil, want rollback")
			}
			if !strings.Contains(err.Error(), test.table) ||
				!strings.Contains(err.Error(), test.identity) {
				t.Fatalf("migration error = %q, want table %q and identity %q",
					err, test.table, test.identity)
			}
			for _, secret := range []string{"cipher-access", "cipher-upstream"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("migration error leaked credential: %v", err)
				}
			}
			var version uint
			if scanErr := db.Raw("SELECT version FROM schema_info").Scan(&version).Error; scanErr != nil {
				t.Fatalf("read schema version after rollback: %v", scanErr)
			}
			if version != 2 {
				t.Fatalf("schema version after rollback = %d, want 2", version)
			}
			if !db.Migrator().HasColumn("groups", "created_at") ||
				db.Migrator().HasColumn("groups", "created_at_ms") {
				t.Fatal("failed migration did not restore the schema v2 tables")
			}
		})
	}
}

func TestAutoMigrateSchemaV3PreservesNullableAbsoluteTimesAsNull(t *testing.T) {
	db := openSchemaV3TestDatabase(t)
	createSchemaV2Fixture(t, db)
	mustExecSchemaV3(t, db, `INSERT INTO jobs (
		id, type, status, payload, result, error, created_at, started_at, finished_at
	) VALUES ('nullable-job', 'test', 'pending', '{}', '{}', '',
		'2026-07-30T00:00:00Z', NULL, NULL)`)
	mustExecSchemaV3(t, db, `INSERT INTO control_operations (
		commit_sequence, operation_id, idempotency_key, digest_version, request_digest,
		operation_kind, resource_identity, canonical_result, required_stages,
		last_completed_stage, failed_stage, completed_at, compacted_at, created_at, updated_at
	) VALUES (41, '00000000-0000-0000-0000-000000000041',
		'00000000-0000-0000-0000-000000000042', 1, zeroblob(32),
		'test', 'test:41', '{}', '[]', '', '', NULL, NULL,
		'2026-07-30T00:00:00Z', '2026-07-30T00:00:00Z')`)

	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate(v2) error = %v", err)
	}
	var nullCounts struct {
		JobStarted         int
		JobFinished        int
		OperationCompleted int
		OperationCompacted int
	}
	if err := db.Raw(`SELECT
		(SELECT started_at_ms IS NULL FROM jobs WHERE id = 'nullable-job') AS job_started,
		(SELECT finished_at_ms IS NULL FROM jobs WHERE id = 'nullable-job') AS job_finished,
		(SELECT completed_at_ms IS NULL FROM control_operations WHERE commit_sequence = 41)
			AS operation_completed,
		(SELECT compacted_at_ms IS NULL FROM control_operations WHERE commit_sequence = 41)
			AS operation_compacted`).Scan(&nullCounts).Error; err != nil {
		t.Fatalf("read nullable migrated times: %v", err)
	}
	if nullCounts != (struct {
		JobStarted         int
		JobFinished        int
		OperationCompleted int
		OperationCompacted int
	}{1, 1, 1, 1}) {
		t.Fatalf("nullable migrated time checks = %+v, want all 1", nullCounts)
	}
}

func openSchemaV3TestDatabase(t *testing.T) *gorm.DB {
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

func createSchemaV2Fixture(t *testing.T, db *gorm.DB) {
	t.Helper()
	statements := []string{
		`CREATE TABLE schema_info (version integer PRIMARY KEY)`,
		`INSERT INTO schema_info(version) VALUES (2)`,
		`CREATE TABLE groups (
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
		`CREATE UNIQUE INDEX idx_groups_name ON groups(name)`,
		`CREATE TABLE upstream_keys (
			id integer PRIMARY KEY AUTOINCREMENT,
			group_id integer NOT NULL,
			key_value text NOT NULL,
			key_hash varchar(128) NOT NULL,
			status varchar(32) NOT NULL DEFAULT 'active'
				CHECK (status IN ('active','disabled')),
			weight_manual integer,
			request_count integer NOT NULL DEFAULT 0,
			tokens_total integer NOT NULL DEFAULT 0,
			cost_total real NOT NULL DEFAULT 0,
			created_at datetime,
			updated_at datetime,
			FOREIGN KEY (group_id) REFERENCES groups(id) ON UPDATE CASCADE ON DELETE CASCADE
		)`,
		`CREATE UNIQUE INDEX idx_upstream_keys_group_hash
			ON upstream_keys(group_id, key_hash)`,
		`CREATE TABLE access_keys (
			id integer PRIMARY KEY AUTOINCREMENT,
			name varchar(255) NOT NULL,
			key_value text NOT NULL,
			key_hash varchar(128) NOT NULL,
			key_suffix char(4) NOT NULL
				CHECK (key_suffix GLOB '[0-9a-f][0-9a-f][0-9a-f][0-9a-f]'),
			status varchar(32) NOT NULL DEFAULT 'active'
				CHECK (status IN ('active','disabled')),
			filters json,
			rpm_limit integer NOT NULL DEFAULT 0,
			daily_cost_limit real NOT NULL DEFAULT 0,
			monthly_cost_limit real NOT NULL DEFAULT 0,
			created_at datetime,
			updated_at datetime
		)`,
		`CREATE UNIQUE INDEX idx_access_keys_key_hash ON access_keys(key_hash)`,
		`CREATE TABLE request_logs (
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
			usage_state varchar(32) NOT NULL DEFAULT 'not_applicable'
				CHECK (usage_state IN ('complete','partial','missing','not_applicable')),
			cost_state varchar(32) NOT NULL DEFAULT 'not_applicable'
				CHECK (cost_state IN ('priced','unpriced','not_applicable')),
			attempts json
		)`,
		`CREATE TABLE usage_stats (
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
		`CREATE UNIQUE INDEX idx_usage_stats_hour_group_model
			ON usage_stats(hour_bucket, group_id, model)`,
		`CREATE TABLE model_prices (
			id integer PRIMARY KEY AUTOINCREMENT,
			pattern varchar(255) NOT NULL,
			input_price real,
			output_price real,
			cache_read_price real,
			cache_write_5m_price real,
			cache_write_1h_price real,
			source varchar(32) NOT NULL CHECK (source = 'user'),
			created_at datetime,
			updated_at datetime
		)`,
		`CREATE UNIQUE INDEX idx_model_prices_pattern ON model_prices(pattern)`,
		`CREATE TABLE system_settings (
			key varchar(255) PRIMARY KEY NOT NULL,
			value text NOT NULL,
			updated_at datetime
		)`,
		`CREATE TABLE jobs (
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
		`CREATE TABLE control_operations (
			commit_sequence integer PRIMARY KEY AUTOINCREMENT,
			operation_id char(36) NOT NULL,
			idempotency_key char(36) NOT NULL,
			digest_version integer NOT NULL CHECK (digest_version > 0),
			request_digest blob NOT NULL CHECK (length(request_digest) = 32),
			operation_kind varchar(32) NOT NULL,
			resource_identity varchar(64) NOT NULL,
			canonical_result blob,
			required_stages json,
			last_completed_stage varchar(32),
			failed_stage varchar(32),
			completed_at datetime,
			compacted_at datetime,
			created_at datetime,
			updated_at datetime
		)`,
		`CREATE UNIQUE INDEX idx_control_operations_operation_id
			ON control_operations(operation_id)`,
		`CREATE UNIQUE INDEX idx_control_operations_idempotency_key
			ON control_operations(idempotency_key)`,
	}
	for _, statement := range statements {
		mustExecSchemaV3(t, db, statement)
	}
}

func insertLegacyRequestLog(
	t *testing.T,
	db *gorm.DB,
	id string,
	completedAt time.Time,
	accessKeyID uint,
	groupID uint,
	clientModel string,
	upstreamModel string,
	status string,
	inputTokens int64,
	outputTokens int64,
	cacheReadTokens int64,
	cacheWrite5MTokens int64,
	cacheWrite1HTokens int64,
	cost float64,
	usageState string,
	costState string,
) {
	t.Helper()
	mustExecSchemaV3(t, db, `INSERT INTO request_logs (
		id, created_at, access_key_id, group_id, protocol, client_model,
		upstream_model, status, status_code, duration_ms, error_code, error_summary,
		affinity_hit, input_tokens, output_tokens, cache_read_tokens,
		cache_write_5m_tokens, cache_write_1h_tokens, cost, usage_state, cost_state, attempts
	) VALUES (?, ?, ?, ?, 'openai-completions', ?, ?, ?, 200, 123,
		'', '', false, ?, ?, ?, ?, ?, ?, ?, ?, '[]')`,
		id, completedAt, accessKeyID, groupID, clientModel, upstreamModel, status,
		inputTokens, outputTokens, cacheReadTokens, cacheWrite5MTokens,
		cacheWrite1HTokens, cost, usageState, costState,
	)
}

func insertLegacyAccessKeyLimits(
	t *testing.T,
	db *gorm.DB,
	id uint,
	dailyCostLimit float64,
	monthlyCostLimit float64,
) {
	t.Helper()
	mustExecSchemaV3(t, db, `INSERT INTO access_keys (
		id, name, key_value, key_hash, key_suffix, status, rpm_limit,
		daily_cost_limit, monthly_cost_limit, created_at, updated_at
	) VALUES (?, ?, 'cipher', ?, '7f2a', 'active', 0, ?, ?,
		'2026-07-30T00:00:00Z', '2026-07-30T00:00:00Z')`,
		id,
		"invalid-limit",
		"invalid-limit-hash-"+strconv.FormatUint(uint64(id), 10),
		dailyCostLimit,
		monthlyCostLimit,
	)
}

func schemaV3Columns(t *testing.T, db *gorm.DB, table string) map[string]schemaV3Column {
	t.Helper()
	var columns []schemaV3Column
	if err := db.Raw("PRAGMA table_info('" + table + "')").Scan(&columns).Error; err != nil {
		t.Fatalf("inspect %s columns: %v", table, err)
	}
	result := make(map[string]schemaV3Column, len(columns))
	for _, column := range columns {
		result[column.Name] = column
	}
	return result
}

func assertSchemaV3IntegerColumn(
	t *testing.T,
	columns map[string]schemaV3Column,
	name string,
	nullable bool,
) {
	t.Helper()
	column, ok := columns[name]
	if !ok {
		t.Errorf("column %q is missing", name)
		return
	}
	if !strings.EqualFold(column.Type, "INTEGER") {
		t.Errorf("%s type = %q, want INTEGER", name, column.Type)
	}
	wantNotNull := 1
	if nullable {
		wantNotNull = 0
	}
	if column.NotNull != wantNotNull {
		t.Errorf("%s notnull = %d, want %d", name, column.NotNull, wantNotNull)
	}
}

func assertSchemaV3IndexColumns(
	t *testing.T,
	db *gorm.DB,
	indexName string,
	want []string,
) {
	t.Helper()
	type indexColumn struct {
		Sequence int    `gorm:"column:seqno"`
		Name     string `gorm:"column:name"`
		Key      int
	}
	var columns []indexColumn
	if err := db.Raw("PRAGMA index_xinfo('" + indexName + "')").Scan(&columns).Error; err != nil {
		t.Fatalf("inspect %s: %v", indexName, err)
	}
	got := make([]string, 0, len(want))
	for _, column := range columns {
		if column.Key == 1 {
			got = append(got, column.Name)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s columns = %v, want %v", indexName, got, want)
	}
}

func mustExecSchemaV3(t *testing.T, db *gorm.DB, query string, args ...any) {
	t.Helper()
	if err := db.Exec(query, args...).Error; err != nil {
		t.Fatalf("exec schema v3 fixture: %v\nquery: %s", err, query)
	}
}

func TestSchemaV3FixtureRejectsNonFiniteCostsAtSQLiteBoundary(t *testing.T) {
	// SQLite stores NaN as NULL, so the v2 NOT NULL cost column rejects it
	// before migration. Infinity remains representable and is covered by the
	// migration rollback matrix above.
	db := openSchemaV3TestDatabase(t)
	createSchemaV2Fixture(t, db)
	err := db.Exec(`INSERT INTO request_logs (
		id, created_at, access_key_id, group_id, protocol, client_model,
		upstream_model, status, status_code, duration_ms, error_code, error_summary,
		affinity_hit, input_tokens, output_tokens, cache_read_tokens,
		cache_write_5m_tokens, cache_write_1h_tokens, cost, usage_state, cost_state, attempts
	) VALUES ('nan-cost', '2026-07-30T00:00:00Z', 0, 0, 'openai-completions',
		'', '', 'success', 200, 0, '', '', false, 0, 0, 0, 0, 0, ?,
		'complete', 'priced', '[]')`, math.NaN()).Error
	if err == nil {
		t.Fatal("SQLite accepted NaN into the v2 NOT NULL request cost")
	}
}
