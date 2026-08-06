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
	"gpt-load/internal/platform/dbtx"
	"gpt-load/internal/storage"
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
		"groups", "upstream_keys", "access_keys", "request_logs", "request_log_attempts",
		"usage_aggregation_journal", "usage_stats", "model_prices", "system_settings",
		"jobs", "control_operations", "schema_migrations",
	} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("table %q is missing", table)
		}
	}

	suffix := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	group := models.Group{
		Name:        fmt.Sprintf("integration-%s-%d", suffix, time.Now().UnixNano()),
		UpstreamURL: "https://integration.example.com",
		Protocols:   models.JSON(`["openai-completions"]`),
		Models:      models.JSON(`[]`),
		Config:      models.JSON(`{}`),
		Enabled:     true,
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	key := models.UpstreamKey{
		GroupID:  group.ID,
		KeyValue: "ciphertext",
		KeyHash:  fmt.Sprintf("integration-%d", time.Now().UnixNano()),
	}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("create upstream key: %v", err)
	}

	setting := models.SystemSetting{
		Key:         "integration.key",
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
		GroupID:       group.ID,
		Model:         "integration-model",
		RequestCount:  1,
		SuccessCount:  1,
	}
	upsert := clause.OnConflict{
		Columns: []clause.Column{
			{Name: "bucket_start_ms"}, {Name: "access_key_id"},
			{Name: "group_id"}, {Name: "model"},
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
