package storage

import (
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"gpt-load/internal/storage/models"
)

// policyBindingRow 构造 group 作用域的测试绑定行。
func policyBindingRow(groupID uint, revision uint64) models.PolicyBinding {
	return models.PolicyBinding{
		Scope: models.PolicyScopeGroup, GroupID: groupID, Revision: revision,
		SchemaVersion: 1, Config: models.JSON(`{"schema_version":1,"rules":[]}`),
	}
}

// schemaField 构造 GormDBDataType 测试用的字段描述。
func schemaField(table, name string) *schema.Field {
	return &schema.Field{Name: name, Schema: &schema.Schema{Table: table}}
}

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

func TestPolicyBindingTargetUniqueConstraintEnforced(t *testing.T) {
	db := openInternalMigrationTestDatabase(t)
	if err := AutoMigrate(db); err != nil {
		t.Fatal(err)
	}

	first := policyBindingRow(42, 1)
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("create first binding: %v", err)
	}

	// Duplicate (scope, group_id, credential_id) must be rejected
	duplicate := policyBindingRow(42, 2)
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("expected unique constraint violation on duplicate policy binding target")
	}
}

func TestPolicyConfigMySQLCapacity(t *testing.T) {
	mysqlDB := &gorm.DB{Config: &gorm.Config{}}
	mysqlDB.Dialector = &mockMySQLDialector{}

	policyField := schemaField("policy_bindings", "Config")
	businessField := schemaField("groups", "Params")

	var jsonVal models.JSON
	// 1. policy_bindings.Config gets longtext in MySQL to accommodate 256KiB
	if dt := jsonVal.GormDBDataType(mysqlDB, policyField); dt != "longtext" {
		t.Fatalf("JSON.GormDBDataType(mysql, policyField) = %q, want \"longtext\"", dt)
	}

	// 2. Business tables (like groups.Params) do NOT get longtext, falling back to default
	if dt := jsonVal.GormDBDataType(mysqlDB, businessField); dt != "" {
		t.Fatalf("JSON.GormDBDataType(mysql, businessField) = %q, want \"\" (fallback)", dt)
	}

	sqliteDB := &gorm.DB{Config: &gorm.Config{}}
	sqliteDB.Dialector = &mockSQLiteDialector{}
	if dt := jsonVal.GormDBDataType(sqliteDB, policyField); dt != "text" {
		t.Fatalf("JSON.GormDBDataType(sqlite, policyField) = %q, want \"text\"", dt)
	}
}

type mockMySQLDialector struct{ gorm.Dialector }

func (mockMySQLDialector) Name() string { return "mysql" }

type mockSQLiteDialector struct{ gorm.Dialector }

func (mockSQLiteDialector) Name() string { return "sqlite" }
