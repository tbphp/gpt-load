package storage

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"
)

func TestAccessKeyPrefixMigrationContract(t *testing.T) {
	testAccessKeyPrefixMigration(t, openInternalMigrationTestDatabase)
}

func TestExternalAccessKeyPrefixMigrationContract(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testAccessKeyPrefixMigration(t, func(t *testing.T) *gorm.DB { return openExternalIncrementalMigrationDatabase(t, dsn) })
}

func testAccessKeyPrefixMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
	for _, scenario := range []string{"fresh", "upgrade", "interrupted"} {
		t.Run(scenario, func(t *testing.T) {
			db := open(t)
			if scenario != "fresh" {
				if err := applyMigrationRegistry(db, migrations[:11]); err != nil {
					t.Fatal(err)
				}
				if err := db.Exec("INSERT INTO access_keys (id, name, key_value, key_hash, key_suffix, status, created_at_ms, updated_at_ms) VALUES (1, 'existing', 'cipher', 'legacy-hash', 'abcd', 'active', 1, 1)").Error; err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "interrupted" && len(migrations) > 11 {
				entry := migrations[11]
				up := entry.Up
				entry.Up = func(tx *gorm.DB) error {
					if err := up(tx); err != nil {
						return err
					}
					return fmt.Errorf("interrupt after access key prefix DDL")
				}
				registry := append(append([]migration(nil), migrations[:11]...), entry)
				if err := applyMigrationRegistry(db, registry); err == nil {
					t.Fatal("interruption succeeded")
				}
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
			if !db.Migrator().HasColumn("access_keys", "key_prefix") {
				t.Fatal("access key mask prefix is missing")
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
			if scenario == "fresh" {
				if err := db.Exec("INSERT INTO access_keys (id, name, key_value, key_hash, key_suffix, status, created_at_ms, updated_at_ms) VALUES (1, 'default', 'cipher', 'legacy-hash', 'abcd', 'active', 1, 1)").Error; err != nil {
					t.Fatal(err)
				}
			}
			var row struct{ KeyPrefix, KeySuffix, KeyValue string }
			if err := db.Table("access_keys").Where("id = 1").Take(&row).Error; err != nil {
				t.Fatal(err)
			}
			if row.KeyPrefix != "sk-gl-" || row.KeySuffix != "abcd" || row.KeyValue != "cipher" {
				t.Fatal("legacy key mask or ciphertext changed")
			}
			for _, prefix := range []string{"", "client"} {
				if err := db.Table("access_keys").Where("id = 1").Update("key_prefix", prefix).Error; err != nil {
					t.Fatal(err)
				}
			}
			for _, prefix := range []any{nil, "abc", "1234567"} {
				if err := db.Table("access_keys").Where("id = 1").Update("key_prefix", prefix).Error; err == nil {
					t.Fatal("invalid prefix was accepted")
				}
			}
		})
	}
}
