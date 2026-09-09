package storage

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
)

func TestCustomAccessKeyMigrationContract(t *testing.T) {
	testCustomAccessKeyMigration(t, openInternalMigrationTestDatabase)
}

func TestExternalCustomAccessKeyMigrationContract(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testCustomAccessKeyMigration(t, func(t *testing.T) *gorm.DB { return openExternalIncrementalMigrationDatabase(t, dsn) })
}

func testCustomAccessKeyMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
	for _, scenario := range []string{"fresh", "upgrade", "interrupted"} {
		t.Run(scenario, func(t *testing.T) {
			db := open(t)
			var key models.AccessKey
			var rule models.AccessKeyCostLimitRule
			var deletedID uint
			if scenario != "fresh" {
				if err := applyMigrationRegistry(db, migrations[:10]); err != nil {
					t.Fatal(err)
				}
				key = models.AccessKey{Name: "existing", KeyValue: "encrypted-test-value", KeyHash: "existing-hash", KeySuffix: "cafe", Status: "active", Filters: models.JSON(`{}`)}
				if err := db.Omit("KeyPrefix").Create(&key).Error; err != nil {
					t.Fatal(err)
				}
				rule = models.AccessKeyCostLimitRule{AccessKeyID: key.ID, Kind: models.AccessKeyCostLimitKindTotal, LimitNanoUSD: 100, RuleRevision: 1}
				if err := db.Create(&rule).Error; err != nil {
					t.Fatal(err)
				}
				state := models.AccessKeyCostLimitState{RuleID: rule.ID, RuleRevision: 1, UsedNanoUSD: 17, SnapshotVersion: 1}
				if err := db.Create(&state).Error; err != nil {
					t.Fatal(err)
				}
				// 删除过的较大 ID 也不能因重建表而被再次分配。
				deleted := models.AccessKey{Name: "deleted", KeyValue: "cipher", KeyHash: "deleted-hash", KeySuffix: "dead", Status: "active", Filters: models.JSON(`{}`)}
				if err := db.Omit("KeyPrefix").Create(&deleted).Error; err != nil {
					t.Fatal(err)
				}
				deletedID = deleted.ID
				if err := db.Delete(&deleted).Error; err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "interrupted" && len(migrations) > 10 {
				entry := migrations[10]
				up := entry.Up
				entry.Up = func(tx *gorm.DB) error {
					if err := up(tx); err != nil {
						return err
					}
					return fmt.Errorf("interrupt after custom access key DDL")
				}
				registry := append(append([]migration(nil), migrations[:10]...), entry)
				if err := applyMigrationRegistry(db, registry); err == nil {
					t.Fatal("interruption succeeded")
				}
				if db.Dialector.Name() == "sqlite" {
					var enabled int
					if err := db.Raw("PRAGMA foreign_keys").Scan(&enabled).Error; err != nil || enabled != 1 {
						t.Fatal("interrupted migration did not restore foreign keys")
					}
				}
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
			if scenario != "fresh" {
				var current models.AccessKey
				if err := db.Take(&current, key.ID).Error; err != nil {
					t.Fatal(err)
				}
				if current.KeyValue != key.KeyValue || current.KeyHash != key.KeyHash || current.KeySuffix != key.KeySuffix {
					t.Fatal("existing credential changed")
				}
				var currentRule models.AccessKeyCostLimitRule
				if err := db.Take(&currentRule, rule.ID).Error; err != nil {
					t.Fatal(err)
				}
				var state models.AccessKeyCostLimitState
				if err := db.Take(&state, rule.ID).Error; err != nil {
					t.Fatal(err)
				}
				if currentRule.LimitNanoUSD != 100 || state.UsedNanoUSD != 17 {
					t.Fatal("quota data changed")
				}
			}
			custom := models.AccessKey{Name: "custom", KeyValue: "encrypted-custom", KeyHash: "custom-hash", KeySuffix: "Z9-_", Status: "active", Filters: models.JSON(`{}`)}
			if err := db.Create(&custom).Error; err != nil {
				t.Fatalf("custom suffix rejected: %v", err)
			}
			if scenario != "fresh" && custom.ID <= deletedID {
				t.Fatal("migration reused a deleted access key ID")
			}
			if err := db.Model(&custom).Update("key_suffix", "****").Error; err != nil {
				t.Fatal(err)
			}
			for _, invalid := range []string{"", "abc", "12345"} {
				if err := db.Model(&custom).Update("key_suffix", invalid).Error; err == nil {
					t.Fatal("invalid suffix length accepted")
				}
			}
			if db.Dialector.Name() == "sqlite" {
				var foreignKeys int
				if err := db.Raw("PRAGMA foreign_keys").Scan(&foreignKeys).Error; err != nil || foreignKeys != 1 {
					t.Fatal("foreign keys not restored")
				}
			}
		})
	}
}
