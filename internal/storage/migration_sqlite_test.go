package storage

import (
	"testing"

	"gorm.io/gorm"
)

func TestSQLiteMigrationRejectsOrphansAndRestoresForeignKeys(t *testing.T) {
	db := openInternalMigrationTestDatabase(t)
	entry := migration{
		ID: "0001_foreign_key_probe",
		Up: func(tx *gorm.DB) error {
			for _, statement := range []string{
				"CREATE TABLE parents (id INTEGER PRIMARY KEY)",
				"CREATE TABLE children (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parents(id))",
				"INSERT INTO children (id, parent_id) VALUES (1, 99)",
			} {
				if err := tx.Exec(statement).Error; err != nil {
					return err
				}
			}
			return nil
		},
		Validate:            func(*gorm.DB) error { return nil },
		ValidateRecoverable: func(*gorm.DB) error { return nil },
	}
	if err := applyMigrationRegistry(db, []migration{entry}); err == nil {
		t.Fatal("migration committed an orphan")
	}
	if db.Migrator().HasTable("parents") || db.Migrator().HasTable("children") || db.Migrator().HasTable("schema_migrations") {
		t.Fatal("failed migration did not roll back")
	}
	var enabled int
	if err := db.Raw("PRAGMA foreign_keys").Scan(&enabled).Error; err != nil || enabled != 1 {
		t.Fatal("failed migration left foreign key enforcement disabled")
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO access_key_cost_limit_rules (access_key_id, kind, limit_nano_usd, period_seconds, rule_revision, created_at_ms, updated_at_ms) VALUES (999, 'total', 1, 0, 1, 1, 1)").Error; err == nil {
		t.Fatal("writes after migration accepted an orphan")
	}
}
