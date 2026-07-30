package storage_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
)

func TestAutoMigrateCreatesSchemaV3SecurityTables(t *testing.T) {
	db := openMigratedDatabase(t)
	if storage.CurrentSchemaVersion != 3 {
		t.Fatalf("CurrentSchemaVersion = %d, want 3", storage.CurrentSchemaVersion)
	}

	type columnInfo struct {
		Name    string
		NotNull int `gorm:"column:notnull"`
	}
	var columns []columnInfo
	if err := db.Raw("PRAGMA table_info('access_keys')").Scan(&columns).Error; err != nil {
		t.Fatalf("inspect access_keys: %v", err)
	}
	var suffixNotNull bool
	for _, column := range columns {
		if column.Name == "key_suffix" {
			suffixNotNull = column.NotNull == 1
		}
	}
	if !suffixNotNull {
		t.Fatal("access_keys.key_suffix is missing or nullable")
	}
	if !db.Migrator().HasTable(&models.ControlOperation{}) {
		t.Fatal("control_operations table is missing")
	}

	valid := models.AccessKey{
		Name: "valid", KeyValue: "ciphertext", KeyHash: "hash-valid", KeySuffix: "7f2a",
		Status: "active", Filters: models.JSON(`{"groups":[],"protocols":[],"models":[]}`),
	}
	if err := db.Create(&valid).Error; err != nil {
		t.Fatalf("create valid AccessKey: %v", err)
	}
	invalid := valid
	invalid.ID = 0
	invalid.Name = "invalid"
	invalid.KeyHash = "hash-invalid"
	invalid.KeySuffix = "7F2A"
	if err := db.Create(&invalid).Error; err == nil {
		t.Fatal("create invalid AccessKey suffix error = nil, want CHECK rejection")
	}
}

func TestAutoMigrateUpgradesV1AccessKeyWithBackups(t *testing.T) {
	dataDir := t.TempDir()
	keyMaterial := strings.Repeat("ab", 32)
	keyPath := filepath.Join(dataDir, encryption.KeyFileName)
	if err := os.WriteFile(keyPath, []byte(keyMaterial+"\n"), 0o600); err != nil {
		t.Fatalf("write encryption key fixture: %v", err)
	}
	keyService, err := encryption.NewServiceWithKeyFile("", dataDir)
	if err != nil {
		t.Fatalf("NewServiceWithKeyFile() error = %v", err)
	}
	dbPath := filepath.Join(dataDir, "gpt-load.db")
	db, err := storage.OpenWithSource(dbPath, config.DatabaseSourceManaged)
	if err != nil {
		t.Fatalf("OpenWithSource() error = %v", err)
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

	createLegacySchemaV1(t, db)
	plaintext := "sk-gl-00112233445566778899aabbccdd7f2a"
	ciphertext, err := keyService.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if err := db.Exec(`INSERT INTO access_keys
		(name,key_value,key_hash,status,filters,rpm_limit,daily_cost_limit,monthly_cost_limit,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		"legacy", ciphertext, keyService.Hash(plaintext), "active",
		`{"groups":[],"protocols":[],"models":[]}`, 0, 0, 0,
		"2026-07-30T00:00:00Z", "2026-07-30T00:00:00Z",
	).Error; err != nil {
		t.Fatalf("insert legacy AccessKey: %v", err)
	}

	if err := storage.AutoMigrateWithEncryption(db, keyService, dataDir); err != nil {
		t.Fatalf("AutoMigrateWithEncryption() error = %v", err)
	}

	var suffix string
	if err := db.Raw("SELECT key_suffix FROM access_keys WHERE name = 'legacy'").Scan(&suffix).Error; err != nil {
		t.Fatalf("read migrated suffix: %v", err)
	}
	if suffix != "7f2a" {
		t.Fatalf("migrated suffix = %q, want 7f2a", suffix)
	}
	var version uint
	if err := db.Raw("SELECT version FROM schema_info").Scan(&version).Error; err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != 3 {
		t.Fatalf("schema version = %d, want 3", version)
	}
	assertSingleSecureBackup(t, dbPath+".schema-v1-backup-*")
	assertSingleSecureBackup(t, keyPath+".schema-v1-backup-*")
}

func TestAutoMigrateV1AccessKeyFailureRollsBackWithoutLeakingCredential(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	createLegacySchemaV1(t, db)

	correct, err := encryption.NewService("correct-migration-key")
	if err != nil {
		t.Fatalf("NewService(correct) error = %v", err)
	}
	wrong, err := encryption.NewService("wrong-migration-key")
	if err != nil {
		t.Fatalf("NewService(wrong) error = %v", err)
	}
	const plaintext = "sk-gl-00112233445566778899aabbccddeeff"
	ciphertext, err := correct.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if err := db.Exec(`INSERT INTO access_keys
		(name,key_value,key_hash,status,filters,rpm_limit,daily_cost_limit,monthly_cost_limit)
		VALUES (?,?,?,?,?,?,?,?)`,
		"legacy", ciphertext, correct.Hash(plaintext), "active",
		`{"groups":[],"protocols":[],"models":[]}`, 0, 0, 0,
	).Error; err != nil {
		t.Fatalf("insert legacy AccessKey: %v", err)
	}

	migrationErr := storage.AutoMigrateWithEncryption(db, wrong, "")
	if migrationErr == nil {
		t.Fatal("AutoMigrateWithEncryption(wrong key) error = nil, want failure")
	}
	for _, forbidden := range []string{plaintext, ciphertext} {
		if strings.Contains(migrationErr.Error(), forbidden) {
			t.Fatalf("migration error leaked credential material: %v", migrationErr)
		}
	}
	var version uint
	if err := db.Raw("SELECT version FROM schema_info").Scan(&version).Error; err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != 1 {
		t.Fatalf("schema version after failure = %d, want 1", version)
	}
	if db.Migrator().HasColumn("access_keys", "key_suffix") {
		t.Fatal("failed migration left key_suffix column behind")
	}
}

func createLegacySchemaV1(t *testing.T, db *gorm.DB) {
	t.Helper()
	statements := []string{
		`CREATE TABLE schema_info (version integer PRIMARY KEY)`,
		`INSERT INTO schema_info(version) VALUES (1)`,
		`CREATE TABLE access_keys (
			id integer PRIMARY KEY AUTOINCREMENT,
			name varchar(255) NOT NULL,
			key_value text NOT NULL,
			key_hash varchar(128) NOT NULL UNIQUE,
			status varchar(32) NOT NULL DEFAULT 'active'
				CHECK (status IN ('active','disabled')),
			filters json,
			rpm_limit integer NOT NULL DEFAULT 0,
			daily_cost_limit real NOT NULL DEFAULT 0,
			monthly_cost_limit real NOT NULL DEFAULT 0,
			created_at datetime,
			updated_at datetime
		)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create schema v1 fixture: %v", err)
		}
	}
}

func assertSingleSecureBackup(t *testing.T, pattern string) {
	t.Helper()
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob backup: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("backup matches = %v, want exactly one", matches)
	}
	assertSecureBackupPermissions(t, matches[0])
}
