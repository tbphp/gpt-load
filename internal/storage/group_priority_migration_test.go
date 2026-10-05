package storage

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"
)

func TestGroupPriorityMigrationContract(t *testing.T) {
	testGroupPriorityMigration(t, openInternalMigrationTestDatabase)
}

func TestExternalGroupPriorityMigrationContract(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testGroupPriorityMigration(t, func(t *testing.T) *gorm.DB { return openExternalIncrementalMigrationDatabase(t, dsn) })
}

func testGroupPriorityMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
	for _, scenario := range []string{"fresh", "upgrade", "interrupted"} {
		t.Run(scenario, func(t *testing.T) {
			db := open(t)
			if len(migrations) < 29 {
				t.Fatal("group priority migration is missing")
			}
			if scenario != "fresh" {
				if err := applyMigrationRegistry(db, migrations[:28]); err != nil {
					t.Fatal(err)
				}
				if err := db.Table("groups").Create(map[string]any{
					"id": 1, "name": "existing", "channel_id": "openai", "connection_type": "api_key",
					"params": "{}", "models": "[]", "enabled": true, "created_at_ms": 0, "updated_at_ms": 0,
				}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "interrupted" {
				entry := migrations[28]
				up := entry.Up
				entry.Up = func(tx *gorm.DB) error {
					if err := up(tx); err != nil {
						return err
					}
					return fmt.Errorf("interrupt priority migration")
				}
				registry := append(append([]migration(nil), migrations[:28]...), entry)
				if err := applyMigrationRegistry(db, registry); err == nil {
					t.Fatal("expected migration interruption")
				}
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
			if !db.Migrator().HasColumn("groups", "priority") {
				t.Fatal("group priority is missing")
			}
			if scenario != "fresh" {
				var priority int32
				if err := db.Table("groups").Select("priority").Where("id = ?", 1).Scan(&priority).Error; err != nil || priority != 0 {
					t.Fatalf("migrated default = %d, error = %v", priority, err)
				}
				for _, value := range []int32{-2147483648, 2147483647, 0} {
					if err := db.Table("groups").Where("id = ?", 1).Update("priority", value).Error; err != nil {
						t.Fatal(err)
					}
					if err := db.Table("groups").Select("priority").Where("id = ?", 1).Scan(&priority).Error; err != nil || priority != value {
						t.Fatalf("priority = %d, want %d, error = %v", priority, value, err)
					}
				}
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
		})
	}
}
