package storage

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	migrationfiles "gpt-load/internal/storage/migrations"
)

func TestPriceMultiplierMigrationAddsConfigurationColumns(t *testing.T) {
	db := openInternalMigrationTestDatabase(t)
	if err := AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"groups", "access_keys"} {
		if !db.Migrator().HasColumn(table, "price_multiplier_micros") {
			t.Fatalf("%s.price_multiplier_micros missing", table)
		}
	}
}

func TestPriceMultiplierMigrationUpgradeAndRecovery(t *testing.T) {
	testPriceMultiplierMigrationContract(t, func(t *testing.T) *gorm.DB { return openInternalMigrationTestDatabase(t) })
}

func TestExternalPriceMultiplierMigrationContract(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_TEST_DSN"))
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testPriceMultiplierMigrationContract(t, func(t *testing.T) *gorm.DB { return openExternalIncrementalMigrationDatabase(t, dsn) })
}

func testPriceMultiplierMigrationContract(t *testing.T, open func(*testing.T) *gorm.DB) {
	t.Helper()
	for _, mode := range []string{"fresh", "upgrade", "interrupted", "columns_added"} {
		t.Run(mode, func(t *testing.T) {
			db := open(t)
			if mode != "fresh" {
				if err := applyMigrationRegistry(db, migrations[:8]); err != nil {
					t.Fatal(err)
				}
				if err := db.Table("groups").Create(map[string]any{
					"id": 1, "name": "legacy multiplier group", "channel_id": "openai", "connection_type": "api_key",
					"params": "{}", "models": "[]", "enabled": true, "created_at_ms": 1, "updated_at_ms": 1,
				}).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Exec(`INSERT INTO access_keys (id, name, key_value, key_hash, key_suffix, status, filters, created_at_ms, updated_at_ms) VALUES (1, 'legacy multiplier key', 'encrypted', 'multiplier-hash', 'cafe', 'active', '{}', 1, 1)`).Error; err != nil {
					t.Fatal(err)
				}
			}
			if mode == "interrupted" || mode == "columns_added" {
				entry := migrations[8]
				entry.Up = func(tx *gorm.DB) error {
					if mode == "columns_added" {
						if err := migrationfiles.Up0009(tx); err != nil {
							return err
						}
						return fmt.Errorf("simulated interruption before recording price multiplier migration")
					}
					if err := tx.Exec(`ALTER TABLE ? ADD COLUMN price_multiplier_micros BIGINT NOT NULL DEFAULT 1000000 CONSTRAINT chk_group_price_multiplier CHECK (price_multiplier_micros >= 0 AND price_multiplier_micros <= 1000000000)`, clause.Table{Name: "groups"}).Error; err != nil {
						return err
					}
					return fmt.Errorf("simulated interruption after first price multiplier column")
				}
				entries := append([]migration(nil), migrations[:8]...)
				entries = append(entries, entry)
				if err := applyMigrationRegistry(db, entries); err == nil {
					t.Fatal("interrupted migration succeeded")
				}
				if db.Dialector.Name() == "mysql" {
					if !db.Migrator().HasColumn("groups", "price_multiplier_micros") {
						t.Fatal("MySQL did not retain first DDL")
					}
				} else if db.Migrator().HasColumn("groups", "price_multiplier_micros") {
					t.Fatal("transactional driver did not roll back interrupted DDL")
				}
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
			if err := migrationfiles.Validate0009(db); err != nil {
				t.Fatal(err)
			}
			assertInternalMigrationComplete(t, db, registeredMigrationIDs())
			if mode == "fresh" {
				return
			}
			for _, table := range []string{"groups", "access_keys"} {
				var value int64
				if err := db.Table(table).Select("price_multiplier_micros").Where("id = 1").Scan(&value).Error; err != nil {
					t.Fatal(err)
				}
				if value != 1_000_000 {
					t.Fatalf("%s legacy multiplier = %d", table, value)
				}
				for _, valid := range []int64{0, 125_000, 1_000_000_000} {
					if err := db.Table(table).Where("id = 1").Update("price_multiplier_micros", valid).Error; err != nil {
						t.Fatalf("%s rejects %d: %v", table, valid, err)
					}
				}
				for _, invalid := range []any{int64(-1), int64(1_000_000_001), nil} {
					if err := db.Table(table).Where("id = 1").Update("price_multiplier_micros", invalid).Error; err == nil {
						t.Fatalf("%s accepted invalid multiplier %v", table, invalid)
					}
				}
			}
		})
	}
}
