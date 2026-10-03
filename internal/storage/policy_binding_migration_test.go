package storage

import (
	"fmt"
	"math"
	"os"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	migrationfiles "gpt-load/internal/storage/migrations"
	"gpt-load/internal/storage/models"
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

func TestPolicyRevisionRoundTrip(t *testing.T) {
	db := openInternalMigrationTestDatabase(t)
	if err := AutoMigrate(db); err != nil {
		t.Fatal(err)
	}

	testCases := []uint64{
		1,
		2,
		42,
		math.MaxInt64,
		math.MaxInt64 + 1,
		^uint64(0),
	}

	for i, maxRev := range testCases {
		binding := models.PolicyBinding{
			Scope:         models.PolicyScopeGroup,
			GroupID:       uint(100 + i),
			CredentialID:  0,
			Revision:      models.PolicyRevision(maxRev),
			SchemaVersion: 1,
			Config:        models.JSON(`{"schema_version":1,"rules":[]}`),
		}
		if err := db.Create(&binding).Error; err != nil {
			t.Fatalf("create binding with rev %d: %v", maxRev, err)
		}
		var loaded models.PolicyBinding
		if err := db.First(&loaded, binding.ID).Error; err != nil {
			t.Fatalf("first binding with rev %d: %v", maxRev, err)
		}
		if uint64(loaded.Revision) != maxRev {
			t.Fatalf("loaded.Revision = %d, want %d", loaded.Revision, maxRev)
		}

		// Verify CAS update at boundary
		nextRev := models.PolicyRevision(maxRev)
		if maxRev < ^uint64(0) {
			nextRev = models.PolicyRevision(maxRev + 1)
		}
		res := db.Model(&models.PolicyBinding{}).
			Where("id = ? AND revision = ?", binding.ID, binding.Revision).
			Update("revision", nextRev)
		if res.Error != nil {
			t.Fatalf("CAS update error: %v", res.Error)
		}
		if res.RowsAffected != 1 {
			t.Fatalf("CAS update affected %d rows, want 1", res.RowsAffected)
		}
	}
}

func TestPolicyBindingIndexValidationRejectsNonUniqueOrWrongColumns(t *testing.T) {
	db := openInternalMigrationTestDatabase(t)
	if err := applyMigrationRegistry(db, migrations[:26]); err != nil {
		t.Fatal(err)
	}
	if err := migrationfiles.Up0029(db); err != nil {
		t.Fatal(err)
	}

	// 1. Drop genuine index, create a non-unique index with the same name -> Validate0029 must fail
	if err := db.Migrator().DropIndex("policy_bindings", "idx_policy_bindings_target"); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE INDEX idx_policy_bindings_target ON policy_bindings (scope, group_id, credential_id)").Error; err != nil {
		t.Fatal(err)
	}
	if err := migrationfiles.Validate0029(db); err == nil {
		t.Fatal("Validate0029 should reject non-unique target index")
	}

	// 2. Drop and create index covering wrong columns (e.g. revision only) -> Validate0029 must fail
	if err := db.Migrator().DropIndex("policy_bindings", "idx_policy_bindings_target"); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX idx_policy_bindings_target ON policy_bindings (revision)").Error; err != nil {
		t.Fatal(err)
	}
	if err := migrationfiles.Validate0029(db); err == nil {
		t.Fatal("Validate0029 should reject index with wrong columns")
	}

	// 部分唯一索引不能保证账号绑定唯一，校验与部分迁移恢复均须拒绝。
	if err := db.Migrator().DropIndex("policy_bindings", "idx_policy_bindings_target"); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX idx_policy_bindings_target ON policy_bindings (scope, group_id, credential_id) WHERE credential_id = 0").Error; err != nil {
		t.Fatal(err)
	}
	if err := migrationfiles.Validate0029(db); err == nil {
		t.Fatal("Validate0029 accepted partial target index")
	}
	if err := migrationfiles.Up0029(db); err == nil {
		t.Fatal("Up0029 accepted partial target index during recovery")
	}

	// 3. Drop and recreate valid unique index -> Validate0029 must succeed
	if err := db.Migrator().DropIndex("policy_bindings", "idx_policy_bindings_target"); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX idx_policy_bindings_target ON policy_bindings (scope, group_id, credential_id)").Error; err != nil {
		t.Fatal(err)
	}
	if err := migrationfiles.Validate0029(db); err != nil {
		t.Fatalf("Validate0029 should accept valid unique index: %v", err)
	}
}

func TestPolicyBindingTargetUniqueConstraintEnforced(t *testing.T) {
	db := openInternalMigrationTestDatabase(t)
	if err := AutoMigrate(db); err != nil {
		t.Fatal(err)
	}

	first := models.PolicyBinding{
		Scope:         models.PolicyScopeGroup,
		GroupID:       42,
		CredentialID:  0,
		Revision:      1,
		SchemaVersion: 1,
		Config:        models.JSON(`{"schema_version":1,"rules":[]}`),
	}
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("create first binding: %v", err)
	}

	// Duplicate (scope, group_id, credential_id) must be rejected
	duplicate := models.PolicyBinding{
		Scope:         models.PolicyScopeGroup,
		GroupID:       42,
		CredentialID:  0,
		Revision:      2,
		SchemaVersion: 1,
		Config:        models.JSON(`{"schema_version":1,"rules":[]}`),
	}
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("expected unique constraint violation on duplicate policy binding target")
	}
}

func TestPolicyRevisionScanCompatibility(t *testing.T) {
	var rev models.PolicyRevision

	// 1. Signed int64 -1 (SQLite highbit representation) -> ^uint64(0)
	if err := rev.Scan(int64(-1)); err != nil || uint64(rev) != ^uint64(0) {
		t.Fatalf("Scan(int64(-1)) = %v, rev = %d, want %d", err, rev, ^uint64(0))
	}

	// 2. Unsigned string decimal representation
	if err := rev.Scan("18446744073709551615"); err != nil || uint64(rev) != ^uint64(0) {
		t.Fatalf("Scan(string) = %v, rev = %d, want %d", err, rev, ^uint64(0))
	}

	// 3. Signed string representation from SQLite old records ("-1")
	if err := rev.Scan("-1"); err != nil || uint64(rev) != ^uint64(0) {
		t.Fatalf("Scan(\"-1\") = %v, rev = %d, want %d", err, rev, ^uint64(0))
	}

	// 4. []byte with "-1"
	if err := rev.Scan([]byte("-1")); err != nil || uint64(rev) != ^uint64(0) {
		t.Fatalf("Scan([]byte(\"-1\")) = %v, rev = %d, want %d", err, rev, ^uint64(0))
	}

	// 5. Normal uint64
	if err := rev.Scan(uint64(12345)); err != nil || uint64(rev) != 12345 {
		t.Fatalf("Scan(uint64) = %v, rev = %d, want 12345", err, rev)
	}

	// 6. Value() returns int64 driver.Value
	val, err := rev.Value()
	if err != nil {
		t.Fatalf("Value() error: %v", err)
	}
	if val != int64(12345) {
		t.Fatalf("Value() = %v (%T), want int64(12345)", val, val)
	}

	// 7. Value() for ^uint64(0) returns int64(-1)
	rev = models.PolicyRevision(^uint64(0))
	valMax, err := rev.Value()
	if err != nil {
		t.Fatalf("Value() error: %v", err)
	}
	if valMax != int64(-1) {
		t.Fatalf("Value() = %v (%T), want int64(-1)", valMax, valMax)
	}

	// 8. Invalid string rejects cleanly
	if err := rev.Scan("invalid-number"); err == nil {
		t.Fatal("Scan(invalid-number) should fail")
	}
}

func TestPolicyConfigMySQLCapacity(t *testing.T) {
	mysqlDB := &gorm.DB{Config: &gorm.Config{}}
	mysqlDB.Dialector = &mockMySQLDialector{}

	policyField := &schema.Field{
		Name: "Config",
		Schema: &schema.Schema{
			Table: "policy_bindings",
		},
	}
	businessField := &schema.Field{
		Name: "Params",
		Schema: &schema.Schema{
			Table: "groups",
		},
	}

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
