package storage

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gpt-load/internal/storage/models"
)

type migrationQueryCounter struct {
	logger.Interface
	total             int
	currentDatabase   int
	informationSchema int
}

func (counter *migrationQueryCounter) LogMode(logger.LogLevel) logger.Interface { return counter }

func (counter *migrationQueryCounter) Trace(_ context.Context, _ time.Time, query func() (string, int64), _ error) {
	sql, _ := query()
	sql = strings.ToLower(sql)
	counter.total++
	if strings.Contains(sql, "select database()") {
		counter.currentDatabase++
	}
	if strings.Contains(sql, "information_schema") {
		counter.informationSchema++
	}
}

func TestExternalMigrationValidationLimitsMetadataQueries(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_TEST_DSN"))
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	db := openExternalIncrementalMigrationDatabase(t, dsn)
	initialize := &migrationQueryCounter{Interface: logger.Discard}
	if err := AutoMigrate(db.Session(&gorm.Session{Logger: initialize})); err != nil {
		t.Fatalf("initialize schema: %v", err)
	}
	t.Logf("driver=%s phase=initialize total=%d current_database=%d information_schema=%d", db.Dialector.Name(), initialize.total, initialize.currentDatabase, initialize.informationSchema)
	initialLimit := 1000
	if db.Dialector.Name() == "mysql" {
		initialLimit = 1800
	}
	if initialize.total > initialLimit {
		t.Errorf("initialization executed %d SQL statements, want at most %d", initialize.total, initialLimit)
	}

	counter := &migrationQueryCounter{Interface: logger.Discard}
	if err := AutoMigrate(db.Session(&gorm.Session{Logger: counter})); err != nil {
		t.Fatalf("validate completed schema: %v", err)
	}
	t.Logf("driver=%s phase=restart total=%d current_database=%d information_schema=%d", db.Dialector.Name(), counter.total, counter.currentDatabase, counter.informationSchema)
	if counter.total > 400 {
		t.Errorf("repeat startup executed %d SQL statements, want at most 400", counter.total)
	}
	if db.Dialector.Name() == "mysql" && counter.currentDatabase > 10 {
		t.Errorf("MySQL repeated SELECT DATABASE() %d times, want at most 10", counter.currentDatabase)
	}
}

func TestExternalMigrationValidationPropagatesMetadataReadFailure(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_TEST_DSN"))
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	db := openExternalIncrementalMigrationDatabase(t, dsn)
	inspection, err := newMigrationInspection(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	inspection.db = db.WithContext(ctx)
	err = inspection.validate(func(validationDB *gorm.DB) error {
		// 对“不存在”的判断可以通过，但元数据读取失败不能被当作不存在。
		_ = validationDB.Migrator().HasTable("groups")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("metadata read error = %v, want context cancellation", err)
	}
}

func TestMigrationLedgerInspectionPropagatesReadFailure(t *testing.T) {
	db := openInternalMigrationTestDatabase(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := migrationTableExists(db, migrationLedgerTable); err == nil {
		t.Fatal("missing migration ledger was inferred from a failed database read")
	}
}

func TestMigrationValidationRetainsSafetyChecks(t *testing.T) {
	verifyMigrationValidationSafety(t, openInternalMigrationTestDatabase(t))
}

func TestExternalMigrationValidationRetainsSafetyChecks(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_TEST_DSN"))
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	verifyMigrationValidationSafety(t, openExternalIncrementalMigrationDatabase(t, dsn))
}

func verifyMigrationValidationSafety(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Table("system_settings").Create(map[string]any{
		"key": "inject_usage_options", "value": "true", "updated_at_ms": 1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := AutoMigrate(db); err == nil || !strings.Contains(err.Error(), "0008_remove_inject_usage_options") {
		t.Fatalf("retired system setting passed validation: %v", err)
	}
	if err := db.Where(&models.SystemSetting{Key: "inject_usage_options"}).Delete(&models.SystemSetting{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("groups").Create(map[string]any{
		"name": "retired-override", "channel_id": "openai", "params": "{}", "models": "[]",
		"overrides": `{"inject_usage_options":true}`, "created_at_ms": 1, "updated_at_ms": 1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := AutoMigrate(db); err == nil || !strings.Contains(err.Error(), "0008_remove_inject_usage_options") {
		t.Fatalf("retired Group override passed validation: %v", err)
	}
	if err := db.Table("groups").Where(map[string]any{"name": "retired-override"}).Update("overrides", "{}").Error; err != nil {
		t.Fatal(err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("valid Group override failed validation: %v", err)
	}
	if err := db.Migrator().DropIndex("model_prices", "idx_model_prices_channel_model"); err != nil {
		t.Fatal(err)
	}
	if err := AutoMigrate(db); err == nil || !strings.Contains(err.Error(), "validate applied migration 0001_initial") {
		t.Fatalf("missing completed index passed validation: %v", err)
	}
	if db.Migrator().HasIndex("model_prices", "idx_model_prices_channel_model") {
		t.Fatal("validation recreated an index owned by a completed migration")
	}
}
