package loader

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
)

// TestExternalDatabaseSystemSettingKeyScope verifies reserved-column reads and
// updates against the real MySQL and PostgreSQL versions used by CI.
func TestExternalDatabaseSystemSettingKeyScope(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_TEST_DSN"))
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}

	db, err := storage.OpenWithSource(dsn, config.DatabaseSourceExternal)
	if err != nil {
		t.Fatalf("open external database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get external database connection pool: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("migrate external database: %v", err)
	}

	key := fmt.Sprintf("_internal.reserved-key-contract.%d", time.Now().UnixNano())
	row := models.SystemSetting{Key: key, Value: "before"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create system setting: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Where(&models.SystemSetting{Key: key}).Delete(&models.SystemSetting{}).Error
	})

	var loaded models.SystemSetting
	if err := systemSettingKeyScope(db, key).Take(&loaded).Error; err != nil {
		t.Fatalf("read system setting by reserved column: %v", err)
	}
	if loaded.Value != "before" {
		t.Fatalf("loaded value = %q, want %q", loaded.Value, "before")
	}
	if err := systemSettingKeyScope(db.Model(&models.SystemSetting{}), key).
		Update("value", "after").Error; err != nil {
		t.Fatalf("update system setting by reserved column: %v", err)
	}
	if err := db.Where(&models.SystemSetting{Key: key}).Take(&loaded).Error; err != nil {
		t.Fatalf("reload system setting: %v", err)
	}
	if loaded.Value != "after" {
		t.Fatalf("updated value = %q, want %q", loaded.Value, "after")
	}
}
