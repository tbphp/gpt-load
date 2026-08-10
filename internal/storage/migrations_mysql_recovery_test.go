package storage

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/platform/config"
)

func TestApplyMySQLMigrationRecoversEveryInitialV2DDLBoundary(t *testing.T) {
	models := initialV2SchemaModels()
	for boundary := 0; boundary <= len(models); boundary++ {
		t.Run(strings.TrimPrefix(initialV2TableNames()[boundaryOrLast(boundary, len(models))], ""), func(t *testing.T) {
			db := openInternalMigrationTestDatabase(t)
			if err := db.AutoMigrate(&schemaMigration{}); err != nil {
				t.Fatalf("create migration ledger: %v", err)
			}
			if err := db.Create(&schemaMigration{ID: migrationResumeMarker(migrations[0].ID)}).Error; err != nil {
				t.Fatalf("create resume marker: %v", err)
			}
			if boundary > 0 {
				if err := db.AutoMigrate(models[:boundary]...); err != nil {
					t.Fatalf("create schema prefix %d: %v", boundary, err)
				}
			}

			if err := applyMySQLInitialV2Migration(db, migrations[0]); err != nil {
				t.Fatalf("resume boundary %d: %v", boundary, err)
			}
			assertInternalMigrationComplete(t, db)
		})
	}
}

func TestApplyMySQLMigrationRejectsUnsafeResumeState(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, internalMigrationDB)
	}{
		{
			name: "unknown table",
			setup: func(t *testing.T, db internalMigrationDB) {
				t.Helper()
				if err := db.Exec("CREATE TABLE foreign_data (id integer PRIMARY KEY)").Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "existing business data",
			setup: func(t *testing.T, db internalMigrationDB) {
				t.Helper()
				if err := db.AutoMigrate(initialV2SchemaModels()[0]); err != nil {
					t.Fatal(err)
				}
				if err := db.Exec(`INSERT INTO groups
					(name, channel_id, params, models, enabled, created_at_ms, updated_at_ms)
					VALUES ('unsafe', 'openai', '{}', '[]', TRUE, 0, 0)`).Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "retired schema column",
			setup: func(t *testing.T, db internalMigrationDB) {
				t.Helper()
				if err := db.Exec("CREATE TABLE groups (id integer PRIMARY KEY, provider_id text)").Error; err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openInternalMigrationTestDatabase(t)
			if err := db.AutoMigrate(&schemaMigration{}); err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&schemaMigration{ID: migrationResumeMarker(migrations[0].ID)}).Error; err != nil {
				t.Fatal(err)
			}
			test.setup(t, internalMigrationDB{db})

			err := applyMySQLInitialV2Migration(db, migrations[0])
			if err == nil || !strings.Contains(err.Error(), "unsafe interrupted baseline") {
				t.Fatalf("applyMySQLInitialV2Migration() error = %v", err)
			}
		})
	}
}

func TestExternalDatabaseMySQLInterruptedBaselineRecovery(t *testing.T) {
	rawDSN := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_TEST_DSN"))
	if rawDSN == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil || !strings.EqualFold(parsed.Scheme, "mysql") {
		t.Skip("interrupted baseline recovery is specific to MySQL")
	}
	admin, err := OpenWithSource(rawDSN, config.DatabaseSourceExternal)
	if err != nil {
		t.Fatalf("open MySQL admin database: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminSQL.Close() })

	models := initialV2SchemaModels()
	for boundary := 0; boundary <= len(models); boundary++ {
		t.Run(fmt.Sprintf("boundary_%02d", boundary), func(t *testing.T) {
			databaseName := fmt.Sprintf("gpt_load_recovery_%d_%02d", time.Now().UnixNano(), boundary)
			if err := admin.Exec("CREATE DATABASE `" + databaseName + "`").Error; err != nil {
				t.Fatalf("create recovery database: %v", err)
			}
			t.Cleanup(func() {
				if dropErr := admin.Exec("DROP DATABASE IF EXISTS `" + databaseName + "`").Error; dropErr != nil {
					t.Errorf("drop recovery database: %v", dropErr)
				}
			})

			recoveryURL := *parsed
			recoveryURL.Path = "/" + databaseName
			recoveryURL.RawPath = ""
			partial, err := OpenWithSource(recoveryURL.String(), config.DatabaseSourceExternal)
			if err != nil {
				t.Fatalf("open recovery database: %v", err)
			}
			if err := partial.AutoMigrate(&schemaMigration{}); err != nil {
				t.Fatalf("create recovery ledger: %v", err)
			}
			if err := partial.Create(&schemaMigration{ID: migrationResumeMarker(migrations[0].ID)}).Error; err != nil {
				t.Fatalf("record recovery marker: %v", err)
			}
			if boundary > 0 {
				if err := partial.AutoMigrate(models[:boundary]...); err != nil {
					t.Fatalf("create MySQL schema prefix %d: %v", boundary, err)
				}
			}
			partialSQL, err := partial.DB()
			if err != nil {
				t.Fatal(err)
			}
			if err := partialSQL.Close(); err != nil {
				t.Fatalf("close interrupted database: %v", err)
			}

			restarted, err := OpenWithSource(recoveryURL.String(), config.DatabaseSourceExternal)
			if err != nil {
				t.Fatalf("reopen recovery database: %v", err)
			}
			restartedSQL, err := restarted.DB()
			if err != nil {
				t.Fatal(err)
			}
			if err := AutoMigrate(restarted); err != nil {
				_ = restartedSQL.Close()
				t.Fatalf("resume MySQL schema prefix %d: %v", boundary, err)
			}
			assertInternalMigrationComplete(t, restarted)
			if err := restartedSQL.Close(); err != nil {
				t.Fatalf("close recovered database: %v", err)
			}
		})
	}
}

type internalMigrationDB struct{ *gorm.DB }

func openInternalMigrationTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func assertInternalMigrationComplete(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, table := range initialV2TableNames() {
		if !db.Migrator().HasTable(table) {
			t.Errorf("table %q is missing", table)
		}
	}
	var ids []string
	if err := db.Table(migrationLedgerTable).Order("id").Pluck("id", &ids).Error; err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != migrations[0].ID {
		t.Fatalf("migration IDs = %v", ids)
	}
}

func boundaryOrLast(boundary, count int) int {
	if boundary < count {
		return boundary
	}
	return count - 1
}
