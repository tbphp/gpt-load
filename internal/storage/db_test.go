package storage_test

import (
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
)

func TestOpenRejectsInvalidDSN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dsn  string
	}{
		{name: "empty", dsn: ""},
		{name: "non sqlite scheme", dsn: "postgres://localhost/gpt-load"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := storage.Open(tt.dsn); err == nil {
				t.Fatalf("Open(%q) error = nil, want a validation error", tt.dsn)
			}
		})
	}
}

func TestOpenCreatesSQLiteDatabase(t *testing.T) {
	t.Parallel()

	dsn := filepath.Join(t.TempDir(), "nested", "gpt-load.db")
	db, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", dsn, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}

func TestAutoMigrateCreatesNineTablesAndSchemaVersion(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)

	wantTables := []string{
		"groups",
		"upstream_keys",
		"access_keys",
		"request_logs",
		"usage_stats",
		"model_prices",
		"system_settings",
		"jobs",
		"schema_info",
	}
	for _, table := range wantTables {
		if !db.Migrator().HasTable(table) {
			t.Errorf("AutoMigrate() did not create table %q", table)
		}
	}

	var versions []uint
	if err := db.Table("schema_info").Pluck("version", &versions).Error; err != nil {
		t.Fatalf("read schema_info: %v", err)
	}
	if len(versions) != 1 || versions[0] != storage.CurrentSchemaVersion {
		t.Fatalf("schema_info versions = %v, want [%d]", versions, storage.CurrentSchemaVersion)
	}

	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("second AutoMigrate() error = %v", err)
	}
	var count int64
	if err := db.Table("schema_info").Count(&count).Error; err != nil {
		t.Fatalf("count schema_info: %v", err)
	}
	if count != 1 {
		t.Fatalf("schema_info row count after a second migration = %d, want 1", count)
	}
}

func TestAutoMigrateCreatesCriticalUniqueConstraints(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)

	t.Run("group signature", func(t *testing.T) {
		first := models.Group{
			Name:        "group-one",
			UpstreamURL: "https://one.example.com",
			Signature:   "same-signature",
			Protocols:   models.JSON(`["openai"]`),
			Models:      models.JSON(`[]`),
			Config:      models.JSON(`{}`),
		}
		second := first
		second.ID = 0
		second.Name = "group-two"
		second.UpstreamURL = "https://two.example.com"

		assertDuplicateRejected(t, db.Create(&first).Error, db.Create(&second).Error)
	})

	t.Run("upstream key group and hash", func(t *testing.T) {
		group := models.Group{
			Name:        "upstream-key-parent",
			UpstreamURL: "https://keys.example.com",
			Signature:   "upstream-key-parent-signature",
			Protocols:   models.JSON(`["openai"]`),
			Models:      models.JSON(`[]`),
		}
		if err := db.Create(&group).Error; err != nil {
			t.Fatalf("create parent group: %v", err)
		}

		first := models.UpstreamKey{
			GroupID:  group.ID,
			KeyValue: "ciphertext-one",
			KeyHash:  "same-key-hash",
		}
		second := first
		second.ID = 0
		second.KeyValue = "ciphertext-two"

		assertDuplicateRejected(t, db.Create(&first).Error, db.Create(&second).Error)
	})

	t.Run("access key hash", func(t *testing.T) {
		first := models.AccessKey{
			Name:     "access-one",
			KeyValue: "ciphertext-one",
			KeyHash:  "same-access-key-hash",
			Filters:  models.JSON(`{}`),
		}
		second := first
		second.ID = 0
		second.Name = "access-two"
		second.KeyValue = "ciphertext-two"

		assertDuplicateRejected(t, db.Create(&first).Error, db.Create(&second).Error)
	})

	t.Run("usage hour group and model", func(t *testing.T) {
		bucket := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
		first := models.UsageStat{
			HourBucket: bucket,
			GroupID:    202,
			Model:      "model-a",
		}
		second := first
		second.ID = 0

		assertDuplicateRejected(t, db.Create(&first).Error, db.Create(&second).Error)
	})
}

func TestAutoMigrateCreatesUpstreamKeyForeignKeyWithCascade(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)

	type foreignKey struct {
		Table    string
		From     string
		To       string
		OnDelete string `gorm:"column:on_delete"`
	}
	var foreignKeys []foreignKey
	if err := db.Raw("PRAGMA foreign_key_list('upstream_keys')").Scan(&foreignKeys).Error; err != nil {
		t.Fatalf("inspect upstream_keys foreign keys: %v", err)
	}

	var groupForeignKey *foreignKey
	for i := range foreignKeys {
		if foreignKeys[i].Table == "groups" && foreignKeys[i].From == "group_id" && foreignKeys[i].To == "id" {
			groupForeignKey = &foreignKeys[i]
			break
		}
	}
	if groupForeignKey == nil {
		t.Fatalf("upstream_keys foreign keys = %+v, want group_id -> groups.id", foreignKeys)
	}
	if groupForeignKey.OnDelete != "CASCADE" {
		t.Fatalf("upstream_keys group foreign key ON DELETE = %q, want CASCADE", groupForeignKey.OnDelete)
	}

	group := models.Group{
		Name:        "cascade-parent",
		UpstreamURL: "https://cascade.example.com",
		Signature:   "cascade-parent-signature",
		Protocols:   models.JSON(`["openai"]`),
		Models:      models.JSON(`[]`),
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create parent group: %v", err)
	}
	key := models.UpstreamKey{GroupID: group.ID, KeyValue: "ciphertext", KeyHash: "cascade-key-hash"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("create upstream key: %v", err)
	}
	if err := db.Delete(&group).Error; err != nil {
		t.Fatalf("delete parent group: %v", err)
	}

	var keyCount int64
	if err := db.Model(&models.UpstreamKey{}).Where("id = ?", key.ID).Count(&keyCount).Error; err != nil {
		t.Fatalf("count child key: %v", err)
	}
	if keyCount != 0 {
		t.Fatalf("upstream key count after deleting group = %d, want 0", keyCount)
	}
}

func openMigratedDatabase(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	return db
}

func assertDuplicateRejected(t *testing.T, firstErr, duplicateErr error) {
	t.Helper()

	if firstErr != nil {
		t.Fatalf("create initial record: %v", firstErr)
	}
	if duplicateErr == nil {
		t.Fatal("create duplicate record error = nil, want unique constraint error")
	}
}
