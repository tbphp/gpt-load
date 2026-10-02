package storage

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"
)

func TestCredentialNameMigrationContract(t *testing.T) {
	t.Parallel()

	testCredentialNameMigration(t, openInternalMigrationTestDatabase)
}

func TestExternalCredentialNameMigrationContract(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testCredentialNameMigration(t, func(t *testing.T) *gorm.DB { return openExternalIncrementalMigrationDatabase(t, dsn) })
}

func testCredentialNameMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
	for _, scenario := range []string{"fresh", "upgrade", "interrupted"} {
		t.Run(scenario, func(t *testing.T) {
			db := open(t)
			if db.Dialector.Name() == "sqlite" {
				t.Parallel()
			}
			if len(migrations) < 26 {
				t.Fatal("credential name migration is missing")
			}
			if scenario != "fresh" {
				if err := applyMigrationRegistry(db, migrations[:25]); err != nil {
					t.Fatal(err)
				}
				if err := db.Table("groups").Create(map[string]any{"id": 1, "name": "existing", "channel_id": "openai", "params": "{}", "models": "[]", "created_at_ms": 1, "updated_at_ms": 1}).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Table("credentials").Create(map[string]any{"id": 1, "group_id": 1, "data": "cipher", "fingerprint": "fingerprint", "identity_fingerprint": "identity", "created_at_ms": 1, "updated_at_ms": 1}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "interrupted" {
				entry := migrations[25]
				up := entry.Up
				entry.Up = func(tx *gorm.DB) error {
					if err := up(tx); err != nil {
						return err
					}
					return fmt.Errorf("interrupt credential name DDL")
				}
				registry := append(append([]migration(nil), migrations[:25]...), entry)
				if err := applyMigrationRegistry(db, registry); err == nil {
					t.Fatal("expected migration interruption")
				}
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
			if !db.Migrator().HasColumn("credentials", "name") {
				t.Fatal("credential name column is missing")
			}
			if scenario != "fresh" {
				var row struct {
					Name, Data, Fingerprint, IdentityFingerprint string
					SecretVersion                                uint64
				}
				if err := db.Table("credentials").Where("id = 1").Take(&row).Error; err != nil {
					t.Fatal(err)
				}
				if row.Name != "" || row.Data != "cipher" || row.Fingerprint != "fingerprint" || row.IdentityFingerprint != "identity" || row.SecretVersion != 1 {
					t.Fatal("migration changed existing credential")
				}
				if err := db.Table("credentials").Where("id = 1").Update("name", "生产账号").Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
		})
	}
}
