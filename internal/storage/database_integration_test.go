package storage_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/dbtx"
	"gpt-load/internal/storage/models"
)

// TestExternalDatabaseConcurrentMigrations is opt-in so ordinary unit tests
// remain hermetic. It starts two migration attempts against the same external
// database to exercise the driver-level serialization contract.
func TestExternalDatabaseConcurrentMigrations(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_TEST_DSN"))
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}

	databases := make([]*gorm.DB, 2)
	for index := range databases {
		db, err := storage.OpenWithSource(dsn, config.DatabaseSourceExternal)
		if err != nil {
			t.Fatalf("OpenWithSource(%d) error = %v", index, err)
		}
		databases[index] = db
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatalf("DB(%d) error = %v", index, err)
		}
		t.Cleanup(func() { _ = sqlDB.Close() })
	}

	errorsCh := make(chan error, len(databases))
	var group sync.WaitGroup
	group.Add(len(databases))
	for _, db := range databases {
		go func(database *gorm.DB) {
			defer group.Done()
			errorsCh <- storage.AutoMigrate(database)
		}(db)
	}
	group.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent AutoMigrate() error = %v", err)
		}
	}
}

// TestExternalDatabaseLifecycle is opt-in so ordinary unit tests remain
// hermetic. Set GPT_LOAD_DATABASE_TEST_DSN to one MySQL or PostgreSQL URL to
// exercise connection, migration, reserved-column queries, foreign keys, and
// the dialect-specific GORM upsert against a real server.
func TestExternalDatabaseLifecycle(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_TEST_DSN"))
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}

	db, err := storage.OpenWithSource(dsn, config.DatabaseSourceExternal)
	if err != nil {
		t.Fatalf("OpenWithSource() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("second AutoMigrate() error = %v", err)
	}
	for _, table := range []string{
		"groups", "credentials", "access_keys", "request_logs", "request_log_attempts",
		"usage_aggregation_journal", "usage_stats", "model_prices", "system_settings",
		"jobs", "control_operations", "credential_stages", "credential_observations",
		"credential_reset_operations", "credential_attempt_stats", "schema_migrations",
		"access_key_cost_limit_rules", "access_key_cost_limit_states",
	} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("table %q is missing", table)
		}
	}
	for table, columns := range map[string][]string{
		"groups":                  {"connection_type"},
		"credentials":             {"identity_fingerprint", "secret_version", "auth_state", "auth_error_code"},
		"request_log_attempts":    {"upstream_protocol"},
		"model_prices":            {"mode_price_schedules"},
		"credential_observations": {"last_auth_refresh_secret_version"},
	} {
		for _, column := range columns {
			if !db.Migrator().HasColumn(table, column) {
				t.Fatalf("column %q.%q is missing", table, column)
			}
		}
	}
	if db.Migrator().HasColumn("request_log_attempts", "upstream_api") {
		t.Fatal("request_log_attempts.upstream_api remains in the Beta.1 initial schema")
	}
	var migrationIDs []string
	if err := db.Table("schema_migrations").Order("id").Pluck("id", &migrationIDs).Error; err != nil {
		t.Fatalf("read migration ledger: %v", err)
	}
	if len(migrationIDs) != 2 || migrationIDs[0] != "0001_initial" ||
		migrationIDs[1] != "0002_access_key_cost_limits" {
		t.Fatalf("migration ledger = %v, want [0001_initial 0002_access_key_cost_limits]", migrationIDs)
	}

	accessKey := models.AccessKey{
		Name:     fmt.Sprintf("integration-cost-limit-%d", time.Now().UnixNano()),
		KeyValue: "encrypted-access-key", KeyHash: fmt.Sprintf("integration-access-%d", time.Now().UnixNano()),
		KeySuffix: "cafe", Status: "active", Filters: models.JSON(`{}`),
	}
	if err := db.Create(&accessKey).Error; err != nil {
		t.Fatalf("create cost-limited access key: %v", err)
	}
	costRule := models.AccessKeyCostLimitRule{
		AccessKeyID: accessKey.ID, Kind: models.AccessKeyCostLimitKindPeriodic,
		LimitNanoUSD: 20_000_000_000, PeriodSeconds: 18_000, RuleRevision: 1,
	}
	if err := db.Create(&costRule).Error; err != nil {
		t.Fatalf("create access key cost limit rule: %v", err)
	}
	if err := db.Create(&models.AccessKeyCostLimitState{
		RuleID: costRule.ID, RuleRevision: 1, SnapshotVersion: 1,
	}).Error; err != nil {
		t.Fatalf("create access key cost limit state: %v", err)
	}
	duplicate := costRule
	duplicate.ID = 0
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("database accepted duplicate periodic access key cost limit")
	}
	if err := db.Delete(&accessKey).Error; err != nil {
		t.Fatalf("delete cost-limited access key: %v", err)
	}
	for _, table := range []string{"access_key_cost_limit_rules", "access_key_cost_limit_states"} {
		var count int64
		query := db.Table(table)
		if table == "access_key_cost_limit_rules" {
			query = query.Where("access_key_id = ?", accessKey.ID)
		} else {
			query = query.Where("rule_id = ?", costRule.ID)
		}
		if err := query.Count(&count).Error; err != nil {
			t.Fatalf("count %s after cascade: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s count after cascade = %d", table, count)
		}
	}

	suffix := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	group := models.Group{
		Name:      fmt.Sprintf("integration-%s-%d", suffix, time.Now().UnixNano()),
		ChannelID: "openai_compatible", Params: models.JSON(`{"base_url":"https://integration.example.com"}`),
		Models: models.JSON(`[]`), Overrides: models.JSON(`{}`), Enabled: true,
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	credential := models.Credential{
		GroupID: group.ID, Data: "encrypted-data",
		Fingerprint: fmt.Sprintf("integration-credential-%d", time.Now().UnixNano()),
	}
	if err := db.Create(&credential).Error; err != nil {
		t.Fatalf("create credential: %v", err)
	}
	stage := models.CredentialStage{
		ID: fmt.Sprintf("stage-%d", time.Now().UnixNano()), ChannelID: "codex",
		ConnectionType: models.ConnectionTypeSubscription, AuthorizationMethod: "device_oauth",
		Status: models.CredentialStageReady, EncryptedPayload: "encrypted-stage-data",
		PayloadSchemaVersion: 1, SafeSummaryJSON: models.JSON(`{"email_mask":"a***z@example.com"}`),
		IdentityFingerprint: "integration-stage-identity", ExpiresAtMS: time.Now().Add(time.Minute).UnixMilli(),
		CreatedAtMS: time.Now().UnixMilli(), UpdatedAtMS: time.Now().UnixMilli(),
	}
	if err := db.Create(&stage).Error; err != nil {
		t.Fatalf("create credential stage: %v", err)
	}
	observation := models.CredentialObservation{
		CredentialID: credential.ID, IdentityFingerprint: credential.IdentityFingerprint,
		SchemaVersion: 1, ObservationVersion: 1, SnapshotJSON: models.JSON(`{"quota_windows":[]}`),
		State: models.CredentialObservationFresh, UpdatedAtMS: time.Now().UnixMilli(),
	}
	if err := db.Create(&observation).Error; err != nil {
		t.Fatalf("create credential observation: %v", err)
	}
	invalidGroup := group
	invalidGroup.ID = 0
	invalidGroup.Name = fmt.Sprintf("invalid-connection-%d", time.Now().UnixNano())
	invalidGroup.ConnectionType = models.ConnectionType("invalid")
	if err := db.Create(&invalidGroup).Error; err == nil {
		t.Fatal("database accepted an invalid Group connection_type")
	}
	invalidStage := stage
	invalidStage.ID = fmt.Sprintf("invalid-stage-%d", time.Now().UnixNano())
	invalidStage.Status = models.CredentialStageStatus("invalid")
	if err := db.Create(&invalidStage).Error; err == nil {
		t.Fatal("database accepted an invalid CredentialStage status")
	}
	duplicateIdentity := models.Credential{
		GroupID: group.ID, Data: "other-encrypted-data",
		Fingerprint:         fmt.Sprintf("other-secret-%d", time.Now().UnixNano()),
		IdentityFingerprint: credential.IdentityFingerprint,
	}
	if err := db.Create(&duplicateIdentity).Error; err == nil {
		t.Fatal("database accepted a duplicate Group credential identity")
	}

	setting := models.SystemSetting{
		// Keep this lifecycle-only row outside the public runtime-settings
		// namespace. The same CI database also executes control workflow tests.
		Key:         models.InternalSystemSettingPrefix + "integration.key",
		Value:       `{}`,
		UpdatedAtMS: time.Now().UnixMilli(),
	}
	if err := dbtx.Run(context.Background(), db, dbtx.Options{
		Mode:      dbtx.Write,
		Operation: "external integration write transaction",
	}, func(tx *gorm.DB) error {
		return tx.Create(&setting).Error
	}); err != nil {
		t.Fatalf("create system setting: %v", err)
	}
	var settingCount int64
	if err := dbtx.Run(context.Background(), db, dbtx.Options{
		Mode:      dbtx.ReadSnapshot,
		Operation: "external integration read transaction",
	}, func(tx *gorm.DB) error {
		return tx.Model(&models.SystemSetting{}).
			Where(&models.SystemSetting{Key: setting.Key}).Count(&settingCount).Error
	}); err != nil {
		t.Fatalf("read system setting in transaction: %v", err)
	}
	if settingCount != 1 {
		t.Fatalf("system setting count in transaction = %d, want 1", settingCount)
	}
	var loadedSetting models.SystemSetting
	if err := db.Where(&models.SystemSetting{Key: setting.Key}).First(&loadedSetting).Error; err != nil {
		t.Fatalf("query system setting: %v", err)
	}

	stat := models.UsageStat{
		BucketStartMS: time.Now().UnixMilli(),
		AccessKeyID:   1,
		ChannelID:     group.ChannelID,
		GroupID:       group.ID,
		CredentialID:  credential.ID,
		Model:         "integration-model",
		RequestCount:  1,
		SuccessCount:  1,
	}
	upsert := clause.OnConflict{
		Columns: []clause.Column{
			{Name: "bucket_start_ms"}, {Name: "access_key_id"},
			{Name: "channel_id"}, {Name: "group_id"},
			{Name: "credential_id"}, {Name: "model"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"request_count", "success_count"}),
	}
	if err := db.Clauses(upsert).Create(&stat).Error; err != nil {
		t.Fatalf("create usage stat: %v", err)
	}
	stat.RequestCount = 2
	stat.SuccessCount = 2
	if err := db.Clauses(upsert).Create(&stat).Error; err != nil {
		t.Fatalf("upsert usage stat: %v", err)
	}
}

// TestExternalDatabaseModelPriceIdentityUsesExactComparison protects the
// case-sensitive model ID contract on every supported external database.
// MySQL requires an explicit binary column collation; PostgreSQL already
// preserves case under its normal text semantics.
func TestExternalDatabaseModelPriceIdentityUsesExactComparison(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_TEST_DSN"))
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}

	db, err := storage.OpenWithSource(dsn, config.DatabaseSourceExternal)
	if err != nil {
		t.Fatalf("OpenWithSource() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}

	base := fmt.Sprintf("external-model-%d", time.Now().UnixNano())
	upper := models.ModelPrice{ChannelID: "openai", ModelID: "Model-" + base}
	lower := models.ModelPrice{ChannelID: "openai", ModelID: "model-" + base}
	if err := db.Create(&upper).Error; err != nil {
		t.Fatalf("create first case-distinct model price: %v", err)
	}
	if err := db.Create(&lower).Error; err != nil {
		t.Fatalf("create second case-distinct model price: %v", err)
	}
}

// TestExternalDatabaseReadSnapshotUsesRepeatableRead is run against a MySQL
// connection configured with READ COMMITTED. The transaction helper must
// explicitly promote the one report transaction before requesting its stable
// snapshot; WITH CONSISTENT SNAPSHOT alone follows the session isolation.
func TestExternalDatabaseReadSnapshotUsesRepeatableRead(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_TEST_DSN"))
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}

	db, err := storage.OpenWithSource(dsn, config.DatabaseSourceExternal)
	if err != nil {
		t.Fatalf("OpenWithSource() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	if db.Dialector.Name() != "mysql" {
		t.Skip("repeatable-read setup is specific to MySQL")
	}

	modelID := fmt.Sprintf("external-snapshot-%d", time.Now().UnixNano())
	err = dbtx.Run(t.Context(), db, dbtx.Options{
		Mode:      dbtx.ReadSnapshot,
		Operation: "external read snapshot contract",
	}, func(tx *gorm.DB) error {
		var before int64
		if err := tx.Model(&models.ModelPrice{}).
			Where("model_id = ?", modelID).
			Count(&before).Error; err != nil {
			return err
		}
		if before != 0 {
			return fmt.Errorf("initial model price count = %d, want 0", before)
		}
		if err := db.Create(&models.ModelPrice{ChannelID: "openai", ModelID: modelID}).Error; err != nil {
			return err
		}
		var after int64
		if err := tx.Model(&models.ModelPrice{}).
			Where("model_id = ?", modelID).
			Count(&after).Error; err != nil {
			return err
		}
		if after != 0 {
			return fmt.Errorf("snapshot model price count after concurrent write = %d, want 0", after)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ReadSnapshot transaction error = %v", err)
	}
}
