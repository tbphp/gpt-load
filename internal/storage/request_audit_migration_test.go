package storage

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func TestRequestAuditMigrationContract(t *testing.T) {
	testRequestAuditMigration(t, openInternalMigrationTestDatabase)
}
func TestExternalRequestAuditMigrationContract(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_TEST_DSN"))
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testRequestAuditMigration(t, func(t *testing.T) *gorm.DB { return openExternalIncrementalMigrationDatabase(t, dsn) })
}
func testRequestAuditMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
	for _, scenario := range []string{"fresh", "upgrade", "interrupted_column", "interrupted_table"} {
		t.Run(scenario, func(t *testing.T) {
			db := open(t)
			if scenario != "fresh" {
				if err := applyMigrationRegistry(db, migrations[:20]); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(scenario, "interrupted") {
				registry := append([]migration(nil), migrations...)
				up := registry[20].Up
				registry[20].Up = func(tx *gorm.DB) error {
					if scenario == "interrupted_column" {
						if err := tx.Exec("ALTER TABLE request_logs ADD COLUMN request_audit JSON").Error; err != nil {
							return err
						}
					} else {
						if err := up(tx); err != nil {
							return err
						}
					}
					return fmt.Errorf("interrupt audit migration")
				}
				if err := applyMigrationRegistry(db, registry); err == nil {
					t.Fatal("expected interruption")
				}
			}
			for range 2 {
				if err := AutoMigrate(db); err != nil {
					t.Fatal(err)
				}
			}
			if !db.Migrator().HasTable("request_audit_usages") || !db.Migrator().HasColumn("request_logs", "request_audit") {
				t.Fatal("audit schema missing")
			}
		})
	}
}
