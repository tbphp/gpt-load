package storage

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestAddRequestLogConversionTraceUpgradesExistingConstraintWithoutLosingRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:conversion-trace-migration?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE request_logs (id TEXT PRIMARY KEY NOT NULL)`,
		`CREATE TABLE request_log_attempts (
			request_id TEXT NOT NULL,
			sequence INTEGER NOT NULL,
			failure_category TEXT NOT NULL CONSTRAINT chk_request_log_attempt_failure_category
				CHECK (failure_category IN ('ok','rate_limited','model_unavailable','invalid_key','upstream_host_error','client_error','downstream_cancel','ambiguous')),
			PRIMARY KEY (request_id, sequence),
			CONSTRAINT fk_attempt_request FOREIGN KEY (request_id) REFERENCES request_logs(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX idx_attempt_failure ON request_log_attempts(failure_category)`,
		`INSERT INTO request_logs(id) VALUES ('request-1')`,
		`INSERT INTO request_log_attempts(request_id, sequence, failure_category) VALUES ('request-1', 1, 'ok')`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("execute fixture schema: %v", err)
		}
	}

	if err := db.Transaction(addRequestLogConversionTrace); err != nil {
		t.Fatalf("addRequestLogConversionTrace() error = %v", err)
	}
	for table, column := range map[string]string{
		"request_logs":         "client_parameters_json",
		"request_log_attempts": "conversion_trace_json",
	} {
		if !db.Migrator().HasColumn(table, column) {
			t.Errorf("%s.%s is missing", table, column)
		}
	}
	var category string
	if err := db.Raw(
		"SELECT failure_category FROM request_log_attempts WHERE request_id = ? AND sequence = ?",
		"request-1", 1,
	).Scan(&category).Error; err != nil || category != "ok" {
		t.Fatalf("historical row category = %q, error = %v", category, err)
	}
	if err := db.Exec(
		"UPDATE request_log_attempts SET failure_category = ? WHERE request_id = ? AND sequence = ?",
		"conversion_unsupported", "request-1", 1,
	).Error; err != nil {
		t.Fatalf("widened failure category rejected conversion_unsupported: %v", err)
	}
	if !db.Migrator().HasIndex("request_log_attempts", "idx_attempt_failure") {
		t.Fatal("request log attempt index was lost")
	}
	var foreignKeys []struct {
		Table    string
		From     string
		To       string
		OnDelete string `gorm:"column:on_delete"`
	}
	if err := db.Raw("PRAGMA foreign_key_list('request_log_attempts')").Scan(&foreignKeys).Error; err != nil {
		t.Fatal(err)
	}
	if len(foreignKeys) != 1 || foreignKeys[0].Table != "request_logs" ||
		foreignKeys[0].From != "request_id" || foreignKeys[0].To != "id" ||
		foreignKeys[0].OnDelete != "CASCADE" {
		t.Fatalf("foreign keys = %#v", foreignKeys)
	}
}
