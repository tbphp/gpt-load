package storage

import "testing"

func TestRequestLogExecutionObservationMigrationUpgradesExistingTablesIdempotently(t *testing.T) {
	db := openInternalMigrationTestDatabase(t)
	if err := db.Exec(`CREATE TABLE request_logs (
		id varchar(36) PRIMARY KEY NOT NULL
	)`).Error; err != nil {
		t.Fatalf("create legacy request_logs: %v", err)
	}
	if err := db.Exec(`CREATE TABLE request_log_attempts (
		request_id varchar(36) NOT NULL,
		sequence integer NOT NULL,
		PRIMARY KEY (request_id, sequence)
	)`).Error; err != nil {
		t.Fatalf("create legacy request_log_attempts: %v", err)
	}

	for run := 1; run <= 2; run++ {
		if err := addRequestLogExecutionObservation(db); err != nil {
			t.Fatalf("migration run %d: %v", run, err)
		}
	}
	for _, column := range []string{"operation"} {
		if !db.Migrator().HasColumn("request_logs", column) {
			t.Errorf("request_logs.%s is missing", column)
		}
	}
	for _, column := range []string{
		"upstream_api",
		"reasoning_mode",
		"reasoning_effort",
		"reasoning_budget_tokens",
	} {
		if !db.Migrator().HasColumn("request_log_attempts", column) {
			t.Errorf("request_log_attempts.%s is missing", column)
		}
	}
}
