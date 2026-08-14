package migrations_test

import (
	"path/filepath"
	"testing"

	"gpt-load/internal/storage"
	migrations "gpt-load/internal/storage/migrations"
)

func TestAutoMigrateCreatesSubscriptionRuntimeSchema(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "subscription-runtime.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable("credential_reset_operations") {
		t.Fatal("credential_reset_operations table is missing")
	}
	for _, column := range []string{"idempotency_key", "request_digest", "credential_id", "redeem_request_id", "state", "result_json"} {
		if !db.Migrator().HasColumn("credential_reset_operations", column) {
			t.Fatalf("credential_reset_operations.%s is missing", column)
		}
	}
	if !db.Migrator().HasIndex("request_logs", "idx_request_logs_credential_completed_id") {
		t.Fatal("request log credential/time index is missing")
	}
	if err := migrations.Up0004(db); err != nil {
		t.Fatalf("repeat Up0004() error = %v", err)
	}
	if err := migrations.Validate0004(db); err != nil {
		t.Fatalf("Validate0004() error = %v", err)
	}
}
