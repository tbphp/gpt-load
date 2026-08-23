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
	migrationfiles "gpt-load/internal/storage/migrations"
)

func TestApplyMySQLMigrationRecoversEveryInitialDDLBoundary(t *testing.T) {
	models := migrationfiles.SchemaModels0001()
	for boundary := 0; boundary <= len(models); boundary++ {
		t.Run(migrationfiles.TableNames0001()[boundaryOrLast(boundary, len(models))], func(t *testing.T) {
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

			if err := applyMySQLMigration(db, migrations[0]); err != nil {
				t.Fatalf("resume boundary %d: %v", boundary, err)
			}
			assertInternalMigrationComplete(t, db, []string{migrations[0].ID})
		})
	}

}

func TestApplyMySQLMigrationRecoversEveryAccessKeyCostLimitDDLBoundary(t *testing.T) {
	models := migrationfiles.SchemaModels0002()
	for boundary := 0; boundary <= len(models); boundary++ {
		t.Run(migrationfiles.TableNames0002()[boundaryOrLast(boundary, len(models))], func(t *testing.T) {
			db := openInternalMigrationTestDatabase(t)
			if err := db.AutoMigrate(&schemaMigration{}); err != nil {
				t.Fatalf("create migration ledger: %v", err)
			}
			if err := migrations[0].Up(db); err != nil {
				t.Fatalf("create baseline schema: %v", err)
			}
			if err := migrations[0].Validate(db); err != nil {
				t.Fatalf("validate baseline schema: %v", err)
			}
			if err := db.Create(&schemaMigration{ID: migrations[0].ID}).Error; err != nil {
				t.Fatalf("record baseline migration: %v", err)
			}
			if err := db.Create(&schemaMigration{ID: migrationResumeMarker(migrations[1].ID)}).Error; err != nil {
				t.Fatalf("create resume marker: %v", err)
			}
			if boundary > 0 {
				if err := db.AutoMigrate(models[:boundary]...); err != nil {
					t.Fatalf("create schema prefix %d: %v", boundary, err)
				}
			}

			if err := applyMySQLMigration(db, migrations[1]); err != nil {
				t.Fatalf("resume boundary %d: %v", boundary, err)
			}
			assertInternalMigrationComplete(t, db, []string{migrations[0].ID, migrations[1].ID})
		})
	}
}

func TestApplyMySQLMigrationRecoversObservationFreshnessRemoval(t *testing.T) {
	for _, alreadyDropped := range []bool{false, true} {
		t.Run(fmt.Sprintf("already_dropped_%t", alreadyDropped), func(t *testing.T) {
			db := openInternalMigrationTestDatabase(t)
			if err := db.AutoMigrate(&schemaMigration{}); err != nil {
				t.Fatalf("create migration ledger: %v", err)
			}
			for index := 0; index < 2; index++ {
				if err := migrations[index].Up(db); err != nil {
					t.Fatalf("apply migration %d: %v", index+1, err)
				}
				if err := db.Create(&schemaMigration{ID: migrations[index].ID}).Error; err != nil {
					t.Fatalf("record migration %d: %v", index+1, err)
				}
			}
			if err := db.Create(&schemaMigration{ID: migrationResumeMarker(migrations[2].ID)}).Error; err != nil {
				t.Fatalf("create resume marker: %v", err)
			}
			if alreadyDropped {
				if err := migrations[2].Up(db); err != nil {
					t.Fatalf("apply interrupted removal: %v", err)
				}
			}

			if err := applyMySQLMigration(db, migrations[2]); err != nil {
				t.Fatalf("resume freshness removal: %v", err)
			}
			assertInternalMigrationComplete(t, db, []string{
				migrations[0].ID,
				migrations[1].ID,
				migrations[2].ID,
			})
		})
	}
}

func TestApplyMySQLMigrationRejectsUnsafeResumeState(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, internalMigrationDB)
	}{
		{
			name: "external table",
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
				if err := db.AutoMigrate(migrationfiles.SchemaModels0001()[0]); err != nil {
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

			err := applyMySQLMigration(db, migrations[0])
			if test.name == "external table" {
				if err != nil {
					t.Fatalf("applyMySQLMigration() error = %v, want external table to be ignored", err)
				}
				if !db.Migrator().HasTable("foreign_data") {
					t.Fatal("migration removed the external table")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "unsafe interrupted migration") {
				t.Fatalf("applyMySQLMigration() error = %v, want unsafe interrupted migration", err)
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

	models := migrationfiles.SchemaModels0001()
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
			assertInternalMigrationComplete(t, restarted, []string{migrations[0].ID, migrations[1].ID, migrations[2].ID, migrations[3].ID})
			if err := restartedSQL.Close(); err != nil {
				t.Fatalf("close recovered database: %v", err)
			}
		})
	}
	runExternalMySQLCostLimitRecovery(t, admin, parsed)
}

func TestExternalDatabaseIncrementalMigrations(t *testing.T) {
	rawDSN := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_TEST_DSN"))
	if rawDSN == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	db := openExternalIncrementalMigrationDatabase(t, rawDSN)
	if err := db.AutoMigrate(&schemaMigration{}); err != nil {
		t.Fatalf("create migration ledger: %v", err)
	}
	if err := migrations[0].Up(db); err != nil {
		t.Fatalf("create 0001 schema: %v", err)
	}
	if err := migrations[0].Validate(db); err != nil {
		t.Fatalf("validate 0001 schema: %v", err)
	}
	if err := db.Create(&schemaMigration{ID: migrations[0].ID}).Error; err != nil {
		t.Fatalf("record 0001 migration: %v", err)
	}

	if err := AutoMigrate(db); err != nil {
		t.Fatalf("apply pending incremental migrations: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("repeat migrated schema validation: %v", err)
	}
	assertInternalMigrationComplete(t, db, []string{migrations[0].ID, migrations[1].ID, migrations[2].ID, migrations[3].ID})
}

func TestExternalDatabaseMySQLRecoversObservationFreshnessCheckDrop(t *testing.T) {
	rawDSN := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_TEST_DSN"))
	if rawDSN == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil || !strings.EqualFold(parsed.Scheme, "mysql") {
		t.Skip("observation freshness recovery is specific to MySQL")
	}
	db := openExternalIncrementalMigrationDatabase(t, rawDSN)
	if err := db.AutoMigrate(&schemaMigration{}); err != nil {
		t.Fatalf("create migration ledger: %v", err)
	}
	for index := 0; index < 2; index++ {
		if err := migrations[index].Up(db); err != nil {
			t.Fatalf("apply migration %d: %v", index+1, err)
		}
		if err := migrations[index].Validate(db); err != nil {
			t.Fatalf("validate migration %d: %v", index+1, err)
		}
		if err := db.Create(&schemaMigration{ID: migrations[index].ID}).Error; err != nil {
			t.Fatalf("record migration %d: %v", index+1, err)
		}
	}
	if err := db.Create(&schemaMigration{ID: migrationResumeMarker(migrations[2].ID)}).Error; err != nil {
		t.Fatalf("record observation freshness recovery marker: %v", err)
	}
	if err := db.Exec(
		"ALTER TABLE `credential_observations` DROP CHECK `chk_credential_observation_fresh_until`",
	).Error; err != nil {
		t.Fatalf("drop observation freshness check: %v", err)
	}

	if err := AutoMigrate(db); err != nil {
		t.Fatalf("resume observation freshness removal: %v", err)
	}
	assertInternalMigrationComplete(t, db, []string{migrations[0].ID, migrations[1].ID, migrations[2].ID, migrations[3].ID})
}

func openExternalIncrementalMigrationDatabase(t *testing.T, rawDSN string) *gorm.DB {
	t.Helper()
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatalf("parse external database DSN: %v", err)
	}
	admin, err := OpenWithSource(rawDSN, config.DatabaseSourceExternal)
	if err != nil {
		t.Fatalf("open external database admin connection: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminSQL.Close() })

	name := fmt.Sprintf("gpt_load_cost_migration_%d", time.Now().UnixNano())
	targetURL := *parsed
	switch strings.ToLower(parsed.Scheme) {
	case "mysql":
		if err := admin.Exec("CREATE DATABASE `" + name + "`").Error; err != nil {
			t.Fatalf("create MySQL migration database: %v", err)
		}
		t.Cleanup(func() {
			if dropErr := admin.Exec("DROP DATABASE IF EXISTS `" + name + "`").Error; dropErr != nil {
				t.Errorf("drop MySQL migration database: %v", dropErr)
			}
		})
		targetURL.Path = "/" + name
		targetURL.RawPath = ""
	case "postgres", "postgresql":
		if err := admin.Exec(`CREATE SCHEMA "` + name + `"`).Error; err != nil {
			t.Fatalf("create PostgreSQL migration schema: %v", err)
		}
		t.Cleanup(func() {
			if dropErr := admin.Exec(`DROP SCHEMA IF EXISTS "` + name + `" CASCADE`).Error; dropErr != nil {
				t.Errorf("drop PostgreSQL migration schema: %v", dropErr)
			}
		})
		query := targetURL.Query()
		query.Set("search_path", name)
		targetURL.RawQuery = query.Encode()
	default:
		t.Fatalf("unsupported external driver %q", parsed.Scheme)
	}

	db, err := OpenWithSource(targetURL.String(), config.DatabaseSourceExternal)
	if err != nil {
		t.Fatalf("open isolated incremental migration database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func runExternalMySQLCostLimitRecovery(t *testing.T, admin *gorm.DB, parsed *url.URL) {
	t.Helper()
	models := migrationfiles.SchemaModels0002()
	for boundary := 0; boundary <= len(models); boundary++ {
		t.Run(fmt.Sprintf("access_key_cost_limits_boundary_%02d", boundary), func(t *testing.T) {
			databaseName := fmt.Sprintf("gpt_load_cost_recovery_%d_%02d", time.Now().UnixNano(), boundary)
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
			if err := migrations[0].Up(partial); err != nil {
				t.Fatalf("create baseline schema: %v", err)
			}
			if err := migrations[0].Validate(partial); err != nil {
				t.Fatalf("validate baseline schema: %v", err)
			}
			if err := partial.Create(&schemaMigration{ID: migrations[0].ID}).Error; err != nil {
				t.Fatalf("record baseline migration: %v", err)
			}
			if err := partial.Create(&schemaMigration{ID: migrationResumeMarker(migrations[1].ID)}).Error; err != nil {
				t.Fatalf("record cost-limit recovery marker: %v", err)
			}
			if boundary > 0 {
				if err := partial.AutoMigrate(models[:boundary]...); err != nil {
					t.Fatalf("create MySQL cost-limit schema prefix %d: %v", boundary, err)
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
				t.Fatalf("resume MySQL cost-limit schema prefix %d: %v", boundary, err)
			}
			assertInternalMigrationComplete(t, restarted, []string{migrations[0].ID, migrations[1].ID, migrations[2].ID, migrations[3].ID})
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

func assertInternalMigrationComplete(t *testing.T, db *gorm.DB, wantIDs []string) {
	t.Helper()
	for _, table := range migrationfiles.TableNames0001() {
		if !db.Migrator().HasTable(table) {
			t.Errorf("table %q is missing", table)
		}
	}
	if len(wantIDs) >= 2 {
		for _, table := range migrationfiles.TableNames0002() {
			if !db.Migrator().HasTable(table) {
				t.Errorf("table %q is missing", table)
			}
		}
	}
	if len(wantIDs) >= 3 && db.Migrator().HasColumn("credential_observations", "fresh_until_ms") {
		t.Error("credential_observations.fresh_until_ms remains after migration 0003")
	}
	if len(wantIDs) >= 4 && !db.Migrator().HasIndex("usage_stats", "idx_usage_stats_group_bucket") {
		t.Error("usage_stats group activity index is missing after migration 0004")
	}
	var ids []string
	if err := db.Table(migrationLedgerTable).Order("id").Pluck("id", &ids).Error; err != nil {
		t.Fatal(err)
	}
	if len(ids) != len(wantIDs) {
		t.Fatalf("migration IDs = %v, want %v", ids, wantIDs)
	}
	for index := range wantIDs {
		if ids[index] != wantIDs[index] {
			t.Fatalf("migration IDs = %v, want %v", ids, wantIDs)
		}
	}
}

func boundaryOrLast(boundary, count int) int {
	if boundary < count {
		return boundary
	}
	return count - 1
}
