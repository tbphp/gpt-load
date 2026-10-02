package storage

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"
)

func TestConcurrencyMigrationContract(t *testing.T) {
	t.Parallel()

	testConcurrencyMigration(t, openInternalMigrationTestDatabase)
}
func TestExternalConcurrencyMigrationContract(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testConcurrencyMigration(t, func(t *testing.T) *gorm.DB { return openExternalIncrementalMigrationDatabase(t, dsn) })
}
func testConcurrencyMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
	for _, scenario := range []string{"fresh", "upgrade", "interrupted"} {
		t.Run(scenario, func(t *testing.T) {
			db := open(t)
			if db.Dialector.Name() == "sqlite" {
				t.Parallel()
			}
			if len(migrations) < 23 {
				t.Fatal("concurrency migration is missing")
			}
			if scenario != "fresh" {
				if err := applyMigrationRegistry(db, migrations[:22]); err != nil {
					t.Fatal(err)
				}
				if err := db.Table("access_keys").Create(map[string]any{"id": 1, "name": "existing", "key_value": "test-cipher", "key_hash": "test-hash", "key_suffix": "cafe", "status": "active", "created_at_ms": 1, "updated_at_ms": 1}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "interrupted" {
				entry := migrations[22]
				up := entry.Up
				entry.Up = func(tx *gorm.DB) error {
					if err := up(tx); err != nil {
						return err
					}
					return fmt.Errorf("interrupt after concurrency DDL")
				}
				if err := applyMigrationRegistry(db, append(append([]migration(nil), migrations[:22]...), entry)); err == nil {
					t.Fatal("interruption succeeded")
				}
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
			if !db.Migrator().HasColumn("access_keys", "concurrency_limit") {
				t.Fatal("concurrency limit is missing")
			}
			if scenario != "fresh" {
				var row struct {
					Name             string
					ConcurrencyLimit *int64
				}
				if err := db.Table("access_keys").Where("id = ?", 1).Take(&row).Error; err != nil {
					t.Fatal(err)
				}
				if row.Name != "existing" || row.ConcurrencyLimit != nil {
					t.Fatalf("existing data changed: %+v", row)
				}
				for _, limit := range []any{int64(0), int64(7), nil} {
					if err := db.Table("access_keys").Where("id = ?", 1).Update("concurrency_limit", limit).Error; err != nil {
						t.Fatal(err)
					}
				}
				if err := db.Table("access_keys").Where("id = ?", 1).Update("concurrency_limit", -1).Error; err == nil {
					t.Fatal("negative limit accepted")
				}
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
		})
	}
}
