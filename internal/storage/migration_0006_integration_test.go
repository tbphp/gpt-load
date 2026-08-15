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

func TestSQLiteUpstreamProtocolMigrationRollsBackFailedDrop(t *testing.T) {
	db := openInternalMigrationTestDatabase(t)
	if err := applyMigrationRegistry(db, migrations[:5]); err != nil {
		t.Fatalf("apply migrations through 0005: %v", err)
	}
	if err := db.Exec(`CREATE VIEW request_attempt_protocol_view AS
		SELECT request_id, upstream_api FROM request_log_attempts`).Error; err != nil {
		t.Fatalf("create dependent view: %v", err)
	}

	if err := AutoMigrate(db); err == nil {
		t.Fatal("0006 unexpectedly succeeded with a dependent legacy-column view")
	}
	if !db.Migrator().HasColumn("request_log_attempts", "upstream_api") ||
		db.Migrator().HasColumn("request_log_attempts", "upstream_protocol") {
		t.Fatal("failed SQLite migration did not roll back its schema changes")
	}
	if err := db.Exec("DROP VIEW request_attempt_protocol_view").Error; err != nil {
		t.Fatalf("drop dependent view: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("retry 0006 after removing dependency: %v", err)
	}
}

// TestExternalDatabaseUpstreamProtocolMigration exercises the real 0005 to
// 0006 upgrade on both supported external engines. Historical protocol values
// are deliberately discarded while the rest of each attempt and its FK remain.
func TestExternalDatabaseUpstreamProtocolMigration(t *testing.T) {
	db := openIsolatedExternalMigrationDatabase(t, "protocol_upgrade")
	if err := applyMigrationRegistry(db, migrations[:5]); err != nil {
		t.Fatalf("apply migrations through 0005: %v", err)
	}
	seedLegacyUpstreamAttempt(t, db)

	if err := AutoMigrate(db); err != nil {
		t.Fatalf("upgrade through 0006: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("repeat migration chain: %v", err)
	}
	if db.Migrator().HasColumn("request_log_attempts", "upstream_api") ||
		!db.Migrator().HasColumn("request_log_attempts", "upstream_protocol") {
		t.Fatal("final request_log_attempts protocol columns are incorrect")
	}
	if !db.Migrator().HasIndex("request_log_attempts", "idx_request_log_attempts_group_completed_request") {
		t.Fatal("request_log_attempts index was not preserved")
	}
	var attempt struct {
		UpstreamProtocol string
		StatusCode       int
		DurationMS       int64 `gorm:"column:duration_ms"`
		ErrorSummary     string
	}
	if err := db.Table("request_log_attempts").Where("request_id = ?", externalProtocolRequestID).Take(&attempt).Error; err != nil {
		t.Fatalf("read migrated request attempt: %v", err)
	}
	if attempt.UpstreamProtocol != "" || attempt.StatusCode != 502 ||
		attempt.DurationMS != 25 || attempt.ErrorSummary != "legacy failure" {
		t.Fatalf("migrated request attempt = %#v", attempt)
	}
	if err := db.Exec("DELETE FROM request_logs WHERE id = ?", externalProtocolRequestID).Error; err != nil {
		t.Fatalf("delete request log: %v", err)
	}
	var childCount int64
	if err := db.Table("request_log_attempts").Where("request_id = ?", externalProtocolRequestID).Count(&childCount).Error; err != nil {
		t.Fatalf("count request attempts after parent delete: %v", err)
	}
	if childCount != 0 {
		t.Fatalf("request attempt FK cascade left %d child row(s)", childCount)
	}
}

// TestExternalDatabasePostgresUpstreamProtocolRollback proves the ADD and DROP
// remain atomic when PostgreSQL rejects the DROP because a dependent view is
// present. MySQL's corresponding safety contract is covered by marker recovery.
func TestExternalDatabasePostgresUpstreamProtocolRollback(t *testing.T) {
	if externalDatabaseScheme(t) != "postgres" {
		t.Skip("transactional DDL rollback contract is specific to PostgreSQL")
	}
	db := openIsolatedExternalMigrationDatabase(t, "protocol_rollback")
	if err := applyMigrationRegistry(db, migrations[:5]); err != nil {
		t.Fatalf("apply migrations through 0005: %v", err)
	}
	if err := db.Exec(`CREATE VIEW request_attempt_protocol_view AS
		SELECT request_id, upstream_api FROM request_log_attempts`).Error; err != nil {
		t.Fatalf("create dependent view: %v", err)
	}

	if err := AutoMigrate(db); err == nil {
		t.Fatal("0006 unexpectedly succeeded with a dependent legacy-column view")
	}
	if !db.Migrator().HasColumn("request_log_attempts", "upstream_api") ||
		db.Migrator().HasColumn("request_log_attempts", "upstream_protocol") {
		t.Fatal("failed PostgreSQL migration did not roll back its schema changes")
	}
	var completed int64
	if err := db.Model(&schemaMigration{}).Where("id = ?", migrations[5].ID).Count(&completed).Error; err != nil {
		t.Fatal(err)
	}
	if completed != 0 {
		t.Fatal("failed PostgreSQL migration was recorded as complete")
	}
	if err := db.Exec("DROP VIEW request_attempt_protocol_view").Error; err != nil {
		t.Fatalf("drop dependent view: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("retry 0006 after removing dependency: %v", err)
	}
}

// TestExternalDatabaseMySQLInterruptedUpstreamProtocolRecovery exercises every
// accepted partial state plus the unsafe states that must preserve the old
// column even when no #building marker existed before startup.
func TestExternalDatabaseMySQLInterruptedUpstreamProtocolRecovery(t *testing.T) {
	if externalDatabaseScheme(t) != "mysql" {
		t.Skip("interrupted 0006 recovery is specific to MySQL")
	}
	tests := []struct {
		name        string
		addNewDDL   string
		dropOld     bool
		newValue    string
		marker      bool
		wantSuccess bool
		wantOld     bool
	}{
		{name: "old_only", marker: true, wantSuccess: true},
		{name: "both", addNewDDL: `varchar(32) NOT NULL DEFAULT ''`, marker: true, wantSuccess: true},
		{name: "new_only", addNewDDL: `varchar(32) NOT NULL DEFAULT ''`, dropOld: true, marker: true, wantSuccess: true},
		{name: "wrong_definition_without_marker", addNewDDL: `varchar(16) NOT NULL DEFAULT ''`, wantOld: true},
		{name: "nonempty_without_marker", addNewDDL: `varchar(32) NOT NULL DEFAULT ''`, newValue: "openai-completions", wantOld: true},
		{name: "neither", dropOld: true, marker: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openIsolatedExternalMigrationDatabase(t, "protocol_"+test.name)
			if err := applyMigrationRegistry(db, migrations[:5]); err != nil {
				t.Fatalf("apply migrations through 0005: %v", err)
			}
			seedLegacyUpstreamAttempt(t, db)
			if test.addNewDDL != "" {
				if err := db.Exec("ALTER TABLE request_log_attempts ADD COLUMN upstream_protocol " + test.addNewDDL).Error; err != nil {
					t.Fatalf("add partial upstream_protocol: %v", err)
				}
			}
			if test.newValue != "" {
				if err := db.Exec("UPDATE request_log_attempts SET upstream_protocol = ?", test.newValue).Error; err != nil {
					t.Fatalf("write unsafe canonical value: %v", err)
				}
			}
			if test.dropOld {
				if err := db.Exec("ALTER TABLE request_log_attempts DROP COLUMN upstream_api").Error; err != nil {
					t.Fatalf("drop partial upstream_api: %v", err)
				}
			}
			if test.marker {
				if err := db.Create(&schemaMigration{ID: migrationResumeMarker(migrations[5].ID)}).Error; err != nil {
					t.Fatalf("record 0006 recovery marker: %v", err)
				}
			}

			err := AutoMigrate(db)
			if test.wantSuccess {
				if err != nil {
					t.Fatalf("recover 0006: %v", err)
				}
				assertInternalMigrationComplete(t, db, []string{
					migrations[0].ID, migrations[1].ID, migrations[2].ID, migrations[3].ID, migrations[4].ID, migrations[5].ID,
				})
				if db.Migrator().HasColumn("request_log_attempts", "upstream_api") ||
					!db.Migrator().HasColumn("request_log_attempts", "upstream_protocol") {
					t.Fatal("safe 0006 recovery did not reach the final schema")
				}
				return
			}
			if err == nil {
				t.Fatal("unsafe 0006 recovery unexpectedly succeeded")
			}
			if test.wantOld && !db.Migrator().HasColumn("request_log_attempts", "upstream_api") {
				t.Fatal("unsafe 0006 recovery dropped the legacy column")
			}
			var completed int64
			if err := db.Model(&schemaMigration{}).Where("id = ?", migrations[5].ID).Count(&completed).Error; err != nil {
				t.Fatal(err)
			}
			if completed != 0 {
				t.Fatal("unsafe 0006 recovery was recorded as complete")
			}
		})
	}
}

const externalProtocolRequestID = "00000000-0000-4000-8000-000000000005"

func seedLegacyUpstreamAttempt(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Exec(`INSERT INTO request_logs (
		id, completed_at_ms, access_key_id, protocol, client_model, upstream_model,
		status, status_code, duration_ms, error_summary
	) VALUES (?, 10, 1, 'openai-completions', 'client-model', 'upstream-model',
		'error', 502, 25, 'request failed')`, externalProtocolRequestID).Error; err != nil {
		t.Fatalf("insert legacy request log: %v", err)
	}
	if err := db.Exec(`INSERT INTO request_log_attempts (
		request_id, sequence, completed_at_ms, group_id, group_name, credential_id,
		upstream_api, status_code, duration_ms, failure_category, action,
		error_code, error_summary, pricing_receipt
	) VALUES (?, 1, 10, 2, 'legacy group', 3,
		'aws-bedrock', 502, 25, 'upstream_host_error', 'terminate',
		'upstream_error', 'legacy failure', '{"matched_rule_id":"rule-1"}')`, externalProtocolRequestID).Error; err != nil {
		t.Fatalf("insert legacy request attempt: %v", err)
	}
}

func externalDatabaseScheme(t *testing.T) string {
	t.Helper()
	rawDSN := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_TEST_DSN"))
	if rawDSN == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatalf("parse external database DSN: %v", err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme == "postgresql" {
		scheme = "postgres"
	}
	if scheme != "mysql" && scheme != "postgres" {
		t.Fatalf("unsupported external database scheme %q", scheme)
	}
	return scheme
}

func openIsolatedExternalMigrationDatabase(t *testing.T, purpose string) *gorm.DB {
	t.Helper()
	rawDSN := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_TEST_DSN"))
	scheme := externalDatabaseScheme(t)
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := OpenWithSource(rawDSN, config.DatabaseSourceExternal)
	if err != nil {
		t.Fatalf("open external admin database: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	purpose = strings.ToLower(strings.NewReplacer("-", "_", " ", "_").Replace(purpose))
	if len(purpose) > 24 {
		purpose = purpose[:24]
	}
	databaseName := fmt.Sprintf("gptl_%s_%d", purpose, time.Now().UnixNano())
	quotedName := `"` + databaseName + `"`
	if scheme == "mysql" {
		quotedName = "`" + databaseName + "`"
	}
	if err := admin.Exec("CREATE DATABASE " + quotedName).Error; err != nil {
		_ = adminSQL.Close()
		t.Fatalf("create isolated external database: %v", err)
	}
	targetURL := *parsed
	targetURL.Path = "/" + databaseName
	targetURL.RawPath = ""
	db, err := OpenWithSource(targetURL.String(), config.DatabaseSourceExternal)
	if err != nil {
		_ = admin.Exec("DROP DATABASE IF EXISTS " + quotedName).Error
		_ = adminSQL.Close()
		t.Fatalf("open isolated external database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		_ = admin.Exec("DROP DATABASE IF EXISTS " + quotedName).Error
		_ = adminSQL.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
		if dropErr := admin.Exec("DROP DATABASE IF EXISTS " + quotedName).Error; dropErr != nil {
			t.Errorf("drop isolated external database: %v", dropErr)
		}
		_ = adminSQL.Close()
	})
	return db
}
