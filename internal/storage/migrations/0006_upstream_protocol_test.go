package migrations_test

import (
	"strings"
	"testing"

	"gpt-load/internal/storage/migrations"
)

func TestUpstreamProtocolMigrationDropsLegacyValuesAndPreservesAttempt(t *testing.T) {
	db := openInitialTestDatabase(t)
	if err := migrations.Up0001(db); err != nil {
		t.Fatalf("Up0001() error = %v", err)
	}
	if err := db.Exec(`INSERT INTO request_logs (
		id, completed_at_ms, access_key_id, protocol, client_model, upstream_model,
		status, status_code, duration_ms, error_summary
	) VALUES ('request-1', 10, 1, 'openai-completions', 'client-model', 'upstream-model',
		'error', 502, 25, 'request failed')`).Error; err != nil {
		t.Fatalf("insert request log: %v", err)
	}
	if err := db.Exec(`INSERT INTO request_log_attempts (
		request_id, sequence, completed_at_ms, group_id, group_name, credential_id,
		upstream_api, status_code, duration_ms, failure_category, action,
		error_code, error_summary, pricing_receipt
	) VALUES ('request-1', 1, 10, 2, 'legacy group', 3,
		'aws-bedrock', 502, 25, 'upstream_host_error', 'terminate',
		'upstream_error', 'legacy failure', '{"matched_rule_id":"rule-1"}')`).Error; err != nil {
		t.Fatalf("insert request attempt: %v", err)
	}

	if err := migrations.Up0006(db); err != nil {
		t.Fatalf("Up0006() error = %v", err)
	}
	if err := migrations.Validate0006(db); err != nil {
		t.Fatalf("Validate0006() error = %v", err)
	}
	if db.Migrator().HasColumn("request_log_attempts", "upstream_api") {
		t.Fatal("legacy upstream_api column remains")
	}
	if !db.Migrator().HasColumn("request_log_attempts", "upstream_protocol") {
		t.Fatal("upstream_protocol column is missing")
	}

	var attempt struct {
		UpstreamProtocol string
		StatusCode       int
		DurationMS       int64 `gorm:"column:duration_ms"`
		ErrorCode        string
		ErrorSummary     string
		PricingReceipt   string
	}
	if err := db.Table("request_log_attempts").Where("request_id = ?", "request-1").Take(&attempt).Error; err != nil {
		t.Fatalf("read migrated attempt: %v", err)
	}
	if attempt.UpstreamProtocol != "" || attempt.StatusCode != 502 || attempt.DurationMS != 25 ||
		attempt.ErrorCode != "upstream_error" || attempt.ErrorSummary != "legacy failure" ||
		!strings.Contains(attempt.PricingReceipt, "rule-1") {
		t.Fatalf("migrated attempt = %#v", attempt)
	}

	if err := migrations.Up0006(db); err != nil {
		t.Fatalf("repeat Up0006() error = %v", err)
	}
}

func TestUpstreamProtocolMigrationRecoversOnlySafePartialStates(t *testing.T) {
	t.Run("both columns with empty new values", func(t *testing.T) {
		db := openInitialTestDatabase(t)
		if err := migrations.Up0001(db); err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`ALTER TABLE request_log_attempts ADD COLUMN upstream_protocol varchar(32) NOT NULL DEFAULT ''`).Error; err != nil {
			t.Fatal(err)
		}
		if err := migrations.ValidateRecoverable0006(db); err != nil {
			t.Fatalf("ValidateRecoverable0006() error = %v", err)
		}
		if err := migrations.Up0006(db); err != nil {
			t.Fatalf("Up0006() error = %v", err)
		}
		if db.Migrator().HasColumn("request_log_attempts", "upstream_api") {
			t.Fatal("legacy column remains after safe recovery")
		}
	})

	t.Run("neither column", func(t *testing.T) {
		db := openInitialTestDatabase(t)
		if err := db.Exec(`CREATE TABLE request_log_attempts (request_id varchar(36) NOT NULL)`).Error; err != nil {
			t.Fatal(err)
		}
		if err := migrations.ValidateRecoverable0006(db); err == nil {
			t.Fatal("ValidateRecoverable0006() error = nil")
		}
	})

	t.Run("nonempty new values preserve old column", func(t *testing.T) {
		db := openInitialTestDatabase(t)
		if err := db.Exec(`CREATE TABLE request_log_attempts (
			upstream_api varchar(64) NOT NULL DEFAULT '',
			upstream_protocol varchar(32) NOT NULL DEFAULT ''
		)`).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`INSERT INTO request_log_attempts (upstream_api, upstream_protocol)
			VALUES ('openai-chat-completions', 'openai-completions')`).Error; err != nil {
			t.Fatal(err)
		}
		if err := migrations.Up0006(db); err == nil {
			t.Fatal("Up0006() error = nil")
		}
		if !db.Migrator().HasColumn("request_log_attempts", "upstream_api") {
			t.Fatal("unsafe recovery dropped the legacy column")
		}
	})
}
