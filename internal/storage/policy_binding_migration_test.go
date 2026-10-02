package storage

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"

	migrationfiles "gpt-load/internal/storage/migrations"
)

func TestPolicyBindingMigrationContract(t *testing.T) {
	testPolicyBindingMigration(t, openInternalMigrationTestDatabase)
}

func TestExternalPolicyBindingMigrationContract(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testPolicyBindingMigration(t, func(t *testing.T) *gorm.DB { return openExternalIncrementalMigrationDatabase(t, dsn) })
}

func testPolicyBindingMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
	for _, scenario := range []string{"fresh", "upgrade", "interrupted"} {
		t.Run(scenario, func(t *testing.T) {
			db := open(t)
			if len(migrations) < 27 {
				t.Fatal("policy binding migration is missing")
			}
			if scenario != "fresh" {
				if err := applyMigrationRegistry(db, migrations[:26]); err != nil {
					t.Fatal(err)
				}
				if err := db.Table("groups").Create(map[string]any{"id": 1, "name": "existing-group", "channel_id": "openai", "params": "{}", "models": "[]", "created_at_ms": 1, "updated_at_ms": 1}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "interrupted" {
				entry := migrations[26]
				up := entry.Up
				entry.Up = func(tx *gorm.DB) error {
					if err := up(tx); err != nil {
						return err
					}
					return fmt.Errorf("interrupt policy binding DDL")
				}
				registry := append(append([]migration(nil), migrations[:26]...), entry)
				if err := applyMigrationRegistry(db, registry); err == nil {
					t.Fatal("expected migration interruption")
				}
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
			if !db.Migrator().HasTable("policy_bindings") {
				t.Fatal("policy_bindings table is missing")
			}
			for _, column := range []string{"id", "scope", "group_id", "credential_id", "revision", "schema_version", "config", "created_at_ms", "updated_at_ms"} {
				if !db.Migrator().HasColumn("policy_bindings", column) {
					t.Fatalf("policy_bindings column %q is missing", column)
				}
			}
			if !db.Migrator().HasIndex("policy_bindings", "idx_policy_bindings_target") {
				t.Fatal("policy_bindings index idx_policy_bindings_target is missing")
			}
		})
	}
}

func TestPolicyBindingMigrationRecoversMissingIndex(t *testing.T) {
	db := openInternalMigrationTestDatabase(t)
	if err := applyMigrationRegistry(db, migrations[:26]); err != nil {
		t.Fatal(err)
	}
	if err := migrationfiles.Up0029(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropIndex("policy_bindings", "idx_policy_bindings_target"); err != nil {
		t.Fatal(err)
	}
	if err := migrationfiles.Up0029(db); err != nil {
		t.Fatalf("recover policy binding migration: %v", err)
	}
	if err := migrationfiles.Validate0029(db); err != nil {
		t.Fatal(err)
	}
}
